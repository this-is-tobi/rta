package git

import (
	"context"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/pkg/view"
)

// "which branch am I on" only half answers where the work is going. Three
// remotes with confusingly similar URLs is how somebody pushes a fix to their
// fork and waits for a review nobody can see.

func TestRemotesListsWhereTheRepositoryReaches(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	for _, r := range []struct{ name, url string }{
		{"origin", "https://git.example.com/team/app.git"},
		{"fork", "ssh://git@git.example.com/me/app.git"},
	} {
		if _, err := repo.CreateRemote(&config.RemoteConfig{
			Name: r.name, URLs: []string{r.url},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// One of them has been fetched from; the other has not.
	fetched(t, repo, "origin", "master", first)
	fetched(t, repo, "origin", "release", first)

	tbl := table(t, runRemotes, req(t, dir, nil))
	rows := map[string][]string{}
	for _, row := range tbl.Rows {
		rows[row[0]] = row
	}
	if got := rows["origin"][1]; got != "https://git.example.com/team/app.git" {
		t.Errorf("origin URL = %q", got)
	}
	// What this repository knows, from the refs a fetch left behind — never
	// from a network call.
	if got := rows["origin"][2]; got != "2" {
		t.Errorf("origin branches = %q, want 2", got)
	}
	if got := rows["fork"][2]; got != "0" {
		t.Errorf("fork branches = %q, want 0 — never fetched is a fact, not a gap", got)
	}
}

// A credential in a remote URL is a password in a file people paste into
// issues. The same rule `git config` follows, in the other place a URL is
// printed.
func TestRemotesMasksACredentialInAURL(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://ci-bot:glpat-SECRETTOKENVALUE@git.example.com/team/app.git"},
	}); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runRemotes, req(t, dir, nil))
	got := tbl.Rows[0][1]
	if strings.Contains(got, "glpat-SECRETTOKENVALUE") {
		t.Fatalf("the token is on screen: %q", got)
	}
	if !strings.Contains(got, "git.example.com/team/app.git") {
		t.Errorf("URL = %q, want the host and path kept — masking is not deleting", got)
	}
}

// A local-only repository says so rather than drawing an empty table, which
// reads as a query that failed.
func TestARepositoryWithNoRemotesSaysSo(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	v, err := runRemotes(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	if !strings.Contains(txt.Body, "local only") {
		t.Errorf("body = %q", txt.Body)
	}
}

// The detail page opens with the answer the tile gives, then the tables it is
// assembled from. Reassembling "am I ahead of origin" out of three tables is
// the work the summary exists to save.
func TestTheDetailedOverviewLeadsWithTheSummaryAndEndsWithTheRemotes(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	v, err := runOverview(context.Background(), req(t, dir, map[string]any{"detail": true}))
	if err != nil {
		t.Fatal(err)
	}
	sections := v.(view.Sections)
	if len(sections.Items) == 0 {
		t.Fatal("the page is empty")
	}
	if got := sections.Items[0].ID; got != "summary" {
		t.Errorf("first section = %q, want the summary", got)
	}
	if got := sections.Items[len(sections.Items)-1].ID; got != "remotes" {
		t.Errorf("last section = %q, want the remotes", got)
	}
	// And the summary is the compact view, not the page again.
	if _, ok := sections.Items[0].View.(view.KeyValue); !ok {
		t.Errorf("the summary section is a %s", view.TypeOf(sections.Items[0].View))
	}
}
