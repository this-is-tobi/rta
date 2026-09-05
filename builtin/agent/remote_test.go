package agent

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

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/mcp"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One process plays both machines, every byte over real HTTP — the same
// full-seam shape builtin/grant's remote tests pin: remotes resolution,
// the passphrase field, the envelope, the roster check, and the queue
// rendering through the same pendingTable the local listing uses.

func remoteCap(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}

func remoteReq(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceTUI)
}

// consentServer stands up the operator channel over the queue this test's
// RTA_DATA_DIR holds, enrolling "tobi", and points remotes.yaml's "lab" at
// it. RTA_DATA_DIR must already be set: the operator key lands there too.
func consentServer(t *testing.T) {
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
	srv := httptest.NewUnstartedServer(mcp.NewOperatorHandler(mcp.OperatorConfig{
		Roster:  roster,
		URL:     base,
		Pending: PendingRemote("lab"),
		Answer:  AnswerRemote("lab"),
		Consent: true,
	}))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  lab:\n    url: "+base+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePendingAllowAndDenyEndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dataDir)
	consentServer(t)
	parked, err := consent.Ask(consent.Call{
		Cap: "kv.get", Safety: "read", Scopes: []string{"db-password"},
		Agent: "lab", Why: "needs a grant",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	v, rerr := remoteCap(t, "agent.pending").Run(context.Background(),
		remoteReq(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if rerr != nil {
		t.Fatal(rerr)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("view is %T, want Table", v)
	}
	if len(table.Rows) != 1 || table.Rows[0][0] != parked.Request.ID {
		t.Fatalf("rows = %+v", table.Rows)
	}

	v, rerr = remoteCap(t, "agent.show").Run(context.Background(),
		remoteReq(map[string]any{"id": parked.Request.ID, "server": "lab", "passphrase": "correct horse"}))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if _, ok := v.(view.Sections); !ok {
		t.Fatalf("show view is %T, want Sections", v)
	}

	v, rerr = remoteCap(t, "agent.allow").Run(context.Background(),
		remoteReq(map[string]any{"id": parked.Request.ID, "server": "lab", "passphrase": "correct horse"}))
	if rerr != nil {
		t.Fatal(rerr)
	}
	kv, ok := v.(view.KeyValue)
	if !ok || kv.Pairs[0].Key != "allowed" || kv.Pairs[0].Value != "kv.get db-password" {
		t.Fatalf("allow said: %+v", v)
	}
	// The decision file's By is the channel's attribution: the roster label
	// the envelope verified, in the operator:<label> shape remote issuance
	// already writes into a grant's Origin.
	raw, err := os.ReadFile(filepath.Join(consent.Dir(), parked.Request.ID+".decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decided struct {
		By string `json:"by"`
	}
	if err := json.Unmarshal(raw, &decided); err != nil {
		t.Fatal(err)
	}
	if decided.By != "operator:tobi" {
		t.Fatalf("decision by = %q, want operator:tobi", decided.By)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if a := parked.Wait(ctx); !a.Answered || !a.Allowed {
		t.Fatalf("answer = %+v, want allowed", a)
	}
	parked.Close()

	denied, err := consent.Ask(consent.Call{Cap: "todo.rm", Safety: "destructive", Agent: "lab"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	v, rerr = remoteCap(t, "agent.deny").Run(context.Background(),
		remoteReq(map[string]any{"id": denied.Request.ID, "server": "lab", "passphrase": "correct horse"}))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if kv, ok := v.(view.KeyValue); !ok || kv.Pairs[0].Key != "denied" {
		t.Fatalf("deny said: %+v", v)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if a := denied.Wait(ctx2); !a.Answered || a.Allowed {
		t.Fatalf("answer = %+v, want denied", a)
	}
}

// A request rewritten under the server is the same alarm remotely as
// locally: kept off the queue, named in the listing, and unanswerable by
// name rather than reported as absent.
func TestRemotePendingSurfacesTamperedRequests(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	consentServer(t)
	now := time.Now().UTC().Truncate(time.Second)
	bad := consent.Request{
		ID: "beef0001", Digest: "not-derived-from-the-display", Cap: "kv.get",
		Safety: "read", AskedAt: now, Deadline: now.Add(time.Minute),
	}
	raw, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(consent.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consent.Dir(), "beef0001.request.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	v, rerr := remoteCap(t, "agent.pending").Run(context.Background(),
		remoteReq(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if rerr != nil {
		t.Fatal(rerr)
	}
	sections, ok := v.(view.Sections)
	if !ok || len(sections.Items) != 2 {
		t.Fatalf("view = %+v, want the queue plus the kept-off section", v)
	}
	body := sections.Items[1].View.(view.Text).Body
	if !strings.Contains(body, "beef0001") {
		t.Fatalf("tampered note says: %s", body)
	}

	_, rerr = remoteCap(t, "agent.allow").Run(context.Background(),
		remoteReq(map[string]any{"id": "beef0001", "server": "lab", "passphrase": "correct horse"}))
	verr, ok := rerr.(*view.Error)
	if !ok || verr.Code != "agent.request.tampered" {
		t.Fatalf("err = %v, want agent.request.tampered", rerr)
	}
}

// The submitted digest pins the file at decision time, on the server, under
// the same load the seal is minted from — an answer naming a call that is
// no longer the one waiting refuses instead of approving.
func TestAnAnswerNamingTheWrongDigestIsRefused(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	parked, err := consent.Ask(consent.Call{Cap: "kv.get", Safety: "read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	_, verr := AnswerRemote("")(operatorid.AnswerSpec{
		ID: parked.Request.ID, Digest: consent.Call{Cap: "todo.rm"}.Digest(), Allow: true,
	}, "tobi")
	if verr == nil || verr.Code != "agent.answer.failed" {
		t.Fatalf("err = %v, want agent.answer.failed", verr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if a := parked.Wait(ctx); a.Answered {
		t.Fatalf("answer = %+v, want nothing decided", a)
	}
}

// The queue is machine-global; a roster is one server's trust decision.
// The consent verbs answer only for the serving server's own --as name, so
// enrollment on one server is not consent authority over every co-resident
// server's questions — the stage-3 review's one held finding.
func TestConsentAnswersAreScopedToTheServersOwnName(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	parked, err := consent.Ask(consent.Call{Cap: "kv.get", Safety: "read", Agent: "other"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	cl, verr := PendingRemote("lab")()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(cl.Waiting) != 0 {
		t.Fatalf("lab's listing shows another server's questions: %+v", cl.Waiting)
	}
	_, verr = AnswerRemote("lab")(operatorid.AnswerSpec{
		ID: parked.Request.ID, Digest: parked.Request.Digest, Allow: true,
	}, "tobi")
	if verr == nil || verr.Code != "agent.answer.elsewhere" {
		t.Fatalf("err = %v, want agent.answer.elsewhere", verr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if a := parked.Wait(ctx); a.Answered {
		t.Fatalf("answer = %+v, want nothing decided", a)
	}
}

// --ttl mints standing authority, and remotely that is the prepare-and-sign
// flow with its own review step — never a side effect of answering.
func TestARemoteAllowRefusesTTL(t *testing.T) {
	_, rerr := remoteCap(t, "agent.allow").Run(context.Background(),
		remoteReq(map[string]any{"id": "aa", "server": "lab", "ttl": "15m"}))
	verr, ok := rerr.(*view.Error)
	if !ok || verr.Code != "agent.remote.ttl" {
		t.Fatalf("err = %v, want agent.remote.ttl", rerr)
	}
}

// A hostile server that displays one call while binding another must not
// get it answered: the client derives the digest from what it read and
// refuses an entry that disagrees with itself, before any answer is sent.
func TestAHostileQueueEntryIsNotAnswered(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	operatorid.ScryptWorkFactor = 10
	if _, verr := operatorid.Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	answerReached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/operator/v1/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "hostile-nonce"})
		case "/operator/v1/call":
			var env operatorid.Envelope
			_ = json.NewDecoder(r.Body).Decode(&env)
			if env.Verb == operatorid.VerbConsentAnswer {
				answerReached = true
			}
			now := time.Now().UTC().Truncate(time.Second)
			// Displayed as sys.cpu, bound to kv.get db-password: the swap the
			// digest exists to make harmless, attempted by the queue's owner.
			lying := consent.Request{
				ID:     "aa",
				Digest: consent.Call{Cap: "kv.get", Scopes: []string{"db-password"}}.Digest(),
				Cap:    "sys.cpu", Safety: "read",
				AskedAt: now, Deadline: now.Add(time.Minute),
			}
			_ = json.NewEncoder(w).Encode(operatorid.ConsentList{Waiting: []consent.Request{lying}})
		}
	}))
	defer srv.Close()
	confDir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(confDir, "config.yaml"))
	if err := os.WriteFile(filepath.Join(confDir, "remotes.yaml"),
		[]byte("servers:\n  evil:\n    url: "+srv.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, rerr := remoteCap(t, "agent.allow").Run(context.Background(),
		remoteReq(map[string]any{"id": "aa", "server": "evil", "passphrase": "correct horse"}))
	verr, ok := rerr.(*view.Error)
	if !ok || verr.Code != "agent.answer.dishonest" {
		t.Fatalf("err = %v, want agent.answer.dishonest", rerr)
	}
	if answerReached {
		t.Fatal("the lying entry was answered anyway")
	}
}
