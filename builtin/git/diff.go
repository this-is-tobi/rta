package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func diffCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.diff",
		Summary:      "Line-level changes — uncommitted, or one commit against its parent",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "Unified diff text, the structured-plugin equivalent of `git diff`. With no " +
			"`commit`, this is every uncommitted change — staged and unstaged together — against " +
			"HEAD; git.status already answers which paths changed, this answers what changed in " +
			"them. `commit` diffs that one commit against its own parent instead, and the root " +
			"commit against the empty tree, the equivalent of `git show <commit>`'s patch half. " +
			"Diffing two arbitrary commits against each other is deliberately not offered in this " +
			"first cut — the two cases above cover what an agent inspecting a repository's current " +
			"state actually needs, and a revision-range comparison is a distinct enough question " +
			"to design on its own rather than bolt on. One diff reads at most 16 MiB of a file and " +
			"64 MiB in all, and spends at most two seconds matching lines; the lines after the patch " +
			"name each file it left out or diffed coarsely.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "commit", Type: plugin.String, Suggest: suggestCommits,
				Help: "diff this commit against its own parent, instead of the working tree against HEAD"},
		},
		Run: runDiff,
	}
}

func runDiff(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}

	gate := pathGate(req, repo)
	if commit := req.String("commit"); commit != "" {
		return diffCommit(ctx, repo, commit, gate)
	}
	return diffWorktree(ctx, repo, gate)
}

// interrupted is a diff the caller stopped waiting for, which answers nothing
// rather than the files it had got to: a patch that ends early reads as the
// whole of a smaller change.
func interrupted(what string) *view.Error {
	return view.Errorf("git.diff.cancelled", "the diff of %s was interrupted", what)
}

// pathGate is the host's path gate asked about one file a diff would show, by
// its path in the repository: the refusal, or nil where the caller may read
// it.
//
// **The gate judged the repository, not what is in it.** The root is drawn
// around a directory, and rta's own data and configuration directories are
// refused inside it wherever they sit — which they do inside a checkout, for
// anybody who versions their home directory: a dotfiles repository at ~
// holds ~/.local/share/rta untracked. git.diff then read every changed and
// untracked file whole, and handed an agent the age identity that decrypts
// the secret store, the grants seal key and the store itself, in one ungated
// call, while fs.hash on the same file was refused as rta's own state. The
// files a diff reads are paths this handler derives, so each is put back to
// the host the way repoRoot puts back the repository.
//
// A --commit diff asks too, about the committed content: a store or a config
// committed by mistake is the same secret whether it comes from the disk or
// from the object store, and git.blame, whose file is a path argument, is
// already refused it. A bare repository has no working tree to place a path
// in, and nothing in it is on disk under that name to be protected.
func pathGate(req plugin.Request, repo *git.Repository) func(path string) *view.Error {
	wt, err := repo.Worktree()
	if err != nil {
		return func(string) *view.Error { return nil }
	}
	root := wt.Filesystem.Root()
	return func(path string) *view.Error {
		_, verr := req.Confine("path", filepath.Join(root, filepath.FromSlash(path)))
		return verr
	}
}

// withheld is a changed file a diff names without showing it, and why.
type withheld struct{ path, why string }

// refusedBy is why a file the path gate refused is not shown.
func refusedBy(verr *view.Error) string { return "the path gate refuses it (" + verr.Code + ")" }

// notDiffed is the tail of a diff naming, in the diff's own shape, what it
// did not show: a file too large to read, and a file it would not or could
// not read.
func notDiffed(large []string, skipped []withheld) string {
	var b strings.Builder
	sort.Strings(large)
	for _, p := range large {
		fmt.Fprintf(&b, "%s changed, larger than %d MiB and not diffed\n", p, maxDiffBytes>>20)
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].path < skipped[j].path })
	for _, w := range skipped {
		fmt.Fprintf(&b, "%s changed, not diffed: %s\n", w.path, w.why)
	}
	return b.String()
}

func diffCommit(ctx context.Context, repo *git.Repository, spec string, gate func(string) *view.Error) (view.View, error) {
	deadline := matchDeadline(ctx)
	hash, err := repo.ResolveRevision(plumbing.Revision(spec))
	if err != nil {
		return nil, view.Errorf("git.diff.unresolved", "%s does not name a commit: %v", spec, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, view.Errorf("git.diff.unresolved", "%s does not name a commit: %v", spec, err)
	}
	toTree, err := commit.Tree()
	if err != nil {
		return nil, view.Errorf("git.diff.failed", "reading %s's tree: %v", spec, err)
	}
	// The root commit is diffed against the empty tree — a nil one, which
	// DiffTree reads as having nothing in it — so every file it holds is
	// shown added, as `git show` does. It answered a sentence saying it had
	// no parent, which every format carried where the patch goes: `rta git
	// diff --commit <root> > root.patch` wrote prose into the patch.
	var fromTree *object.Tree
	parent, err := commit.Parent(0)
	switch {
	case err == nil:
		if fromTree, err = parent.Tree(); err != nil {
			return nil, view.Errorf("git.diff.failed", "reading %s's parent's tree: %v", spec, err)
		}
	case !errors.Is(err, object.ErrParentNotFound):
		return nil, view.Errorf("git.diff.failed", "finding %s's parent: %v", spec, err)
	}
	// The options Commit.Patch uses, rename detection included, so a commit
	// with a parent is diffed exactly as it was before the root one was. The
	// request's context, though, where Commit.Patch uses a background one: a
	// root commit is often a whole codebase imported at once, and a caller
	// that has stopped waiting for it should not leave the diff running.
	changes, err := object.DiffTreeWithOptions(ctx, fromTree, toTree, object.DefaultDiffTreeOptions)
	switch {
	case ctx.Err() != nil:
		return nil, interrupted(shortHash(commit.Hash))
	case err != nil:
		return nil, view.Errorf("git.diff.failed", "diffing %s: %v", spec, err)
	}
	changes, large, refused, cut := boundChanges(repo, changes, gate)
	body, coarse, err := commitPatch(ctx, repo, changes, deadline)
	switch {
	case ctx.Err() != nil:
		return nil, interrupted(shortHash(commit.Hash))
	case err != nil:
		return nil, view.Errorf("git.diff.failed", "diffing %s: %v", spec, err)
	}
	// **go-git renders nothing at all for a submodule pointer change.**
	// Measured, not assumed: for a commit that only bumps a submodule, the
	// patch comes back with one FilePatch whose Files() are both nil and
	// whose Chunks() is empty, and String() is the empty string — so a
	// commit that did change something arrived here as "" and textOrEmpty
	// announced "no uncommitted changes", which is wrong twice over on a
	// --commit diff. `git show` prints `sub | 2 +-` for the same commit.
	//
	// The pointers are read off the trees instead, which is where they are.
	if bumps := submoduleBumps(fromTree, toTree); len(bumps) > 0 {
		if body != "" {
			body += "\n"
		}
		body += strings.Join(bumps, "\n") + "\n"
	}
	// Named in the diff's own shape, as the worktree diff names what it
	// did not read.
	body += notDiffed(large, refused) + pastBudget(cut) + matchedCoarsely(coarse)
	// An empty patch is the answer, and the sentence is what a person is
	// told in its place (view.Text.Empty), for the reason a clean working
	// tree's is: as the body, `rta git diff --commit <empty> > x.patch` wrote
	// it into the patch, and -o json handed it to a script as the diff.
	//
	// It named a change only in a mode as one of the reasons, and go-git
	// renders that — `old mode`, `new mode` — as `git show` does. What is left
	// is a commit whose tree is its first parent's: an empty one, or a merge
	// that kept that parent's side.
	return view.Text{Body: body, Empty: shortHash(commit.Hash) + " changed nothing this can show — " +
		"an empty commit, or a merge that kept its first parent's tree"}, nil
}

// maxTotalDiffBytes bounds what one diff reads in all, of a commit or of the
// working tree. Each file is held to maxDiffBytes, but a commit can hold a
// hundred thousand files under that, and both sides of every change are read
// whole to line-diff them. A root commit is often a whole codebase imported
// at once, and it is diffed in full since it stopped answering a sentence; on
// an MCP server that is one free call holding the lot in memory.
//
// The working tree had only the per-file bound, as though it could not hold
// as much as a commit, and it holds more: whatever nobody has committed or
// ignored, build output and logs among it. Twenty untracked logs just under
// the per-file bound cost one git_diff three gigabytes. Past the budget the
// rest is counted, not read, in the order the diff lists its files. A
// variable so a test can lower it.
var maxTotalDiffBytes int64 = 64 << 20

// pastBudget is the line counting the files a diff left out once it had read
// maxTotalDiffBytes, or nothing when it left none.
func pastBudget(cut int) string {
	if cut == 0 {
		return ""
	}
	return fmt.Sprintf("%d more %s changed and not diffed: one diff reads at most %d MiB\n",
		cut, format.PluralOf(cut, "file"), maxTotalDiffBytes>>20)
}

// matchedCoarsely is the tail naming the files a diff showed before it had
// matched all their lines, once it had spent matchTime: each is a correct
// patch, in which some lines both sides share show as removed and added.
func matchedCoarsely(paths []string) string {
	var b strings.Builder
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(&b, "%s changed, diffed coarsely: one diff spends at most %v matching lines, "+
			"so some lines it kept show as removed and added\n", p, matchTime)
	}
	return b.String()
}

// commitPatch is the patch of a commit's changes, and the files in it it
// matched coarsely.
//
// Built here, with the encoder the worktree diff uses, rather than by
// go-git's Changes.PatchContext: that calls utils/diff.Do on every file,
// with a one-hour timeout and no way to pass another, so a --commit diff
// had no bound on the time it spent however few bytes it read. Otherwise it
// is the patch go-git built, file for file (changePatch). The caller's
// context is looked at between files, as PatchContext looked at it.
func commitPatch(ctx context.Context, repo *git.Repository, changes object.Changes, deadline time.Time) (
	string, []string, error,
) {
	patches := make([]diff.FilePatch, 0, len(changes))
	var coarse []string
	for _, ch := range changes {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		fp, cut, err := changePatch(repo, ch, deadline)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", changePath(ch), err)
		}
		if fp == nil {
			continue
		}
		if cut {
			coarse = append(coarse, changePath(ch))
		}
		patches = append(patches, fp)
	}
	return (&filePatches{patches: patches}).String(), coarse, nil
}

// binarySniff is how much of a file git reads to judge it binary, and go-git
// with it: a NUL in the first eight thousand bytes.
const binarySniff = 8000

// changePatch is one change of a commit, as go-git's patch showed it: nothing
// for a side that is a submodule, which submoduleBumps names instead;
// "Binary files differ" for a side with a NUL where git looks for one, and
// for two sides with no lines at all (an empty file added or removed, which
// go-git renders that way and which a patch of lines has no hunk for); the
// lines otherwise. cut says the lines were matched coarsely.
func changePatch(repo *git.Repository, ch *object.Change, deadline time.Time) (diff.FilePatch, bool, error) {
	var (
		files    [2]*diffFile
		contents [2]string
		binary   bool
	)
	for i, e := range []object.ChangeEntry{ch.From, ch.To} {
		if e.Name == "" {
			continue
		}
		if !e.TreeEntry.Mode.IsFile() {
			return nil, false, nil
		}
		blob, err := repo.BlobObject(e.TreeEntry.Hash)
		if err != nil {
			return nil, false, err
		}
		content, err := blobContent(blob)
		if err != nil {
			return nil, false, err
		}
		files[i] = &diffFile{path: e.Name, hash: e.TreeEntry.Hash, mode: e.TreeEntry.Mode}
		contents[i] = content
		binary = binary || strings.IndexByte(content[:min(len(content), binarySniff)], 0) >= 0
	}
	if files[0] == nil && files[1] == nil {
		return nil, false, nil
	}
	if binary {
		return &filePatch{from: files[0], to: files[1], binary: true}, false, nil
	}
	lines := diffLines(contents[0], contents[1], deadline)
	chunks := lines.chunks()
	return &filePatch{from: files[0], to: files[1], chunks: chunks, binary: len(chunks) == 0}, lines.cut, nil
}

// blobContent is a blob's content, read whole: boundChanges has held its size
// to maxDiffBytes before anything here reads it.
func blobContent(blob *object.Blob) (string, error) {
	r, err := blob.Reader()
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	var b strings.Builder
	b.Grow(int(blob.Size))
	if _, err := io.Copy(&b, r); err != nil {
		return "", err
	}
	return b.String(), nil
}

// boundChanges keeps the changes a --commit diff reads, in order, and names
// the ones it leaves: a file the path gate refuses, a file over maxDiffBytes
// by its path, and whatever no longer fits the commit's budget by count.
// Sizes come from the object store without reading the content, so deciding
// costs nothing it is meant to save.
//
// Both names of a change go to the gate, since a rename carries the content
// of the one it came from.
func boundChanges(repo *git.Repository, changes object.Changes, gate func(string) *view.Error) (
	kept object.Changes, large []string, refused []withheld, cut int,
) {
	budget := maxTotalDiffBytes
	for _, ch := range changes {
		if verr := gateEither(gate, ch.From, ch.To); verr != nil {
			refused = append(refused, withheld{changePath(ch), refusedBy(verr)})
			continue
		}
		from, to := blobSize(repo, ch.From), blobSize(repo, ch.To)
		switch {
		case from > maxDiffBytes || to > maxDiffBytes:
			large = append(large, changePath(ch))
		case from+to > budget:
			cut++
		default:
			budget -= from + to
			kept = append(kept, ch)
		}
	}
	return kept, large, refused, cut
}

// gateEither is the gate's refusal of the first side of a change it refuses,
// the side of an addition or a deletion that is not there skipped. A side
// that is a symlink is judged by where it sits, as the worktree diff judges
// one (gatedAt): its content is the link's text.
func gateEither(gate func(string) *view.Error, sides ...object.ChangeEntry) *view.Error {
	for _, s := range sides {
		if s.Name == "" {
			continue
		}
		if verr := gate(gatedAt(s.Name, s.TreeEntry.Mode == filemode.Symlink)); verr != nil {
			return verr
		}
	}
	return nil
}

// gatedAt is the path the gate is asked about for a file a diff shows: the
// file's own, and a symlink's directory. A diff shows a link by its text,
// never by what it points at, and the gate resolves every link in what it is
// asked about — a link to a file outside the root would be refused for a
// read that never leaves it.
func gatedAt(name string, symlink bool) string {
	if symlink {
		return pathpkg.Dir(name)
	}
	return name
}

// blobSize is the size of one side of a change, 0 for a side that does not
// exist. A submodule's entry names a commit in another repository, which
// this one does not hold, and it has no content to read here either.
func blobSize(repo *git.Repository, e object.ChangeEntry) int64 {
	if e.TreeEntry.Hash.IsZero() {
		return 0
	}
	size, err := repo.Storer.EncodedObjectSize(e.TreeEntry.Hash)
	if err != nil {
		return 0
	}
	return size
}

func changePath(ch *object.Change) string {
	if ch.To.Name != "" {
		return ch.To.Name
	}
	return ch.From.Name
}

// submoduleBumps names the submodules a commit moved, and where it moved
// them, because the patch encoder emits nothing for a gitlink entry.
//
// One line per submodule in the shape the rest of a diff reads in, rather
// than a second view: the caller asked for a diff, and this is the part of
// it the encoder dropped. A nil from is the empty tree, a root commit's.
func submoduleBumps(fromTree, toTree *object.Tree) []string {
	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return nil
	}
	var out []string
	for _, ch := range changes {
		if ch.From.TreeEntry.Mode != filemode.Submodule && ch.To.TreeEntry.Mode != filemode.Submodule {
			continue
		}
		var from, to plumbing.Hash
		if ch.From.TreeEntry.Mode == filemode.Submodule {
			from = ch.From.TreeEntry.Hash
		}
		if ch.To.TreeEntry.Mode == filemode.Submodule {
			to = ch.To.TreeEntry.Hash
		}
		out = append(out, submoduleLine(changePath(ch), from, to))
	}
	sort.Strings(out)
	return out
}

// submoduleLine names a submodule a diff moved: the commit it was at and the
// one it is at now, a zero hash for the side that has none.
func submoduleLine(name string, from, to plumbing.Hash) string {
	switch {
	case from.IsZero():
		return "submodule " + name + " added at " + shortHash(to)
	case to.IsZero():
		return "submodule " + name + " removed, was " + shortHash(from)
	}
	return "submodule " + name + " " + shortHash(from) + " -> " + shortHash(to)
}

// gitlinkAt is the commit HEAD records for a submodule at path, and whether
// HEAD or the index records one there at all.
func gitlinkAt(headTree *object.Tree, idx *index.Index, path string) (plumbing.Hash, bool) {
	var from plumbing.Hash
	link := false
	if headTree != nil {
		if e, err := headTree.FindEntry(path); err == nil && e.Mode == filemode.Submodule {
			from, link = e.Hash, true
		}
	}
	if idx != nil {
		if e, err := idx.Entry(path); err == nil && e.Mode == filemode.Submodule {
			link = true
		}
	}
	return from, link
}

// submoduleHeads is the commit each submodule's checkout is at, by its path:
// the HEAD of the repository git keeps for it under .git/modules, which is
// where go-git's status reads it too. Read from that store directly rather
// than through go-git's Submodule.Repository, which initialises a repository
// there when it finds none — a write, from a capability that makes none.
func submoduleHeads(repo *git.Repository, wt *git.Worktree) map[string]plumbing.Hash {
	heads := map[string]plumbing.Hash{}
	subs, err := wt.Submodules()
	if err != nil {
		return heads
	}
	for _, s := range subs {
		c := s.Config()
		module, err := repo.Storer.Module(c.Name)
		if err != nil {
			continue
		}
		if ref, err := storer.ResolveReference(module, plumbing.HEAD); err == nil {
			heads[c.Path] = ref.Hash()
		}
	}
	return heads
}

// maxDiffBytes bounds one file a diff reads whole. Both sides of
// a changed file are held in memory to line-diff them — the committed blob
// and the file on disk — and nothing bounded either, so a generated asset
// or a data file that changed put its whole size into the process twice
// over, on an MCP server as readily as at a terminal. Sixteen megabytes is
// past any file a person reads a diff of; what is over it is named rather
// than diffed, and git.blame, which reads its file whole at every commit
// that touched it, refuses one over it. A variable so a test can lower it.
var maxDiffBytes int64 = 16 << 20

func diffWorktree(ctx context.Context, repo *git.Repository, gate func(string) *view.Error) (view.View, error) {
	deadline := matchDeadline(ctx)
	wt, err := repo.Worktree()
	if err != nil {
		return nil, view.Errorf("git.diff.worktree", "no working tree here: %v", err).
			WithHint("a bare repository has no working tree to diff")
	}
	status, err := worktreeStatus(repo, wt)
	if err != nil {
		return nil, view.Errorf("git.diff.failed", "reading status: %v", err)
	}
	if status.IsClean() {
		return textOrEmpty(""), nil
	}

	var headTree *object.Tree
	if head, herr := repo.Head(); herr == nil {
		if commit, cerr := repo.CommitObject(head.Hash()); cerr == nil {
			headTree, _ = commit.Tree()
		}
	}

	root := wt.Filesystem.Root()
	patches := make([]diff.FilePatch, 0, len(status))
	var large []string
	var skipped []withheld
	var coarse []string
	budget, cut := maxTotalDiffBytes, 0
	// A submodule is named by the commits it moved between, as a commit's
	// diff names one (submoduleBumps) and as git does. Its checkout is a
	// directory, and it was named as "a directory where a file was", where
	// git shows `Subproject commit` and the two hashes.
	idx, _ := repo.Storer.Index()
	var bumps []string
	var heads map[string]plumbing.Hash
	for _, path := range changedPaths(status) {
		if ctx.Err() != nil {
			return nil, interrupted("the working tree")
		}
		disk := onDisk(root, path)
		if from, link := gitlinkAt(headTree, idx, path); link {
			if heads == nil {
				heads = submoduleHeads(repo, wt)
			}
			var to plumbing.Hash
			if disk != nil && disk.IsDir() {
				to = heads[path]
			}
			if from != to {
				bumps = append(bumps, submoduleLine(path, from, to))
			}
			continue
		}
		if verr := gate(gatedAt(path, disk != nil && disk.Mode()&os.ModeSymlink != 0)); verr != nil {
			skipped = append(skipped, withheld{path, refusedBy(verr)})
			continue
		}
		from, to := sideSizes(headTree, path, disk)
		switch {
		case from > maxDiffBytes || to > maxDiffBytes:
			large = append(large, path)
			continue
		case from+to > budget:
			cut++
			continue
		}
		budget -= from + to
		// One file that cannot be read is named, and the rest of the diff is
		// still the answer: it returned on the first, so an untracked link to
		// a directory — bazel-out, a `current` pointing at a release — left
		// the caller with no patch at all, for a file git diffs as one line.
		fp, coarsely, ferr := diffOneFile(root, headTree, path, disk, deadline)
		if ferr != nil {
			skipped = append(skipped, withheld{path, unreadable(ferr)})
			continue
		}
		if coarsely {
			coarse = append(coarse, path)
		}
		if fp != nil {
			patches = append(patches, fp)
		}
	}
	body := (&filePatches{patches: patches}).String()
	if len(bumps) > 0 {
		if body != "" {
			body += "\n"
		}
		body += strings.Join(bumps, "\n") + "\n"
	}
	// Named in the diff's own shape, the way a submodule bump is: the
	// caller asked what changed, and a file too large to show, or one it may
	// not read, is part of the answer rather than a row quietly missing from
	// it. git.status names the same paths to the same caller.
	body += notDiffed(large, skipped) + pastBudget(cut) + matchedCoarsely(coarse)
	return textOrEmpty(body), nil
}

// changedPaths is every path the status holds a change for, in the order git
// prints a diff in: byte order of the whole path, the index's own.
//
// A status is a map, and the diff was built in its iteration order, which Go
// randomises: the same working tree gave its files in a different order on
// every call, so two answers to one question never compared equal, and a
// patch saved twice differed in every line.
func changedPaths(status git.Status) []string {
	paths := make([]string, 0, len(status))
	for path, fs := range status {
		if fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// onDisk is what the working tree holds at path, the entry itself rather
// than what a symlink there points at — nil for nothing at all, which a
// deleted file and one removed since the status was read both are.
//
// The os package on the working tree's own root, and not the go-billy
// filesystem go-git hands back: billy follows a symlink wherever it reads
// one, and rewrites an absolute link's text relative to its chroot, where
// git diffs a link as the text it holds.
//
// Nothing, too, behind a directory that has become a link. git stops at a
// symlink on the way to a path — a tracked notes/a.txt whose notes was
// replaced by a link to other/ is deleted, and notes is a new link — and so
// does go-git's status, which lists it deleted. A lstat of the whole path
// follows every link but the last, so the diff showed other/a.txt's content
// as the change to notes/a.txt, and named a file the gate refused for a link
// leading out of the root, where git has one deleted file to show.
func onDisk(root, path string) os.FileInfo {
	dir := root
	parts := strings.Split(path, "/")
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return nil
		}
	}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil
	}
	return info
}

// sideSizes is the size of each side of a changed path, 0 for a side that is
// not there: the blob's recorded size and a stat of the entry on disk, so
// holding a file to maxDiffBytes and the lot to maxTotalDiffBytes costs no
// read of either.
func sideSizes(headTree *object.Tree, path string, disk os.FileInfo) (from, to int64) {
	if headTree != nil {
		if size, err := headTree.Size(path); err == nil {
			from = size
		}
	}
	if disk != nil {
		to = disk.Size()
	}
	return from, to
}

// diffOneFile builds the patch for a single changed path: HEAD's committed
// content (empty for a file HEAD never had) against what's on disk right
// now (empty for a file the worktree deleted). coarsely says the deadline
// cut the matching of its lines short.
func diffOneFile(root string, headTree *object.Tree, path string, disk os.FileInfo, deadline time.Time) (
	fp diff.FilePatch, coarsely bool, err error,
) {
	var from *diffFile
	oldContent := ""
	if headTree != nil {
		if f, err := headTree.File(path); err == nil {
			c, err := f.Contents()
			if err != nil {
				return nil, false, err
			}
			oldContent = c
			from = &diffFile{path: path, hash: f.Hash, mode: f.Mode}
		}
	}

	var to *diffFile
	newContent := ""
	if disk != nil {
		content, mode, err := readWorktreeEntry(root, path, disk)
		if err != nil {
			return nil, false, err
		}
		newContent = content
		if mode == filemode.Regular && from != nil && from.mode != filemode.Symlink {
			mode = from.mode
		}
		to = &diffFile{path: path, hash: plumbing.ZeroHash, mode: mode}
	}

	if oldContent == newContent {
		return nil, false, nil
	}
	if isBinary(oldContent) || isBinary(newContent) {
		return &filePatch{from: from, to: to, binary: true}, false, nil
	}
	lines := diffLines(oldContent, newContent, deadline)
	return &filePatch{from: from, to: to, chunks: lines.chunks()}, lines.cut, nil
}

// readWorktreeEntry is the content git diffs for what is at path, and the
// mode it records it under. A symlink is its text, as git stores and diffs
// one: read through it instead, a retargeted link showed the new target's
// contents where git shows the new name.
func readWorktreeEntry(root, path string, disk os.FileInfo) (string, filemode.FileMode, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	if disk.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return "", 0, err
		}
		return target, filemode.Symlink, nil
	}
	// **Only a regular file is opened, and never in a way that can wait.**
	// go-git's status lists a named pipe as untracked, and opening one with
	// no writer blocks in open(2) until a writer comes — which no context
	// can interrupt, so git_diff never answered and each call held an OS
	// thread for good. A pipe, a socket or a device is named instead, and git
	// tracks none of them. The open is non-blocking and the file it reached
	// is held to the one the lstat saw, so a regular file swapped for a pipe
	// or a link in between is named as changed rather than waited on or read
	// through.
	if !disk.Mode().IsRegular() {
		return "", 0, notAFile(disk.Mode())
	}
	f, err := os.OpenFile(full, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(opened, disk) {
		return "", 0, notDiffable("it changed while it was being read")
	}
	// No further than the size the lstat saw, which is what the budget
	// counted, and what git reads too: a file still being written to is
	// diffed as it was when it was measured, rather than for as long as it
	// keeps growing.
	data, err := io.ReadAll(io.LimitReader(f, disk.Size()))
	if err != nil {
		return "", 0, err
	}
	return string(data), filemode.Regular, nil
}

// notDiffable is a reason a diff does not show a file that is the reason
// whole, rather than one the operating system gave for failing to read it.
type notDiffable string

func (n notDiffable) Error() string { return string(n) }

// notAFile names what is on disk in place of a file, by its kind.
func notAFile(m os.FileMode) notDiffable {
	switch {
	case m&os.ModeNamedPipe != 0:
		return "a named pipe, which git does not track"
	case m&os.ModeSocket != 0:
		return "a socket, which git does not track"
	case m&(os.ModeDevice|os.ModeCharDevice) != 0:
		return "a device, which git does not track"
	case m.IsDir():
		return "a directory where a file was"
	}
	return "not a regular file"
}

// unreadable is why a changed file could not be read, as the diff names it:
// the operating system's reason, without the absolute path it carries, since
// the line already names the file.
func unreadable(err error) string {
	var reason notDiffable
	if errors.As(err, &reason) {
		return string(reason)
	}
	var pe *iofs.PathError
	if errors.As(err, &pe) {
		return "cannot read it: " + pe.Err.Error()
	}
	return "cannot read it: " + err.Error()
}

// isBinary mirrors git's own heuristic closely enough for this purpose: real
// text has no reason to contain a NUL byte, and diffing one byte-for-byte
// against another as if both were lines of text produces output nobody
// could read anyway.
func isBinary(content string) bool {
	for i := 0; i < len(content); i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

// textOrEmpty is a working tree's patch, which is empty when nothing is
// uncommitted — and empty is the answer, not a sentence about it. The body
// was that sentence, so every format carried it: `rta git diff > x.patch` on
// a clean tree wrote "no uncommitted changes" into the patch, and -o json
// handed it to a script as the diff. The sentence is for a screen alone.
func textOrEmpty(body string) view.View {
	return view.Text{Body: body, Empty: "no uncommitted changes"}
}

// The four small types below implement plumbing/format/diff's Patch,
// FilePatch, File and Chunk interfaces for a diff this package computed
// itself — the working tree against HEAD, which go-git has no built-in
// comparison for, and a commit against its parent, whose go-git patch
// matches lines with no bound on the time it takes (commitPatch) — reusing
// the library's own unified-diff encoder rather than hand-formatting
// `diff --git`/`@@` text.

type filePatches struct {
	patches []diff.FilePatch
}

func (p *filePatches) FilePatches() []diff.FilePatch { return p.patches }
func (p *filePatches) Message() string               { return "" }

func (p *filePatches) String() string {
	var buf bytes.Buffer
	_ = diff.NewUnifiedEncoder(&buf, diff.DefaultContextLines).Encode(p)
	return buf.String()
}

type filePatch struct {
	from, to *diffFile
	chunks   []diff.Chunk
	binary   bool
}

func (fp *filePatch) IsBinary() bool { return fp.binary }

func (fp *filePatch) Files() (from, to diff.File) {
	if fp.from != nil {
		from = fp.from
	}
	if fp.to != nil {
		to = fp.to
	}
	return
}

func (fp *filePatch) Chunks() []diff.Chunk { return fp.chunks }

type diffFile struct {
	path string
	hash plumbing.Hash
	mode filemode.FileMode
}

func (f *diffFile) Hash() plumbing.Hash     { return f.hash }
func (f *diffFile) Mode() filemode.FileMode { return f.mode }
func (f *diffFile) Path() string            { return f.path }

type textChunk struct {
	content string
	op      diff.Operation
}

func (c *textChunk) Content() string      { return c.content }
func (c *textChunk) Type() diff.Operation { return c.op }
