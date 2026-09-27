package git

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func blameCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.blame",
		Summary:      "Who last touched each line of a file, and when",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "One row per line: who last changed it, when, and the commit that did — the " +
			"structured equivalent of `git blame`. Reads the file's history to answer, and refuses " +
			"a version of it over 16 MiB, or a history of more than 64 MiB in all, rather than read " +
			"it. It spends at most two seconds on that history: a line it has not traced by then " +
			"carries the commit it had reached, marked ^ as git marks a boundary, meaning the line " +
			"is at least that old. So it is not offered as a dashboard tile the way a bounded, " +
			"no-input capability would be.",
		NoPreview: true,
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "file", Type: plugin.Path, Positional: true, Required: true, Help: fileHelp("the file to blame")},
		},
		Run: runBlame,
	}
}

func runBlame(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()
	head, err := repo.Head()
	if err != nil {
		return nil, view.Errorf("git.blame.nohead", "no commit to blame from: %v", err).
			WithHint("an empty repository has no history yet")
	}
	// Through a store that bounds what the blame reads, so that the whole of
	// the history it walks is held to it, and not only HEAD (boundedHistory).
	store := &boundedHistory{EncodedObjectStorer: repo.Storer}
	commit, err := object.GetCommit(store, head.Hash())
	if err != nil {
		return nil, view.Errorf("git.blame.failed", "reading HEAD commit: %v", err)
	}

	file, verr := repoFile(repo, req.String("file"), req.Surface(), req.Surface().ArgumentName("file"))
	if verr != nil {
		return nil, verr
	}
	// Put to the gate where git would check it out, as a diff's files are
	// (pathGate). In a checkout that is where the boundary judged it already;
	// in a repository with no working tree the boundary judged it from the
	// current directory, where no such file is, and a dotfiles repository
	// blamed rta's own identity line by line.
	if verr := pathGate(req, repo)(file); verr != nil {
		return nil, verr
	}
	// Held to the bound a diff holds one file to. Blame reads the file whole
	// at every commit that touched it and answers a row per line: a 100 MB
	// file cost one ungated call 3.2 GB and a quarter of a gigabyte of
	// answer, from a file a caller need only have committed.
	toolarge := func(msg string, args ...any) *view.Error {
		return view.Errorf("git.blame.toolarge", msg, args...).
			WithHint(req.Surface().CapabilityWith("git.log", "file") + " names the commits that touched it")
	}
	if tree, terr := commit.Tree(); terr == nil {
		if size, serr := tree.Size(file); serr == nil && size > maxDiffBytes {
			return nil, toolarge("%s is %s at HEAD, larger than the %d MiB this blames",
				file, format.Bytes(size), maxDiffBytes>>20)
		}
	}
	result, err := blame(ctx, store, commit, file, matchDeadline(ctx))
	var over *historyTooLarge
	switch {
	case ctx.Err() != nil:
		return nil, view.Errorf("git.blame.cancelled", "the blame of %s was interrupted", file)
	case errors.As(err, &over):
		return nil, toolarge("%s: blaming it reads %s", file, over)
	case err != nil:
		return nil, view.Errorf("git.blame.failed", "%s: %v", file, err).
			WithHint("the file must be tracked at HEAD: a new one has no history to blame until it is committed")
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Line", Kind: view.KindNumber},
		{Name: "Hash"},
		{Name: "Author"},
		{Name: "Date", Kind: view.KindTimestamp},
		{Name: "Content"},
	}, Empty: file + " is empty at HEAD"}
	boundary := 0
	for i, l := range result.lines {
		hash := shortHash(l.commit.Hash)
		if l.boundary {
			hash = "^" + hash
			boundary++
		}
		t.Rows = append(t.Rows, []string{
			strconv.Itoa(i + 1),
			hash,
			l.commit.Author.Name,
			l.commit.Author.When.Format("2006-01-02 15:04"),
			result.text(i),
		})
	}
	t.Total = len(t.Rows)
	if boundary > 0 {
		t.Warnings = append(t.Warnings, view.Error{
			Code: "git.blame.partial",
			Message: fmt.Sprintf("%s the walk had not traced after %v carry the commit it had reached, "+
				"marked ^: each is at least that old, and may be older", format.CountOf(boundary, "line"), matchTime),
			Hint: req.Surface().CapabilityWith("git.log", "file") + " names the commits that touched it",
		})
	}
	return t, nil
}

// boundedHistory is the object store a blame reads the repository through,
// which holds what it reads of blobs to a budget.
//
// **A blame reads more of the history than the file at HEAD.** It reads the
// file whole at every commit that changed it, so a version that was 30 MB
// and is 37 bytes at HEAD was read whole, past the check on HEAD, and cost a
// two-line blame 346 MB. And where the file was added or renamed, the blame
// compares that whole commit with its parent to look for the rename, reading
// every file it deleted or added — so a file added beside three logs just
// under the per-file bound cost a one-line blame 342 MB. Neither is a file
// anybody asked about, and a repository is content an agent may have been
// handed rather than written.
//
// So each blob is held to maxDiffBytes, the bound git.diff holds one file
// to, and everything the walk reads to maxBlameBytes. The walk was once held
// step by step instead, a commit's worth of reads at a time, and that left
// its length free: ten versions of a file each just under the per-file
// bound, every one read whole and line-diffed against the next, cost one
// call over half a gigabyte and six minutes of CPU, each step within bounds.
// A blob is counted each time the walk asks the store for it, which rename
// detection does more than once for the same file, so the count errs on the
// side of reading less.
//
// And each read looks at the call's deadline, since what rename detection
// costs is not the bytes it reads alone: a file added beside twenty thousand
// files of a few bytes moved and rewritten is four hundred million pairs,
// and one blame of it was still running after two minutes. The walk stops
// at the read the deadline passes (errPastDeadline), as it stops between
// commits, and renameLimit keeps the pairs to git's own number.
type boundedHistory struct {
	storer.EncodedObjectStorer
	read     int64
	deadline time.Time
}

// errPastDeadline is a blame reading its history past the call's deadline.
var errPastDeadline = errors.New("the blame ran past its time")

// maxBlameBytes bounds what one blame reads of the history in all: the
// versions of its file, and the files changed beside it where it was added
// or renamed. The budget git.diff holds a whole commit to, since a blame is
// a diff of every version it walks against the next. A file of a hundred
// kilobytes is blamed through a few hundred versions under it, counted as
// boundedHistory counts them; past it, git.log names the commits and
// git.diff shows each. A variable so a test can lower it.
var maxBlameBytes int64 = 64 << 20

func (s *boundedHistory) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	if t == plumbing.BlobObject {
		if !s.deadline.IsZero() && time.Now().After(s.deadline) {
			return nil, errPastDeadline
		}
		if size, err := s.EncodedObjectSize(h); err == nil {
			if size > maxDiffBytes {
				return nil, &historyTooLarge{size: size}
			}
			if s.read += size; s.read > maxBlameBytes {
				return nil, &historyTooLarge{}
			}
		}
	}
	return s.EncodedObjectStorer.EncodedObject(t, h)
}

// historyTooLarge is a blob past the bound boundedHistory holds each one to,
// or, with no size, a walk past its budget, worded to follow "blaming it
// reads".
type historyTooLarge struct {
	size int64
}

func (e *historyTooLarge) Error() string {
	if e.size == 0 {
		return fmt.Sprintf("more than the %s one blame reads in all, from the versions of it and the "+
			"files changed where it was added or renamed", format.Bytes(maxBlameBytes))
	}
	return fmt.Sprintf("%s from its history — a version of it, or a file changed where it was added or "+
		"renamed — larger than the %d MiB this reads of one file", format.Bytes(e.size), maxDiffBytes>>20)
}

// blamed is the commit a line of the blamed file is attributed to; boundary
// says the walk stopped there before it had traced the line any further, so
// that the line is at least as old as the commit and may be older.
type blamed struct {
	commit   *object.Commit
	boundary bool
}

// blameResult is a line of the file for each line of it, and who it is
// attributed to.
type blameResult struct {
	content string
	at      []int
	lines   []blamed
}

// text is line i of the blamed file as a row shows it, without its newline.
func (r *blameResult) text(i int) string {
	return strings.TrimSuffix(r.content[r.at[i]:r.at[i+1]], "\n")
}

// suspect is a line of the blamed file not yet attributed, by its number
// there and in the version of the file the commit it is waiting at holds.
type suspect struct {
	final, line int
}

// origin is a version of the blamed file some of its lines are waiting at:
// the commit, the file's path there, and its content.
type origin struct {
	commit   *object.Commit
	path     string
	content  string
	suspects []suspect
}

// blame attributes each line of path at head to the commit that last changed
// it, by git's own rule of passing the blame back (blame.c's pass_blame):
// the lines of a version wait at its commit; if a parent holds the file
// unchanged, every line passes to the first such parent; otherwise each
// parent in turn takes the lines its version shares, matched as a diff
// matches them, and the lines no parent takes were written here. A parent
// that holds no file of that name is asked for one it was renamed from.
// Commits are taken newest first, and the lines waiting at one commit
// through several children are traced together.
//
// **rta's own, and not go-git's Blame, because that had no bound on its
// time.** It matched each version against the next with utils/diff.Do,
// under a one-hour timeout, and compared every commit the file was added or
// renamed in with its parent as a whole patch, every file of it line-diffed
// to find one rename: three versions of a 10 MiB file, inside every byte
// budget, kept one call busy for more than ten minutes. Here the matching
// is diffLines under the call's deadline, the rename search compares trees
// without diffing any file, and the walk looks at the deadline and the
// caller's context before each commit. Past the deadline, or where a
// version could not be matched in time, the lines still waiting are
// attributed to the commit they wait at, as a boundary: at least that old.
func blame(ctx context.Context, store *boundedHistory, head *object.Commit, path string, deadline time.Time) (
	*blameResult, error,
) {
	file, err := head.File(path)
	if err != nil {
		return nil, err
	}
	content, err := file.Contents()
	if err != nil {
		return nil, err
	}
	// The store's reads are held to the deadline once the version at HEAD
	// is read, which a blame cannot answer without: past it already, every
	// line is at least as old as HEAD, and says so.
	store.deadline = deadline
	result := &blameResult{content: content, at: splitLines(content)}
	result.lines = make([]blamed, len(result.at)-1)
	first := &origin{commit: head, path: path, content: content}
	for i := range result.lines {
		first.suspects = append(first.suspects, suspect{final: i, line: i})
	}
	w := &blameWalk{store: store, deadline: deadline, result: result, waiting: map[originKey]*origin{}}
	w.push(first)
	for w.queue.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		o := heap.Pop(&w.queue).(*origin)
		delete(w.waiting, originKey{o.commit.Hash, o.path})
		if time.Now().After(deadline) {
			w.stop(o)
			break
		}
		finished, err := w.pass(ctx, o)
		if errors.Is(err, errPastDeadline) {
			finished, err = false, nil
		}
		if err != nil {
			return nil, err
		}
		if !finished {
			w.stop(o)
			break
		}
	}
	return result, nil
}

// originKey is what makes two origins the same version: a commit and a path.
type originKey struct {
	commit plumbing.Hash
	path   string
}

type blameWalk struct {
	store    storer.EncodedObjectStorer
	deadline time.Time
	result   *blameResult
	queue    originQueue
	waiting  map[originKey]*origin
}

// push queues an origin, or adds its lines to the same version already
// queued, so that a version is read and matched once however many children
// hand it lines. A version no line waits at is not walked through: an empty
// file has nothing to blame on anybody.
func (w *blameWalk) push(o *origin) {
	if len(o.suspects) == 0 {
		return
	}
	key := originKey{o.commit.Hash, o.path}
	if queued, ok := w.waiting[key]; ok {
		queued.suspects = mergeSuspects(queued.suspects, o.suspects)
		return
	}
	w.waiting[key] = o
	heap.Push(&w.queue, o)
}

// attribute gives each of o's lines to commit.
func (w *blameWalk) attribute(o *origin, commit *object.Commit, boundary bool) {
	for _, s := range o.suspects {
		w.result.lines[s.final] = blamed{commit: commit, boundary: boundary}
	}
}

// stop ends the walk at o: its lines and every line still waiting anywhere
// are attributed, as a boundary, to the commit they wait at.
func (w *blameWalk) stop(o *origin) {
	w.attribute(o, o.commit, true)
	for w.queue.Len() > 0 {
		rest := heap.Pop(&w.queue).(*origin)
		w.attribute(rest, rest.commit, true)
	}
}

// parentVersion is a parent of an origin's commit and the file's path and
// blob there.
type parentVersion struct {
	commit *object.Commit
	path   string
	blob   plumbing.Hash
}

// pass hands o's lines to its parents, and attributes to o's commit the ones
// none of them takes. finished is false when a version could not be matched
// before the deadline, in which case nothing was handed or attributed.
func (w *blameWalk) pass(ctx context.Context, o *origin) (finished bool, _ error) {
	parents, err := w.parentVersions(ctx, o)
	if err != nil {
		return false, err
	}
	tree, err := o.commit.Tree()
	if err != nil {
		return false, err
	}
	own, err := tree.FindEntry(o.path)
	if err != nil {
		return false, err
	}
	for _, p := range parents {
		if p.blob == own.Hash {
			w.push(&origin{commit: p.commit, path: p.path, content: o.content, suspects: o.suspects})
			return true, nil
		}
	}
	left := o.suspects
	var handed []*origin
	for _, p := range parents {
		if len(left) == 0 {
			break
		}
		prev, err := p.commit.File(p.path)
		if err != nil {
			return false, err
		}
		content, err := prev.Contents()
		if err != nil {
			return false, err
		}
		lines := diffLines(content, o.content, w.deadline)
		if lines.cut {
			return false, nil
		}
		taken := &origin{commit: p.commit, path: p.path, content: content}
		left = takeShared(lines.runs, left, &taken.suspects)
		if len(taken.suspects) > 0 {
			handed = append(handed, taken)
		}
	}
	for _, h := range handed {
		w.push(h)
	}
	w.attribute(&origin{suspects: left}, o.commit, false)
	return true, nil
}

// takeShared moves the suspects on lines a diff kept into taken, renumbered
// as the parent's version numbers them, and returns the ones it did not.
// Both runs and suspects are in line order.
func takeShared(runs []lineRun, suspects []suspect, taken *[]suspect) []suspect {
	var left []suspect
	r := 0
	for _, s := range suspects {
		for r < len(runs) && (runs[r].op == diff.Delete || runs[r].b+runs[r].n <= s.line) {
			r++
		}
		if r < len(runs) && runs[r].op == diff.Equal && runs[r].b <= s.line {
			*taken = append(*taken, suspect{final: s.final, line: runs[r].a + s.line - runs[r].b})
			continue
		}
		left = append(left, s)
	}
	return left
}

// parentVersions is each parent of o's commit that holds the file, in the
// commit's order, under its own name or the one it was renamed from. A
// parent missing from the object store — a shallow clone's boundary — holds
// nothing this can read, so the lines stop at o's commit, as git's do.
func (w *blameWalk) parentVersions(ctx context.Context, o *origin) ([]parentVersion, error) {
	var out []parentVersion
	var tree *object.Tree
	for _, h := range o.commit.ParentHashes {
		p, err := object.GetCommit(w.store, h)
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ptree, err := p.Tree()
		if err != nil {
			return nil, err
		}
		if entry, err := ptree.FindEntry(o.path); err == nil {
			if entry.Mode.IsFile() {
				out = append(out, parentVersion{commit: p, path: o.path, blob: entry.Hash})
			}
			continue
		}
		if tree == nil {
			if tree, err = o.commit.Tree(); err != nil {
				return nil, err
			}
		}
		from, blob, err := renamedFrom(ctx, w.deadline, ptree, tree, o.path)
		if err != nil {
			return nil, err
		}
		if from != "" {
			out = append(out, parentVersion{commit: p, path: from, blob: blob})
		}
	}
	return out, nil
}

// renamedFrom is the path, and the blob, of the file a commit renamed to
// path, as go-git's default rename detection pairs a parent's tree with the
// commit's, held to git's limit (renameOptions): "" where path was added
// rather than renamed. The trees are compared and no file in them is
// line-diffed; go-git's blame built the whole patch of the commit to find the
// one rename, matching the lines of every file it changed. What rename
// detection reads is counted by the store the trees were read through
// (boundedHistory), and the comparison of the trees stops at the walk's
// deadline, as those reads do: an octopus merge that added the file compares
// its tree with each parent's, and each comparison of a tree of a million
// files costs a second.
func renamedFrom(ctx context.Context, deadline time.Time, parent, tree *object.Tree, path string) (
	string, plumbing.Hash, error,
) {
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	changes, err := object.DiffTreeWithOptions(bounded, parent, tree, renameOptions())
	if err != nil {
		if ctx.Err() == nil && bounded.Err() != nil {
			return "", plumbing.ZeroHash, errPastDeadline
		}
		return "", plumbing.ZeroHash, err
	}
	for _, ch := range changes {
		if ch.To.Name == path && ch.From.Name != "" && ch.From.TreeEntry.Mode.IsFile() {
			return ch.From.Name, ch.From.TreeEntry.Hash, nil
		}
	}
	return "", plumbing.ZeroHash, nil
}

// mergeSuspects is the lines of two lists waiting at one version, in the
// order of their line there.
func mergeSuspects(a, b []suspect) []suspect {
	out := make([]suspect, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		if b[0].line < a[0].line {
			out, b = append(out, b[0]), b[1:]
		} else {
			out, a = append(out, a[0]), a[1:]
		}
	}
	return append(append(out, a...), b...)
}

// originQueue orders the versions lines wait at newest commit first, by the
// committer's date as git's blame orders them, and by hash where two share
// one, so that the walk is the same on every call.
type originQueue []*origin

func (q originQueue) Len() int { return len(q) }
func (q originQueue) Less(i, j int) bool {
	a, b := q[i].commit, q[j].commit
	if !a.Committer.When.Equal(b.Committer.When) {
		return a.Committer.When.After(b.Committer.When)
	}
	if a.Hash != b.Hash {
		return a.Hash.String() < b.Hash.String()
	}
	return q[i].path < q[j].path
}
func (q originQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *originQueue) Push(x any)   { *q = append(*q, x.(*origin)) }
func (q *originQueue) Pop() any {
	old := *q
	o := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]
	return o
}
