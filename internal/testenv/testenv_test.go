package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// Everything the person's own configuration is read through moves to an empty place,
// and the toolchain does not: a test that runs `go` must find the caches it would find
// on this machine and not a cold cache under the new HOME.
func TestIsolateMovesTheHomeAndLeavesTheGoToolchainWhereItWas(t *testing.T) {
	// Restored when the test ends, whatever Isolate set. A machine that exports none of
	// the toolchain's variables is the one the pinning is for, so they are blank.
	for _, name := range []string{
		"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "RTA_CONFIG", "RTA_DATA_DIR",
	} {
		t.Setenv(name, os.Getenv(name))
	}
	for _, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"} {
		t.Setenv(name, "")
	}
	cache, mods := goEnv(t, "GOCACHE"), goEnv(t, "GOMODCACHE")

	dir := t.TempDir()
	if err := Isolate(dir); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "RTA_CONFIG", "RTA_DATA_DIR",
	} {
		if got := os.Getenv(name); !strings.HasPrefix(got, dir+string(filepath.Separator)) {
			t.Errorf("%s = %q, not under %s", name, got, dir)
		}
	}
	if _, err := os.Stat(os.Getenv("RTA_CONFIG")); !os.IsNotExist(err) {
		t.Errorf("the config file Isolate names exists: %v", err)
	}
	if got := goEnv(t, "GOCACHE"); got != cache {
		t.Errorf("GOCACHE moved from %q to %q", cache, got)
	}
	if got := goEnv(t, "GOMODCACHE"); got != mods {
		t.Errorf("GOMODCACHE moved from %q to %q", mods, got)
	}
}

// git exports GIT_DIR to a hook and to the command of `git rebase --exec` run from a
// linked worktree, and a fixture made with `git -C <directory> ...` then lands in that
// repository instead of the directory. After Isolate it lands where the test says.
func TestIsolateLetsAFixtureRepositoryBeItsOwnWhereGitNamesAnother(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on $PATH")
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		base := []string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}
		out, err := exec.Command(gitBin, append(base, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	for _, name := range []string{
		"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "RTA_CONFIG", "RTA_DATA_DIR",
	} {
		t.Setenv(name, os.Getenv(name))
	}
	// This test is run by hooks and by `git rebase --exec` too, with the variables it is
	// about already set: put away before the first git, and put back when it is over.
	for _, name := range gitRepositoryVariables {
		if value, set := os.LookupEnv(name); set {
			t.Cleanup(func() { os.Setenv(name, value) })
			os.Unsetenv(name)
		}
	}

	other := t.TempDir()
	run(other, "init", "--quiet")
	run(other, "commit", "--quiet", "--allow-empty", "-m", "the repository the tests are run from")
	before := run(other, "rev-list", "--all", "--count")

	for _, name := range gitRepositoryVariables {
		t.Setenv(name, filepath.Join(other, ".git"))
	}
	if err := Isolate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, name := range gitRepositoryVariables {
		if got, set := os.LookupEnv(name); set {
			t.Errorf("%s = %q survived Isolate", name, got)
		}
	}

	fixture := t.TempDir()
	run(fixture, "init", "--quiet")
	run(fixture, "commit", "--quiet", "--allow-empty", "-m", "fixture")
	if got := run(fixture, "rev-list", "--all", "--count"); got != "1" {
		t.Errorf("the fixture has %s commits, want its own one", got)
	}
	if got := run(other, "rev-list", "--all", "--count"); got != before {
		t.Errorf("the other repository went from %s commits to %s", before, got)
	}
}

// A toolchain variable the person already set is theirs, and is not replaced by the
// value `go env` would have answered.
func TestIsolateKeepsAToolchainVariableThatWasSet(t *testing.T) {
	for _, name := range []string{
		"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "RTA_CONFIG", "RTA_DATA_DIR",
		"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV",
	} {
		t.Setenv(name, os.Getenv(name))
	}
	own := t.TempDir()
	t.Setenv("GOMODCACHE", own)
	if err := Isolate(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GOMODCACHE"); got != own {
		t.Errorf("GOMODCACHE = %q, want the one that was set, %q", got, own)
	}
}
