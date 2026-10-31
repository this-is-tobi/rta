package pluginhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The gate in front of a strict plugin reads a file a call names the way the
// policy behind it does. A store's key kept inside a granted location is
// carved out of what the plugin may read, so a call naming it is refused before
// the plugin is sent it, as plugin.confine.file, while the file beside it, which
// the plugin may read, passes. Whether rta's own file is one the deny set names
// or one the macOS profile cannot spell (Unnamed), it is rta's either way.
func TestStrictRefusesACallNamingRtasOwnFileInsideAGrantedLocation(t *testing.T) {
	granted := t.TempDir()
	key := filepath.Join(granted, "rta.key")
	beside := filepath.Join(granted, "known_hosts")
	for _, f := range []string{key, beside} {
		if err := os.WriteFile(f, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	named := DenySet{Strict: true, Readable: withTarget(granted), NoAccess: withTarget(key)}
	unnamed := DenySet{Strict: true, Readable: withTarget(granted), Unnamed: []string{key}}
	inputs := []plugin.Field{{Name: "file", Type: plugin.Path}}
	for _, c := range []struct {
		label   string
		d       DenySet
		path    string
		refused bool
	}{
		{"the file beside the key", named, beside, false},
		{"the key the deny set names", named, key, true},
		{"the key the profile cannot name", unnamed, key, true},
	} {
		req := plugin.NewRequest(map[string]any{"file": c.path}, false, false).WithSurface(plugin.SurfaceCLI)
		verr := c.d.namedFile("demo.restore", inputs, req)
		if (verr != nil) != c.refused {
			t.Errorf("%s: refused = %v, want %v (%v)", c.label, verr != nil, c.refused, verr)
			continue
		}
		if verr != nil && verr.Code != "plugin.confine.file" {
			t.Errorf("%s: refused as %s, want plugin.confine.file", c.label, verr.Code)
		}
	}
}
