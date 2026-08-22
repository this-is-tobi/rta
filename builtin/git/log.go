package git

import (
	"context"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const defaultLogLimit = 20

func logCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "git.log",
		Summary:    "Recent commit history",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "The most recent commits reaching HEAD, newest first — hash, author, date and " +
			"the message's own first line, each a table row rather than text to parse. --file " +
			"narrows it to commits that touched one path, the structured equivalent of " +
			"`git log -- <path>`.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "limit", Type: plugin.Int, Default: defaultLogLimit, Min: 1, Max: 500,
				Help: "how many of the most recent commits to show"},
			{Name: "file", Type: plugin.Path, Help: "limit to commits that touched this path"},
		},
		Run: runLog,
	}
}

func runLog(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req.String("path"))
	if verr != nil {
		return nil, verr
	}

	opts := &git.LogOptions{Order: git.LogOrderCommitterTime}
	if file := req.String("file"); file != "" {
		opts.FileName = &file
	}
	iter, err := repo.Log(opts)
	if err != nil {
		return nil, view.Errorf("git.log.failed", "reading log: %v", err)
	}
	defer iter.Close()

	limit := req.Int("limit")
	t := view.Table{Columns: []view.Column{
		{Name: "Hash"},
		{Name: "Author"},
		{Name: "Date", Kind: view.KindTimestamp},
		{Name: "Message"},
	}}
	err = iter.ForEach(func(c *object.Commit) error {
		if len(t.Rows) >= limit {
			return storer.ErrStop
		}
		t.Rows = append(t.Rows, []string{
			shortHash(c.Hash),
			c.Author.Name,
			c.Author.When.Format("2006-01-02 15:04"),
			firstLine(c.Message),
		})
		return nil
	})
	if err != nil {
		return nil, view.Errorf("git.log.failed", "walking log: %v", err)
	}
	t.Total = len(t.Rows)
	return t, nil
}

// firstLine is a commit's own summary line — the part every git tool that
// shows one line per commit already treats as the headline, the rest being
// a body nobody expects in a table cell.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
