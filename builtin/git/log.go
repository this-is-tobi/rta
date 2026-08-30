package git

import (
	"context"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const defaultLogLimit = 20

func logCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.log",
		Summary:      "Recent commit history",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
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
	repo, verr := openRepo(ctx, req)
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

// suggestCommitLimit is how far back a completion looks. A commit anybody is
// naming by hand is a recent one; walking further is work done per keystroke
// for answers nobody scrolls to.
const suggestCommitLimit = 20

// suggestCommits offers recent commits of the repository being asked about,
// short hash and the message's own first line.
//
// A revision is the one input in this plugin that nobody retypes correctly,
// and the answer is entirely local: the object store on this machine, no
// network and no process.
//
// Deliberately not openRepo, which clones a remote endpoint into memory. That
// is right for a run somebody asked for and wrong on a keystroke — a --path
// naming a URL simply offers nothing rather than fetching a repository because
// somebody pressed tab.
func suggestCommits(_ context.Context, req plugin.Request) []string {
	path := req.String("path")
	if path == "" {
		path = "."
	}
	if ep, err := transport.NewEndpoint(path); err == nil && ep.Protocol != "file" {
		return nil
	}
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return nil
	}
	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return nil
	}
	defer iter.Close()
	var out []string
	_ = iter.ForEach(func(c *object.Commit) error {
		if len(out) >= suggestCommitLimit {
			return storer.ErrStop
		}
		out = append(out, shortHash(c.Hash)+"\t"+firstLine(c.Message))
		return nil
	})
	return out
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
