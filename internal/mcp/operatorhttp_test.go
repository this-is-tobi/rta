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

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/operator"
)

// These tests drive the operator channel over the real Serve stack — the
// mux, the cross-origin wrapper, the bearer wall beside it — because the
// property under test is exactly the seam: signed envelopes in, without any
// bearer, while everything else on the listener still demands one.

func startOperatorRemote(t *testing.T, roster operator.Roster) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(testRegistry(t), "test", Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, server, ln, RemoteOptions{
			Verifier: StaticTokenVerifier(map[string]string{"tok-a": "alice"}),
			Operator: NewOperatorHandler(OperatorConfig{
				Roster: roster, Version: "test", Agent: "demo-agent", Stderr: io.Discard,
			}),
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
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
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

	status, body := postEnvelope(t, addr, signer.Sign(fetchChallenge(t, addr), operator.VerbStatus, nil))
	if status != http.StatusOK {
		t.Fatalf("status verb: %d — %s", status, body)
	}
	var st operator.Status
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Agent != "demo-agent" || len(st.Operators) != 1 || st.Operators[0] != "tobi" {
		t.Fatalf("status = %+v", st)
	}

	status, body = postEnvelope(t, addr, signer.Sign(fetchChallenge(t, addr), operator.VerbGrantList, nil))
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

	env := signer.Sign(fetchChallenge(t, addr), operator.VerbStatus, nil)
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
	env := stranger.Sign(fetchChallenge(t, addr), operator.VerbStatus, nil)
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
