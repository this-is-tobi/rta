package lock

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/lockdown"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

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
