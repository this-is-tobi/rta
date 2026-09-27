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
			"structured equivalent of `git blame`. Reads the whole file's history to answer, so it " +
			"is not offered as a dashboard tile the way a bounded, no-input capability would be; a " +
			"large file with a long history can take a real moment.",
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

	file, verr := repoFile(repo, req.String("file"), req.Surface().ArgumentName("file"))
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
// which holds what it reads of blobs to the bounds a diff has.
//
// **A blame reads more of the history than the file at HEAD.** It reads the
// file whole at every commit that touched it, so a version that was 30 MB
// and is 37 bytes at HEAD was read whole, past the check on HEAD, and cost a
// two-line blame 346 MB. And where the file was added or renamed, go-git's
// blame diffs that whole commit against its parent to look for the rename —
// every file the commit changed, read whole and held at once — so a file
// added beside three logs just under the per-file bound cost a one-line
// blame 342 MB. Neither is a file anybody asked about, and a repository is
// content an agent may have been handed rather than written.
//
// So each blob is held to maxDiffBytes, and what one step of the walk reads
// to maxTotalDiffBytes: the bounds git.diff holds one file and one commit
// to. A step is what is read between two commits — go-git's blame reads a
// parent, then the file's version in it or, where it has none, that commit's
// whole diff. That boundary is go-git's order rather than a promise: were it
// to move, a step would be bounded more loosely or more tightly, and each
// blob would still be held to its own bound.
type boundedHistory struct {
	storer.EncodedObjectStorer
	step int64
}

func (s *boundedHistory) EncodedObject(t plumbing.ObjectType, h plumbing.Hash) (plumbing.EncodedObject, error) {
	switch t {
	case plumbing.CommitObject:
		s.step = 0
	case plumbing.BlobObject:
		size, err := s.EncodedObjectSize(h)
		if err != nil {
			break
		}
		if size > maxDiffBytes {
			return nil, &historyTooLarge{size: size}
		}
		if s.step += size; s.step > maxTotalDiffBytes {
			return nil, &historyTooLarge{step: true}
		}
	}
	return s.EncodedObjectStorer.EncodedObject(t, h)
}

// historyTooLarge is a blob, or one step's worth of them, past the bound
// boundedHistory holds a blame to, worded to follow "blaming it reads".
type historyTooLarge struct {
	size int64
	step bool
}

func (e *historyTooLarge) Error() string {
	if e.step {
		return fmt.Sprintf("more than the %d MiB a diff of one commit reads from one commit of its history, "+
			"which changed that much beside it where it was added or renamed", maxTotalDiffBytes>>20)
	}
	return fmt.Sprintf("%s from its history — a version of it, or a file changed where it was added or "+
		"renamed — larger than the %d MiB this reads of one file", format.Bytes(e.size), maxDiffBytes>>20)
}
