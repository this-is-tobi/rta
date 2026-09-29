package fs

import (
	"context"
	"crypto/md5"  //nolint:gosec // a hash column for a listing, never a security decision
	"crypto/sha1" //nolint:gosec // same: fs.hash offers the algorithms people compare against, md5 and sha1 included
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/pkg/format"
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
		surface:  req.Surface(),
		target:   req.LinkTarget,
	}
	if dev, ok := deviceOf(path); ok {
		b.device = dev
	}
	children := b.children(ctx, path, 1)
	// **A walk the deadline cut short is not a tree.** children returns nil
	// on cancellation with no marker of its own, and nothing looked again —
	// so a timeout partway through produced a normally-shaped, apparently
	// complete view of a directory nobody finished reading. fs.usage has
	// made this exact check since it was written.
	if err := ctx.Err(); err != nil {
		return nil, view.Errorf("fs.tree.cancelled", "walk of %s was interrupted", path)
	}
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
	// surface is the caller's, for the name a branch's "12 hidden" marker
	// gives the input that shows them.
	surface plugin.Surface
	// target is what a link's detail may say it holds (Request.LinkTarget).
	//
	// A link's text is a name, and over MCP it was shown whatever it named:
	// a link inside the root holding /outside/hop told an agent confined to
	// the root what lies outside it — the name the bridge withholds when
	// the same link is the path an agent gives (a neutral phrase instead,
	// which says the link leads out without saying where). The surface
	// answers by its own rule, so the two cannot drift: a target naming
	// only places under the roots, and every target at a terminal, is shown
	// as written.
	target func(dir, target string) string
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
			} else if b.target != nil {
				target = b.target(dir, target)
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
				switch n, counted := countEntries(full, b.hidden); {
				case !counted:
					b.stats.unreadable++
					node.Detail = "unreadable"
				case n > 0:
					b.stats.notDescended++
					b.stats.beyond += n
					node.Detail = format.CountOf(n, "entry")
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
		nodes = append(nodes, view.Node{Label: "…", Detail: fmt.Sprintf("%d hidden (%s)", hiddenCount, b.surface.InputName("all"))})
	}
	return nodes
}

// countEntries says how many entries sit inside a directory the walk stopped
// at, and whether it could find out at all.
//
// **"I could not count these" is not "there are none".** The bool used to be
// a 0, and a directory at the --depth boundary this user cannot read got no
// detail at all — rendering as a bare `name/`, which is exactly how an empty
// directory renders. The whole reason the boundary reports a count is that
// not descending is not the same as being empty; answering 0 for an
// unreadable one handed back the confusion the count exists to remove.
func countEntries(dir string, includeHidden bool) (int, bool) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	if includeHidden {
		return len(items), true
	}
	n := 0
	for _, item := range items {
		if !strings.HasPrefix(item.Name(), ".") {
			n++
		}
	}
	return n, true
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
	// An expect with no checksum in it is refused rather than skipped. The
	// comparison is the point of the capability, and a skipped one leaves a
	// hash and no verdict, which reads as a check nobody failed: the ordinary
	// way to get here is "sha256:$SUM" with the variable unset, the moment a
	// script meant to stop on a mismatch.
	rawExpect := req.String("expect")
	expect := normalizeChecksum(rawExpect)
	if expect == "" && strings.TrimSpace(rawExpect) != "" {
		return nil, view.Errorf("fs.hash.expect", "%q holds no checksum to compare against", rawExpect).
			WithHint(req.Surface().InputName("expect") + " takes the checksum itself, bare or as shasum prints it")
	}

	// Stat'ed first for the refusal a directory gets, which names the
	// capability that measures one. What is opened is judged again by
	// pathin, which opens only a regular file off the CLI: a named pipe
	// opened blocking held this call, and an OS thread, for good. Both
	// through pathin, which under a root opens from the root rather than
	// by the name a caller could have swapped for a link out since.
	info, err := pathin.Stat(req, path)
	if err != nil {
		return nil, pathError("fs.hash", path, err)
	}
	if info.IsDir() {
		return nil, view.Errorf("fs.hash.isdir", "%s is a directory", path).
			WithHint("hash a file; " + req.Surface().CapabilityName("fs.usage") + " measures a directory")
	}

	f, info, err := pathin.Open(req, path)
	var notAFile *pathin.NotAFileError
	switch {
	case errors.As(err, &notAFile):
		return nil, view.Errorf("fs.hash.notafile", "%v", err).
			WithHint("name a regular file to hash")
	case err != nil:
		return nil, pathError("fs.hash", path, err)
	}
	defer func() { _ = f.Close() }()

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
	if expect != "" {
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
//
// The checksum is the first word, taken before anything else is read: the
// algorithm prefix used to be cut at the first colon in the whole line, so
// `<hash>  report:v2.txt` — shasum's own output for a file with a colon in
// its name — compared "v2.txt" against the hash and reported a correct file
// as not the described one. A prefix set off by a space, "sha256: <hash>",
// is a first word ending in the colon, and the hash is the word after it.
func normalizeChecksum(raw string) string {
	words := strings.Fields(raw)
	if len(words) == 0 {
		return ""
	}
	s := words[0]
	if strings.HasSuffix(s, ":") && len(words) > 1 {
		s = words[1]
	}
	if _, after, found := strings.Cut(s, ":"); found {
		s = after
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

	shown := format.CountOf(s.dirs, "directory") + " · " + format.CountOf(s.files, "file")
	if s.links > 0 {
		shown += " · " + format.CountOf(s.links, "symlink")
	}
	limit := "all entries"
	if b.limit > 0 {
		limit = "up to " + format.CountOf(b.limit, "entry")
	}
	summary := []view.Pair{
		{Key: "path", Value: path},
		{Key: "showing", Value: shown},
		{Key: "bounded by", Value: fmt.Sprintf("%s, %s per directory", format.CountOf(b.maxDepth, "level"), limit)},
	}

	var missing []view.Pair
	if s.beyond > 0 {
		missing = append(missing, view.Pair{
			Key: "below depth",
			Value: fmt.Sprintf("%s in %s the walk stopped at",
				format.CountOf(s.beyond, "entry"), format.CountOf(s.notDescended, "directory")),
		})
	}
	if s.truncated > 0 {
		missing = append(missing, view.Pair{
			Key:   "past limit",
			Value: format.CountOf(s.truncated, "entry") + " trimmed from the directories that hold more",
		})
	}
	if s.hidden > 0 {
		missing = append(missing, view.Pair{
			Key:   "hidden",
			Value: format.CountOf(s.hidden, "dotfile") + " — " + req.Surface().InputName("all") + " includes them",
		})
	}
	if s.otherFS > 0 {
		missing = append(missing, view.Pair{
			Key: "another filesystem",
			Value: format.CountOf(s.otherFS, "mount point") +
				", not crossed — so a scan cannot wander onto a network mount",
		})
	}
	if s.unreadable > 0 {
		missing = append(missing, view.Pair{
			Key:   "unreadable",
			Value: format.CountOf(s.unreadable, "entry") + " this user cannot read",
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
