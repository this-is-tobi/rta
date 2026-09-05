package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/view"
)

// These tests drive the operator channel over the real Serve stack — the
// mux, the cross-origin wrapper, the bearer wall beside it — because the
// property under test is exactly the seam: signed envelopes in, without any
// bearer, while everything else on the listener still demands one.

func startOperatorRemote(t *testing.T, roster operator.Roster) string {
	return startOperatorWith(t, OperatorConfig{Roster: roster})
}

// startOperatorWith serves cfg over the real stack, filling in the identity
// fields a test rarely cares about; cfg.URL is always the bound address,
// because that is what the test's client will sign.
func startOperatorWith(t *testing.T, cfg OperatorConfig) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.URL = "http://" + ln.Addr().String()
	if cfg.Version == "" {
		cfg.Version = "test"
	}
	if cfg.Agent == "" {
		cfg.Agent = "demo-agent"
	}
	cfg.Stderr = io.Discard
	server := NewServer(testRegistry(t), "test", Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, server, ln, RemoteOptions{
			Verifier: StaticTokenVerifier(map[string]string{"tok-a": "alice"}),
			Operator: NewOperatorHandler(cfg),
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve did not shut down cleanly: %v", err)
		}
	})
	return ln.Addr().String()
}

func enrolled(t *testing.T, label string) (operator.Signer, operator.Roster) {
	return enrolledAs(t, label, "")
}

// enrolledAs enrolls this machine's key under label with an annotation
// suffix on its roster line — " role=read" builds the read-only enrollment.
func enrolledAs(t *testing.T, label, annotation string) (operator.Signer, operator.Roster) {
	t.Helper()
	operator.ScryptWorkFactor = 10
	s, verr := operator.Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, verr := operator.RosterLine(label)
	if verr != nil {
		t.Fatal(verr)
	}
	path := t.TempDir() + "/operators"
	if err := os.WriteFile(path, []byte(line+annotation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roster, _, err := operator.LoadRoster(path)
	if err != nil {
		t.Fatal(err)
	}
	return s, roster
}

func fetchChallenge(t *testing.T, addr string) string {
	t.Helper()
	res, err := http.Post("http://"+addr+"/operator/v1/challenge", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Nonce == "" {
		t.Fatalf("challenge did not answer a nonce: %v", err)
	}
	return body.Nonce
}

func postEnvelope(t *testing.T, addr string, env operator.Envelope) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post("http://"+addr+"/operator/v1/call", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

func TestTheOperatorChannelAnswersASignedCall(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	now := time.Now()
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Issued: now, Expires: now.Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	addr := startOperatorRemote(t, roster)

	status, body := postEnvelope(t, addr, signer.Sign("http://"+addr, fetchChallenge(t, addr), operator.VerbStatus, nil))
	if status != http.StatusOK {
		t.Fatalf("status verb: %d — %s", status, body)
	}
	var st operator.Status
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Agent != "demo-agent" || len(st.Operators) != 1 ||
		st.Operators[0] != (operator.OperatorInfo{Label: "tobi", Role: operator.RoleFull}) {
		t.Fatalf("status = %+v", st)
	}

	status, body = postEnvelope(t, addr, signer.Sign("http://"+addr, fetchChallenge(t, addr), operator.VerbGrantList, nil))
	if status != http.StatusOK {
		t.Fatalf("grant.list verb: %d — %s", status, body)
	}
	var gl operator.GrantList
	if err := json.Unmarshal(body, &gl); err != nil {
		t.Fatal(err)
	}
	if len(gl.Grants) != 1 || gl.Grants[0].Target != "demo.item.reveal" {
		t.Fatalf("grant.list = %+v", gl)
	}
}

func TestACapturedEnvelopeReplaysNowhere(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorRemote(t, roster)

	env := signer.Sign("http://"+addr, fetchChallenge(t, addr), operator.VerbStatus, nil)
	if status, body := postEnvelope(t, addr, env); status != http.StatusOK {
		t.Fatalf("first use: %d — %s", status, body)
	}
	status, body := postEnvelope(t, addr, env)
	if status != http.StatusUnauthorized {
		t.Fatalf("replay: %d — %s", status, body)
	}
	// The refusal names nothing an unauthenticated caller can learn from.
	if bytes.Contains(body, []byte("nonce")) || bytes.Contains(body, []byte("signature")) {
		t.Fatalf("the refusal explains itself to a stranger: %s", body)
	}
}

func TestAStrangerCannotCall(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	_, roster := enrolled(t, "tobi")
	addr := startOperatorRemote(t, roster)

	// A different keypair, never enrolled — the fingerprint claim is free,
	// the signature is not.
	if err := os.Remove(operator.Path()); err != nil {
		t.Fatal(err)
	}
	stranger, verr := operator.Init("other pass")
	if verr != nil {
		t.Fatal(verr)
	}
	env := stranger.Sign("http://"+addr, fetchChallenge(t, addr), operator.VerbStatus, nil)
	if status, _ := postEnvelope(t, addr, env); status != http.StatusUnauthorized {
		t.Fatalf("an un-enrolled key was answered: %d", status)
	}
}

// Without --operators the paths do not exist: they fall through to the MCP
// handler's bearer wall, indistinguishable from a server that never heard of
// the channel.
func TestWithoutARosterThePathIsBearerWalled(t *testing.T) {
	addr := startRemote(t, Options{}, StaticTokenVerifier(map[string]string{"tok-a": "alice"}))
	res, err := http.Post("http://"+addr+"/operator/v1/challenge", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("challenge without a roster: %d, want 401", res.StatusCode)
	}
}

// errCode digs the view.Error code out of a refusal body.
func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var remote struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &remote); err != nil {
		t.Fatalf("body is not an error envelope: %s", body)
	}
	return remote.Error.Code
}

// The issue verb's own expectations, each named to an authenticated
// operator: unsigned, misattributed, clock-skewed and over-ceiling
// submissions are refused with codes, and preparation is refused outright
// on a server whose guard never enrolled anyone.
func TestSubmittedGrantsAreHeldToTheirShape(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr
	if verr := guard.EnableRemote(roster.Entries(), at); verr != nil {
		t.Fatal(verr)
	}
	submit := func(g grant.Grant) (int, []byte) {
		raw, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), operator.VerbGrantIssue, raw))
	}
	base := func() grant.Grant {
		now := time.Now()
		return grant.Grant{
			Target: "demo.item.reveal", From: grant.FromOperatorPrefix + "tobi",
			Issued: now, Expires: now.Add(15 * time.Minute), Server: at,
		}
	}

	unsigned := base()
	if _, body := submit(unsigned); errCode(t, body) != "core.operator.issue.unsigned" {
		t.Fatalf("unsigned: %s", body)
	}

	misattributed := base()
	misattributed.From = grant.FromOperatorPrefix + "somebody-else"
	grant.SignWith(signer.GrantSigner(), &misattributed)
	if _, body := submit(misattributed); errCode(t, body) != "core.operator.issue.from" {
		t.Fatalf("misattributed: %s", body)
	}

	skewed := base()
	skewed.Issued = skewed.Issued.Add(-time.Hour)
	grant.SignWith(signer.GrantSigner(), &skewed)
	if _, body := submit(skewed); errCode(t, body) != "core.operator.issue.skew" {
		t.Fatalf("skewed: %s", body)
	}

	over := base()
	over.Expires = over.Issued.Add(100 * 24 * time.Hour)
	grant.SignWith(signer.GrantSigner(), &over)
	if _, body := submit(over); errCode(t, body) != "core.operator.issue.ttl" {
		t.Fatalf("over ceiling: %s", body)
	}

	preloaded := base()
	preloaded.MaxUses = 1
	preloaded.Uses = -1000000
	grant.SignWith(signer.GrantSigner(), &preloaded)
	if _, body := submit(preloaded); errCode(t, body) != "core.operator.issue.bookkeeping" {
		t.Fatalf("pre-loaded bookkeeping: %s", body)
	}

	elsewhere := base()
	elsewhere.Server = "https://elsewhere.example"
	grant.SignWith(signer.GrantSigner(), &elsewhere)
	if _, body := submit(elsewhere); errCode(t, body) != "core.operator.issue.server" {
		t.Fatalf("bound elsewhere: %s", body)
	}

	good := base()
	grant.SignWith(signer.GrantSigner(), &good)
	if status, body := submit(good); status != http.StatusOK {
		t.Fatalf("a well-shaped submission was refused: %s", body)
	}
	held, verr := grant.Load()
	if verr != nil || len(held) != 1 || held[0].From != grant.FromOperatorPrefix+"tobi" {
		t.Fatalf("held = %+v, %v", held, verr)
	}
}

// Preparation on a never-provisioned server refuses before the operator
// signs anything, naming the provisioning step — and a signed grant
// submitted anyway dies on the store's own gate.
func TestPrepareRefusesWithoutARemoteGuard(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{
		Roster: roster,
		Prepare: func(operator.IssueSpec, string) (operator.Prepared, *view.Error) {
			t.Fatal("prepare ran with no remote guard")
			return operator.Prepared{}, nil
		},
	})
	at := "http://" + addr
	_, body := postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), operator.VerbGrantPrepare, []byte(`{}`)))
	if errCode(t, body) != "core.operator.guard" {
		t.Fatalf("prepare without a remote guard: %s", body)
	}
}

// A verb the serve command never wired is refused by name, not served
// half-broken.
func TestAnUnwiredVerbSaysSo(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr
	status, body := postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), operator.VerbGrantRevoke, []byte(`{"all":true}`)))
	if errCode(t, body) != "core.operator.verb" {
		t.Fatalf("unwired revoke: %s", body)
	}
	if status != http.StatusForbidden {
		t.Fatalf("unwired revoke: HTTP %d, want 403 — a configuration refusal is not a server failure", status)
	}
}

// A role=read enrollment answers the read verbs and nothing else. The gate
// sits before verb wiring and before the unknown-verb answer, so a
// mutation this server never wired — or a verb this build never heard of —
// refuses as "outside your role", named to the authenticated caller, and a
// consent answer signed by a watching dashboard's stolen key mints nothing.
func TestAReadOnlyKeyIsGatedToItsVerbs(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolledAs(t, "dash", " role=read")
	addr := startOperatorWith(t, OperatorConfig{
		Roster:  roster,
		Consent: true,
		Pending: func() (operator.ConsentList, *view.Error) { return operator.ConsentList{}, nil },
	})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}

	status, body := call(operator.VerbStatus, nil)
	if status != http.StatusOK {
		t.Fatalf("status verb: %d — %s", status, body)
	}
	var st operator.Status
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Operators) != 1 || st.Operators[0].Role != operator.RoleRead {
		t.Fatalf("status does not report the role: %+v", st.Operators)
	}
	if status, body := call(operator.VerbGrantList, nil); status != http.StatusOK {
		t.Fatalf("grant.list verb: %d — %s", status, body)
	}
	if status, body := call(operator.VerbConsentList, nil); status != http.StatusOK {
		t.Fatalf("consent.list verb: %d — %s", status, body)
	}
	if status, body := call(operator.VerbLockList, nil); status != http.StatusOK {
		t.Fatalf("lock.list verb: %d — %s", status, body)
	}

	for _, verb := range []string{
		operator.VerbGrantRevoke, operator.VerbGrantPrepare, operator.VerbGrantIssue,
		operator.VerbConsentAnswer, operator.VerbLockAdd, operator.VerbLockRm, "grant.future",
	} {
		status, body := call(verb, []byte(`{}`))
		if errCode(t, body) != "core.operator.role" {
			t.Fatalf("%s under role=read: %s", verb, body)
		}
		// 403, not 500: a policy refusal must not read as a failing server
		// to whatever dashboards this read-only key exists to feed.
		if status != http.StatusForbidden {
			t.Fatalf("%s under role=read: HTTP %d, want 403", verb, status)
		}
	}
}

// Every refusal the mutation verbs can actually surface — their own
// codes, and the wired packages' — answers 4xx, never 500: a policy or
// grammar refusal must not page whoever watches this server's error
// rate, and (because the ledger's Refused/Failed split rides statusFor)
// must not be sealed into the record as an operator's authorized work
// failing. The representatives here are real codes from the packages the
// verbs dispatch into, not statusFor's own literals read back.
func TestRefusalsAreClassifiedNotPaged(t *testing.T) {
	for code, want := range map[string]int{
		// the caller's submission to fix
		"core.operator.issue.unsigned":      http.StatusBadRequest,
		"core.operator.issue.yet-unwritten": http.StatusBadRequest,
		"grant.agent.charset":               http.StatusBadRequest,
		"grant.scope.traversal":             http.StatusBadRequest,
		"grant.notarget":                    http.StatusBadRequest,
		"core.lock.note":                    http.StatusBadRequest,
		"agent.request.unknown":             http.StatusBadRequest,
		"agent.answer.elsewhere":            http.StatusBadRequest,
		// authorization, policy, and the tamper defenses saying no
		"grant.policy.refused":    http.StatusForbidden,
		"core.grant.guard.forged": http.StatusForbidden,
		"agent.request.tampered":  http.StatusForbidden,
		"agent.answer.failed":     http.StatusForbidden,
		// unnamed stays a page — real failures belong on the error rate
		"core.grant.store": http.StatusInternalServerError,
	} {
		if got := statusFor(code); got != want {
			t.Errorf("statusFor(%s) = %d, want %d", code, got, want)
		}
	}
}
