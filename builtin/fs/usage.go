package fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
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
	device   uint64
	maxDepth int
	skipped  int
	// withheld counts rta's own state, which a scan under a root neither
	// enters nor sizes (pathin.WithheldError): left out of the total as skipped
	// entries are, and said apart from them, since it is neither unreadable
	// nor elsewhere.
	withheld int
	largest  []fileSize
	// apparent counts each file by its length, as `du --apparent-size` does;
	// without it a file is the disk it takes (sizeOf).
	apparent bool
	// linked holds the files already counted that have other names, so that a
	// second name for one adds nothing.
	linked map[fileID]struct{}
}

// sizeOf is what a regular file adds to the total.
//
// **The disk the file takes, and each file once.** The question is what is
// using space, and the length of a file is not the answer for the two kinds
// that are usually the answer: a sparse file, a VM image or a database with
// holes, whose length is the whole of the address space it could grow into
// and whose disk use is what was written, and a file with several names
// (pnpm's node_modules, an overlay's lower layers, a backup tool's snapshots),
// each of which was counted in full. A scan that reported a 100 GiB image the
// size of a few megabytes, or a tree three times the disk it fills, ranked
// the wrong things first, which `du`, the tool this stands for, does not.
func (s *scanner) sizeOf(info os.FileInfo) int64 {
	if s.apparent {
		return info.Size()
	}
	size, id, shared := diskUsage(info)
	if shared {
		if _, counted := s.linked[id]; counted {
			return 0
		}
		s.linked[id] = struct{}{}
	}
	return size
}

// fileSize is a file the scan found, by its path from the directory scanned.
type fileSize struct {
	path string
	size int64
}

// keepLargest is how many individual files the scan remembers. The detail
// page shows them, and they are the answer far more often than the directory
// ranking is — one forgotten core dump beats twenty evenly-sized folders.
const keepLargest = 15

func newScanner(root *pathin.Dir, maxDepth int, apparent bool) *scanner {
	s := &scanner{maxDepth: maxDepth, apparent: apparent, linked: map[fileID]struct{}{}}
	if here, err := root.Stat(); err == nil {
		s.device, _ = deviceOfInfo(here)
	}
	return s
}

// descend opens name in dir to scan, counting it as left out when it cannot
// be: rta's own state apart from the rest.
func (s *scanner) descend(dir *pathin.Dir, name string) (*pathin.Dir, bool) {
	sub, err := dir.OpenDir(name)
	var withheld *pathin.WithheldError
	switch {
	case errors.As(err, &withheld):
		s.withheld++
	case err != nil:
		s.skipped++
	}
	return sub, err == nil
}

// withholds reports whether the bounds withhold name, a file in dir that
// info describes, counting it when they do: a file of rta's configuration
// under a root is left out of the total as the directory of its state is.
func (s *scanner) withholds(dir *pathin.Dir, name string, info os.FileInfo) bool {
	if dir.Withheld(name, info) == nil {
		return false
	}
	s.withheld++
	return true
}

// walk totals one directory subtree, returning its size and file count. rel
// is its path from the directory scanned, for the files it remembers.
//
// Through the directory held open (pathin.Dir) rather than by path names: a
// scan that looked at a name and then read the directory by it listed
// whatever a caller who can write in the tree had put at the name since,
// which under a root was a link out.
func (s *scanner) walk(ctx context.Context, dir *pathin.Dir, rel string, depth int) (int64, int) {
	if err := ctx.Err(); err != nil {
		return 0, 0
	}
	items, err := dir.ReadDir()
	if err != nil {
		s.skipped++
		return 0, 0
	}
	var total int64
	var count int
	for _, item := range items {
		full := filepath.Join(rel, item.Name())
		// Lstat, not Stat: a symlink's own size, never its target's.
		info, err := dir.Lstat(item.Name())
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
			sub, ok := s.descend(dir, item.Name())
			if !ok {
				continue
			}
			size, subCount := s.walk(ctx, sub, full, depth+1)
			_ = sub.Close()
			total += size
			count += subCount
		case info.Mode().IsRegular():
			if s.withholds(dir, item.Name(), info) {
				continue
			}
			size := s.sizeOf(info)
			total += size
			count++
			s.remember(full, size)
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
	info, statErr := pathin.Stat(req, path)
	if statErr != nil {
		return nil, pathError("fs.usage", path, statErr)
	}
	if !info.IsDir() {
		return nil, view.Errorf("fs.usage.notadir", "%s is %s, not a directory", path, notADir(info.Mode())).
			WithHint(req.Surface().CapabilityName("fs.hash") + " inspects one file — or pass the directory holding it")
	}
	dir, openErr := pathin.OpenDir(req, path)
	if openErr != nil {
		return nil, pathError("fs.usage", path, openErr)
	}
	defer func() { _ = dir.Close() }()

	s := newScanner(dir, req.Int("depth"), req.Bool("apparent"))
	items, readErr := dir.ReadDir()
	if readErr != nil {
		return nil, pathError("fs.usage", path, readErr)
	}

	entries := make([]entry, 0, len(items))
	var total int64
	for _, item := range items {
		fi, err := dir.Lstat(item.Name())
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
			sub, ok := s.descend(dir, item.Name())
			if !ok {
				continue
			}
			e.size, e.files = s.walk(ctx, sub, item.Name(), 1)
			_ = sub.Close()
		default:
			if s.withholds(dir, item.Name(), fi) {
				continue
			}
			e.size, e.files = fi.Size(), 1
			if fi.Mode().IsRegular() {
				e.size = s.sizeOf(fi)
				s.remember(item.Name(), e.size)
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
	// An empty directory is the table with no rows, not a sentence saying it
	// is empty. The handler cannot see the output format, so the sentence
	// was what every format got: `-o json | jq '.rows[]'` met a text view it
	// was never promised, and -o csv refused one and exited 2, the code for
	// something unexpected, where the empty table prints its header and
	// exits 0. The sentence rides beside the table instead, for a person
	// alone: see usageTable.
	t := usageTable(path, entries, total, req.Int("limit"), s.skipped+s.withheld)
	// **The ranking and the total are built from what could be read, and the
	// compact form never said so.** The detail page has reported `skipped`
	// all along; the table somebody actually looks at presented a share of a
	// total that silently excluded every unreadable or cross-filesystem
	// subtree — so "this directory is 95% of the tree" was a percentage of a
	// smaller tree than the one on screen.
	if left := s.skipped + s.withheld; left > 0 {
		why := "unreadable, or on another filesystem"
		if s.withheld > 0 {
			why = "unreadable, on another filesystem, or rta's own state"
		}
		t.Warnings = append(t.Warnings, view.Error{
			Code: "fs.usage.partial",
			Message: format.CountOf(left, "entry") +
				" could not be counted, so the sizes and shares here are of what was read",
			Hint: why + " — " + req.Surface().InputName("detail") + " breaks the scan down",
		})
	}
	return t, nil
}

// usageTable ranks the entries under path. With none, it carries the sentence
// a person is shown in place of an empty grid, which reads as a scan that
// failed (view.Table.Empty) — "empty" only when nothing was skipped, since a
// directory none of whose entries could be counted is not an empty one, and
// the warning under the table says why.
func usageTable(path string, entries []entry, total int64, limit, skipped int) view.Table {
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
		name := textclean.Name(e.name)
		if e.dir {
			name += "/"
		}
		t.Rows = append(t.Rows, []string{
			name, humanBytes(e.size), share(e.size, total), fmt.Sprintf("%d", e.files),
		})
	}
	t.Total = len(entries)
	if len(entries) == 0 {
		t.Empty = path + " is empty."
		if skipped > 0 {
			t.Empty = "Nothing under " + path + " could be counted."
		}
	}
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
		{Key: "contents", Value: format.CountOf(len(entries), "entry") + " · " +
			format.CountOf(files, "file") + " beneath · " + format.CountOf(dirs, "directory")},
	}
	if s.skipped > 0 {
		// Never silently. A total computed over an unknown fraction of a tree
		// looks exactly like a correct one.
		summary = append(summary, view.Pair{
			Key:   "skipped",
			Value: format.CountOf(s.skipped, "entry") + " — unreadable, or on another filesystem",
		})
	}
	if s.withheld > 0 {
		summary = append(summary, view.Pair{
			Key:   "withheld",
			Value: format.CountOf(s.withheld, "entry") + " of rta's own state, which no agent may look into",
		})
	}

	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", view.KeyValue{Pairs: summary})
	p.PutAs("entries", "biggest entries", usageTable(path, entries, total, req.Int("limit"), s.skipped+s.withheld))

	if len(s.largest) > 0 {
		lt := view.Table{Columns: []view.Column{{Name: "File"}, {Name: "Size", Kind: view.KindBytes}}}
		for _, f := range s.largest {
			lt.Rows = append(lt.Rows, []string{textclean.Name(f.path), humanBytes(f.size)})
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

// humanBytes formats a size the way a person reads one: binary units, because
// that is what every other size in this tool reports.
func humanBytes(n int64) string { return format.Bytes(n) }

// resolvePath expands ~ and makes the path absolute, so that every message
// names the same thing the caller would name.
func resolvePath(raw string) (string, *view.Error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		p = "."
	}
	p = pathguard.ExpandTilde(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", view.Errorf("fs.path", "resolving %q: %v", raw, err)
	}
	return abs, nil
}

// notADir names what a path that is not a directory is, for the refusal
// that says so: a file, or what pathin calls anything else. "is a file" was
// said of a named pipe and a socket as well, which are not.
func notADir(m os.FileMode) string {
	if m.IsRegular() {
		return "a file"
	}
	return pathin.Kind(m)
}

func pathError(code, path string, err error) *view.Error {
	// A refusal of the call's bounds (pathin) says what it is, and which
	// root it is about, better than a sentence about reading could.
	var refused *view.Error
	if errors.As(err, &refused) {
		return refused
	}
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
