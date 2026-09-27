package git

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gitconfig "github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/pkg/plugin"
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

// scopeRows is every row of scope in tbl, as key=value.
func scopeRows(tbl view.Table, scope string) []string {
	var out []string
	for _, r := range tbl.Rows {
		if r[0] == scope {
			out = append(out, r[1]+"="+r[2])
		}
	}
	return out
}

// git's system file is compiled in as its build's prefix, and this read
// /etc/gitconfig alone: Homebrew's git reads /opt/homebrew/etc/gitconfig, and
// Apple's reads its developer tools' gitconfig before /etc/gitconfig, so a key
// set in either was missing. Every one that exists is the system scope, in
// the order git reads them.
func TestConfigReadsTheSystemFileOfEveryUsualBuildOfGit(t *testing.T) {
	home := machineConfig(t, "")
	vendorGitConfigs = []string{filepath.Join(home, "clt", "gitconfig")}
	systemGitConfigs = []string{filepath.Join(home, "etc", "gitconfig"), filepath.Join(home, "brew", "gitconfig")}
	writeFile(t, home, "clt/gitconfig", "[credential]\n\thelper = osxkeychain\n")
	writeFile(t, home, "brew/gitconfig", "[init]\n\tdefaultBranch = trunk\n")
	// Another build's prefix this user cannot read, and one under a file,
	// are passed over as git passes them over, not a failure.
	writeFile(t, home, "locked/etc/gitconfig", "[a]\n\tb = c\n")
	if err := os.Chmod(filepath.Join(home, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, "locked"), 0o755) })
	writeFile(t, home, "afile", "")
	systemGitConfigs = append(systemGitConfigs,
		filepath.Join(home, "locked", "etc", "gitconfig"), filepath.Join(home, "afile", "etc", "gitconfig"))
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	want := []string{"credential.helper=osxkeychain", "init.defaultBranch=trunk"}
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		want = append(want, "a.b=c") // a mode of 0 locks nothing away from either
	}
	if got := scopeRows(table(t, runConfig, req(t, dir, nil)), "system"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("system rows = %v, want %v", got, want)
	}
}

// The environment chooses the files as it does for git: GIT_CONFIG_SYSTEM
// names the system file in place of the build's own, with Apple's still read
// beside it; GIT_CONFIG_GLOBAL names the global one in place of both; set to
// nothing, either reads no file; and GIT_CONFIG_NOSYSTEM reads no system file
// at all.
func TestConfigHonoursTheEnvironmentGitReadsItsFilesBy(t *testing.T) {
	home := machineConfig(t, "[user]\n\tname = From Home\n")
	vendorGitConfigs = []string{filepath.Join(home, "clt", "gitconfig")}
	systemGitConfigs = []string{filepath.Join(home, "etc", "gitconfig")}
	writeFile(t, home, "clt/gitconfig", "[a]\n\tclt = 1\n")
	writeFile(t, home, "etc/gitconfig", "[a]\n\tetc = 1\n")
	writeFile(t, home, "named-system", "[a]\n\tnamed = 1\n")
	writeFile(t, home, "named-global", "[a]\n\tglobal = 1\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	rows := func(scope string) string {
		return strings.Join(scopeRows(table(t, runConfig, req(t, dir, nil)), scope), " ")
	}
	if got := rows("system"); got != "a.clt=1 a.etc=1" {
		t.Errorf("system rows = %q, with no variable set", got)
	}
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "named-system"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "named-global"))
	if got := rows("system"); got != "a.clt=1 a.named=1" {
		t.Errorf("system rows = %q, want the named file in place of /etc/gitconfig's, Apple's beside it", got)
	}
	if got := rows("global"); got != "a.global=1" {
		t.Errorf("global rows = %q, want the named file's alone", got)
	}
	t.Setenv("GIT_CONFIG_SYSTEM", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if got := rows("system"); got != "a.clt=1" {
		t.Errorf("system rows = %q, with GIT_CONFIG_SYSTEM set to nothing", got)
	}
	if got := rows("global"); got != "" {
		t.Errorf("global rows = %q, with GIT_CONFIG_GLOBAL set to nothing", got)
	}
	for value, reads := range map[string]bool{"1": false, "true": false, "YES": false, "0": true, "false": true, "": true} {
		t.Setenv("GIT_CONFIG_NOSYSTEM", value)
		if got := rows("system"); (got != "") != reads {
			t.Errorf("system rows = %q, with GIT_CONFIG_NOSYSTEM=%q", got, value)
		}
	}
}

// An include is read by git as though it were written in its place, and not
// followed here: the rows name it as written, and a warning counts what they
// do not show, on every surface.
func TestConfigCountsTheIncludesItDoesNotFollow(t *testing.T) {
	machineConfig(t, "[include]\n\tpath = ~/more.gitconfig\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[includeIf \"gitdir:~/work/\"]\n\tpath = ~/work.gitconfig\n")

	tbl := table(t, runConfig, req(t, dir, nil))
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "git.config.include" ||
		!strings.HasPrefix(tbl.Warnings[0].Message, "2 files the config includes are not read") {
		t.Errorf("warnings = %+v, want git.config.include counting both includes", tbl.Warnings)
	}
	if got := rowFor(t, tbl, "Key", "includeIf.gitdir:~/work/.path"); got[2] != "~/work.gitconfig" {
		t.Errorf("the includeIf row = %v, want it shown as written", got)
	}
	mcp := table(t, runConfig, guarded(t, dir, dir).WithSurface(plugin.SurfaceMCP))
	if len(mcp.Warnings) != 1 || !strings.HasPrefix(mcp.Warnings[0].Message, "1 file the config includes is not read") {
		t.Errorf("over MCP, warnings = %+v, want the repository's own include alone counted", mcp.Warnings)
	}
}
