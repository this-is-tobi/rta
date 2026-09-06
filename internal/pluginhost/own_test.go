package pluginhost

import (
	"path/filepath"
	"testing"
)

// An installed artifact may read its own directory and nothing else gains
// from the carve-out: DenySet.Own says why a process needs it. A plugin found
// anywhere else — $PATH, the bin/ of links, a stray binary at the top of the
// data directory — is launched under the machine's policy unchanged.
func TestAnInstalledArtifactMayReadItsOwnDirectoryOnly(t *testing.T) {
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))

	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(ManagedStore(), "pg", "abc123", "rta-plugin-pg")
	launched, err := d.Launching(own)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(launched.Own, filepath.Dir(own)) {
		t.Errorf("the artifact's own directory is not readable to it: %v", launched.Own)
	}
	// Both spellings of that one directory and nothing else, the way every
	// deny entry carries its link and its target.
	resolved := resolveDeepest(filepath.Dir(own))
	for _, p := range launched.Own {
		if p != filepath.Dir(own) && p != resolved {
			t.Errorf("Own names %s, which is not the artifact's directory", p)
		}
	}
	// The denials themselves are untouched: the carve-out is an allow rendered
	// after them, never a path taken out of a tier.
	if !containsPath(launched.NoAccess, data) {
		t.Errorf("launching removed the data directory from the deny set: %v", launched.NoAccess)
	}

	for _, elsewhere := range []string{
		"/usr/local/bin/rta-plugin-pg",
		filepath.Join(ManagedBin(), "rta-plugin-pg"),
		filepath.Join(data, "rta-plugin-pg"),
		filepath.Join(data, "plugins", "store-old", "pg", "rta-plugin-pg"),
	} {
		l, err := d.Launching(elsewhere)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Own) != 0 {
			t.Errorf("launching %s carved out %v", elsewhere, l.Own)
		}
	}
}

// The carve-out is policy, so two launches that differ only in it must not
// share a cached process.
func TestTheSpecHashTracksTheCarveOut(t *testing.T) {
	a := DenySet{NoAccess: []string{"/one"}}
	b := DenySet{NoAccess: []string{"/one"}, Own: []string{"/one/plugins/store/pg/abc"}}
	if specHash(a) == specHash(b) {
		t.Error("adding the artifact's own directory did not change the hash")
	}
}

// An entry that could close the policy form early is refused here as it is
// everywhere else, rather than rendered.
func TestACarveOutThatCouldCloseThePolicyFormIsRefused(t *testing.T) {
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Launching(filepath.Join(ManagedStore(), `pg")(allow default`, "abc", "rta-plugin-pg")); err == nil {
		t.Error("a directory name that closes a form was rendered into the policy")
	}
}
