package grant

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	operatorid "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// One process plays both machines: the server handler reads the grant store
// the test seeded, the client side unlocks the operator key and signs, and
// every byte crosses real HTTP. What this pins is the full seam — remotes
// resolution, the passphrase field, the envelope, the roster check, and the
// rows rendering through the same grantsTable the local listing uses.
func TestRemoteListReadsTheServersRoster(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	line, verr := operatorid.RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}
	rosterPath := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(rosterPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roster, _, err := operatorid.LoadRoster(rosterPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if verr := core.Issue(core.Grant{
		Target: "demo.item.reveal", Issued: now, Expires: now.Add(time.Hour), Note: "for the test",
	}, true); verr != nil {
		t.Fatal(verr)
	}
	srv := httptest.NewServer(mcp.NewOperatorHandler(mcp.OperatorConfig{Roster: roster}))
	defer srv.Close()
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  lab:\n    url: "+srv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := listCap(t).Run(context.Background(),
		reqTUI(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("view is %T, want Table", v)
	}
	if len(table.Rows) != 1 || table.Rows[0][0] != "demo.item.reveal" {
		t.Fatalf("rows = %+v", table.Rows)
	}
}

func TestRemoteListRefusesDetail(t *testing.T) {
	_, err := listCap(t).Run(context.Background(),
		reqTUI(map[string]any{"server": "lab", "detail": true}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "grant.remote.detail" {
		t.Fatalf("err = %v, want grant.remote.detail", err)
	}
}

func listCap(t *testing.T) plugin.Capability {
	t.Helper()
	for _, c := range Plugin(func() []plugin.Capability { return nil }).Capabilities {
		if c.ID == "grant.list" {
			return c
		}
	}
	t.Fatal("no grant.list capability")
	return plugin.Capability{}
}
