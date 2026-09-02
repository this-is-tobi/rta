package operator

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	id "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func TestMain(m *testing.M) {
	id.ScryptWorkFactor = 10
	os.Exit(m.Run())
}

func capability(t *testing.T, capID string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return c
		}
	}
	t.Fatalf("no capability %s", capID)
	return plugin.Capability{}
}

// The TUI surface carries the passphrase as a masked field; the CLI refuses
// the same value on argv, which is what makes it the surface tests use.
func reqTUI(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceTUI)
}

func pairs(t *testing.T, v view.View) map[string]string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("view is %T, want KeyValue", v)
	}
	out := map[string]string{}
	for _, p := range kv.Pairs {
		out[p.Key] = p.Value
	}
	return out
}

func TestInitMintsAndStatusPrintsTheSameKey(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	v, err := capability(t, "operator.init").Run(context.Background(),
		reqTUI(map[string]any{"passphrase": "correct horse", "label": "tobi"}))
	if err != nil {
		t.Fatal(err)
	}
	minted := pairs(t, v)
	if minted["fingerprint"] == "" || minted["fingerprint"] != id.Fingerprint() {
		t.Fatalf("fingerprint = %q, stored = %q", minted["fingerprint"], id.Fingerprint())
	}
	line, verr := id.RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}
	if !strings.Contains(minted["enroll"], line) {
		t.Fatalf("enroll cell %q does not carry the roster line %q", minted["enroll"], line)
	}

	v, err = capability(t, "operator.status").Run(context.Background(),
		reqTUI(map[string]any{"label": "tobi"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairs(t, v)["fingerprint"]; got != minted["fingerprint"] {
		t.Fatalf("status fingerprint = %q, want %q", got, minted["fingerprint"])
	}
}

func TestTheOperatorNamespaceRefusesMCP(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	for _, capID := range []string{"operator.init", "operator.status"} {
		_, err := capability(t, capID).Run(context.Background(),
			plugin.NewRequest(nil, false, true).WithSurface(plugin.SurfaceMCP))
		verr, ok := err.(*view.Error)
		if !ok || verr.Code != "operator.surface" {
			t.Fatalf("%s over MCP: %v, want operator.surface", capID, err)
		}
	}
}

func TestThePassphraseFlagIsRefusedOnTheCLI(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	_, err := capability(t, "operator.init").Run(context.Background(),
		plugin.NewRequest(map[string]any{"passphrase": "leaked", "label": "tobi"}, false, true).
			WithSurface(plugin.SurfaceCLI))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.operator.passphrase.argv" {
		t.Fatalf("err = %v, want core.operator.passphrase.argv", err)
	}
}

// The full client flow against the real handler: remotes.yaml names the
// server, the passphrase unlocks the key, the envelope signs, the server
// verifies against its roster and answers. One process playing both
// machines, but every byte crosses real HTTP.
func TestStatusWithAServerAsksTheServer(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, verr := id.Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	line, verr := id.RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}
	rosterPath := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(rosterPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roster, _, err := id.LoadRoster(rosterPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcp.NewOperatorHandler(mcp.OperatorConfig{
		Roster: roster, Version: "9.9-test", Agent: "lab-agent",
	}))
	defer srv.Close()

	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	remotes := "servers:\n  lab:\n    url: " + srv.URL + "\n"
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"), []byte(remotes), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := capability(t, "operator.status").Run(context.Background(),
		reqTUI(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if err != nil {
		t.Fatal(err)
	}
	got := pairs(t, v)
	if got["version"] != "9.9-test" || got["agent"] != "lab-agent" {
		t.Fatalf("remote status = %+v", got)
	}
	if !strings.Contains(got["operators"], "tobi") {
		t.Fatalf("operators cell %q does not name the enrollment", got["operators"])
	}
}
