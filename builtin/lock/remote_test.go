package lock

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/mcp"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One process plays both machines, every byte over real HTTP — the shape
// builtin/agent and builtin/grant already pin for their remote halves. The
// lock verbs need less wiring than either: the server dispatches them
// against its own store directly, since locks are core state with no
// capability behind them, so the handler takes a roster and a URL and
// nothing else. RTA_DATA_DIR must already be set: the operator key and the
// server's lock store both land there.
func lockServer(t *testing.T) {
	t.Helper()
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
	srv := httptest.NewUnstartedServer(mcp.NewOperatorHandler(mcp.OperatorConfig{Roster: roster, URL: base}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	remotes(t, "lab", base)
}

// remotes points remotes.yaml's name at a server, in a config directory of
// this test's own.
func remotes(t *testing.T, name, url string) {
	t.Helper()
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  "+name+":\n    url: "+url+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// remoteReq is a request the way the TUI's form makes one: the passphrase
// arrives as a value, which the CLI surface refuses on purpose (argv).
func remoteReq(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceTUI)
}

func pairValue(t *testing.T, v view.View, key string) string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("got %s, want key/value pairs", view.TypeOf(v))
	}
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("no %q pair in %+v", key, kv.Pairs)
	return ""
}

// A hostile server that answers a lock.add with a different principal must
// not get that principal printed as the confirmation: "locked agent claude"
// over a lock the server says names something else is a false all-clear
// with nothing on the operator's screen to check it against. remoteRm
// already spells the principal from the operator's own input; this pins the
// add half to the same rule.
func TestARemoteAddDoesNotTakeTheServersWordForWhoWasLocked(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/operator/v1/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "hostile-nonce"})
		case "/operator/v1/call":
			var env operatorid.Envelope
			_ = json.NewDecoder(r.Body).Decode(&env)
			if env.Verb != operatorid.VerbLockAdd {
				http.Error(w, "unexpected verb", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(lockdown.Lock{
				Kind: lockdown.KindCredential, Name: "somebody-else", Note: "not what you typed", At: time.Now(),
			})
		}
	}))
	defer srv.Close()
	remotes(t, "evil", srv.URL)

	v, err := capByID(t, "lock.add").Run(context.Background(), remoteReq(map[string]any{
		"kind": "agent", "name": "claude", "note": "incident", "server": "evil", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(t, v, "locked"); got != "agent claude on evil" {
		t.Errorf("the confirmation reads %q — the server's word, not the operator's", got)
	}
	if got := pairValue(t, v, "shown to them"); got != "incident" {
		t.Errorf("the note reads %q — the server's word, not the operator's", got)
	}
}

func TestRemoteLockAddListAndRmEndToEnd(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	lockServer(t)
	ctx := context.Background()

	v, err := capByID(t, "lock.add").Run(ctx, remoteReq(map[string]any{
		"kind": "agent", "name": "claude", "note": "incident", "server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(t, v, "locked"); got != "agent claude on lab" {
		t.Errorf("locked = %q", got)
	}
	locks, verr := lockdown.Load()
	if verr != nil || len(locks) != 1 {
		t.Fatalf("the server's store: %+v, %v", locks, verr)
	}
	if locks[0].By != grant.FromOperatorPrefix+"tobi" || locks[0].Note != "incident" {
		t.Errorf("the stored row is not attributed to the enrolled operator: %+v", locks[0])
	}

	v, err = capByID(t, "lock.list").Run(ctx, remoteReq(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok || len(table.Rows) != 1 || table.Rows[0][1] != "claude" || table.Rows[0][3] != grant.FromOperatorPrefix+"tobi" {
		t.Fatalf("remote list = %+v", v)
	}

	v, err = capByID(t, "lock.rm").Run(ctx, remoteReq(map[string]any{
		"kind": "agent", "name": "claude", "server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(t, v, "unlocked"); got != "agent claude on lab" {
		t.Errorf("unlocked = %q", got)
	}
	if locks, _ := lockdown.Load(); len(locks) != 0 {
		t.Errorf("the lock survived a remote rm: %+v", locks)
	}
	v, err = capByID(t, "lock.rm").Run(ctx, remoteReq(map[string]any{
		"kind": "agent", "name": "claude", "server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(t, v, "nothing to lift"); got != "agent claude on lab was not locked" {
		t.Errorf("second rm = %q", got)
	}
}

// The remote read path renders the server's rows through the same table the
// local listing uses, and a TTL'd lock's window survives the JSON round trip
// through operator.LockList.
func TestTheRemoteReadPathRendersTheServersTable(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	lockServer(t)
	for _, spec := range [][2]string{{"claude", ""}, {"codex", "1h"}} {
		l, verr := lockdown.Build("agent", spec[0], "", spec[1], "terminal")
		if verr != nil {
			t.Fatal(verr)
		}
		if verr := lockdown.Add(l); verr != nil {
			t.Fatal(verr)
		}
	}
	stored, _ := lockdown.Load()
	v, err := capByID(t, "lock.list").Run(context.Background(),
		remoteReq(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok || len(table.Rows) != 2 || len(table.Columns) != 5 {
		t.Fatalf("remote list = %+v", v)
	}
	for _, row := range table.Rows {
		for _, l := range stored {
			if l.Name != row[1] {
				continue
			}
			want := "until removed"
			if !l.Expires.IsZero() {
				want = "until " + l.Expires.Local().Format("2006-01-02 15:04")
			}
			if row[4] != want {
				t.Errorf("%s stands %q, want %q", l.Name, row[4], want)
			}
		}
	}
}
