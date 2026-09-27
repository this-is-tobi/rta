package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
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
			"it. So it is not offered as a dashboard tile the way a bounded, no-input capability " +
			"would be; a large file with a long history can take a real moment.",
		NoPreview: true,
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "file", Type: plugin.Path, Positional: true, Required: true, Help: fileHelp("the file to blame")},
		},
		Run: runBlame,
	}
}

func runBlame(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	head, err := repo.Head()
	if err != nil {
		return nil, view.Errorf("git.blame.nohead", "no commit to blame from: %v", err).
			WithHint("an empty repository has no history yet")
	}
	// Through a store that bounds what the blame reads, so that the whole of
	// the history it walks is held to it, and not only HEAD (boundedHistory).
	commit, err := object.GetCommit(&boundedHistory{EncodedObjectStorer: repo.Storer}, head.Hash())
	if err != nil {
		return nil, view.Errorf("git.blame.failed", "reading HEAD commit: %v", err)
	}

	file, verr := repoFile(repo, req.String("file"), req.Surface(), req.Surface().ArgumentName("file"))
	if verr != nil {
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
	result, err := git.Blame(commit, file)
	var over *historyTooLarge
	if errors.As(err, &over) {
		return nil, toolarge("%s: blaming it reads %s", file, over)
	}
	if err != nil {
		return nil, view.Errorf("git.blame.failed", "%s: %v", file, err).
			WithHint("the file must be tracked at HEAD: a new one has no history to blame until it is committed")
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Line", Kind: view.KindNumber},
		{Name: "Hash"},
		{Name: "Author"},
		{Name: "Date", Kind: view.KindTimestamp},
		{Name: "Content"},
	}}
	for i, l := range result.Lines {
		t.Rows = append(t.Rows, []string{
			strconv.Itoa(i + 1),
			shortHash(l.Hash),
			l.AuthorName,
			l.Date.Format("2006-01-02 15:04"),
			l.Text,
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// boundedHistory is the object store a blame reads the repository through,
// which holds what it reads of blobs to a budget.
//
// **A blame reads more of the history than the file at HEAD.** It reads the
// file whole at every commit that changed it, so a version that was 30 MB
// and is 37 bytes at HEAD was read whole, past the check on HEAD, and cost a
// two-line blame 346 MB. And where the file was added or renamed, go-git's
// blame diffs that whole commit against its parent to look for the rename —
// every file the commit changed, read whole and held at once — so a file
// added beside three logs just under the per-file bound cost a one-line
// blame 342 MB. Neither is a file anybody asked about, and a repository is
// content an agent may have been handed rather than written.
//
// So each blob is held to maxDiffBytes, the bound git.diff holds one file
// to, and everything the walk reads to maxBlameBytes. The walk was once held
// step by step instead, a commit's worth of reads at a time, and that left
// its length free: ten versions of a file each just under the per-file
// bound, every one read whole and line-diffed against the next, cost one
// call over half a gigabyte and six minutes of CPU, each step within bounds.
// A blob is counted each time the walk asks the store for it, which for a
// version of the file is more than once — to find it in a parent, then to
// read it — so the count errs on the side of reading less.
type boundedHistory struct {
	storer.EncodedObjectStorer
	read int64
}

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
