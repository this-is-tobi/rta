package git

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func remotesCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.remotes",
		Summary:      "Where this repository fetches from and pushes to",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "The other half of \"which branch am I on\": which server that branch reaches, " +
			"and whether this machine has ever heard from it. Three remotes with confusingly " +
			"similar URLs is how somebody pushes a fix to their fork and waits for a review " +
			"nobody can see.\n\n" +
			"Branches counts what this repository knows about that remote — the refs a fetch " +
			"left behind — so a remote that has never been fetched reads as 0 rather than as " +
			"missing.\n\n" +
			"A credential embedded in a remote URL is masked, the same rule `git config` " +
			"follows: `https://user:token@host/repo.git` is a password in a file people paste " +
			"into issues, and it is not what anybody is asking this for.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runRemotes,
	}
}

func runRemotes(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return nil, view.Errorf("git.remotes.failed", "reading remotes: %v", err)
	}
	known := knownBranches(repo)

	t := view.Table{Columns: []view.Column{
		{Name: "Remote"},
		{Name: "URL"},
		{Name: "Branches", Kind: view.KindNumber},
	}}
	sort.Slice(remotes, func(i, j int) bool {
		return remotes[i].Config().Name < remotes[j].Config().Name
	})
	for _, r := range remotes {
		c := r.Config()
		// One row per URL. A remote with a separate pushurl is two different
		// places under one name, and collapsing them is how "I pushed it" and
		// "it is not there" both stay true.
		urls := c.URLs
		if len(urls) == 0 {
			urls = []string{""}
		}
		for _, u := range urls {
			t.Rows = append(t.Rows, []string{
				c.Name, maskURLCredentials(u), strconv.Itoa(known[c.Name]),
			})
		}
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return view.Text{Body: "No remotes — this repository is local only."}, nil
	}
	return t, nil
}

// knownBranches counts the remote-tracking refs each remote has left behind.
//
// From the refs rather than from a network call, like everything else in this
// plugin: the question a person is asking is what *this* repository knows, and
// a capability that dialled a server to draw a table would be a different and
// much more expensive thing wearing the same name.
func knownBranches(repo *git.Repository) map[string]int {
	out := map[string]int{}
	refs, err := repo.References()
	if err != nil {
		return out
	}
	_ = refs.ForEach(func(r *plumbing.Reference) error {
		name := r.Name()
		if !name.IsRemote() {
			return nil
		}
		if remote, _, ok := strings.Cut(name.Short(), "/"); ok {
			out[remote]++
		}
		return nil
	})
	return out
}
