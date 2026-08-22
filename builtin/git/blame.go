package git

import (
	"context"
	"strconv"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func blameCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "git.blame",
		Summary:    "Who last touched each line of a file, and when",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "One row per line: who last changed it, when, and the commit that did — the " +
			"structured equivalent of `git blame`. Reads the whole file's history to answer, so it " +
			"is not offered as a dashboard tile the way a bounded, no-input capability would be; a " +
			"large file with a long history can take a real moment.",
		NoPreview: true,
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "file", Type: plugin.Path, Positional: true, Required: true,
				Help: "path to the file, relative to the repository root"},
		},
		Run: runBlame,
	}
}

func runBlame(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req.String("path"))
	if verr != nil {
		return nil, verr
	}
	head, err := repo.Head()
	if err != nil {
		return nil, view.Errorf("git.blame.nohead", "no commit to blame from: %v", err).
			WithHint("an empty repository has no history yet")
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, view.Errorf("git.blame.failed", "reading HEAD commit: %v", err)
	}

	file := req.String("file")
	result, err := git.Blame(commit, file)
	if err != nil {
		return nil, view.Errorf("git.blame.failed", "%s: %v", file, err).
			WithHint("the path is relative to the repository root, and must be tracked at HEAD")
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
