package grant

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/mcp"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One process plays both machines: the server handler reads the grant store
// the test seeded, the client side unlocks the operator key and signs, and
// every byte crosses real HTTP. What this pins is the full seam — remotes
// resolution, the passphrase field, the envelope, the roster check, and the
// rows rendering through the same grantsTable the local listing uses.
func TestRemoteListReadsTheServersRoster(t *testing.T) {
	setup(t)
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	srv := httptest.NewUnstartedServer(mcp.NewOperatorHandler(mcp.OperatorConfig{Roster: roster, URL: base}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  lab:\n    url: "+base+"\n"), 0o600); err != nil {
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
	for _, c := range Plugin(func() []plugin.Capability { return nil }, builtIn).Capabilities {
		if c.ID == "grant.list" {
			return c
		}
	}
	t.Fatal("no grant.list capability")
	return plugin.Capability{}
}

// The whole stage-2 loop in one process, every byte over real HTTP: the
// server's guard enrolls the roster, allow --server prepares there, signs
// here and issues; the row lands attributed to the operator; revoke
// --server takes it back.
func TestRemoteAllowThenRevokeEndToEnd(t *testing.T) {
	setup(t)
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	if verr := guard.EnableRemote(roster.Entries(), base); verr != nil {
		t.Fatal(verr)
	}
	srv := httptest.NewUnstartedServer(mcp.NewOperatorHandler(mcp.OperatorConfig{
		Roster: roster, URL: base,
		Prepare: PrepareRemote(catalog, builtIn),
		Revoke:  RevokeRemote,
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  lab:\n    url: "+base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := guardCap(t, "grant.allow").Run(context.Background(), reqTUI(map[string]any{
		"target": "kv.get", "ttl": "15m", "note": "remote e2e", "agent": "lab-agent",
		"server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	if !strings.Contains(body, "lab-agent on lab may call kv.get for 15m") {
		t.Fatalf("allow said: %s", body)
	}

	held, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(held) != 1 || held[0].From != core.FromOperatorPrefix+"tobi" || held[0].Sig == "" {
		t.Fatalf("held = %+v", held)
	}

	// The preview crosses the wire with write off: the count is the
	// server's truth.
	v, err = guardCap(t, "grant.revoke").Run(context.Background(),
		plugin.NewRequest(map[string]any{
			"target": "kv.get", "server": "lab", "passphrase": "correct horse",
		}, true, true).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "would revoke 1 grant(s)") {
		t.Fatalf("dry-run revoke said: %s", body)
	}

	v, err = guardCap(t, "grant.revoke").Run(context.Background(), reqTUI(map[string]any{
		"target": "kv.get", "server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "revoked 1 grant(s)") {
		t.Fatalf("revoke said: %s", body)
	}
	if held, verr := core.Load(); verr != nil || len(held) != 0 {
		t.Fatalf("held after revoke = %+v, %v", held, verr)
	}
}

// A hostile server that widens what prepare returns must not get it
// signed: the client checks every spec-controlled field before the key
// touches anything, and the issue verb is never reached.
func TestAHostilePrepareIsNotASigningOracle(t *testing.T) {
	setup(t)
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	issueReached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/operator/v1/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "hostile-nonce"})
		case "/operator/v1/call":
			var env operatorid.Envelope
			_ = json.NewDecoder(r.Body).Decode(&env)
			if env.Verb == operatorid.VerbGrantIssue {
				issueReached = true
			}
			now := time.Now()
			widened := core.Grant{
				// The operator asked for kv.get, 15m; the hostile draft is the
				// whole kv namespace for a day.
				Target: "kv", From: core.FromOperatorPrefix + "tobi",
				Issued: now, Expires: now.Add(24 * time.Hour),
				TTL: "15m", Server: "http://" + r.Host,
			}
			_ = json.NewEncoder(w).Encode(operatorid.Prepared{Grant: widened})
		}
	}))
	defer srv.Close()
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  evil:\n    url: "+srv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := guardCap(t, "grant.allow").Run(context.Background(), reqTUI(map[string]any{
		"target": "kv.get", "ttl": "15m", "agent": "lab-agent",
		"server": "evil", "passphrase": "correct horse",
	}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.operator.prepare.mismatch" {
		t.Fatalf("err = %v, want core.operator.prepare.mismatch", err)
	}
	if issueReached {
		t.Fatal("the widened draft was signed and submitted anyway")
	}
}
