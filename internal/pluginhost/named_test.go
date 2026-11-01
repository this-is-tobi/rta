package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A path named for a plugin to read under strict stops where the macOS profile
// already stops every plugin: never rta's own state, never a credential
// location, which is a need's, never the home directory whole, never a path
// that is not there. A directory beside them is a path to name.
func TestANamedPathStopsWhereTheDenyListStops(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RTA_DATA_DIR", filepath.Join(home, ".local", "share", "rta"))
	t.Setenv("RTA_CONFIG", filepath.Join(home, ".config", "rta", "config.yaml"))
	t.Setenv("RTA_KV_IDENTITY", "")
	for _, dir := range []string{"certs", ".ssh", ".config/rta", ".local/share/rta", ".config/gcloud"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	certs := filepath.Join(home, "certs")
	if got, verr := CheckPath("~/certs"); verr != nil || got != certs {
		t.Errorf("~/certs: %q, %v — want it named, absolute", got, verr)
	}
	// A directory holding a credential location is named and carved at launch.
	if _, verr := CheckPath(filepath.Join(home, ".config")); verr != nil {
		t.Errorf("~/.config, which holds gcloud's and rta's, was refused: %v", verr)
	}
	for raw, want := range map[string]string{
		home:                                    "holds the home directory",
		"/":                                     "holds the home directory",
		filepath.Join(home, ".ssh"):             "the ssh credential location",
		filepath.Join(home, ".config/gcloud"):   "credential location",
		filepath.Join(home, ".config/rta"):      "rta's own state",
		filepath.Join(home, ".local/share/rta"): "rta's own state",
		filepath.Join(home, "missing"):          "is not there",
	} {
		_, verr := CheckPath(raw)
		if verr == nil || verr.Code != "plugin.access.path" || !strings.Contains(verr.Message, want) {
			t.Errorf("%s: %v, want plugin.access.path saying %q", raw, verr, want)
		}
	}
	// A link into a credential location is judged by where it leads.
	link := filepath.Join(home, "certs", "keys")
	if err := os.Symlink(filepath.Join(home, ".ssh"), link); err != nil {
		t.Fatal(err)
	}
	if _, verr := CheckPath(link); verr == nil {
		t.Error("a link into ~/.ssh was named")
	}
}

// A process is handed back from the cache by its policy's hash, so a path
// named for the artifact has to change it, or the plugin started before it was
// named keeps answering without it.
func TestTheSpecHashTracksANamedPath(t *testing.T) {
	plain := DenySet{Strict: true, NoRead: []string{"/home/u/.ssh"}}
	named := plain
	named.Named = []string{"/home/u/certs"}
	if specHash(plain) == specHash(named) {
		t.Error("a named path left the process's hash as it was")
	}
	unstrict := plain
	unstrict.Strict = false
	if specHash(plain) == specHash(unstrict) {
		t.Error("a strict launch hashes as an unconfined one")
	}
}

// A call naming a file under a named path passes the host's gate, and one
// under a credential location carved out of it does not.
func TestAFileUnderANamedPathIsReadable(t *testing.T) {
	home := t.TempDir()
	certs, gcloud := filepath.Join(home, "certs"), filepath.Join(home, "certs", "gcloud")
	for _, dir := range []string{certs, gcloud} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(certs, "ca.pem"), filepath.Join(gcloud, "key.json")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := DenySet{Strict: true, Named: []string{certs}, NoRead: []string{gcloud}}
	if !d.readable(filepath.Join(certs, "ca.pem")) {
		t.Error("a file under the named path is not readable")
	}
	if d.readable(filepath.Join(gcloud, "key.json")) {
		t.Error("a file in a credential location carved out of the named path is readable")
	}
}
