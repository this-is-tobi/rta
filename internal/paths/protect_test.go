package paths_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/pkg/plugin"
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

// The store's key is wherever RTA_KV_IDENTITY says, which the secrets page
// tells an operator to use for a key kept anywhere, and a server over HTTP
// reads its callers' tokens and its operators' keys from files named on its
// command line, documented in the working directory. None is under rta's
// own directories, so neither gate denied any of them: a confined plugin read
// the key that decrypts every copy of the store, and an agent named the token
// file under the root it was served. Each is denied as rta's own directories
// are, to a plugin granted ~/.ssh too when the key is one in it, and the key
// named as ~/… is the file kv opens: an MCP client's JSON env, a systemd
// Environment= line or a quoted export hands rta the tilde unexpanded, and
// taken literally it denied a directory called ~ under the working directory.
func TestTheFilesRtaIsPointedAtAreDeniedAsItsOwnDirectoriesAre(t *testing.T) {
	home := machine(t)
	key := filepath.Join(home, ".config", "age", "rta.txt")
	sshKey := filepath.Join(home, ".ssh", "id_ed25519")
	tokens := filepath.Join(home, "work", "tokens.txt")
	roster := filepath.Join(home, "work", "operators.txt")
	notes := filepath.Join(home, "work", "notes.txt")
	for _, f := range []string{key, sshKey, tokens, roster, notes} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(paths.ForgetProtected)
	paths.Protect(tokens, roster)

	for name, c := range map[string]struct {
		env, key string
		granted  []plugin.Need
	}{
		"a key kept anywhere":             {key, key, nil},
		"a key named as ~/…":              {"~/.config/age/rta.txt", key, nil},
		"a key in ~/.ssh, ~/.ssh granted": {sshKey, sshKey, []plugin.Need{plugin.NeedSSH}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RTA_KV_IDENTITY", c.env)
			deny, err := pluginhost.ResolveAllowing(c.granted, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{c.key, tokens, roster} {
				if !has(deny.NoAccess, f) {
					t.Errorf("%s is not denied read+write to a confined plugin: %v", f, deny.NoAccess)
				}
			}
			if !has(deny.NoMove, filepath.Dir(c.key)) {
				t.Errorf("%s may be renamed out from under the rule: %v", filepath.Dir(c.key), deny.NoMove)
			}
			if has(deny.NoAccess, notes) {
				t.Errorf("a file beside the token file is denied: %v", deny.NoAccess)
			}
			g, err := pathguard.New(home)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{c.key, tokens, roster} {
				if _, verr := g.Check("path", f); verr == nil || verr.Code != "core.mcp.path.protected" {
					t.Errorf("%s was allowed to a path argument (%v)", f, verr)
				}
			}
			if _, verr := g.Check("path", notes); verr != nil {
				t.Errorf("a file beside the token file was refused: %v", verr)
			}
		})
	}
}

// A file of rta's own is wherever the operator put it, and that is not always
// a path the sandbox profile would spell: a key synced under ~/Dropbox
// (Personal)/ held two characters the profile's check refused, and the deny
// set is resolved for every launch, so every plugin then failed to start — on
// Linux too, where no profile is built. Parentheses are ordinary characters
// inside a quoted string of the profile (pluginhost's confine tests run one),
// so that key is denied like any other. A path holding a character no quoting
// in the profile carries is left out of what a plugin is denied and reported
// for `rta doctor` to name; the path gate refuses it all the same, and every
// plugin still loads.
func TestAFileOfRtasOwnKeptAnywhereNeverStopsAPluginLoading(t *testing.T) {
	home := machine(t)
	synced := filepath.Join(home, "Dropbox (Personal)", "keys", "rta.txt")
	quoted := filepath.Join(home, `the "work" box`, "tokens.txt")
	for _, f := range []string{synced, quoted} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(paths.ForgetProtected)
	paths.Protect(quoted)
	t.Setenv("RTA_KV_IDENTITY", synced)

	deny, err := pluginhost.Resolve()
	if err != nil {
		t.Fatalf("a file of rta's own kept anywhere stopped every plugin loading: %v", err)
	}
	if !has(deny.NoAccess, synced) {
		t.Errorf("%s is not denied read+write to a confined plugin: %v", synced, deny.NoAccess)
	}
	if !has(deny.NoMove, filepath.Dir(synced)) {
		t.Errorf("%s may be renamed out from under the rule: %v", filepath.Dir(synced), deny.NoMove)
	}
	if has(deny.NoAccess, quoted) || !slices.Equal(deny.Unnamed, []string{quoted}) {
		t.Errorf("%s: denied %v, reported %v; want it left out of the profile and reported",
			quoted, deny.NoAccess, deny.Unnamed)
	}
	g, err := pathguard.New(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{synced, quoted} {
		if _, verr := g.Check("path", f); verr == nil || verr.Code != "core.mcp.path.protected" {
			t.Errorf("%s was allowed to a path argument (%v)", f, verr)
		}
	}
}
