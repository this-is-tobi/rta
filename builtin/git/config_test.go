package git

import (
	"context"
	"testing"

	gitconfig "github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/pkg/view"
)

// addConfigRows is exercised directly against a hand-built Config, so the
// row shape (scope, dotted key, value — including the subsection case) is
// proven without depending on what any real machine's gitconfig happens to
// contain.
func TestAddConfigRowsHandlesSectionsAndSubsections(t *testing.T) {
	cfg := gitconfig.NewConfig()
	cfg.Raw.Section("user").AddOption("name", "Ada Lovelace")
	cfg.Raw.Section("remote").Subsection("origin").AddOption("url", "https://example.com/repo.git")

	tbl := view.Table{Columns: []view.Column{{Name: "Scope"}, {Name: "Key"}, {Name: "Value"}}}
	addConfigRows(&tbl, "global", cfg)

	if got := rowFor(t, tbl, "Key", "user.name"); got[0] != "global" || got[2] != "Ada Lovelace" {
		t.Errorf("user.name row = %v, want [global user.name \"Ada Lovelace\"]", got)
	}
	if got := rowFor(t, tbl, "Key", "remote.origin.url"); got[0] != "global" || got[2] != "https://example.com/repo.git" {
		t.Errorf("remote.origin.url row = %v, want scope global", got)
	}
}

// core.bare is written by PlainInit itself (config.Config.Marshal always
// calls marshalCore), so it is a key guaranteed present without this test
// having to set up anything — unlike the rest of a real .gitconfig, which
// varies by machine and would make an assertion on it flaky.
func TestConfigReadsLocalScopeFromTheRepository(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	tbl := table(t, runConfig, req(t, dir, nil))
	if got := rowFor(t, tbl, "Key", "core.bare"); got[0] != "local" || got[2] != "false" {
		t.Errorf("core.bare row = %v, want [local core.bare false]", got)
	}
}

func TestConfigReadsSubsectionKeysFromARemote(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{
		Name: "origin", URLs: []string{"https://example.com/repo.git"},
	}); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runConfig, req(t, dir, nil))
	if got := rowFor(t, tbl, "Key", "remote.origin.url"); got[0] != "local" || got[2] != "https://example.com/repo.git" {
		t.Errorf("remote.origin.url row = %v, want [local remote.origin.url https://example.com/repo.git]", got)
	}
}

// git reads both global files, $XDG_CONFIG_HOME/git/config (or
// ~/.config/git/config) and then ~/.gitconfig, and go-git's LoadConfig reads
// the first that exists and stops: a key set only in the other was missing
// from the answer to what git is configured with.
func TestConfigReadsEveryGlobalFileGitReads(t *testing.T) {
	home := machineConfig(t, "[user]\n\tname = From Home\n")
	writeFile(t, home, ".config/git/config", "[user]\n\temail = from-xdg@example.com\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	tbl := table(t, runConfig, req(t, dir, nil))
	if got := rowFor(t, tbl, "Key", "user.email"); got[0] != "global" || got[2] != "from-xdg@example.com" {
		t.Errorf("user.email row = %v, want the global scope's, from ~/.config/git/config", got)
	}
	if got := rowFor(t, tbl, "Key", "user.name"); got[0] != "global" || got[2] != "From Home" {
		t.Errorf("user.name row = %v, want the global scope's, from ~/.gitconfig", got)
	}
}

func TestConfigOnANonRepositoryFailsWithAClearError(t *testing.T) {
	dir := t.TempDir()
	_, err := runConfig(context.Background(), req(t, dir, nil))
	if err == nil {
		t.Fatal("expected an error opening a non-repository")
	}
}
