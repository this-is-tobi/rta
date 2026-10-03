package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/internal/render/cli"
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
	if got := rows["origin"][3]; got != "2" {
		t.Errorf("origin branches = %q, want 2", got)
	}
	if got := rows["fork"][3]; got != "0" {
		t.Errorf("fork branches = %q, want 0 — never fetched is a fact, not a gap", got)
	}
}

// A remote's URLs say what git does with each: it fetches from the first url
// and no other, and pushes to every pushurl where one is set and to every url
// where none is. Two rows of one name that differ in a few characters did not
// say which one a push reaches, the mistake this capability exists to catch.
func TestEachURLOfARemoteSaysWhatGitUsesItFor(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+
		"[remote \"plain\"]\n\turl = https://a.example/r.git\n"+
		"[remote \"split\"]\n\turl = https://b.example/r.git\n\turl = https://unused.example/r.git\n"+
		"\tpushurl = https://c.example/r.git\n"+
		"[remote \"both\"]\n\turl = https://d.example/r.git\n\turl = https://e.example/r.git\n"+
		"[remote \"bare\"]\n\tfetch = +refs/heads/*:refs/remotes/bare/*\n")

	var got []string
	for _, r := range table(t, runRemotes, req(t, dir, nil)).Rows {
		got = append(got, r[0]+" "+r[1]+" ["+r[2]+"]")
	}
	want := []string{
		"bare  []", "both https://d.example/r.git [fetch, push]", "both https://e.example/r.git [push]",
		"plain https://a.example/r.git [fetch, push]", "split https://b.example/r.git [fetch]",
		"split https://unused.example/r.git []", "split https://c.example/r.git [push]",
	}
	if strings.Join(got, "; ") != strings.Join(want, "; ") {
		t.Errorf("rows = %q, want %q", got, want)
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

// A local-only repository says so on a screen rather than drawing an empty
// table, which reads as a query that failed — and is still a table to a
// parser, where the sentence in its place gave `jq '.rows[]'` a text view and
// -o csv a shape it refused with exit 2.
func TestARepositoryWithNoRemotesSaysSo(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	v, err := runRemotes(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok || len(tbl.Rows) != 0 || !strings.Contains(tbl.Empty, "local only") {
		t.Fatalf("remotes = %#v, want an empty table saying the repository is local only", v)
	}
	var out bytes.Buffer
	if err := cli.Render(&out, v, cli.Options{Format: cli.CSV}); err != nil || !strings.HasPrefix(out.String(), "Remote,") {
		t.Errorf("csv = %q (%v), want the header row alone", out.String(), err)
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

// gitSays is what the git on PATH prints for args in dir, without the machine's
// system config; ok is false where there is no git to ask, or it fails.
func gitSays(t *testing.T, dir string, args ...string) (out string, ok bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return "", false
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	b, err := cmd.Output()
	return strings.TrimSuffix(string(b), "\n"), err == nil
}

// git reads a remote, and what a branch tracks, from every file of config it
// reads: a remote set in ~/.gitconfig or a file it includes is one git fetches
// from, a pushurl set there is where it pushes, a url.<base>.insteadOf there
// rewrites the repository's own URL, and branch.<name>.merge there is the
// branch's upstream. git.remotes, git.branches and the overview read the
// repository's own .git/config alone, so each was missing where git shows it.
// Each is read as git reads it, a row naming the file it is set in; over MCP
// from the files git.config shows there, the repository's own, a credential in
// a URL masked as git.config masks it.
func TestARemoteSetInAnyFileGitReadsIsListedWhereItsScopeIsShown(t *testing.T) {
	home := machineConfig(t, "[remote \"corp\"]\n\turl = https://bob:planted-token@corp.example/r.git\n"+
		"[include]\n\tpath = ~/more.gitconfig\n[url \"https://mirror.example/\"]\n\tinsteadOf = https://slow.example/\n")
	writeFile(t, home, "more.gitconfig", "[remote \"origin\"]\n\tpushurl = https://push.example/r.git\n"+
		"[branch \"master\"]\n\tremote = origin\n\tmerge = refs/heads/main\n")
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	fetched(t, repo, "origin", "main", first)
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://slow.example/r.git\n"+
		"\tfetch = +refs/heads/*:refs/remotes/origin/*\n[include]\n\tpath = shared.cfg\n")
	writeFile(t, dir, ".git/shared.cfg", "[remote \"inner\"]\n\turl = https://inner.example/r.git\n")

	cli := table(t, runRemotes, req(t, dir, nil))
	if got, want := fmt.Sprint(cli.Rows), fmt.Sprint([][]string{
		{"corp", "https://bob:" + view.Mask + "@corp.example/r.git", "fetch, push", "0", filepath.Join(home, ".gitconfig")},
		{"inner", "https://inner.example/r.git", "fetch, push", "0", ".git/shared.cfg"},
		{"origin", "https://mirror.example/r.git", "fetch", "1", ".git/config"},
		{"origin", "https://push.example/r.git", "push", "1", filepath.Join(home, "more.gitconfig")},
	}); got != want {
		t.Errorf("at a terminal, git.remotes rows = %s, want %s", got, want)
	}
	if names, ok := gitSays(t, dir, "remote"); ok && names != "corp\ninner\norigin" {
		t.Errorf("git lists the remotes %q", names)
	}
	if url, ok := gitSays(t, dir, "remote", "get-url", "origin"); ok && url != "https://mirror.example/r.git" {
		t.Errorf("git reads origin's URL as %q", url)
	}
	if got := rowFor(t, table(t, runBranches, req(t, dir, nil)), "Name", "master"); got[2] != "origin/main" ||
		got[3] != "up to date" {
		t.Errorf("at a terminal, master = %v, want it tracking origin/main, as the included file sets", got)
	}
	if upstream, ok := gitSays(t, dir, "rev-parse", "--abbrev-ref", "master@{upstream}"); ok && upstream != "origin/main" {
		t.Errorf("git reads master's upstream as %q", upstream)
	}
	overview, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil || !strings.Contains(fmt.Sprint(overview), "origin/main (up to date)") {
		t.Errorf("at a terminal, git.overview = %v %v, want it tracking origin/main", overview, err)
	}

	mcp := table(t, runRemotes, mcpReq(t, dir, dir))
	if got, want := fmt.Sprint(mcp.Rows), fmt.Sprint([][]string{
		{"inner", "https://inner.example/r.git", "fetch, push", "0", ".git/shared.cfg"},
		{"origin", "https://slow.example/r.git", "fetch, push", "1", ".git/config"},
	}); got != want {
		t.Errorf("over MCP, git.remotes rows = %s, want %s", got, want)
	}
	if got := rowFor(t, table(t, runBranches, mcpReq(t, dir, dir)), "Name", "master"); got[2] != "" {
		t.Errorf("over MCP, master = %v, want no upstream, the operator's config setting it", got)
	}
	overview, err = runOverview(context.Background(), mcpReq(t, dir, dir))
	if err != nil || strings.Contains(fmt.Sprint(overview), "origin/main") {
		t.Errorf("over MCP, git.overview = %v %v, want no tracking the operator's config sets", overview, err)
	}
}

// A remote, or what a branch tracks, set in a file an include names that is
// not followed — over MCP one outside the roots, anywhere one this cannot
// decide — is missing from git.remotes and git.branches, and each says so, as
// git.config does, rather than calling the repository local only. The warning
// names the include as it is written, whether or not the file is there.
func TestARemoteAnUnfollowedIncludeSetsIsSaidToBeMissing(t *testing.T) {
	home := machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, home, "outside.cfg", "[remote \"planted\"]\n\turl = https://planted.example/r.git\n"+
		"[branch \"master\"]\n\tremote = planted\n\tmerge = refs/heads/main\n")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = ~/outside.cfg\n")
	codes := func(ws []view.Error) (out []string) {
		for _, w := range ws {
			out = append(out, w.Code)
		}
		return out
	}

	for _, planted := range []bool{true, false} {
		if !planted {
			if err := os.Remove(filepath.Join(home, "outside.cfg")); err != nil {
				t.Fatal(err)
			}
		}
		remotes := table(t, runRemotes, mcpReq(t, dir, dir))
		if len(remotes.Rows) != 0 || !slices.Equal(codes(remotes.Warnings), []string{"git.remotes.include.outside"}) ||
			!strings.Contains(remotes.Warnings[0].Message, "~/outside.cfg") || strings.Contains(remotes.Empty, "local only") {
			t.Errorf("over MCP, the file there: %v, git.remotes = %+v, want no rows and the include named", planted, remotes)
		}
		branches := table(t, runBranches, mcpReq(t, dir, dir))
		if !slices.Equal(codes(branches.Warnings), []string{"git.branches.include.outside"}) {
			t.Errorf("over MCP, the file there: %v, git.branches warnings = %+v, want the include named", planted,
				branches.Warnings)
		}
	}

	writeFile(t, home, "outside.cfg", "[remote \"planted\"]\n\turl = https://planted.example/r.git\n")
	if cli := table(t, runRemotes, req(t, dir, nil)); len(cli.Rows) != 1 || len(cli.Warnings) != 0 {
		t.Errorf("at a terminal, git.remotes = %+v, want the included remote and no warning", cli)
	}
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = %(prefix)/etc/remotes.cfg\n")
	if cli := table(t, runRemotes, req(t, dir, nil)); !slices.Equal(codes(cli.Warnings), []string{"git.remotes.include"}) {
		t.Errorf("at a terminal, git.remotes warnings = %+v, want the include this cannot decide said", cli.Warnings)
	}
}
