package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
	"github.com/this-is-tobi/rta/pkg/plugin"
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
// a home directory holding a .gitconfig with content, and a system file that
// does not exist — so what this machine's own config says cannot reach an
// assertion.
func machineConfig(t *testing.T, gitconfig string) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	saved := systemGitConfig
	systemGitConfig = filepath.Join(home, "no-system-gitconfig")
	t.Cleanup(func() { systemGitConfig = saved })
	if gitconfig != "" {
		writeFile(t, home, ".gitconfig", gitconfig)
	}
	return home
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
// came, which no context can interrupt. It is refused as not a file instead.
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
