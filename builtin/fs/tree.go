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

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
	tree := view.Tree{Roots: []view.Node{root}}
	if req.Bool("detail") {
		return treeDetail(ctx, req, path, tree, b), nil
	}
	return tree, nil
}

type treeBuilder struct {
	maxDepth int
	limit    int
	hidden   bool
	device   uint64
	stats    treeStats
}

// treeStats is what the walk learned on its way past. The compact tree says
// each of these in the branch it happened in — "3 more", "12 hidden (--all)"
// — which is the right place to read it while looking at that branch and the
// wrong place to answer "am I looking at the whole directory". A person who
// asks for the detail page is asking the second question.
type treeStats struct {
	dirs, files, links int
	hidden             int // skipped for being dotfiles
	truncated          int // cut by --limit
	notDescended       int // directories at --depth
	beyond             int // entries inside those
	unreadable         int
	otherFS            int
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
		b.stats.unreadable++
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
	b.stats.hidden += hiddenCount

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
		b.stats.truncated += truncated
	}

	nodes := make([]view.Node, 0, len(shown)+2)
	for _, item := range shown {
		full := filepath.Join(dir, item.Name())
		info, err := os.Lstat(full)
		if err != nil {
			b.stats.unreadable++
			nodes = append(nodes, view.Node{Label: item.Name(), Detail: "unreadable"})
			continue
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			b.stats.links++
			target, err := os.Readlink(full)
			if err != nil {
				target = "?"
			}
			nodes = append(nodes, view.Node{Label: item.Name(), Detail: "→ " + target})
		case info.IsDir():
			b.stats.dirs++
			node := view.Node{Label: item.Name() + "/"}
			switch {
			case !b.sameDevice(info):
				b.stats.otherFS++
				node.Detail = "another filesystem"
			case depth >= b.maxDepth:
				// Not descending is not the same as being empty, and the
				// difference is the whole reason to say how many are down there.
				if n := countEntries(full, b.hidden); n > 0 {
					b.stats.notDescended++
					b.stats.beyond += n
					node.Detail = fmt.Sprintf("%d entries", n)
				}
			default:
				node.Children = b.children(ctx, full, depth+1)
			}
			nodes = append(nodes, node)
		default:
			b.stats.files++
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

// treeDetail is fs.tree with the whole screen: the same tree, plus the two
// things a bounded walk owes the person reading it.
//
// The first is what the tree is a sample of. Every other plugin's dashboard
// tile expands into a composed page and this one did not, which left fs as
// the only tile where opening it full-screen showed exactly what the tile
// already showed.
//
// The second is what is missing, gathered into one place. The compact tree
// already says "3 more" and "12 hidden (--all)" in the branch each applies
// to, and that is the right place to read it while looking at that branch.
// It is the wrong place to answer "is this the whole directory", because
// answering that means finding every one of those markers first — and the
// markers for depth, permissions and filesystem boundaries look nothing
// alike. A reader who does not find them concludes the tree is complete,
// which is the one conclusion a bounded walk must never invite.
//
// It composes fs.usage, which an earlier version of this comment argued
// against, and the argument was wrong in a specific way worth recording: it
// treated NoPreview as "never run this from anywhere". NoPreview means do not
// run me *unprompted* — the dashboard refreshes on a timer, and a recursive
// scan every cycle is a real cost nobody asked for. A detail page is the
// opposite case. Somebody pressed enter.
//
// The objection underneath it was sound, and is answered by passing the bound
// rather than by dropping the section: fs.usage descends until maxDepth, so
// handing it this request's own --depth makes its walk the same shape as the
// walk that just happened. What would have been wrong is composing it
// unbounded.
//
// The rest comes from the walk that already happened, so the page costs one
// extra scan of a subtree already visited, not a scan of the world.
func treeDetail(ctx context.Context, req plugin.Request, path string, tree view.Tree, b *treeBuilder) view.View {
	s := b.stats

	shown := fmt.Sprintf("%d directories · %d files", s.dirs, s.files)
	if s.links > 0 {
		shown += fmt.Sprintf(" · %d symlinks", s.links)
	}
	limit := "all entries"
	if b.limit > 0 {
		limit = fmt.Sprintf("up to %d entries", b.limit)
	}
	summary := []view.Pair{
		{Key: "path", Value: path},
		{Key: "showing", Value: shown},
		{Key: "bounded by", Value: fmt.Sprintf("%s, %s per directory", levels(b.maxDepth), limit)},
	}

	var missing []view.Pair
	if s.beyond > 0 {
		missing = append(missing, view.Pair{
			Key: "below --depth",
			Value: fmt.Sprintf("%d entries in %d directories the walk stopped at",
				s.beyond, s.notDescended),
		})
	}
	if s.truncated > 0 {
		missing = append(missing, view.Pair{
			Key:   "past --limit",
			Value: fmt.Sprintf("%d entries trimmed from the directories that hold more", s.truncated),
		})
	}
	if s.hidden > 0 {
		noun := "dotfiles"
		if s.hidden == 1 {
			noun = "dotfile"
		}
		missing = append(missing, view.Pair{
			Key:   "hidden",
			Value: fmt.Sprintf("%d %s — pass --all to include them", s.hidden, noun),
		})
	}
	if s.otherFS > 0 {
		missing = append(missing, view.Pair{
			Key: "another filesystem",
			Value: fmt.Sprintf("%d mount points, not crossed — so a scan cannot wander onto a network mount",
				s.otherFS),
		})
	}
	if s.unreadable > 0 {
		missing = append(missing, view.Pair{
			Key:   "unreadable",
			Value: fmt.Sprintf("%d entries this user cannot read", s.unreadable),
		})
	}

	// PutAs rather than Put throughout: Put leaves the section id empty, and
	// the id is the handle a script or an agent addresses a section by now
	// that it is emitted in JSON. The title is prose and free to change; an
	// id derived from it would not be stable, which is why Page makes this
	// opt-in rather than deriving one.
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", view.KeyValue{Pairs: summary})
	p.PutAs("tree", "tree", tree)
	if len(missing) == 0 {
		// Stated rather than left to an absent heading: "complete" and "this
		// build forgot to count" render identically as nothing at all.
		p.PutAs("not-shown", "not shown", view.Text{Body: "Nothing — this is every entry under " + path + "."})
	} else {
		p.PutAs("not-shown", "not shown", view.KeyValue{Pairs: missing})
	}
	// Bounded to this request's own depth, so the section answers "what is
	// using the space in what I am looking at" rather than starting an
	// unbounded scan from a keypress. Its own limit, because usage ranks
	// biggest-first and a tree's per-directory entry cap means something else.
	p.AddAs("largest", "largest entries", runUsage, plugin.Read, map[string]any{
		"path":  path,
		"depth": req.Int("depth"),
		"limit": detailUsageTop,
	})
	return p.View()
}

// detailUsageTop bounds the biggest-entries section: enough to show where the
// space went, short enough that the tree above it stays on the page.
const detailUsageTop = 10

func levels(n int) string {
	if n == 1 {
		return "1 level"
	}
	return fmt.Sprintf("%d levels", n)
}
