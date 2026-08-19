package fs

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func runTree(ctx context.Context, req plugin.Request) (view.View, error) {
	path, err := resolvePath(req.String("path"))
	if err != nil {
		return nil, err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, pathError("fs.tree", path, statErr)
	}
	if !info.IsDir() {
		return nil, view.Errorf("fs.tree.notadir", "%s is a file, not a directory", path).
			WithHint("pass the directory holding it")
	}

	depth := req.Int("depth")
	if depth < 1 {
		depth = 1
	}
	b := &treeBuilder{
		maxDepth: depth,
		limit:    req.Int("limit"),
		hidden:   req.Bool("all"),
	}
	if dev, ok := deviceOf(path); ok {
		b.device = dev
	}
	children := b.children(ctx, path, 1)
	root := view.Node{
		Label:    filepath.Base(path) + "/",
		Detail:   path,
		Children: children,
	}
	return view.Tree{Roots: []view.Node{root}}, nil
}

type treeBuilder struct {
	maxDepth int
	limit    int
	hidden   bool
	device   uint64
}

func (b *treeBuilder) sameDevice(info os.FileInfo) bool {
	if b.device == 0 {
		return true
	}
	dev, ok := deviceOfInfo(info)
	if !ok {
		return true
	}
	return dev == b.device
}

// children lists one directory. A branch cut short by --depth or --limit says
// so in its own label: a tree that quietly stopped listing looks exactly like
// a directory that is empty, and that is a lie about the filesystem.
func (b *treeBuilder) children(ctx context.Context, dir string, depth int) []view.Node {
	if err := ctx.Err(); err != nil {
		return nil
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return []view.Node{{Label: "…", Detail: "unreadable: " + reason(err)}}
	}

	kept := make([]os.DirEntry, 0, len(items))
	for _, item := range items {
		if !b.hidden && strings.HasPrefix(item.Name(), ".") {
			continue
		}
		kept = append(kept, item)
	}
	hiddenCount := len(items) - len(kept)

	// Directories first, then by name: the order a person reads a listing in,
	// and the order every file browser has used for thirty years.
	sort.SliceStable(kept, func(i, j int) bool {
		di, dj := kept[i].IsDir(), kept[j].IsDir()
		if di != dj {
			return di
		}
		return kept[i].Name() < kept[j].Name()
	})

	shown := kept
	truncated := 0
	if b.limit > 0 && len(shown) > b.limit {
		truncated = len(shown) - b.limit
		shown = shown[:b.limit]
	}

	nodes := make([]view.Node, 0, len(shown)+2)
	for _, item := range shown {
		full := filepath.Join(dir, item.Name())
		info, err := os.Lstat(full)
		if err != nil {
			nodes = append(nodes, view.Node{Label: item.Name(), Detail: "unreadable"})
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				target = "?"
			}
			nodes = append(nodes, view.Node{Label: item.Name(), Detail: "→ " + target})
		case info.IsDir():
			node := view.Node{Label: item.Name() + "/"}
			switch {
			case !b.sameDevice(info):
				node.Detail = "another filesystem"
			case depth >= b.maxDepth:
				// Not descending is not the same as being empty, and the
				// difference is the whole reason to say how many are down there.
				if n := countEntries(full, b.hidden); n > 0 {
					node.Detail = fmt.Sprintf("%d entries", n)
				}
			default:
				node.Children = b.children(ctx, full, depth+1)
			}
			nodes = append(nodes, node)
		default:
			nodes = append(nodes, view.Node{Label: item.Name(), Detail: humanBytes(info.Size())})
		}
	}
	if truncated > 0 {
		nodes = append(nodes, view.Node{Label: "…", Detail: fmt.Sprintf("%d more", truncated)})
	}
	if hiddenCount > 0 {
		nodes = append(nodes, view.Node{Label: "…", Detail: fmt.Sprintf("%d hidden (--all)", hiddenCount)})
	}
	return nodes
}

func countEntries(dir string, includeHidden bool) int {
	items, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	if includeHidden {
		return len(items)
	}
	n := 0
	for _, item := range items {
		if !strings.HasPrefix(item.Name(), ".") {
			n++
		}
	}
	return n
}

func reason(err error) string {
	if os.IsPermission(err) {
		return "permission denied"
	}
	return err.Error()
}

// hashers are the algorithms worth offering. sha1 and md5 are here because
// they are still what a great many projects publish, and refusing to check a
// checksum somebody actually has helps nobody — the output says what they are
// good for.
var hashers = map[string]func() hash.Hash{
	"sha256": sha256.New,
	"sha512": sha512.New,
	"sha1":   sha1.New,
	"md5":    md5.New,
}

// weakHashes are fine for detecting accidental corruption and useless against
// anybody who wants the file to match.
var weakHashes = map[string]bool{"sha1": true, "md5": true}

func runHash(ctx context.Context, req plugin.Request) (view.View, error) {
	path, pathErr := resolvePath(req.String("path"))
	if pathErr != nil {
		return nil, pathErr
	}
	algo := strings.ToLower(strings.TrimSpace(req.String("algo")))
	if algo == "" {
		algo = "sha256"
	}
	newHash, ok := hashers[algo]
	if !ok {
		names := make([]string, 0, len(hashers))
		for k := range hashers {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, view.Errorf("fs.hash.algo", "unknown algorithm %q", algo).
			WithHint("one of: " + strings.Join(names, ", "))
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, pathError("fs.hash", path, err)
	}
	if info.IsDir() {
		return nil, view.Errorf("fs.hash.isdir", "%s is a directory", path).
			WithHint("hash a file; use fs usage to measure a directory")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, pathError("fs.hash", path, err)
	}
	defer f.Close()

	h := newHash()
	if _, err := io.Copy(h, readerWithContext(ctx, f)); err != nil {
		return nil, view.Errorf("fs.hash.read", "reading %s: %v", path, err)
	}
	sum := hex.EncodeToString(h.Sum(nil))

	pairs := []view.Pair{
		{Key: "file", Value: path},
		{Key: "size", Value: humanBytes(info.Size())},
		{Key: algo, Value: sum},
	}
	if expect := normalizeChecksum(req.String("expect")); expect != "" {
		// The point of the capability. Comparing two 64-character hex strings
		// by eye is a task humans are measurably bad at, and the failure is
		// silent.
		if expect == sum {
			pairs = append(pairs, view.Pair{Key: "match", Value: "yes — the file is the one described"})
		} else {
			pairs = append(pairs,
				view.Pair{Key: "match", Value: "NO — this is not the described file"},
				view.Pair{Key: "expected", Value: expect})
		}
	}
	if weakHashes[algo] {
		pairs = append(pairs, view.Pair{Key: "note", Value: algo +
			" detects accidental corruption; it does not detect a file somebody wanted to match"})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// normalizeChecksum takes a checksum as it was pasted: any case, wrapped in
// whitespace, possibly carrying its algorithm prefix, possibly followed by
// the filename the way shasum prints it.
func normalizeChecksum(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if _, after, found := strings.Cut(s, ":"); found {
		s = strings.TrimSpace(after)
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i] // "abc123  filename" — the shasum output format
	}
	return strings.ToLower(strings.TrimPrefix(s, "*"))
}

// readerWithContext lets a hash of a very large file be interrupted, rather
// than ignoring cancellation until the read finishes.
func readerWithContext(ctx context.Context, r io.Reader) io.Reader {
	return &ctxReader{ctx: ctx, r: r}
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
