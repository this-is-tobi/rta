package git

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	defaultLogLimit = 20
	maxLogLimit     = 500
)

func logCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.log",
		Summary:      "Recent commit history",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "The most recent commits reaching HEAD, newest first — hash, author, date and " +
			"the message's own first line, each a table row rather than text to parse. `file` " +
			"narrows it to commits that touched one path — a file, or anything beneath a " +
			"directory — the structured equivalent of `git log -- <path>`.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "limit", Type: plugin.Int, Config: "log.limit", Default: defaultLogLimit, Min: 1, Max: maxLogLimit,
				Help: "how many of the most recent commits to show"},
			{Name: "file", Type: plugin.Path, Help: fileHelp("commits touching this file or directory")},
		},
		Run: runLog,
	}
}

func runLog(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()

	t := view.Table{Columns: []view.Column{
		{Name: "Hash"},
		{Name: "Author"},
		{Name: "Date", Kind: view.KindTimestamp},
		{Name: "Message"},
	}, Empty: "no commits yet"}
	opts := &git.LogOptions{Order: git.LogOrderCommitterTime}
	narrowed := false
	if file := req.String("file"); file != "" {
		rel, verr := repoFile(repo, file, req.Surface(), req.Surface().InputName("file"))
		if verr != nil {
			return nil, verr
		}
		opts.PathFilter = within(rel)
		if rel != "." {
			t.Empty = "no commit reaching HEAD touched " + rel
			narrowed = true
		}
	}
	// A repository with no commits yet has an empty history, as git.branches
	// answers it has no branches, rather than go-git's "reference not found".
	if _, err := repo.Head(); errors.Is(err, plumbing.ErrReferenceNotFound) {
		t.Empty = "no commits yet"
		return t, nil
	}
	iter, err := repo.Log(opts)
	if err != nil {
		return nil, view.Errorf("git.log.failed", "reading log: %v", err)
	}
	defer iter.Close()

	limit := req.Int("limit")
	older := false
	err = iter.ForEach(func(c *object.Commit) error {
		if len(t.Rows) >= limit {
			older = true
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
	if older {
		t.Warnings = append(t.Warnings, olderCommits(req.Surface(), len(t.Rows), limit, narrowed))
		return t, nil
	}
	t.Total = len(t.Rows)
	return t, nil
}

// olderCommits says that the history goes on past the commits shown, in place
// of the total a cut list has no cheap way to give.
//
// **A total equal to the rows sent says the history is whole.** The handshake
// tells every agent that a table's total counts every row there is and that
// more than the rows sent means the list is cut, so a log answering `total: 5`
// with five rows read as a repository of exactly five commits, in a free read,
// at every limit. The walk already holds the answer to the question that
// matters: the callback is handed the commit after the last one shown, or it is
// not, and one commit past the limit is all the cut costs to see. Counting the
// whole history to put a number beside twenty rows would walk every commit a
// large repository has, and with `file` every tree a commit touched, so there
// is no number: the total is absent when the list was cut and exact when the
// walk ran out of commits first.
//
// An advisory warning and no cursor: the commits shown are all there, and a
// cursor would need an input that starts the walk somewhere else, a feature
// this list has not been asked for. `limit` reaches five hundred back and
// `file` follows one path as far as it goes.
//
// The way on is what the call has not already used. A log already narrowed to
// one path, at the most `limit` allows, was told to narrow it by the one input
// it had spent, which is no advice at all: what is left to it is a deeper path.
func olderCommits(sf plugin.Surface, shown, limit int, narrowed bool) view.Error {
	hint := sf.InputName("file") + " narrows it to the commits that touched one path"
	if narrowed {
		hint = "a narrower path in " + sf.InputName("file") + " narrows it further"
	}
	if limit < maxLogLimit {
		hint = sf.InputName("limit") + " shows more, up to " + strconv.Itoa(maxLogLimit) + "; " + hint
	}
	return view.Error{
		Code:     "git.log.older",
		Advisory: true,
		Message:  "the history continues past the newest " + format.CountOf(shown, "commit"),
		Hint:     hint,
	}
}

// within is the filter `git log -- <path>` applies to the paths a commit
// changed: the path itself, or anything beneath it when it names a directory.
//
// **A directory matched nothing.** LogOptions.FileName compares a changed
// path with the name for equality, so `--file deploy` answered "no commit
// reaching HEAD touched deploy" in a repository where most of the commits
// had: a plain falsehood, in the sentence that exists so an empty table is
// not read as a failure. The description promised `git log -- <path>`, and
// git takes a directory there.
func within(rel string) func(string) bool {
	if rel == "." {
		return func(string) bool { return true }
	}
	below := rel + "/"
	return func(changed string) bool { return changed == rel || strings.HasPrefix(changed, below) }
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
	// Found and opened as a run finds and opens it, which opens no pipe
	// planted in the repository: completion's two-second bound cannot
	// interrupt an open(2) any more than a caller can.
	root, verr := repoRoot(req, path)
	if verr != nil {
		return nil
	}
	files := &repoFiles{req: req}
	defer files.close()
	repo, verr := openAt(req, files, root, path, readsObjects)
	if verr != nil {
		return nil
	}
	defer release(repo)
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
