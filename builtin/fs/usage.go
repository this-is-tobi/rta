package fs

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// entry is one thing under the scanned path, with everything beneath it
// already counted.
type entry struct {
	name  string
	dir   bool
	size  int64
	files int
}

// scan walks a directory and totals what it finds.
//
// Two rules keep it from ever being surprising, and both are about a scan
// that answers a different question than the one asked:
//
//   - Symlinks are never followed. A link into a parent makes a walk
//     infinite, and a link to somewhere else makes the total a measure of
//     that somewhere else.
//   - Filesystem boundaries are never crossed. "What is using space here"
//     means this device; descending into a network mount or /proc turns a
//     one-second answer into a hang, and counts space that is not yours.
//
// Unreadable directories are counted and reported rather than failing the
// scan: a permission error three levels down should not cost the answer, but
// a total that quietly excluded half a tree is worse than no total at all.
type scanner struct {
	root     string
	device   uint64
	maxDepth int
	skipped  int
	largest  []fileSize
}

type fileSize struct {
	path string
	size int64
}

// keepLargest is how many individual files the scan remembers. The detail
// page shows them, and they are the answer far more often than the directory
// ranking is — one forgotten core dump beats twenty evenly-sized folders.
const keepLargest = 15

func newScanner(root string, maxDepth int) *scanner {
	s := &scanner{root: root, maxDepth: maxDepth}
	if dev, ok := deviceOf(root); ok {
		s.device = dev
	}
	return s
}

// walk totals one directory subtree, returning its size and file count.
func (s *scanner) walk(ctx context.Context, dir string, depth int) (int64, int) {
	if err := ctx.Err(); err != nil {
		return 0, 0
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		s.skipped++
		return 0, 0
	}
	var total int64
	var count int
	for _, item := range items {
		full := filepath.Join(dir, item.Name())
		// Lstat, not Stat: a symlink's own size, never its target's.
		info, err := os.Lstat(full)
		if err != nil {
			s.skipped++
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			total += info.Size()
			count++
		case info.IsDir():
			if !s.sameDevice(info) {
				s.skipped++
				continue
			}
			if s.maxDepth > 0 && depth >= s.maxDepth {
				continue
			}
			sub, subCount := s.walk(ctx, full, depth+1)
			total += sub
			count += subCount
		case info.Mode().IsRegular():
			total += info.Size()
			count++
			s.remember(full, info.Size())
		}
	}
	return total, count
}

// remember keeps the largest files seen, without holding every path in memory
// for a scan of a million files.
func (s *scanner) remember(path string, size int64) {
	if len(s.largest) == keepLargest && size <= s.largest[len(s.largest)-1].size {
		return
	}
	s.largest = append(s.largest, fileSize{path, size})
	sort.Slice(s.largest, func(i, j int) bool { return s.largest[i].size > s.largest[j].size })
	if len(s.largest) > keepLargest {
		s.largest = s.largest[:keepLargest]
	}
}

func runUsage(ctx context.Context, req plugin.Request) (view.View, error) {
	path, err := resolvePath(req.String("path"))
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, pathError("fs.usage", path, statErr)
	}
	if !info.IsDir() {
		return nil, view.Errorf("fs.usage.notadir", "%s is a file, not a directory", path).
			WithHint("use fs hash to inspect one file, or pass the directory holding it")
	}

	s := newScanner(path, req.Int("depth"))
	items, readErr := os.ReadDir(path)
	if readErr != nil {
		return nil, pathError("fs.usage", path, readErr)
	}

	entries := make([]entry, 0, len(items))
	var total int64
	for _, item := range items {
		full := filepath.Join(path, item.Name())
		fi, err := os.Lstat(full)
		if err != nil {
			s.skipped++
			continue
		}
		e := entry{name: item.Name(), dir: fi.IsDir() && fi.Mode()&os.ModeSymlink == 0}
		switch {
		case e.dir:
			if !s.sameDevice(fi) {
				s.skipped++
				continue
			}
			e.size, e.files = s.walk(ctx, full, 1)
		default:
			e.size, e.files = fi.Size(), 1
			if fi.Mode().IsRegular() {
				s.remember(full, fi.Size())
			}
		}
		total += e.size
		entries = append(entries, e)
	}
	if err := ctx.Err(); err != nil {
		return nil, view.Errorf("fs.usage.cancelled", "scan of %s was interrupted", path)
	}

	// Biggest first — the whole point — with names breaking ties so that two
	// runs over an unchanged tree read identically.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].size != entries[j].size {
			return entries[i].size > entries[j].size
		}
		return entries[i].name < entries[j].name
	})

	if req.Bool("detail") {
		return usageDetail(ctx, req, path, entries, total, s), nil
	}
	return usageTable(entries, total, req.Int("limit")), nil
}

func usageTable(entries []entry, total int64, limit int) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Entry"},
		{Name: "Size", Kind: view.KindBytes},
		// Percent rather than KindUsage: this is one entry's proportion of
		// what was scanned, and nothing is filling up. A directory holding
		// 95% of a tree is the answer somebody ran this to get.
		{Name: "Share", Kind: view.KindPercent},
		{Name: "Files", Kind: view.KindNumber},
	}}
	shown := entries
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, e := range shown {
		name := e.name
		if e.dir {
			name += "/"
		}
		t.Rows = append(t.Rows, []string{
			name, humanBytes(e.size), share(e.size, total), fmt.Sprintf("%d", e.files),
		})
	}
	t.Total = len(entries)
	return t
}

func usageDetail(ctx context.Context, req plugin.Request, path string,
	entries []entry, total int64, s *scanner) view.View {

	var files, dirs int
	for _, e := range entries {
		if e.dir {
			dirs++
		}
		files += e.files
	}
	summary := []view.Pair{
		{Key: "path", Value: path},
		{Key: "total", Value: humanBytes(total)},
		{Key: "contents", Value: fmt.Sprintf("%d entries · %d files beneath · %d directories", len(entries), files, dirs)},
	}
	if s.skipped > 0 {
		// Never silently. A total computed over an unknown fraction of a tree
		// looks exactly like a correct one.
		summary = append(summary, view.Pair{
			Key:   "skipped",
			Value: fmt.Sprintf("%d entries — unreadable, or on another filesystem", s.skipped),
		})
	}

	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", view.KeyValue{Pairs: summary})
	p.PutAs("entries", "biggest entries", usageTable(entries, total, req.Int("limit")))

	if len(s.largest) > 0 {
		lt := view.Table{Columns: []view.Column{{Name: "File"}, {Name: "Size", Kind: view.KindBytes}}}
		for _, f := range s.largest {
			rel, err := filepath.Rel(path, f.path)
			if err != nil {
				rel = f.path
			}
			lt.Rows = append(lt.Rows, []string{rel, humanBytes(f.size)})
		}
		lt.Total = len(lt.Rows)
		p.PutAs("largest-files", "largest files anywhere beneath", lt)
	}
	return p.View()
}

// share renders a percentage of the scanned total, which is what the reader
// is looking at — not of the disk, which they did not ask about.
func share(size, total int64) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(size)*100/float64(total))
}

// humanBytes formats a size the way a person reads one. Binary units, because
// that is what every other size in this tool reports.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// resolvePath expands ~ and makes the path absolute, so that every message
// names the same thing the caller would name.
func resolvePath(raw string) (string, *view.Error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		p = "."
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", view.Errorf("fs.path", "resolving %q: %v", raw, err)
	}
	return abs, nil
}

func pathError(code, path string, err error) *view.Error {
	switch {
	case os.IsNotExist(err):
		return view.Errorf(code+".notfound", "no such path: %s", path)
	case os.IsPermission(err):
		return view.Errorf(code+".denied", "cannot read %s: permission denied", path).
			WithHint("read it as a user that can, rather than running the whole tool elevated")
	}
	var pathErr *fs.PathError
	if ok := asPathError(err, &pathErr); ok {
		return view.Errorf(code+".failed", "%s: %v", path, pathErr.Err)
	}
	return view.Errorf(code+".failed", "%s: %v", path, err)
}
