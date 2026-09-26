package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	godiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"

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
			"--commit, this is every uncommitted change — staged and unstaged together — against " +
			"HEAD; git.status already answers which paths changed, this answers what changed in " +
			"them. --commit diffs that one commit against its own parent instead, the equivalent " +
			"of `git show <commit>`'s patch half. Diffing two arbitrary commits against each other " +
			"is deliberately not offered in this first cut — the two cases above cover what an " +
			"agent inspecting a repository's current state actually needs, and a revision-range " +
			"comparison is a distinct enough question to design on its own rather than bolt on.",
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

	if commit := req.String("commit"); commit != "" {
		return diffCommit(repo, commit)
	}
	return diffWorktree(repo)
}

func diffCommit(repo *git.Repository, spec string) (view.View, error) {
	hash, err := repo.ResolveRevision(plumbing.Revision(spec))
	if err != nil {
		return nil, view.Errorf("git.diff.unresolved", "%s does not name a commit: %v", spec, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, view.Errorf("git.diff.unresolved", "%s does not name a commit: %v", spec, err)
	}
	parent, err := commit.Parent(0)
	if err != nil {
		if errors.Is(err, object.ErrParentNotFound) {
			return view.Text{Body: shortHash(commit.Hash) +
				" is the root commit — it has no parent to diff against."}, nil
		}
		return nil, view.Errorf("git.diff.failed", "finding %s's parent: %v", spec, err)
	}
	patch, err := parent.Patch(commit)
	if err != nil {
		return nil, view.Errorf("git.diff.failed", "diffing %s: %v", spec, err)
	}
	body := patch.String()
	// **go-git renders nothing at all for a submodule pointer change.**
	// Measured, not assumed: for a commit that only bumps a submodule, the
	// patch comes back with one FilePatch whose Files() are both nil and
	// whose Chunks() is empty, and String() is the empty string — so a
	// commit that did change something arrived here as "" and textOrEmpty
	// announced "no uncommitted changes", which is wrong twice over on a
	// --commit diff. `git show` prints `sub | 2 +-` for the same commit.
	//
	// The pointers are read off the trees instead, which is where they are.
	if bumps := submoduleBumps(parent, commit); len(bumps) > 0 {
		if body != "" {
			body += "\n"
		}
		body += strings.Join(bumps, "\n") + "\n"
	}
	if body == "" {
		return view.Text{Body: shortHash(commit.Hash) + " changed nothing this can show — " +
			"an empty commit, or a change only in a mode or a tree go-git renders no patch for"}, nil
	}
	return view.Text{Body: body}, nil
}

// submoduleBumps names the submodules a commit moved, and where it moved
// them, because the patch encoder emits nothing for a gitlink entry.
//
// One line per submodule in the shape the rest of a diff reads in, rather
// than a second view: the caller asked for a diff, and this is the part of
// it the encoder dropped.
func submoduleBumps(from, to *object.Commit) []string {
	fromTree, err := from.Tree()
	if err != nil {
		return nil
	}
	toTree, err := to.Tree()
	if err != nil {
		return nil
	}
	changes, err := object.DiffTree(fromTree, toTree)
	if err != nil {
		return nil
	}
	var out []string
	for _, ch := range changes {
		if ch.From.TreeEntry.Mode != filemode.Submodule && ch.To.TreeEntry.Mode != filemode.Submodule {
			continue
		}
		name := ch.To.Name
		if name == "" {
			name = ch.From.Name
		}
		switch {
		case ch.From.TreeEntry.Mode != filemode.Submodule:
			out = append(out, "submodule "+name+" added at "+shortHash(ch.To.TreeEntry.Hash))
		case ch.To.TreeEntry.Mode != filemode.Submodule:
			out = append(out, "submodule "+name+" removed, was "+shortHash(ch.From.TreeEntry.Hash))
		default:
			out = append(out, "submodule "+name+" "+shortHash(ch.From.TreeEntry.Hash)+
				" -> "+shortHash(ch.To.TreeEntry.Hash))
		}
	}
	sort.Strings(out)
	return out
}

// maxDiffBytes bounds one file the worktree diff reads whole. Both sides of
// a changed file are held in memory to line-diff them — the committed blob
// and the file on disk — and nothing bounded either, so a generated asset
// or a data file that changed put its whole size into the process twice
// over, on an MCP server as readily as at a terminal. Sixteen megabytes is
// past any file a person reads a diff of; what is over it is named rather
// than diffed. A variable so a test can lower it.
var maxDiffBytes int64 = 16 << 20

func diffWorktree(repo *git.Repository) (view.View, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return nil, view.Errorf("git.diff.worktree", "no working tree here: %v", err).
			WithHint("a bare repository has no working tree to diff")
	}
	status, err := wt.Status()
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

	patches := make([]diff.FilePatch, 0, len(status))
	var large []string
	for path, fs := range status {
		if fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified {
			continue
		}
		if tooLarge(wt, headTree, path, fs) {
			large = append(large, path)
			continue
		}
		fp, ferr := diffOneFile(wt, headTree, path, fs)
		if ferr != nil {
			return nil, view.Errorf("git.diff.failed", "diffing %s: %v", path, ferr)
		}
		if fp != nil {
			patches = append(patches, fp)
		}
	}
	body := (&filePatches{patches: patches}).String()
	// Named in the diff's own shape, the way a submodule bump is: the
	// caller asked what changed, and a file too large to show is part of
	// the answer rather than a row quietly missing from it.
	if len(large) > 0 {
		sort.Strings(large)
		for _, p := range large {
			body += fmt.Sprintf("%s changed, larger than %d MiB and not diffed\n", p, maxDiffBytes>>20)
		}
	}
	return textOrEmpty(body), nil
}

// tooLarge reports a changed path either side of which is over maxDiffBytes,
// from sizes alone — the blob's recorded size and a stat of the file — so
// deciding costs no read of either.
func tooLarge(wt *git.Worktree, headTree *object.Tree, path string, fs *git.FileStatus) bool {
	if headTree != nil {
		if f, err := headTree.File(path); err == nil && f.Size > maxDiffBytes {
			return true
		}
	}
	if fs.Worktree != git.Deleted {
		if info, err := wt.Filesystem.Stat(path); err == nil && info.Size() > maxDiffBytes {
			return true
		}
	}
	return false
}

// diffOneFile builds the patch for a single changed path: HEAD's committed
// content (empty for a file HEAD never had) against what's on disk right
// now (empty for a file the worktree deleted).
func diffOneFile(wt *git.Worktree, headTree *object.Tree, path string, fs *git.FileStatus) (diff.FilePatch, error) {
	var from *diffFile
	oldContent := ""
	if headTree != nil {
		if f, err := headTree.File(path); err == nil {
			c, err := f.Contents()
			if err != nil {
				return nil, err
			}
			oldContent = c
			from = &diffFile{path: path, hash: f.Hash, mode: f.Mode}
		}
	}

	var to *diffFile
	newContent := ""
	if fs.Worktree != git.Deleted {
		content, err := readWorktreeFile(wt, path)
		if err != nil {
			return nil, err
		}
		newContent = content
		mode := filemode.Regular
		if from != nil {
			mode = from.mode
		}
		to = &diffFile{path: path, hash: plumbing.ZeroHash, mode: mode}
	}

	if oldContent == newContent {
		return nil, nil
	}
	if isBinary(oldContent) || isBinary(newContent) {
		return &filePatch{from: from, to: to, binary: true}, nil
	}
	return &filePatch{from: from, to: to, chunks: toChunks(godiff.Do(oldContent, newContent))}, nil
}

func readWorktreeFile(wt *git.Worktree, path string) (string, error) {
	f, err := wt.Filesystem.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data), nil
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

func toChunks(diffs []diffmatchpatch.Diff) []diff.Chunk {
	chunks := make([]diff.Chunk, 0, len(diffs))
	for _, d := range diffs {
		op := diff.Equal
		switch d.Type {
		case diffmatchpatch.DiffInsert:
			op = diff.Add
		case diffmatchpatch.DiffDelete:
			op = diff.Delete
		}
		chunks = append(chunks, &textChunk{content: d.Text, op: op})
	}
	return chunks
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
// itself (the working tree against HEAD, which go-git has no built-in
// comparison for) — reusing the library's own unified-diff encoder rather
// than hand-formatting `diff --git`/`@@` text, the fiddly part every other
// case in this file gets for free from object.Commit.Patch.

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
