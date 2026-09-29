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
	"syscall"
	"testing"
	"time"

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

// answersOf is what each capability that reads the config answers r with, as
// one string: its view or its refusal, in a fixed order.
func answersOf(t *testing.T, r plugin.Request) string {
	t.Helper()
	var b strings.Builder
	for _, c := range []struct {
		name string
		run  plugin.Handler
	}{
		{"git.config", runConfig}, {"git.hooks", runHooks}, {"git.status", runStatus}, {"git.log", runLog},
		{"git.remotes", runRemotes}, {"git.branches", runBranches}, {"git.overview", runOverview},
	} {
		v, err := c.run(context.Background(), r)
		fmt.Fprintf(&b, "%s: %+v %v\n", c.name, v, err)
	}
	return b.String()
}

// pipeOpened is a named pipe at path with a writer waiting on it, whose
// open(2) returns only once something opens the pipe to read it: opened
// reports whether anything did, then lets the writer go. nil where there are
// no named pipes.
func pipeOpened(t *testing.T, path string) (opened func() bool) {
	t.Helper()
	if mkfifo(path) != nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = f.Close()
		}
	}()
	time.Sleep(20 * time.Millisecond)
	return func() bool {
		select {
		case <-done:
			return true
		default:
		}
		if f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			<-done
			_ = f.Close()
		}
		return false
	}
}

// An include can name any file on the machine, and whether git reads the one
// it names shows in every answer: a missing file is passed over, one that is
// not config refuses the call, one that parses counts, and one past the 4 MiB
// includes are read to refuses it, so a caller who can write the repository's
// config inside the roots could learn, of a file outside them, whether it
// exists, whether it is config, and how large it is, by bisection. Over MCP
// such an include, in the repository's config or in a file it includes from
// inside the roots, is not followed at all: the file is never opened, every
// answer is the one a missing file gets, whatever is there, and git.config and
// git.hooks name the include as one outside the roots. At a terminal each is
// read as git reads it.
func TestAnIncludeOfAFileOutsideTheRootsIsNeverLookedAtOverMCP(t *testing.T) {
	home := machineConfig(t, "")
	dir, repo := testRepo(t)
	// Long ago, so that the overview's age of it is the same from one call to
	// the next.
	commitFileAt(t, repo, dir, "a.txt", "v1\n", "initial", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	writeFile(t, dir, "new.txt", "new\n")
	writeExecutable(t, dir, "planted-hooks/pre-commit")
	writeFile(t, dir, "planted-ignores", "*.txt\n")
	writeFile(t, dir, ".git/inner.cfg", "[include]\n\tpath = ~/probe.cfg\n")
	probe := filepath.Join(home, "probe.cfg")
	variants := []struct {
		name  string
		plant func()
	}{
		{"missing", func() {}},
		{"config", func() {
			writeFile(t, home, "probe.cfg", "[core]\n\thooksPath = "+filepath.Join(dir, "planted-hooks")+
				"\n\texcludesFile = "+filepath.Join(dir, "planted-ignores")+"\n[x]\n\tafter = planted\n"+
				"[remote \"planted\"]\n\tpromisor = true\n\turl = https://planted.example/r\n")
		}},
		{"not config", func() { writeFile(t, home, "probe.cfg", "this is not config\n") }},
		{"a directory", func() {
			if err := os.Mkdir(probe, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"past the cap", func() {
			writeFile(t, home, "probe.cfg", "")
			if err := os.Truncate(probe, maxIncludedBytes+1); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable", func() {
			writeFile(t, home, "probe.cfg", "[x]\n\tafter = planted\n")
			if err := os.Chmod(probe, 0); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, include := range []string{"path = ~/probe.cfg", "path = inner.cfg"} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\t"+include+"\n[x]\n\tafter = local\n")
		want, atTerminal := "", map[string]string{}
		for _, v := range variants {
			if err := os.RemoveAll(probe); err != nil {
				t.Fatal(err)
			}
			v.plant()
			got := answersOf(t, mcpReq(t, dir, dir))
			if want == "" {
				want = got
			} else if got != want {
				t.Errorf("over MCP, with %s, the file outside the roots %s:\n%s\nwhere a missing one gets:\n%s",
					include, v.name, got, want)
			}
			atTerminal[v.name] = answersOf(t, req(t, dir, nil))
			_ = os.Chmod(probe, 0o644)
		}
		for _, v := range variants[1:] {
			if atTerminal[v.name] == atTerminal["missing"] {
				t.Errorf("at a terminal, with %s, the file outside the roots %s is answered as a missing one", include,
					v.name)
			}
		}
		_ = os.RemoveAll(probe)
		if opened := pipeOpened(t, probe); opened != nil {
			got := answersOf(t, mcpReq(t, dir, dir))
			if opened() {
				t.Errorf("over MCP, with %s, the named pipe outside the roots was opened", include)
			}
			if got != want {
				t.Errorf("over MCP, with %s, a named pipe outside the roots:\n%s\nwhere a missing one gets:\n%s",
					include, got, want)
			}
		}
		_ = os.RemoveAll(probe)
	}
}

// Over MCP git.config names an include of a file outside the roots as one not
// followed, among the rows of the files it does follow, a file included from
// inside the roots with where it is; git.hooks names it where it could set
// the directory git runs hooks from, since git follows it. The operator's own
// config, and a file it includes from anywhere, is theirs, and read as before:
// its core.hooksPath is the directory git.hooks lists. At a terminal the file
// is read as git reads it, a credential in it masked.
func TestAnIncludeOutsideTheRootsIsNamedOverMCPAndTheOperatorsIsFollowed(t *testing.T) {
	home := machineConfig(t, "[include]\n\tpath = ~/operator.cfg\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	// The operator's value names the checkout where its links lead, as the
	// path the MCP call is given is judged, for the row to show it from there.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, home, "operator.cfg", "[core]\n\thooksPath = "+filepath.Join(resolved, "operator-hooks")+"\n")
	writeExecutable(t, dir, "operator-hooks/pre-commit")
	writeFile(t, home, ".my.cnf", "[client]\n\tuser = planted-user\n\tpassword = planted-password\n"+
		"\thost = planted-host.example\n[core]\n\thooksPath = planted-hooks\n")
	writeFile(t, dir, ".git/shared.cfg", "[x]\n\tshared = in-the-root\n")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = ~/.my.cnf\n"+
		"\tpath = shared.cfg\n")

	mcp := table(t, runConfig, mcpReq(t, dir, dir))
	shown := fmt.Sprint(mcp.Rows, mcp.Warnings)
	for _, p := range []string{"planted", "client.", "operator.cfg"} {
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
		!strings.Contains(mcp.Warnings[0].Message, "~/.my.cnf, which is not followed") {
		t.Errorf("over MCP, warnings = %+v, want git.config.include.outside naming the include", mcp.Warnings)
	}
	hooks := table(t, runHooks, mcpReq(t, dir, dir))
	if row := rowFor(t, hooks, "Name", "pre-commit"); row[2] != "operator-hooks/pre-commit" {
		t.Errorf("over MCP, pre-commit row = %v, want the directory the operator's included file names", row)
	}
	if len(hooks.Warnings) != 1 || hooks.Warnings[0].Code != "git.hooks.include.outside" ||
		!strings.Contains(hooks.Warnings[0].Message, "~/.my.cnf") {
		t.Errorf("over MCP, git.hooks warnings = %+v, want the include named as one not followed", hooks.Warnings)
	}
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = ~/.my.cnf\n[core]\n"+
		"\thooksPath = operator-hooks\n")
	if hooks := table(t, runHooks, mcpReq(t, dir, dir)); len(hooks.Warnings) != 0 {
		t.Errorf("over MCP, git.hooks warnings = %+v, with core.hooksPath set after the include", hooks.Warnings)
	}

	cli := table(t, runConfig, req(t, dir, nil))
	if got := keyRows(cli, "client.host"); !slices.Equal(got, []string{"planted-host.example@" +
		filepath.Join(home, ".my.cnf")}) {
		t.Errorf("at a terminal, client.host rows = %v, want the included file's", got)
	}
	if got := keyRows(cli, "client.password"); len(got) != 1 || !strings.HasPrefix(got[0], view.Mask+"@") {
		t.Errorf("at a terminal, client.password rows = %v, want the value masked, as a credential is", got)
	}
	if len(cli.Warnings) != 0 {
		t.Errorf("at a terminal, warnings = %+v, want none", cli.Warnings)
	}
	if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[2] != "operator-hooks/pre-commit" {
		t.Errorf("at a terminal, pre-commit row = %v, want the directory the repository's config sets last", row)
	}

	writeFile(t, home, ".aws/credentials", "[default]\n\taws_access_key_id = AKIAPLANTEDKEYID\n")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = ~/.aws/credentials\n")
	if _, err := runConfig(context.Background(), req(t, dir, nil)); errCode(err) != "git.config.failed" {
		t.Errorf("at a terminal, including ~/.aws/credentials, git.config = %v, want it refused as git refuses it", err)
	}
	if _, _, runs, ok := gitGets(t, dir, "core.bare"); ok && runs {
		t.Error("git runs including ~/.aws/credentials")
	}
}

// rta's own state is refused wherever it sits, and a file an include names is
// no exception: a root drawn around the home directory holds
// ~/.local/share/rta, and an include naming a file there was read, and
// counted for every answer. Over MCP it refuses the call as the gate refuses
// the path; a file outside the roots that names it is never opened, so what it
// includes is never reached, and the answer is the one with no such include.
// At a terminal each is read, as git reads it.
func TestAnIncludeOfRtasOwnStateIsRefusedOverMCP(t *testing.T) {
	home := machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	state := filepath.Join(dir, ".local", "share", "rta")
	t.Setenv("RTA_DATA_DIR", state)
	writeFile(t, state, "planted.cfg", "[core]\n\thooksPath = planted-hooks\n")
	writeExecutable(t, dir, "planted-hooks/pre-commit")
	writeFile(t, home, "outside.cfg", "[include]\n\tpath = "+filepath.Join(state, "planted.cfg")+"\n")
	for name, c := range map[string]struct{ include, code string }{
		"named in the repository's config":  {filepath.Join(state, "planted.cfg"), "core.mcp.path.protected"},
		"named in a file outside the roots": {"~/outside.cfg", ""},
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = "+c.include+"\n")
		for capability, run := range map[string]plugin.Handler{"git.config": runConfig, "git.hooks": runHooks, "git.log": runLog} {
			v, err := run(context.Background(), mcpReq(t, dir, dir))
			if errCode(err) != c.code || strings.Contains(fmt.Sprint(v), "planted") {
				t.Errorf("%s, %s over MCP = %v %v, want %q", name, capability, v, err, c.code)
			}
		}
		if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[2] != "planted-hooks/pre-commit" {
			t.Errorf("%s, at a terminal, pre-commit row = %v, want the directory the included file names", name, row)
		}
	}
}

// A hasconfig:remote.*.url condition is a glob, and whether the file it names
// is read shows in the answer. One written in the repository's config, which a
// caller can write inside the roots, was matched against every URL git reads,
// so a caller could try pattern after pattern against URLs it is never shown —
// the operator's own, the environment's, one set in a file outside the roots
// — and spell one out. Over MCP such a condition is matched against the URLs
// the repository's own config sets inside the roots alone: a pattern that
// matches a URL the caller is not shown gets the answer one that matches
// nothing gets, and a refusal a file of the operator's would bring about is
// not reached. One the operator's config writes is matched against every URL,
// as before. At a terminal each is matched as git matches it.
func TestAHasconfigConditionInTheRepositoryMatchesItsOwnRemotesOverMCP(t *testing.T) {
	home := machineConfig(t, "[remote \"operator\"]\n\turl = https://bob:planted-token@git.example/team/r.git\n"+
		"[includeIf \"hasconfig:remote.*.url:https://*@git.example/team/**\"]\n\tpath = ~/team.cfg\n")
	dir, repo := testRepo(t)
	commitFileAt(t, repo, dir, "a.txt", "v1\n", "initial", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, home, "team.cfg", "[core]\n\thooksPath = "+filepath.Join(resolved, "team-hooks")+"\n")
	writeExecutable(t, dir, "team-hooks/pre-commit")
	writeFile(t, home, "outside.cfg", "[remote \"outside\"]\n\turl = https://outside.example/planted/r.git\n")
	writeFile(t, dir, ".git/inner.cfg", "[remote \"inner\"]\n\turl = https://inner.example/r.git\n")
	writeFile(t, dir, ".git/probe.cfg", "[x]\n\tprobe = matched\n[core]\n\thooksPath = probe-hooks\n")
	writeExecutable(t, dir, "probe-hooks/pre-push")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'remote.env.url'='https://env.example/planted/r.git'")
	own := "[include]\n\tpath = ~/outside.cfg\n\tpath = inner.cfg\n[remote \"own\"]\n\turl = https://own.example/r.git\n"
	unmatched := ""
	for _, c := range []struct {
		pattern       string
		terminal, mcp bool
	}{
		{"https://nowhere.example/**", false, false},
		{"https://bob:planted-token@git.example/**", true, false},
		{"https://bob:planted-*@git.example/**", true, false},
		{"https://*@git.example/team/**", true, false},
		{"https://outside.example/**", true, false},
		{"https://env.example/**", true, false},
		{"https://own.example/**", true, true},
		{"https://inner.example/**", true, true},
	} {
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+own+"[includeIf \"hasconfig:remote.*.url:"+
			c.pattern+"\"]\n\tpath = probe.cfg\n")
		if got := len(keyRows(table(t, runConfig, req(t, dir, nil)), "x.probe")) == 1; got != c.terminal {
			t.Errorf("at a terminal, with %s, the file it names is read: %v, want %v", c.pattern, got, c.terminal)
		}
		if got := len(keyRows(table(t, runConfig, mcpReq(t, dir, dir)), "x.probe")) == 1; got != c.mcp {
			t.Errorf("over MCP, with %s, the file it names is read: %v, want %v", c.pattern, got, c.mcp)
		}
		if value, _, runs, ok := gitGets(t, dir, "x.probe"); ok && runs && (value == "matched") != c.terminal {
			skipGitOlderThan(t, gitSince{"2.36.0", "read hasconfig: conditions"})
			t.Errorf("git, with %s, reads the file it names: %v", c.pattern, value == "matched")
		}
		if c.mcp {
			continue
		}
		// The pattern itself is a row of git.config's, its credential masked.
		got := strings.ReplaceAll(answersOf(t, mcpReq(t, dir, dir)), c.pattern, "<pattern>")
		got = strings.ReplaceAll(got, maskURLCredentials(c.pattern), "<pattern>")
		if unmatched == "" {
			unmatched = got
			if row := rowFor(t, table(t, runHooks, mcpReq(t, dir, dir)), "Name", "pre-commit"); row[2] != "team-hooks/pre-commit" {
				t.Errorf("over MCP, pre-commit row = %v, want the directory the operator's condition includes", row)
			}
		} else if got != unmatched {
			t.Errorf("over MCP, with %s, which matches a URL the caller is not shown:\n%s\nwhere one matching "+
				"nothing gets:\n%s", c.pattern, got, unmatched)
		}
	}

	home = machineConfig(t, "[includeIf \"gitdir:/\"]\n\tpath = ~/url.cfg\n")
	writeFile(t, home, "url.cfg", "[remote \"z\"]\n\turl = https://z.example/r\n")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[includeIf \"hasconfig:remote.*.url:https://z/**\"]\n"+
		"\tpath = probe.cfg\n")
	if _, err := runConfig(context.Background(), req(t, dir, nil)); err == nil ||
		!strings.Contains(err.Error(), "remote URLs cannot be configured") {
		t.Errorf("at a terminal, git.config = %v, want it refused as git refuses it", err)
	}
	if v, err := runConfig(context.Background(), mcpReq(t, dir, dir)); err != nil || strings.Contains(fmt.Sprint(v), "url.cfg") {
		t.Errorf("over MCP, git.config = %v %v, want the operator's file never reached", v, err)
	}
}

// **A file of the operator's config inside the roots is one the caller can
// write, and it counts as the repository's over MCP, whoever includes it.**
// The two oracles closed for the repository's own config — whether a file
// outside the roots exists and parses, told by following an include of it,
// and a URL the caller is never shown, spelled out by a hasconfig:remote.*.url
// pattern — reopened through it: ~/.gitconfig itself under a root drawn
// around the home directory, and ~/work/.gitconfig, which ~/.gitconfig
// includes, served with a root of ~/work. Written there, an include of a file
// outside the roots is not followed, a hasconfig condition is matched against
// the repository's own URLs alone, and a core.excludesFile outside the roots
// is not applied, as they are in the repository's config. At a terminal each
// is read as git reads it.
func TestAnOperatorsConfigInsideTheRootsCountsAsTheRepositorysOverMCP(t *testing.T) {
	for _, c := range []struct {
		name string
		// layout is the root, the repository and the file of config inside
		// the root the caller writes, under home.
		layout func(t *testing.T, home string) (root, dir, written string)
	}{
		{"~/.gitconfig under a root of the home directory", func(t *testing.T, home string) (string, string, string) {
			dir := filepath.Join(home, "proj")
			repoAt(t, home, dir)
			return home, dir, filepath.Join(home, ".gitconfig")
		}},
		{"~/work/.gitconfig included by ~/.gitconfig under a root of ~/work",
			func(t *testing.T, home string) (string, string, string) {
				work := filepath.Join(home, "work")
				dir := filepath.Join(work, "proj")
				repoAt(t, work, dir)
				writeFile(t, home, ".gitconfig", "[include]\n\tpath = ~/work/.gitconfig\n")
				return work, dir, filepath.Join(work, ".gitconfig")
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := machineConfig(t, "")
			root, dir, written := c.layout(t, home)
			includeOutsideIsNeverLookedAt(t, root, dir, written)
			hasconfigMatchesTheRepositorysOwn(t, root, dir, written)
			excludesOutsideIsNeverApplied(t, root, dir, written)
		})
	}
}

// includeOutsideIsNeverLookedAt fails where, over MCP confined to root, an
// include written in written, a file of config inside root, of a file outside
// it answers by what is there; and where a terminal does not. The include
// names the file itself, then a link inside root leading to it, which the
// gate judges inside the roots where it leads nowhere.
func includeOutsideIsNeverLookedAt(t *testing.T, root, dir, written string) {
	t.Helper()
	outside := t.TempDir()
	probe := filepath.Join(outside, "probe.cfg")
	link := filepath.Join(root, "probe-link.cfg")
	if err := os.Symlink(probe, link); err != nil {
		t.Fatal(err)
	}
	for _, named := range []string{probe, link} {
		includeNamedIsNeverLookedAt(t, root, dir, written, named, probe)
	}
}

// includeNamedIsNeverLookedAt is includeOutsideIsNeverLookedAt with the
// include naming named, which leads to probe.
func includeNamedIsNeverLookedAt(t *testing.T, root, dir, written, named, probe string) {
	t.Helper()
	outside := filepath.Dir(probe)
	writeFile(t, filepath.Dir(written), filepath.Base(written), "[include]\n\tpath = "+named+"\n")
	plants := map[string]func(){
		"missing":    func() {},
		"config":     func() { writeFile(t, outside, "probe.cfg", "[core]\n\thooksPath = /planted\n") },
		"not config": func() { writeFile(t, outside, "probe.cfg", "this is not config\n") },
		"a directory": func() {
			if err := os.Mkdir(probe, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	mcp, terminal := map[string]string{}, map[string]string{}
	for name, plant := range plants {
		if err := os.RemoveAll(probe); err != nil {
			t.Fatal(err)
		}
		plant()
		mcp[name] = answersOf(t, mcpReq(t, root, dir))
		terminal[name] = answersOf(t, req(t, dir, nil))
	}
	_ = os.RemoveAll(probe)
	for name := range plants {
		if mcp[name] != mcp["missing"] {
			t.Errorf("over MCP, through %s, the file outside the roots %s:\n%s\nwhere a missing one gets:\n%s",
				named, name, mcp[name], mcp["missing"])
		}
		if name != "missing" && terminal[name] == terminal["missing"] {
			t.Errorf("at a terminal, through %s, the file outside the roots %s is answered as a missing one", named,
				name)
		}
	}
	if w := table(t, runHooks, mcpReq(t, root, dir)).Warnings; len(w) != 1 ||
		w[0].Code != "git.hooks.include.outside" || !strings.Contains(w[0].Message, named) {
		t.Errorf("over MCP, git.hooks warnings = %+v, want the include named as one not followed", w)
	}
}

// And ~/.gitconfig itself, under a root drawn around the home directory, a
// link to a file outside it: a caller can make that link lead anywhere, and
// reading what it led to told whether that file exists and parses as config.
// Over MCP it is not read, whatever is at the far end, and git.hooks names it;
// at a terminal it is read, as git reads it.
func TestAMachineWideConfigLinkedOutOfTheRootsIsNotReadOverMCP(t *testing.T) {
	home := machineConfig(t, "")
	dir := filepath.Join(home, "proj")
	repoAt(t, home, dir)
	writeExecutable(t, dir, "planted-hooks/pre-commit")
	outside := t.TempDir()
	far := filepath.Join(outside, "gitconfig")
	if err := os.Symlink(far, filepath.Join(home, ".gitconfig")); err != nil {
		t.Fatal(err)
	}
	mcp := map[string]string{}
	for name, plant := range map[string]func(){
		"missing": func() {},
		"config": func() {
			writeFile(t, outside, "gitconfig", "[core]\n\thooksPath = "+filepath.Join(realPath(dir), "planted-hooks")+
				"\n")
		},
		"not config": func() { writeFile(t, outside, "gitconfig", "this is not config\n") },
	} {
		_ = os.RemoveAll(far)
		plant()
		mcp[name] = answersOf(t, mcpReq(t, home, dir))
		if name == "config" {
			if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[2] != "planted-hooks/pre-commit" {
				t.Errorf("at a terminal, pre-commit row = %v, want the directory the linked file names", row)
			}
		}
	}
	for name, got := range mcp {
		if got != mcp["missing"] {
			t.Errorf("over MCP, the file ~/.gitconfig leads to %s:\n%s\nwhere a missing one gets:\n%s", name, got,
				mcp["missing"])
		}
	}
	if w := table(t, runHooks, mcpReq(t, home, dir)).Warnings; len(w) != 1 || w[0].Code != "git.hooks.include.outside" ||
		!strings.Contains(w[0].Message, filepath.Join(home, ".gitconfig")+", which is not followed") {
		t.Errorf("over MCP, git.hooks warnings = %+v, want ~/.gitconfig named as a file not read", w)
	}
}

// And a file of the operator's config outside the roots, as it is spelled,
// that a link of the operator's leads into them: ~/.gitconfig, or a file it
// includes, a link to the copy a dotfiles repository under ~/work keeps,
// served with a root of ~/work. The copy is the caller's to swap for a link
// to any file on the machine, and the gate, resolving every link, finds the
// name outside the roots: read by name as the operator's, it told whether
// the file the caller's link led to exists and parses as config. Over MCP it
// is not read, whatever is at the far end; at a terminal it is, as git reads
// it.
func TestAConfigLinkedIntoTheRootsAndOutIsNotReadOverMCP(t *testing.T) {
	for _, c := range []struct {
		name string
		// link is the name of the operator's link into the roots, made under
		// home, and the ~/.gitconfig that reaches it.
		link, gitconfig string
	}{
		{"~/.gitconfig", ".gitconfig", ""},
		{"a file ~/.gitconfig includes", "more.gitconfig", "[include]\n\tpath = ~/more.gitconfig\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := machineConfig(t, c.gitconfig)
			work := filepath.Join(home, "work")
			dir := filepath.Join(work, "proj")
			repoAt(t, work, dir)
			writeExecutable(t, dir, "planted-hooks/pre-commit")
			copied := filepath.Join(work, "dotfiles", "gitconfig")
			if err := os.MkdirAll(filepath.Dir(copied), 0o755); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			far := filepath.Join(outside, "gitconfig")
			if err := os.Symlink(far, copied); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(copied, filepath.Join(home, c.link)); err != nil {
				t.Fatal(err)
			}
			mcp := map[string]string{}
			for name, plant := range map[string]func(){
				"missing": func() {},
				"config": func() {
					writeFile(t, outside, "gitconfig", "[core]\n\thooksPath = "+
						filepath.Join(realPath(dir), "planted-hooks")+"\n")
				},
				"not config": func() { writeFile(t, outside, "gitconfig", "this is not config\n") },
			} {
				_ = os.RemoveAll(far)
				plant()
				mcp[name] = answersOf(t, mcpReq(t, work, dir))
				if name == "config" {
					row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit")
					if row[2] != "planted-hooks/pre-commit" {
						t.Errorf("at a terminal, pre-commit row = %v, want the directory the linked file names", row)
					}
				}
			}
			for name, got := range mcp {
				if got != mcp["missing"] {
					t.Errorf("over MCP, the file %s leads to %s:\n%s\nwhere a missing one gets:\n%s", c.link, name,
						got, mcp["missing"])
				}
			}
			w := table(t, runHooks, mcpReq(t, work, dir)).Warnings
			if len(w) != 1 || w[0].Code != "git.hooks.include.outside" ||
				!strings.Contains(w[0].Message, c.link+", which is not followed") {
				t.Errorf("over MCP, git.hooks warnings = %+v, want %s named as a file not read", w, c.link)
			}
		})
	}
}

// excludesOutsideIsNeverApplied fails where, over MCP confined to root, a
// core.excludesFile written in written, a file of config inside root, naming
// a file outside it is applied, or answers by whether that file is there; and
// where a terminal does not apply it. It names the file itself, then a link
// inside root leading to it.
func excludesOutsideIsNeverApplied(t *testing.T, root, dir, written string) {
	t.Helper()
	outside := t.TempDir()
	planted := filepath.Join(outside, "planted-ignores")
	link := filepath.Join(root, "ignores-link")
	if err := os.Symlink(planted, link); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "new.txt", "new\n")
	for _, named := range []string{planted, link} {
		writeFile(t, filepath.Dir(written), filepath.Base(written), "[core]\n\texcludesFile = "+named+"\n")
		answers := map[bool]string{}
		for _, there := range []bool{true, false} {
			_ = os.Remove(planted)
			if there {
				writeFile(t, outside, "planted-ignores", "*.txt\n")
			}
			w := ignoreWarning(table(t, runStatus, mcpReq(t, root, dir)))
			if !slices.Contains(untracked(t, mcpReq(t, root, dir)), "new.txt") || w == nil ||
				!strings.Contains(w.Message, "path gate") {
				t.Errorf("over MCP, through %s, an excludes file outside the roots was applied, or not named as "+
					"refused: %+v", named, w)
			}
			answers[there] = fmt.Sprint(w)
		}
		if answers[true] != answers[false] {
			t.Errorf("over MCP, through %s, the excludes file outside the roots answers by whether it is there: "+
				"%s where it is, %s where it is not", named, answers[true], answers[false])
		}
		writeFile(t, outside, "planted-ignores", "*.txt\n")
		if got := untracked(t, req(t, dir, nil)); slices.Contains(got, "new.txt") {
			t.Errorf("at a terminal, through %s, untracked = %v, want new.txt ignored", named, got)
		}
	}
	_ = os.Remove(filepath.Join(dir, "new.txt"))
}

// hasconfigMatchesTheRepositorysOwn fails where, over MCP confined to root, a
// hasconfig:remote.*.url condition written in written, a file of config inside
// root, is matched against a URL the caller is never shown; and where a
// terminal does not match it.
func hasconfigMatchesTheRepositorysOwn(t *testing.T, root, dir, written string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_PARAMETERS", "'remote.env.url'='https://env.example/planted/r.git'")
	writeFile(t, dir, "probe.cfg", "[core]\n\thooksPath = probe-hooks\n")
	writeExecutable(t, dir, "probe-hooks/pre-push")
	pre := "[remote \"own\"]\n\turl = https://own.example/r.git\n"
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+pre)
	hooks := func(r plugin.Request, pattern string) string {
		writeFile(t, filepath.Dir(written), filepath.Base(written), "[includeIf \"hasconfig:remote.*.url:"+pattern+
			"\"]\n\tpath = "+filepath.Join(dir, "probe.cfg")+"\n")
		return strings.ReplaceAll(fmt.Sprint(table(t, runHooks, r).Rows), pattern, "<pattern>")
	}
	for _, pattern := range []string{"https://env.example/**", "https://env.example/planted/**"} {
		if got, none := hooks(mcpReq(t, root, dir), pattern), hooks(mcpReq(t, root, dir),
			"https://nowhere.example/**"); got != none {
			t.Errorf("over MCP, %s, which matches a URL the caller is not shown, answers %s where one matching "+
				"nothing answers %s", pattern, got, none)
		}
		if got, none := hooks(req(t, dir, nil), pattern), hooks(req(t, dir, nil),
			"https://nowhere.example/**"); got == none {
			t.Errorf("at a terminal, %s is answered as a pattern matching nothing: %s", pattern, got)
		}
	}
	if got := hooks(mcpReq(t, root, dir), "https://own.example/**"); !strings.Contains(got, "pre-push") {
		t.Errorf("over MCP, a pattern matching the repository's own URL is not matched: %s", got)
	}
	unsetenv(t, "GIT_CONFIG_PARAMETERS")
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
// is, and over MCP one set in a file outside the roots is not read, the
// include not being followed.
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
		"~/ignores.gitconfig": "",
		"ignores.cfg":         filepath.Join(outside, "planted-ignores"),
	} {
		writeFile(t, dir, ".git/ignores.cfg", "[core]\n\texcludesFile = "+filepath.Join(outside, "planted-ignores")+"\n")
		writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n[include]\n\tpath = "+include+"\n")
		if got := untracked(t, req(t, dir, nil)); len(got) != 0 {
			t.Errorf("at a terminal, including %s, untracked = %v, want new.txt ignored", include, got)
		}
		tbl := table(t, runStatus, mcpReq(t, dir, dir))
		w := ignoreWarning(tbl)
		switch {
		case shown == "" && (w != nil || !slices.Contains(untracked(t, mcpReq(t, dir, dir)), "new.txt")):
			t.Errorf("over MCP, including %s, the ignore warning is %+v, want no excludes file read", include, w)
		case shown != "" && (w == nil || !strings.Contains(w.Message, shown) || !strings.Contains(w.Message, "path gate")):
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
