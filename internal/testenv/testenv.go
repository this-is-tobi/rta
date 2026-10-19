// Package testenv is what a package's TestMain needs so that none of its tests reads
// the person who runs them.
//
// A test that builds the root command, the dashboard or a grant roster loads the
// configuration and the environment that is switched on, and a test that does not
// name a config file of its own reads whatever `~/.config/rta/config.yaml` holds on
// the machine it runs on: a `theme:`, an `output:` or a profile there changed what it
// drew, and a failure on one machine was no failure on another. It is a rule about
// the package and not about a helper, which is something a new test can forget to
// call, so a package says it once, in TestMain.
package testenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Isolate points HOME, every XDG directory, RTA_CONFIG and RTA_DATA_DIR at an empty
// place under dir, which a test that wants a config file or a data directory of its
// own still overrides with t.Setenv, and a package that keeps one for all its tests
// sets again after this.
//
// **The Go toolchain's own places are pinned first.** `go` finds its build cache, its
// module cache and the settings of `go env -w` through HOME (and XDG_CACHE_HOME), so
// a test that runs it, as `rta plugin dev` does, would find them empty under the new
// HOME and fetch every module again, or fail where there is no network. The values are
// read once, before anything moves, and kept as they are where the person already set
// one.
//
// **The repository the tests are run from is let go too.** git exports GIT_DIR to a hook,
// and to the command of `git rebase --exec` run from a linked worktree, so a test that
// runs `git -C <temporary directory> commit` acts on that repository and not on its own:
// the plugin distribution tests committed their fixtures into the branch being rebased,
// and a `git init` among them flipped core.bare of the shared repository. The variables
// that name a repository are dropped, so that a fixture is made in the directory the test
// names.
func Isolate(dir string) error {
	if err := pinGoToolchain(); err != nil {
		return err
	}
	for _, name := range gitRepositoryVariables {
		if err := os.Unsetenv(name); err != nil {
			return err
		}
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"RTA_CONFIG":      filepath.Join(home, ".config", "rta", "config.yaml"),
		"RTA_DATA_DIR":    filepath.Join(home, ".local", "share", "rta"),
	} {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}

// gitRepositoryVariables are the variables that make git act on a repository other than
// the one in the directory it is run in.
var gitRepositoryVariables = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_PREFIX",
}

func pinGoToolchain() error {
	names := []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}
	values, ok := askGo(names)
	if !ok {
		return nil
	}
	for i, name := range names {
		if values[i] == "" || os.Getenv(name) != "" {
			continue
		}
		if err := os.Setenv(name, values[i]); err != nil {
			return err
		}
	}
	return nil
}

// askGo is what `go env` answers for names, one value to a name, and false where there
// is no go to ask or it cannot say: a machine without the toolchain has nothing to pin.
func askGo(names []string) ([]string, bool) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, false
	}
	out, err := exec.CommandContext(context.Background(), goBin, append([]string{"env"}, names...)...).Output()
	if err != nil {
		return nil, false
	}
	values := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	return values, len(values) == len(names)
}
