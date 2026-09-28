package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func writeHook(t *testing.T, dir, name string, executable bool) {
	t.Helper()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(filepath.Join(hooksDir, name), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

// PlainInit writes no hook templates at all (unlike the real git binary,
// which drops in *.sample files for every hook git knows about) — this
// fixture builds all three states by hand rather than relying on any of
// them pre-existing.
func TestHooksClassifiesActiveSampleAndDisabled(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeHook(t, dir, "pre-commit", true)
	writeHook(t, dir, "pre-push.sample", false)
	writeHook(t, dir, "commit-msg", false)

	tbl := table(t, runHooks, req(t, dir, nil))
	if got := rowFor(t, tbl, "Name", "pre-commit")[1]; got != "active" {
		t.Errorf("pre-commit status = %q, want active", got)
	}
	if got := rowFor(t, tbl, "Name", "pre-push")[1]; got != "sample" {
		t.Errorf("pre-push status = %q, want sample — the .sample suffix must be stripped from Name", got)
	}
	if got := rowFor(t, tbl, "Name", "commit-msg")[1]; got != "disabled" {
		t.Errorf("commit-msg status = %q, want disabled — present but not executable, so git skips it", got)
	}
	if tbl.Total != len(tbl.Rows) {
		t.Errorf("Total = %d, want %d", tbl.Total, len(tbl.Rows))
	}
}

func TestHooksOnARepositoryWithNoHooksDirectoryReturnsEmptyNotAnError(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	tbl := table(t, runHooks, req(t, dir, nil))
	if len(tbl.Rows) != 0 {
		t.Errorf("rows = %v, want none — PlainInit never creates a hooks directory", tbl.Rows)
	}
}

func TestHooksFilesystemRefusesAMemoryBackedClone(t *testing.T) {
	bareDir := bareRepo(t)
	repo, verr := gitclone.InMemory(context.Background(), bareDir, gitclone.Options{})
	if verr != nil {
		t.Fatal(verr)
	}

	_, verr = hooksFilesystem(repo)
	if verr == nil {
		t.Fatal("expected an error — a memory-backed clone has no on-disk hooks directory")
	}
	if verr.Code != "git.hooks.unavailable" {
		t.Errorf("code = %q, want git.hooks.unavailable", verr.Code)
	}
}

func TestHooksOnANonRepositoryFailsWithAClearError(t *testing.T) {
	dir := t.TempDir()
	_, err := runHooks(context.Background(), req(t, dir, nil))
	if err == nil {
		t.Fatal("expected an error opening a non-repository")
	}
}

// machineConfig points the machine-wide git config at files the test owns:
// a home directory holding a .gitconfig with content, system files that do
// not exist, and none of the environment that chooses others — so what this
// machine's own config says cannot reach an assertion.
func machineConfig(t *testing.T, gitconfig string) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, name := range []string{
		"GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "DEVELOPER_DIR",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS",
	} {
		unsetenv(t, name)
	}
	vendor, system := vendorGitConfigs, systemGitConfigs
	vendorGitConfigs = []string{filepath.Join(home, "no-vendor-gitconfig")}
	systemGitConfigs = []string{filepath.Join(home, "no-system-gitconfig")}
	t.Cleanup(func() { vendorGitConfigs, systemGitConfigs = vendor, system })
	if gitconfig != "" {
		writeFile(t, home, ".gitconfig", gitconfig)
	}
	return home
}

// unsetenv unsets name for the test, and puts it back after: set to nothing
// is not unset for the variables git reads.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

func setHooksPath(t *testing.T, repo *git.Repository, value string) {
	t.Helper()
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Raw.Section("core").SetOption("hooksPath", value)
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, dir, name, "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// git runs hooks from core.hooksPath when it is set — husky sets it, and so
// do many organisations — and reads .git/hooks only when it is not. The
// capability listed .git/hooks whatever the config said, and answered "no
// active hooks" for a repository whose pre-commit git ran on every commit.
func TestHooksAreListedFromCoreHooksPath(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	setHooksPath(t, repo, ".githooks")
	writeExecutable(t, dir, ".githooks/pre-commit")
	writeHook(t, dir, "post-commit", true)

	tbl := table(t, runHooks, req(t, dir, nil))
	row := rowFor(t, tbl, "Name", "pre-commit")
	if row[1] != "active" || row[2] != ".githooks/pre-commit" {
		t.Errorf("pre-commit row = %v, want active, at .githooks/pre-commit", row)
	}
	for _, r := range tbl.Rows {
		if r[0] == "post-commit" {
			t.Errorf("a hook in .git/hooks is listed although git does not read that directory: %v", r)
		}
	}
}

// Without core.hooksPath, the hooks directory is the repository's own, and
// each row says where its file is.
func TestHooksWithoutCoreHooksPathAreTheRepositorysOwn(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeHook(t, dir, "pre-commit", true)

	row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit")
	if row[1] != "active" || row[2] != ".git/hooks/pre-commit" {
		t.Errorf("pre-commit row = %v, want active, at .git/hooks/pre-commit", row)
	}
}

// core.hooksPath is read the way git reads it: from the operator's own config
// when the repository's does not set it, with a leading ~ for their home, and
// the repository's value first when both do. Over MCP a directory outside the
// root is refused, as any path there is.
func TestCoreHooksPathIsReadFromEveryScopeGitReads(t *testing.T) {
	home := machineConfig(t, "[core]\n\thooksPath = ~/org-hooks\n")
	writeExecutable(t, home, "org-hooks/commit-msg")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "commit-msg")
	if want := filepath.Join(home, "org-hooks", "commit-msg"); row[1] != "active" || row[2] != want {
		t.Errorf("commit-msg row = %v, want active, at %s", row, want)
	}

	if _, err := runHooks(context.Background(), guarded(t, dir, dir)); errCode(err) != "core.mcp.path.outside" {
		t.Errorf("a hooks directory outside the root, over MCP: %q, want core.mcp.path.outside", errCode(err))
	}

	setHooksPath(t, repo, ".githooks")
	writeExecutable(t, dir, ".githooks/pre-push")
	tbl := table(t, runHooks, guarded(t, dir, dir))
	if row := rowFor(t, tbl, "Name", "pre-push"); row[2] != ".githooks/pre-push" {
		t.Errorf("pre-push row = %v, want the repository's own core.hooksPath first", row)
	}
}

// The operator's own git config is read over MCP too, for core.hooksPath, and
// a named pipe in place of one of its files blocked open(2) until a writer
// came, which no context can interrupt. It is refused as not a file instead,
// by git.hooks on every surface and by git.config at a terminal.
func TestAPipeInPlaceOfTheOperatorsGitConfigIsRefusedRatherThanWaitedOn(t *testing.T) {
	home := machineConfig(t, "")
	if err := mkfifo(filepath.Join(home, ".gitconfig")); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	for _, c := range []struct {
		name string
		h    plugin.Handler
		r    plugin.Request
		code string
	}{
		{"git.hooks over MCP", runHooks, guarded(t, dir, dir).WithSurface(plugin.SurfaceMCP), "git.hooks.failed"},
		{"git.hooks", runHooks, req(t, dir, nil), "git.hooks.failed"},
		{"git.config", runConfig, req(t, dir, nil), "git.config.failed"},
	} {
		done := make(chan error, 1)
		go func() {
			_, err := c.h(context.Background(), c.r)
			done <- err
		}()
		select {
		case err := <-done:
			if code := errCode(err); code != c.code {
				t.Errorf("%s with a pipe in place of ~/.gitconfig: %q, want %s", c.name, code, c.code)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s is waiting on a pipe in place of the operator's git config", c.name)
		}
	}
}

// core.hooksPath set in any file git reads is the directory git runs hooks
// from: a Homebrew build's system file, or the global file GIT_CONFIG_GLOBAL
// names, both of which this missed, listing .git/hooks while git ran another
// directory's pre-commit.
func TestCoreHooksPathIsReadFromEverySystemFileAndTheNamedGlobalOne(t *testing.T) {
	home := machineConfig(t, "")
	systemGitConfigs = []string{filepath.Join(home, "etc", "gitconfig"), filepath.Join(home, "brew", "gitconfig")}
	writeFile(t, home, "brew/gitconfig", "[core]\n\thooksPath = ~/brew-hooks\n")
	writeExecutable(t, home, "brew-hooks/pre-commit")
	writeExecutable(t, home, "env-hooks/commit-msg")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeHook(t, dir, "post-commit", true)

	tbl := table(t, runHooks, req(t, dir, nil))
	if row := rowFor(t, tbl, "Name", "pre-commit"); row[2] != filepath.Join(home, "brew-hooks", "pre-commit") {
		t.Errorf("pre-commit row = %v, want the directory Homebrew's system file names", row)
	}
	writeFile(t, home, "named", "[core]\n\thooksPath = ~/env-hooks\n")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "named"))
	tbl = table(t, runHooks, req(t, dir, nil))
	if row := rowFor(t, tbl, "Name", "commit-msg"); row[2] != filepath.Join(home, "env-hooks", "commit-msg") {
		t.Errorf("commit-msg row = %v, want the directory GIT_CONFIG_GLOBAL's file names", row)
	}
}

// An include is not followed, and one that could set core.hooksPath is
// counted rather than passed over: one in a file read at or after the file
// the value came from, since what git reads later wins. The repository's own
// value is read last, so an include of the operator's cannot move it.
func TestHooksCountTheIncludesThatCouldMoveTheDirectory(t *testing.T) {
	machineConfig(t, "[includeIf \"gitdir:~/work/\"]\n\tpath = ~/work.gitconfig\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	tbl := table(t, runHooks, req(t, dir, nil))
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "git.hooks.include" ||
		!strings.HasPrefix(tbl.Warnings[0].Message, "1 file the config includes is not read") {
		t.Errorf("warnings = %+v, want git.hooks.include counting the operator's include", tbl.Warnings)
	}
	mcp := table(t, runHooks, guarded(t, dir, dir).WithSurface(plugin.SurfaceMCP))
	if len(mcp.Warnings) != 1 || strings.Contains(mcp.Warnings[0].Message, "work") {
		t.Errorf("over MCP, warnings = %+v, want the include counted and not named", mcp.Warnings)
	}

	setHooksPath(t, repo, ".githooks")
	if tbl := table(t, runHooks, req(t, dir, nil)); len(tbl.Warnings) != 0 {
		t.Errorf("warnings = %+v, with the value set in the repository's own config", tbl.Warnings)
	}
}

// Two system files setting core.hooksPath differently are two builds of git
// running hooks from two directories, and one is listed: it says so, naming
// the files.
func TestHooksSayWhenSystemFilesOfTwoBuildsDisagree(t *testing.T) {
	home := machineConfig(t, "")
	etc, brew := filepath.Join(home, "etc", "gitconfig"), filepath.Join(home, "brew", "gitconfig")
	systemGitConfigs = []string{etc, brew}
	writeFile(t, home, "etc/gitconfig", "[core]\n\thooksPath = /etc-hooks\n")
	writeFile(t, home, "brew/gitconfig", "[core]\n\thooksPath = /brew-hooks\n")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	tbl := table(t, runHooks, req(t, dir, nil))
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "git.hooks.system" ||
		!strings.Contains(tbl.Warnings[0].Message, etc+", "+brew) {
		t.Fatalf("warnings = %+v, want git.hooks.system naming both files", tbl.Warnings)
	}
	writeFile(t, home, "etc/gitconfig", "[core]\n\thooksPath = /brew-hooks\n")
	if tbl := table(t, runHooks, req(t, dir, nil)); len(tbl.Warnings) != 0 {
		t.Errorf("warnings = %+v, with both files naming one directory", tbl.Warnings)
	}
}

// `git sparse-checkout set` turns on extensions.worktreeConfig, and go-git
// refuses every repository that has it, so each capability here answered
// such a checkout as not a git repository. It opens, and config.worktree,
// which git reads after the repository's config once the extension asks it
// to, is read as the worktree scope: by git.config, over MCP too, since it
// is the repository's, and by git.hooks for core.hooksPath, where it is the
// last word.
func TestARepositoryWithWorktreeConfigOpensAndItsWorktreeScopeIsRead(t *testing.T) {
	machineConfig(t, "")
	for _, version := range []string{"0", "1"} {
		t.Run("format "+version, func(t *testing.T) {
			dir, repo := testRepo(t)
			commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
			writeFile(t, dir, ".git/config", "[core]\n\trepositoryformatversion = "+version+
				"\n\tbare = false\n\thooksPath = .local-hooks\n[extensions]\n\tworktreeConfig = true\n")
			writeFile(t, dir, ".git/config.worktree", "[core]\n\tsparseCheckout = true\n\thooksPath = .git/wt-hooks\n")
			writeExecutable(t, dir, ".git/wt-hooks/pre-commit")

			if tbl := table(t, runStatus, req(t, dir, nil)); len(tbl.Rows) != 0 {
				t.Errorf("status = %v, want a clean tree", tbl.Rows)
			}
			for name, r := range map[string]plugin.Request{
				"terminal": req(t, dir, nil), "MCP": guarded(t, dir, dir).WithSurface(plugin.SurfaceMCP),
			} {
				if row := rowFor(t, table(t, runConfig, r), "Key", "core.sparseCheckout"); row[0] != "worktree" {
					t.Errorf("%s: core.sparseCheckout row = %v, want the worktree scope", name, row)
				}
			}
			if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[2] != ".git/wt-hooks/pre-commit" {
				t.Errorf("pre-commit row = %v, want the directory config.worktree names", row)
			}
		})
	}
}

// The config git's environment sets for one command is read after every
// file, as git reads it: core.hooksPath set there, as `git -c` sets it for a
// command git runs, is the directory git runs hooks from, over the
// repository's own value. And an environment git refuses to run with fails
// the call, since git runs no hook with it.
func TestCoreHooksPathSetInGitsEnvironmentIsTheLastWord(t *testing.T) {
	home := machineConfig(t, "")
	writeExecutable(t, home, "env-hooks/pre-push")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	setHooksPath(t, repo, ".githooks")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hookspath'='"+filepath.Join(home, "env-hooks")+"'")

	if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-push"); row[2] != filepath.Join(home, "env-hooks", "pre-push") {
		t.Errorf("pre-push row = %v, want the directory GIT_CONFIG_PARAMETERS names", row)
	}
	t.Setenv("GIT_CONFIG_COUNT", "x")
	if _, err := runHooks(context.Background(), req(t, dir, nil)); errCode(err) != "git.hooks.failed" {
		t.Errorf("git.hooks with a GIT_CONFIG_COUNT git refuses: %v, want git.hooks.failed", err)
	}
}

// hooksPathByGit is where the git on PATH looks for a pre-commit in dir, and
// whether it runs at all; ok is false where there is no git to ask.
func hooksPathByGit(t *testing.T, dir string) (path string, runs, ok bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return "", false, false
	}
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-path", "hooks/pre-commit")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err == nil, true
}

// core.hooksPath set to nothing is set: git looks for each hook at the top of
// the filesystem, /pre-commit, and runs one there. This skipped the empty
// value and listed the directory an earlier file named as the one git runs
// hooks from. It lists the top of the filesystem, and says why; over MCP that
// is a directory outside the roots, and refused as one.
func TestCoreHooksPathSetToNothingIsTheTopOfTheFilesystem(t *testing.T) {
	home := machineConfig(t, "[core]\n\thooksPath = "+filepath.Join(t.TempDir(), "global-hooks")+"\n")
	writeExecutable(t, home, "global-hooks/pre-commit")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\thooksPath =\n")

	tbl := table(t, runHooks, req(t, dir, nil))
	for _, row := range tbl.Rows {
		if !strings.HasPrefix(row[2], "/") || strings.Count(row[2], "/") != 1 {
			t.Errorf("row %v is not a file at the top of the filesystem", row)
		}
	}
	var empty *view.Error
	for i, w := range tbl.Warnings {
		if w.Code == "git.hooks.empty" {
			empty = &tbl.Warnings[i]
		}
	}
	if empty == nil || !strings.Contains(empty.Message, "the repository's config sets core.hooksPath to nothing") {
		t.Errorf("warnings = %+v, want git.hooks.empty naming the repository's config", tbl.Warnings)
	}
	if _, err := runHooks(context.Background(), guarded(t, dir, dir)); errCode(err) != "core.mcp.path.outside" {
		t.Errorf("over MCP, git.hooks = %v, want the top of the filesystem refused as outside the roots", err)
	}
	if path, runs, ok := hooksPathByGit(t, dir); ok && (!runs || path != "/pre-commit") {
		t.Errorf("git looks for the pre-commit at %q (runs: %v), not /pre-commit", path, runs)
	}

	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hooksPath'=''")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\thooksPath = .githooks\n")
	if tbl := table(t, runHooks, req(t, dir, nil)); len(tbl.Warnings) == 0 || tbl.Warnings[0].Code != "git.hooks.empty" ||
		!strings.HasPrefix(tbl.Warnings[0].Message, "the config in git's environment sets core.hooksPath to nothing") {
		t.Errorf("with `git -c core.hooksPath=`, warnings = %+v, want git.hooks.empty naming the environment", tbl.Warnings)
	}
}

// core.hooksPath with no value at all, a name alone on its line or `git -c
// core.hooksPath` with no =, is one git refuses: "missing value", and it runs
// nothing, no hook and no command. go-git reads it as set to nothing, and
// this read it as unset, listing the repository's own hooks as the ones that
// run. It is refused, as git refuses it; a later file setting it again is the
// value git reads, and no error.
func TestCoreHooksPathWithNoValueIsRefusedAsGitRefusesIt(t *testing.T) {
	machineConfig(t, "")
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeHook(t, dir, "pre-commit", true)
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\thooksPath\n")

	_, err := runHooks(context.Background(), req(t, dir, nil))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "git.hooks.failed" ||
		!strings.Contains(verr.Message, "the repository's config sets core.hooksPath with no value") {
		t.Errorf("git.hooks with a valueless core.hooksPath = %v, want it refused naming the file", err)
	}
	if _, runs, ok := hooksPathByGit(t, dir); ok && runs {
		t.Error("git runs with a valueless core.hooksPath")
	}

	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hooksPath'=")
	if _, err := runHooks(context.Background(), req(t, dir, nil)); errCode(err) != "git.hooks.failed" {
		t.Errorf("with `git -c core.hooksPath`, git.hooks = %v, want git.hooks.failed", err)
	}

	unsetenv(t, "GIT_CONFIG_PARAMETERS")
	home := machineConfig(t, "[core]\n\thooksPath\n")
	writeExecutable(t, dir, ".githooks/pre-push")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\thooksPath = .githooks\n")
	rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-push")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	if path, runs, ok := hooksPathByGit(t, dir); ok && (!runs || path != ".githooks/pre-commit") {
		t.Errorf("git looks for the pre-commit at %q (runs: %v), not in .githooks", path, runs)
	}
}

// GIT_CONFIG_GLOBAL=/dev/null and GIT_CONFIG_SYSTEM=/dev/null are how a CI job
// or a test runs git with none of the machine's config, and git reads the
// null device as a file that is empty. This refused it as not a regular
// file: git.hooks and git.config failed, and git.status named the excludes
// file as not applied, so git.diff showed no untracked file at all. And
// `core.excludesFile = /dev/null` reads no excludes file, on every surface.
func TestTheNullDeviceIsAnEmptyConfig(t *testing.T) {
	machineConfig(t, "")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeHook(t, dir, "pre-commit", true)
	writeFile(t, dir, "new.txt", "new\n")

	if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[1] != "active" {
		t.Errorf("pre-commit row = %v, want it active", row)
	}
	table(t, runConfig, req(t, dir, nil))
	if tbl := table(t, runStatus, req(t, dir, nil)); len(tbl.Warnings) != 0 {
		t.Errorf("status warnings = %+v, want none", tbl.Warnings)
	}
	if body := text(t, runDiff, req(t, dir, nil)); !strings.Contains(body, "+new") {
		t.Errorf("the diff does not show the untracked file:\n%s", body)
	}

	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n\texcludesFile = "+os.DevNull+"\n")
	for name, r := range map[string]plugin.Request{"terminal": req(t, dir, nil), "MCP": guarded(t, dir, dir)} {
		if tbl := table(t, runStatus, r); len(tbl.Warnings) != 0 || len(tbl.Rows) != 1 {
			t.Errorf("%s, with core.excludesFile = %s, rows %v, warnings %+v, want new.txt alone", name, os.DevNull,
				tbl.Rows, tbl.Warnings)
		}
	}
}
