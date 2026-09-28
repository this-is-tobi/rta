package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// gitGets is what the git on PATH reads key as in dir, run from dir as a
// person there runs it: set is whether some file sets it, and runs whether
// git reads its config at all; ok is false where there is no git to ask.
func gitGets(t *testing.T, dir, key string) (value string, set, runs, ok bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return "", false, false, false
	}
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "PWD="+dir)
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSuffix(string(out), "\n"), true, true, true
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return "", false, true, true
	}
	return "", false, false, true
}

// keyRows is each row of tbl, git.config's, for key, as value@origin, in the
// order of the rows.
func keyRows(tbl view.Table, key string) []string {
	var out []string
	for _, r := range tbl.Rows {
		if strings.EqualFold(r[1], key) {
			out = append(out, r[2]+"@"+r[3])
		}
	}
	return out
}

// mcpReq is the request an MCP call arrives as, confined to root and asking
// about path.
func mcpReq(t *testing.T, root, path string) plugin.Request {
	t.Helper()
	return guarded(t, root, path).WithSurface(plugin.SurfaceMCP)
}

// A file an include names is read by git as though it were written in place
// of the include: a relative path from the file the include is in, ~ from the
// home directory, a key set after the include winning over the file's and one
// set before it losing, and a file that is not there passed over. None of it
// was read, so git.hooks listed the repository's own hooks while git ran the
// directory an included file named. Each is read as git reads it, and each
// row names the file it came from.
func TestAnIncludeIsReadInItsPlaceAsGitReadsIt(t *testing.T) {
	home := machineConfig(t, "[include]\n\tpath = more.gitconfig\n")
	writeFile(t, home, "more.gitconfig", "[x]\n\tglobal = from-home\n[include]\n\tpath = ~/deeper.gitconfig\n")
	writeFile(t, home, "deeper.gitconfig", "[x]\n\tdeeper = yes\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\thooksPath = before\n[include]\n\tpath = shared.cfg\n"+
		"[x]\n\tafter = local\n")
	writeFile(t, dir, ".git/shared.cfg", "[core]\n\thooksPath = from-include\n[x]\n\tafter = included\n"+
		"[include]\n\tpath = missing.cfg\n")
	writeExecutable(t, dir, "from-include/pre-commit")

	tbl := table(t, runConfig, req(t, dir, nil))
	for key, want := range map[string][]string{
		"x.global":       {"from-home@" + filepath.Join(home, "more.gitconfig")},
		"x.deeper":       {"yes@" + filepath.Join(home, "deeper.gitconfig")},
		"core.hooksPath": {"before@.git/config", "from-include@.git/shared.cfg"},
		"x.after":        {"included@.git/shared.cfg", "local@.git/config"},
	} {
		if got := keyRows(tbl, key); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", key, got, want)
		}
	}
	if len(tbl.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none", tbl.Warnings)
	}
	if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[2] != "from-include/pre-commit" {
		t.Errorf("pre-commit row = %v, want the directory the included file names", row)
	}
	for key, want := range map[string]string{"core.hooksPath": "from-include", "x.after": "local", "x.deeper": "yes"} {
		if got, set, runs, ok := gitGets(t, dir, key); ok && (!runs || !set || got != want) {
			t.Errorf("git reads %s as %q (set %v, runs %v), where this test says %q", key, got, set, runs, want)
		}
	}
}

// git takes an include from the environment only by an absolute path, or one
// from ~: a relative one has no file to be taken from, and git refuses to run
// with it ("relative config includes must come from files").
func TestAnIncludeInGitsEnvironmentIsFollowedWhereItIsAbsolute(t *testing.T) {
	home := machineConfig(t, "")
	writeFile(t, home, "env.gitconfig", "[core]\n\thooksPath = ~/env-hooks\n")
	writeExecutable(t, home, "env-hooks/pre-push")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	t.Setenv("GIT_CONFIG_PARAMETERS", "'include.path'='~/env.gitconfig'")
	if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-push"); row[2] != filepath.Join(home, "env-hooks", "pre-push") {
		t.Errorf("pre-push row = %v, want the directory the environment's include names", row)
	}
	if got := keyRows(table(t, runConfig, req(t, dir, nil)), "core.hooksPath"); !slices.Equal(got,
		[]string{"~/env-hooks@" + filepath.Join(home, "env.gitconfig")}) {
		t.Errorf("core.hooksPath rows = %v, want the environment's included file's", got)
	}

	t.Setenv("GIT_CONFIG_PARAMETERS", "'include.path'='env.gitconfig'")
	if _, err := runHooks(context.Background(), req(t, dir, nil)); errCode(err) != "git.hooks.failed" ||
		!strings.Contains(err.Error(), "relative config includes must come from files") {
		t.Errorf("git.hooks with a relative include in the environment = %v, want it refused as git refuses it", err)
	}
	if _, _, runs, ok := gitGets(t, dir, "core.hooksPath"); ok && runs {
		t.Error("git runs with a relative include in its environment")
	}
}

// includeIfHolds reports whether git.config, and the git on PATH where there
// is one, read the file ~/y.cfg that config, the repository's own, includes
// under a condition, and fails the test where they disagree with holds. The
// comparison is left out where that git is older than since.
func includeIfHolds(t *testing.T, dir, config string, holds bool, since gitSince) {
	t.Helper()
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+config)
	if got := keyRows(table(t, runConfig, req(t, dir, nil)), "x.y"); (len(got) == 1) != holds {
		t.Errorf("x.y rows = %v, where the condition holds: %v", got, holds)
	}
	if since.version != "" {
		skipGitOlderThan(t, since)
	}
	if _, set, runs, ok := gitGets(t, dir, "x.y"); ok && (!runs || set != holds) {
		t.Errorf("git reads the included file: %v (runs: %v), where this says the condition holds: %v", set, runs, holds)
	}
}

// Every condition an includeIf can have, decided as git's
// include_condition_is_true decides it, and by git's own matcher: gitdir:
// against the git directory, relative patterns matched anywhere and a
// trailing slash matching all under it; gitdir/i: the same in any case;
// onbranch: against the branch HEAD names; hasconfig:remote.*.url: against
// every remote's URL, one set after the condition included. A condition git
// has no prefix for, or spelled in another case, holds nowhere.
func TestEveryIncludeIfConditionIsDecidedAsGitDecidesIt(t *testing.T) {
	home := machineConfig(t, "")
	writeFile(t, home, "y.cfg", "[x]\n\ty = yes\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(real)
	branches, remotes := gitSince{"2.23.0", "read onbranch: conditions"}, gitSince{"2.36.0", "read hasconfig: conditions"}
	for _, c := range []struct {
		name, cond string
		holds      bool
		since      gitSince
	}{
		{"the git directory", "gitdir:" + real + "/.git", true, gitSince{}},
		{"a directory above it", "gitdir:" + parent + "/", true, gitSince{}},
		{"the checkout, with no slash", "gitdir:" + real, false, gitSince{}},
		{"the checkout's name, anywhere", "gitdir:" + filepath.Base(real) + "/", true, gitSince{}},
		{"a star", "gitdir:" + parent + "/*/.git", true, gitSince{}},
		{"a star, which crosses no slash", "gitdir:" + filepath.Dir(parent) + "/*/.git", false, gitSince{}},
		{"the including file's directory", "gitdir:./", false, gitSince{}},
		{"another case", "gitdir:" + strings.ToUpper(real) + "/", false, gitSince{}},
		{"another case, folded", "gitdir/i:" + strings.ToUpper(real) + "/", true, gitSince{}},
		{"the home directory", "gitdir:~/", false, gitSince{}},
		{"nothing, which is everything", "gitdir:", true, gitSince{}},
		{"the branch", "onbranch:master", true, branches},
		{"a glob of the branch", "onbranch:mas*", true, branches},
		{"another branch", "onbranch:main", false, branches},
		{"the branch's ref", "onbranch:refs/heads/master", false, branches},
		{"a remote's URL", "hasconfig:remote.*.url:https://example.com/**", true, remotes},
		{"no remote's URL", "hasconfig:remote.*.url:https://elsewhere.example/**", false, remotes},
		{"a star, which crosses no slash in a URL", "hasconfig:remote.*.url:https://example.com/*", false, remotes},
		{"a remote by its name", "hasconfig:remote.origin.url:https://example.com/**", false, remotes},
		{"a condition in another case", "GitDir:/", false, gitSince{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			includeIfHolds(t, dir, "[includeIf \""+c.cond+"\"]\n\tpath = ~/y.cfg\n"+
				"[remote \"origin\"]\n\turl = https://example.com/team/repo.git\n", c.holds, c.since)
		})
	}
}

// A gitdir: pattern starting with ./ is taken from the directory of the file
// it is written in, and ~ is the home directory with its links resolved, as
// git takes both: each holds where the repository is under them.
func TestAGitDirPatternIsTakenFromItsFileAndTheHomeDirectory(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	parent := filepath.Dir(dir)
	writeFile(t, parent, "y.cfg", "[x]\n\ty = yes\n")
	writeFile(t, parent, "global.cfg", "[includeIf \"gitdir:./\"]\n\tpath = y.cfg\n")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(parent, "global.cfg"))
	includeIfHolds(t, dir, "", true, gitSince{})

	t.Setenv("HOME", parent)
	unsetenv(t, "GIT_CONFIG_GLOBAL")
	includeIfHolds(t, dir, "[includeIf \"gitdir:~/\"]\n\tpath = ~/y.cfg\n", true, gitSince{})
}

// onbranch: is the branch HEAD names, one with no commit yet included, and
// holds nowhere on a detached HEAD; in a linked worktree it is that
// worktree's HEAD, and gitdir: is matched against the worktree's own git
// directory, which is under the main checkout's.
func TestOnBranchIsTheBranchHEADNames(t *testing.T) {
	home := machineConfig(t, "")
	writeFile(t, home, "y.cfg", "[x]\n\ty = yes\n")
	dir, repo := testRepo(t)
	head := commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	since := gitSince{"2.23.0", "read onbranch: conditions"}

	setHead := func(ref *plumbing.Reference) {
		t.Helper()
		if err := repo.Storer.SetReference(ref); err != nil {
			t.Fatal(err)
		}
	}
	setHead(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/feature/one"))
	for cond, holds := range map[string]bool{"feature/": true, "feature/one": true, "feature*": false, "master": false} {
		includeIfHolds(t, dir, "[includeIf \"onbranch:"+cond+"\"]\n\tpath = ~/y.cfg\n", holds, since)
	}
	setHead(plumbing.NewHashReference(plumbing.HEAD, head))
	includeIfHolds(t, dir, "[includeIf \"onbranch:**\"]\n\tpath = ~/y.cfg\n", false, since)

	setHead(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/master"))
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(filepath.Dir(real), "linked")
	admin := filepath.Join(real, ".git", "worktrees", "linked")
	writeFile(t, admin, "HEAD", "ref: refs/heads/side\n")
	writeFile(t, admin, "commondir", "../..\n")
	writeFile(t, admin, "gitdir", filepath.Join(linked, ".git")+"\n")
	writeFile(t, linked, ".git", "gitdir: "+admin+"\n")
	for cond, holds := range map[string]bool{
		"onbranch:side": true, "onbranch:master": false, "gitdir:" + real + "/.git/": true, "gitdir:" + linked + "/": false,
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[includeIf \""+cond+"\"]\n\tpath = ~/y.cfg\n")
		if got := keyRows(table(t, runConfig, req(t, linked, nil)), "x.y"); (len(got) == 1) != holds {
			t.Errorf("in the linked worktree, with %s, x.y rows = %v, where the condition holds: %v", cond, got, holds)
		}
		if _, set, runs, ok := gitGets(t, linked, "x.y"); ok && (!runs || set != holds) {
			t.Errorf("in the linked worktree, with %s, git reads the included file: %v (runs: %v)", cond, set, runs)
		}
	}
}

// includeChain writes .git/d1.cfg to .git/d<n>.cfg, each setting x.d<i> and
// including the next, and has the repository's config include the first.
func includeChain(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		writeFile(t, dir, ".git/d"+strconv.Itoa(i)+".cfg", "[x]\n\td"+strconv.Itoa(i)+" = 1\n[include]\n\tpath = d"+strconv.Itoa(i+1)+".cfg\n")
	}
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = d1.cfg\n")
}

// git follows includes of includes ten deep, and stops at the eleventh
// ("exceeded maximum include depth"), which is how a file that includes
// itself ends: it runs no command, and no hook, with one. An eleventh that is
// not there is passed over, as any include of a file that is not there is.
func TestIncludesPastGitsDepthAreRefusedAsGitRefusesThem(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	includeChain(t, dir, 10)
	if got := keyRows(table(t, runConfig, req(t, dir, nil)), "x.d10"); len(got) != 1 {
		t.Errorf("x.d10 rows = %v, want the tenth include read, the eleventh not being there", got)
	}
	if _, set, runs, ok := gitGets(t, dir, "x.d10"); ok && (!runs || !set) {
		t.Errorf("git reads x.d10: %v (runs: %v), with ten includes deep", set, runs)
	}

	writeFile(t, dir, ".git/d11.cfg", "[x]\n\td11 = 1\n")
	for name, config := range map[string]string{
		"eleven deep":             "",
		"a file including itself": "[include]\n\tpath = config\n",
	} {
		if config != "" {
			writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+config)
		}
		for capability, run := range map[string]plugin.Handler{"git.config": runConfig, "git.hooks": runHooks, "git.log": runLog} {
			if _, err := run(context.Background(), req(t, dir, nil)); err == nil ||
				!strings.Contains(err.Error(), "exceeded maximum include depth") {
				t.Errorf("%s, %s = %v, want it refused as git refuses it", name, capability, err)
			}
		}
		if _, _, runs, ok := gitGets(t, dir, "x.d1"); ok && runs {
			t.Errorf("%s, git runs", name)
		}
	}
}

// git stops at an include it cannot read, where it is there: a directory, a
// file it may not open, an include with no value at all, which git reads as
// "missing value", and a file of what is not config. What is not there, and
// the null device, it reads as nothing. And nothing is opened that would
// wait: a named pipe is refused as not a file, as git.config refuses one in
// the operator's own files' place.
func TestAnIncludeGitCannotReadIsRefused(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	if err := os.MkdirAll(filepath.Join(dir, ".git", "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".git/junk.cfg", "this is not config\n")
	writeFile(t, dir, ".git/unreadable.cfg", "[x]\n\ty = 1\n")
	if err := os.Chmod(filepath.Join(dir, ".git", "unreadable.cfg"), 0); err != nil {
		t.Fatal(err)
	}
	pipe := mkfifo(filepath.Join(dir, ".git", "pipe.cfg")) == nil
	for include, refused := range map[string]bool{
		"path = adir":                 true,
		"path =":                      true,
		"path":                        true,
		"path = junk.cfg":             true,
		"path = unreadable.cfg":       os.Geteuid() != 0,
		"path = pipe.cfg":             pipe,
		"path = missing.cfg":          false,
		"path = " + os.DevNull:        false,
		"path = missing/under/it.cfg": false,
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\t"+include+"\n")
		_, err := runConfig(context.Background(), req(t, dir, nil))
		if (err != nil) != refused || refused && errCode(err) != "git.config.failed" {
			t.Errorf("with %q, git.config = %v, want it refused: %v", include, err, refused)
		}
		if include == "path = pipe.cfg" {
			continue
		}
		if _, _, runs, ok := gitGets(t, dir, "x.y"); ok && runs == refused && os.Geteuid() != 0 {
			t.Errorf("with %q, git runs: %v", include, runs)
		}
	}
}

// An include can name any file on the machine, and one of sections and keys
// reads as config: ~/.my.cnf, a MySQL client's password among its keys, is
// one, and a repository's .git/config, which a caller can write inside the
// root, could have git.config show it. ~/.aws/credentials is one git refuses,
// its keys holding underscores, and so is it refused here, without a word of
// what the parser stopped at.
// Over MCP a file the repository's config includes from outside the roots is
// read, and counts for every answer, as git reads it: its core.hooksPath is
// where git.hooks lists hooks from, and its promisor makes the repository a
// partial clone. None of its keys or values is shown, nor what a file it
// includes holds, since where that is is written in it; git.config names the
// include as one outside the roots, a refusal says a remote is named there
// without naming it, and a hooks directory it names outside the roots is
// refused without its name. A file included from inside the roots is shown,
// with where it is. At a terminal the operator's own files are all shown.
func TestAFileIncludedFromOutsideTheRootsCountsAndIsNeverShownOverMCP(t *testing.T) {
	home := machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, home, ".my.cnf", "[client]\n\tuser = planted-user\n\tpassword = planted-password\n"+
		"\thost = planted-host.example\n[core]\n\thooksPath = from-credentials\n"+
		"[include]\n\tpath = "+filepath.Join(dir, "nested.cfg")+"\n\tpath = %(prefix)/planted-undecided\n")
	writeFile(t, dir, "nested.cfg", "[x]\n\tnested = planted-nested-value\n")
	writeFile(t, dir, ".git/shared.cfg", "[x]\n\tshared = in-the-root\n")
	writeExecutable(t, dir, "from-credentials/pre-commit")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = ~/.my.cnf\n"+
		"\tpath = shared.cfg\n")
	planted := []string{"planted-user", "planted-password", "planted-host", "client.", "from-credentials",
		"planted-nested-value", "nested.cfg"}

	mcp := table(t, runConfig, mcpReq(t, dir, dir))
	shown := fmt.Sprint(mcp.Rows, mcp.Warnings)
	for _, p := range planted {
		if strings.Contains(shown, p) {
			t.Errorf("over MCP, %q reached git.config: %v", p, shown)
		}
	}
	if got := keyRows(mcp, "include.path"); !slices.Equal(got, []string{"~/.my.cnf@.git/config",
		"shared.cfg@.git/config"}) {
		t.Errorf("over MCP, include.path rows = %v, want both includes as written", got)
	}
	if got := keyRows(mcp, "x.shared"); !slices.Equal(got, []string{"in-the-root@.git/shared.cfg"}) {
		t.Errorf("over MCP, x.shared rows = %v, want the file included from inside the roots shown", got)
	}
	if len(mcp.Warnings) != 1 || mcp.Warnings[0].Code != "git.config.include.outside" ||
		!strings.Contains(mcp.Warnings[0].Message, "~/.my.cnf") {
		t.Errorf("over MCP, warnings = %+v, want git.config.include.outside naming the include", mcp.Warnings)
	}
	if row := rowFor(t, table(t, runHooks, mcpReq(t, dir, dir)), "Name", "pre-commit"); row[2] != "from-credentials/pre-commit" {
		t.Errorf("over MCP, pre-commit row = %v, want the directory the file outside the roots names", row)
	}

	cli := table(t, runConfig, req(t, dir, nil))
	for key, want := range map[string]string{
		"client.host": "planted-host.example@" + filepath.Join(home, ".my.cnf"),
		"x.nested":    "planted-nested-value@nested.cfg",
	} {
		if got := keyRows(cli, key); len(got) != 1 || !strings.HasSuffix(got[0], want) {
			t.Errorf("at a terminal, %s rows = %v, want one ending %q", key, got, want)
		}
	}
	if len(cli.Warnings) != 1 || cli.Warnings[0].Code != "git.config.include" {
		t.Errorf("at a terminal, warnings = %+v, want the include under git's install prefix said to be unread",
			cli.Warnings)
	}

	writeFile(t, home, ".my.cnf", "[core]\n\thooksPath = "+filepath.Join(home, "planted-hooks")+"\n")
	if _, err := runHooks(context.Background(), mcpReq(t, dir, dir)); errCode(err) != "core.mcp.path.outside" ||
		strings.Contains(err.Error(), "planted-hooks") {
		t.Errorf("over MCP, git.hooks = %v, want the directory refused as outside the roots, and not named", err)
	}

	writeFile(t, home, ".my.cnf", "[remote \"planted-remote\"]\n\tpromisor = true\n")
	_, err := runLog(context.Background(), mcpReq(t, dir, dir))
	if errCode(err) != "git.objects.partial" || strings.Contains(err.Error(), "planted-remote") ||
		!strings.Contains(err.Error(), "a remote named in a file included from outside the roots") {
		t.Errorf("over MCP, git.log = %v, want a partial clone refused without the remote's name", err)
	}
	if _, err := runLog(context.Background(), req(t, dir, nil)); errCode(err) != "git.objects.partial" ||
		!strings.Contains(err.Error(), "planted-remote") {
		t.Errorf("at a terminal, git.log = %v, want a partial clone refused naming the remote", err)
	}

	writeFile(t, home, ".aws/credentials", "[default]\n\taws_access_key_id = AKIAPLANTEDKEYID\n")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = ~/.aws/credentials\n")
	for name, r := range map[string]plugin.Request{"over MCP": mcpReq(t, dir, dir), "at a terminal": req(t, dir, nil)} {
		_, err := runConfig(context.Background(), r)
		if errCode(err) != "git.config.failed" || name == "over MCP" && (strings.Contains(err.Error(), "AKIA") ||
			strings.Contains(err.Error(), "aws_") || !strings.Contains(err.Error(), "no file of config git reads")) {
			t.Errorf("%s, including ~/.aws/credentials, git.config = %v, want it refused saying nothing of it", name, err)
		}
	}
	if _, _, runs, ok := gitGets(t, dir, "core.bare"); ok && runs {
		t.Error("git runs including ~/.aws/credentials")
	}
}

// rta's own state is refused wherever it sits, and a file an include names is
// no exception: a root drawn around the home directory holds
// ~/.local/share/rta, and an include naming a file there was read, and
// counted for every answer, as a file outside the roots is. Over MCP it
// refuses the call as the gate refuses the path, where a file outside the
// roots names it too, without naming what that file includes; at a terminal it
// is read, as git reads it.
func TestAnIncludeOfRtasOwnStateIsRefusedOverMCP(t *testing.T) {
	home := machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	state := filepath.Join(dir, ".local", "share", "rta")
	t.Setenv("RTA_DATA_DIR", state)
	writeFile(t, state, "planted.cfg", "[core]\n\thooksPath = planted-hooks\n")
	writeExecutable(t, dir, "planted-hooks/pre-commit")
	writeFile(t, home, "outside.cfg", "[include]\n\tpath = "+filepath.Join(state, "planted.cfg")+"\n")
	for name, include := range map[string]string{
		"named in the repository's config":  filepath.Join(state, "planted.cfg"),
		"named in a file outside the roots": "~/outside.cfg",
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = "+include+"\n")
		for capability, run := range map[string]plugin.Handler{"git.config": runConfig, "git.hooks": runHooks, "git.log": runLog} {
			_, err := run(context.Background(), mcpReq(t, dir, dir))
			if errCode(err) != "core.mcp.path.protected" {
				t.Errorf("%s, %s over MCP = %v, want the include refused as rta's own state", name, capability, err)
			}
			if include == "~/outside.cfg" && err != nil && strings.Contains(err.Error(), "planted") {
				t.Errorf("%s, %s over MCP = %v, naming what a file it does not show includes", name, capability, err)
			}
		}
		if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[2] != "planted-hooks/pre-commit" {
			t.Errorf("%s, at a terminal, pre-commit row = %v, want the directory the included file names", name, row)
		}
	}
}

// A hasconfig:remote.*.url condition is a glob a caller writes into the
// repository's config, and whether the file it names is read shows in the
// answer: matched against a URL as written, it would spell out, one
// character after another, a token kept in a URL of the operator's that the
// caller can never read. Over MCP such a URL is matched with its credentials
// masked, as git.config shows one, so a pattern that is after the token
// matches nothing and one that is not matches as it does at a terminal.
func TestAHasconfigConditionCannotSpellOutACredentialOverMCP(t *testing.T) {
	machineConfig(t, "[remote \"operator\"]\n\turl = https://bob:planted-token@git.example/team/r.git\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".git/probe.cfg", "[x]\n\tprobe = matched\n")
	for pattern, overMCP := range map[string]bool{
		"https://bob:planted-token@git.example/**": false,
		"https://bob:planted-*@git.example/**":     false,
		"https://bob:*@git.example/**":             true,
		"https://*@git.example/team/**":            true,
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[includeIf \"hasconfig:remote.*.url:"+pattern+
			"\"]\n\tpath = probe.cfg\n")
		if got := keyRows(table(t, runConfig, req(t, dir, nil)), "x.probe"); len(got) != 1 {
			t.Errorf("at a terminal, with %s, x.probe rows = %v, want the file read", pattern, got)
		}
		if got := keyRows(table(t, runConfig, mcpReq(t, dir, dir)), "x.probe"); (len(got) == 1) != overMCP {
			t.Errorf("over MCP, with %s, x.probe rows = %v, want the file read: %v", pattern, got, overMCP)
		}
	}
}

// git decides a repository's format from its own config file alone, and
// follows no include there: an include setting a format version and
// extensions.objectFormat = sha256 leaves the repository the SHA-1 one git
// opens, and one setting extensions.worktreeConfig reads no config.worktree.
// So neither is taken from one here.
func TestARepositorysFormatIsNeverTakenFromAnInclude(t *testing.T) {
	dir := withConfig(t, "[include]\n\tpath = format.cfg\n")
	writeFile(t, dir, ".git/format.cfg", "[core]\n\trepositoryformatversion = 1\n[extensions]\n"+
		"\tobjectFormat = sha256\n\tworktreeConfig = true\n\tpartialClone = origin\n")
	writeFile(t, dir, ".git/config.worktree", "[core]\n\thooksPath = from-worktree\n")
	for name, code := range codes(t, dir) {
		if code != "" {
			t.Errorf("%s = %q, where the format an include sets is none git reads", name, code)
		}
	}
	for _, row := range table(t, runConfig, req(t, dir, nil)).Rows {
		if row[0] == "worktree" {
			t.Errorf("config row %v, from a config.worktree git does not read", row)
		}
	}
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-object-format", "--git-path", "hooks")
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil || !strings.HasPrefix(string(out), "sha1\n") ||
			strings.Contains(string(out), "from-worktree") {
			t.Errorf("git reads the repository as %q (%v)", out, err)
		}
	}
}

// A remote an included file makes a promisor makes the repository a partial
// clone to git, whose objects this reader cannot all read, and it is refused
// naming the file; a core.excludesFile a file the repository's config
// includes names is put to the gate, as one the repository's own config names
// is, and over MCP one set in a file outside the roots is not named.
func TestAPromisorOrAnExcludesFileSetInAnIncludeCounts(t *testing.T) {
	home := machineConfig(t, "[include]\n\tpath = partial.gitconfig\n")
	writeFile(t, home, "partial.gitconfig", "[remote \"origin\"]\n\tpromisor = true\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	var verr *view.Error
	if _, err := runLog(context.Background(), req(t, dir, nil)); !errors.As(err, &verr) ||
		verr.Code != "git.objects.partial" || !strings.Contains(verr.Message, "from origin") ||
		!strings.Contains(verr.Hint, filepath.Join(home, "partial.gitconfig")) {
		t.Errorf("git.log = %v, want a partial clone refused naming the included file", err)
	}
	if fetches, ok := fetchesByGit(t, dir, "origin"); ok && !fetches {
		t.Error("git does not read the repository as a partial clone")
	}

	home = machineConfig(t, "")
	outside := t.TempDir()
	writeFile(t, outside, "planted-ignores", "*.txt\n")
	writeFile(t, home, "ignores.gitconfig", "[core]\n\texcludesFile = "+filepath.Join(outside, "planted-ignores")+"\n")
	writeFile(t, dir, "new.txt", "new\n")
	for include, shown := range map[string]string{
		"~/ignores.gitconfig": "core.excludesFile",
		"ignores.cfg":         filepath.Join(outside, "planted-ignores"),
	} {
		writeFile(t, dir, ".git/ignores.cfg", "[core]\n\texcludesFile = "+filepath.Join(outside, "planted-ignores")+"\n")
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = "+include+"\n")
		if got := untracked(t, req(t, dir, nil)); len(got) != 0 {
			t.Errorf("at a terminal, including %s, untracked = %v, want new.txt ignored", include, got)
		}
		tbl := table(t, runStatus, mcpReq(t, dir, dir))
		w := ignoreWarning(tbl)
		if w == nil || !strings.Contains(w.Message, shown) || !strings.Contains(w.Message, "path gate") ||
			shown == "core.excludesFile" && strings.Contains(w.Message, "planted-ignores") {
			t.Errorf("over MCP, including %s, the ignore warning is %+v, want the excludes file refused as %q", include,
				w, shown)
		}
	}
}

// An include this cannot decide is not read, and said to be: one naming the
// directory git was installed under, which only the git that reads it knows,
// one under a ~user whose home this cannot look up, and an onbranch:
// condition in a repository whose refs are kept in a reftable. git.hooks
// counts one that could move the directory it lists.
func TestAnIncludeThisCannotDecideIsSaidToBeUnread(t *testing.T) {
	machineConfig(t, "[include]\n\tpath = %(prefix)/etc/gitconfig.d/company\n\tpath = ~no-such-user-here/x\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	for name, run := range map[string]plugin.Handler{"git.config": runConfig, "git.hooks": runHooks} {
		tbl := table(t, run, req(t, dir, nil))
		if len(tbl.Warnings) != 1 || !strings.HasSuffix(tbl.Warnings[0].Code, ".include") ||
			!strings.HasPrefix(tbl.Warnings[0].Message, "2 files the config includes are not read") ||
			!strings.Contains(tbl.Warnings[0].Message, "the directory git was installed under") ||
			!strings.Contains(tbl.Warnings[0].Message, "its home directory is one this cannot look up") {
			t.Errorf("%s warnings = %+v, want the include said to be unread, and why", name, tbl.Warnings)
		}
	}
	setHooksPath(t, repo, ".githooks")
	if tbl := table(t, runHooks, req(t, dir, nil)); len(tbl.Warnings) != 0 {
		t.Errorf("git.hooks warnings = %+v, with the value set after the include", tbl.Warnings)
	}

	machineConfig(t, "")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\trepositoryformatversion = 1\n[extensions]\n"+
		"\trefStorage = reftable\n[includeIf \"onbranch:main\"]\n\tpath = ~/y.cfg\n")
	tbl := table(t, runConfig, req(t, dir, nil))
	if len(tbl.Warnings) != 1 || !strings.Contains(tbl.Warnings[0].Message, "reftable") {
		t.Errorf("in a reftable repository, warnings = %+v, want the onbranch: include said to be unread", tbl.Warnings)
	}
}

// Where the config has a hasconfig:remote.*.url condition, git reads every
// remote's URL, and refuses to run where a file an includeIf includes sets
// one, whatever that includeIf's condition ("remote URLs cannot be configured
// in file directly or indirectly included by includeIf.hasconfig:remote.*.url").
// A file include.path includes may set one, and it counts.
func TestARemoteURLAnIncludeIfIncludesIsRefusedBesideAHasconfigCondition(t *testing.T) {
	home := machineConfig(t, "")
	writeFile(t, home, "url.cfg", "[remote \"z\"]\n\turl = https://z.example/r\n")
	writeFile(t, home, "y.cfg", "[x]\n\ty = yes\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	for name, c := range map[string]struct {
		config  string
		refused bool
	}{
		"a URL an includeIf includes": {"[includeIf \"gitdir:/\"]\n\tpath = ~/url.cfg\n" +
			"[includeIf \"hasconfig:remote.*.url:https://none/**\"]\n\tpath = ~/y.cfg\n", true},
		"no hasconfig: condition": {"[includeIf \"gitdir:/\"]\n\tpath = ~/url.cfg\n", false},
		"a URL include.path includes": {"[include]\n\tpath = ~/url.cfg\n" +
			"[includeIf \"hasconfig:remote.*.url:https://z.example/*\"]\n\tpath = ~/y.cfg\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+c.config)
			tbl, err := runConfig(context.Background(), req(t, dir, nil))
			switch {
			case c.refused && (err == nil || !strings.Contains(err.Error(), "remote URLs cannot be configured")):
				t.Errorf("git.config = %v, want it refused as git refuses it", err)
			case !c.refused && err != nil:
				t.Errorf("git.config = %v, want an answer", err)
			case !c.refused && strings.Contains(c.config, "hasconfig") && len(keyRows(tbl.(view.Table), "x.y")) != 1:
				t.Error("the URL an include sets did not count")
			}
			skipGitOlderThan(t, gitSince{"2.36.0", "read hasconfig: conditions"})
			if _, _, runs, ok := gitGets(t, dir, "core.bare"); ok && runs == c.refused {
				t.Errorf("git runs: %v", runs)
			}
		})
	}
}

// git bounds how deep includes go and not how many there are, so a config
// that includes one file a thousand times over, which a caller can write
// inside the root, is refused past what one reading follows, as is one whose
// includes hold more than one file of config may.
func TestWhatTheIncludesOfOneReadingReadIsBounded(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".git/empty.cfg", "")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n"+
		strings.Repeat("\tpath = empty.cfg\n", maxIncludes+1))
	if _, err := runConfig(context.Background(), req(t, dir, nil)); err == nil ||
		!strings.Contains(err.Error(), "more than 1000 files in all") {
		t.Errorf("git.config = %v, want it refused past %d includes", err, maxIncludes)
	}

	writeFile(t, dir, ".git/half.cfg", "[x]\n"+strings.Repeat("\ty = 0123456789abcdef0123456789abcdef\n", 60000))
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = half.cfg\n\tpath = half.cfg\n")
	if _, err := runConfig(context.Background(), req(t, dir, nil)); err == nil ||
		!strings.Contains(err.Error(), "4.0 MiB this reads of them in all") {
		t.Errorf("git.config = %v, want it refused past %d bytes of includes", err, maxIncludedBytes)
	}
}
