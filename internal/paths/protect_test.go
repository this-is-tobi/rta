package paths_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/pluginhost"
)

// machine is a user's home with no override of anything, the way a macOS or a
// Linux laptop is when nobody exported a variable.
func machine(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("RTA_DATA_DIR", filepath.Join(home, "data"))
	return home
}

func has(list []string, want string) bool {
	return slices.ContainsFunc(list, func(p string) bool { return p == want })
}

// **Moving the configuration directory moved what protects it.** The plugin
// sandbox's read+write denial and the MCP path gate both name rta's own
// configuration directory, and both ask paths for it rather than keeping a
// copy, so ~/.config/rta is denied the day it becomes the directory. Shown
// here rather than argued: the old macOS spelling is not in either, the new
// one is, and the ancestors that must keep their names (a rename of ~/.config
// would take the rule's target out from under it) are pinned.
func TestTheSandboxDeniesTheConfigDirectoryWhereverItIs(t *testing.T) {
	for _, c := range []struct {
		name string
		xdg  bool
	}{
		{"the default, ~/.config/rta", false},
		{"an XDG_CONFIG_HOME of the user's", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := machine(t)
			own := filepath.Join(home, ".config", "rta")
			if c.xdg {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "dotfiles", "config"))
				own = filepath.Join(home, "dotfiles", "config", "rta")
			}
			deny, err := pluginhost.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if !has(deny.NoAccess, own) {
				t.Errorf("%s is not denied read+write to a confined plugin: %v", own, deny.NoAccess)
			}
			if !has(deny.NoMove, filepath.Dir(own)) {
				t.Errorf("%s may be renamed out from under the rule: %v", filepath.Dir(own), deny.NoMove)
			}
			old := filepath.Join(home, "Library", "Application Support", "rta")
			if has(deny.NoAccess, old) {
				t.Errorf("the old macOS directory is denied although it does not exist: %v", deny.NoAccess)
			}
		})
	}
}

func TestThePathGateRefusesTheConfigDirectoryWhereverItIs(t *testing.T) {
	for _, c := range []struct {
		name string
		xdg  bool
	}{
		{"the default, ~/.config/rta", false},
		{"an XDG_CONFIG_HOME of the user's", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := machine(t)
			own := filepath.Join(home, ".config", "rta")
			if c.xdg {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "dotfiles", "config"))
				own = filepath.Join(home, "dotfiles", "config", "rta")
			}
			if err := os.MkdirAll(own, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"config.yaml", "remotes.yaml", "kv.identity", "policy.yaml"} {
				if err := os.WriteFile(filepath.Join(own, name), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			notes := filepath.Join(home, "notes.txt")
			if err := os.WriteFile(notes, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			g, err := pathguard.New(home)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"config.yaml", "remotes.yaml", "kv.identity", "policy.yaml"} {
				if _, verr := g.Check("path", filepath.Join(own, name)); verr == nil || verr.Code != "core.mcp.path.protected" {
					t.Errorf("%s in the config directory was allowed (%v)", name, verr)
				}
			}
			if _, verr := g.Check("path", notes); verr != nil {
				t.Errorf("a file beside the config directory was refused: %v", verr)
			}
		})
	}
}

// What an earlier build left at the old macOS location is still its owner's
// secret — a kv.identity there decrypts the store — and a rename of the
// directory must not be how the protection on it lapsed. Both gates keep
// denying it for as long as it is there, and neither mentions it once it is
// gone.
func TestWhatIsLeftAtTheOldMacOSLocationStaysDenied(t *testing.T) {
	home := machine(t)
	old := filepath.Join(home, "Library", "Application Support", "rta")
	defer paths.SetUserConfigDir(func() (string, error) {
		return filepath.Join(home, "Library", "Application Support"), nil
	})()

	deny, err := pluginhost.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if has(deny.NoAccess, old) {
		t.Fatalf("the old directory is denied before it exists: %v", deny.NoAccess)
	}

	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "kv.identity"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	deny, err = pluginhost.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !has(deny.NoAccess, old) {
		t.Errorf("%s is not denied read+write to a confined plugin: %v", old, deny.NoAccess)
	}
	g, err := pathguard.New(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", filepath.Join(old, "kv.identity")); verr == nil || verr.Code != "core.mcp.path.protected" {
		t.Errorf("the old kv.identity was allowed (%v)", verr)
	}
}
