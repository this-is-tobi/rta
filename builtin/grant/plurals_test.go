package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// "revoked 1 grant(s)" is the shape of sentence written once and read every
// day, on the commands an operator runs when something has gone wrong. One
// grant is counted in the singular, and the words around it agree.
func TestOneGrantIsCountedInTheSingular(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password"})
	dry := func(h plugin.Handler, values map[string]any) string {
		t.Helper()
		v, err := h(context.Background(), plugin.NewRequest(values, true, true).WithSurface(plugin.SurfaceCLI))
		if err != nil {
			t.Fatal(err)
		}
		return v.(view.Text).Body
	}

	type said struct{ name, body, want string }
	var all []said
	all = append(all,
		said{"renew", run(t, renewH, map[string]any{"target": "kv.get"}).(view.Text).Body, "renewed 1 grant:"},
		said{"dry revoke", dry(runRevoke, map[string]any{"target": "kv.get"}), "would revoke 1 grant"},
		said{"guard on", dry(guardCap(t, "grant.guard.on").Run, nil),
			"the 1 grant currently held is cleared — it was issued without one"},
		said{"suppressed", suppressedNote(plugin.SurfaceCLI, 1), "1 grant on disk is suppressed by your team's policy"},
		said{"remote suppressed", remoteSuppressedNote("lab", 1),
			"1 grant on lab is suppressed by its team's policy.\nIt is not deleted"},
	)
	all = append(all, said{"revoke", run(t, runRevoke, map[string]any{"target": "kv.get"}).(view.Text).Body, "revoked 1 grant"})
	for _, s := range all {
		if strings.Contains(s.body, "(s)") || !strings.Contains(s.body, s.want) {
			t.Errorf("%s said %q, want %q", s.name, s.body, s.want)
		}
	}
}

// stagingAt writes a config whose staging profile points kv at path.
func stagingAt(t *testing.T, config, path string) {
	t.Helper()
	body := "profiles:\n  staging:\n    plugins:\n      kv:\n        set: {path: " + path + "}\n"
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", config)
}

// renew's note on a connection repointed since issue counts the grants it
// renewed there, not the connections they name: two grants on one changed
// connection read "1 of these name a connection", a count of neither.
func TestRenewCountsTheGrantsOnAChangedConnection(t *testing.T) {
	for _, c := range []struct {
		records []string
		want    string
	}{
		{[]string{"db-password"}, "note: 1 of these names a connection that has changed since it was " +
			"issued (staging), so the deadline moved and it still authorizes nothing"},
		{[]string{"db-password", "api-token"}, "note: 2 of these name a connection that has changed since " +
			"they were issued (staging), so the deadline moved and they still authorize nothing"},
	} {
		setup(t)
		config := filepath.Join(t.TempDir(), "config.yaml")
		stagingAt(t, config, "/a")
		for _, record := range c.records {
			run(t, allowH, map[string]any{"target": "kv.get", "scope": record, "profile": "staging"})
		}
		stagingAt(t, config, "/b")
		if body := run(t, renewH, map[string]any{"target": "kv.get"}).(view.Text).Body; !strings.Contains(body, c.want) {
			t.Errorf("renew said %q, want %q", body, c.want)
		}
	}
}
