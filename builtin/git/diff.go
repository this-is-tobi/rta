package git

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	godiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
	return textOrEmpty(patch.String()), nil
}

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
		return view.Text{Body: "no uncommitted changes"}, nil
	}

	var headTree *object.Tree
	if head, herr := repo.Head(); herr == nil {
		if commit, cerr := repo.CommitObject(head.Hash()); cerr == nil {
			headTree, _ = commit.Tree()
		}
	}

	patches := make([]diff.FilePatch, 0, len(status))
	for path, fs := range status {
		if fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified {
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
	return textOrEmpty((&filePatches{patches: patches}).String()), nil
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

func textOrEmpty(body string) view.View {
	if body == "" {
		return view.Text{Body: "no uncommitted changes"}
	}
	return view.Text{Body: body}
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
