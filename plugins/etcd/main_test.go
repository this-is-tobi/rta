package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// req builds a resolved request the way the host would — defaults applied,
// caller values on top — so these test the values a handler actually sees
// rather than a hand-made map.
func req(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

// Every shared connection input must be Local. These fields together name
// which cluster a call reaches and as whom, and an MCP caller may not choose
// that — an agent that could would point rta at a cluster of its own and have
// the host supply the operator's credential beside it.
//
// Written against connFields() rather than a list of names, so an input added
// later is covered the day it is added.
func TestEveryConnectionInputIsLocal(t *testing.T) {
	for _, f := range connFields() {
		if !f.Local {
			t.Errorf("%s: connection input is not Local — an MCP caller could redirect this call", f.Name)
		}
	}
}

// Only a genuine credential opts into EnvFallback. A field that merely chooses
// a destination must not be fillable from an ambient variable the MCP server
// happened to inherit.
func TestOnlySecretsUseEnvFallback(t *testing.T) {
	for _, f := range connFields() {
		if f.EnvFallback && f.Type != plugin.Secret {
			t.Errorf("%s: non-secret input declares EnvFallback (%s); a destination must come from a caller or config",
				f.Name, f.Type)
		}
	}
}

// The three certificate paths are read off this machine's disk. An input
// naming a file that the host then opens is a file-read primitive if a caller
// can choose the path, so these being Local is load-bearing rather than
// consistent-looking.
func TestCertificatePathsCannotBeChosenByACaller(t *testing.T) {
	want := map[string]bool{"ca-file": false, "cert-file": false, "key-file": false}
	for _, f := range connFields() {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
			if !f.Local {
				t.Errorf("%s: a caller-settable path the host then reads is a file-read primitive", f.Name)
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("%s: no longer declared — this test is now guarding nothing", name)
		}
	}
}

// Every capability here reaches off the box, so cap must have forced
// NoPreview on all of them. That is what keeps the automatic dashboard from
// deciding, on its own, that the store a production cluster depends on is
// worth polling every few seconds.
func TestEveryCapabilityIsNoPreview(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NoPreview {
			t.Errorf("%s: NoPreview = false, want true — every capability here reaches off the box", c.ID)
		}
	}
}

// **The line this plugin draws, pinned.**
//
// Nothing here mutates a cluster. The two writes are writes for what they
// disclose, and they are opposite ends of the same scale — which is why only
// one of them takes a grant.
//
// etcd.kv.get returns one stored value, so a person can consent to it by name
// and the grant is worth having. etcd.snapshot returns every stored value, so
// there is no name to put in a grant: it refuses MCP outright instead, which
// is keys.backup's line and pg.dump's, and leaving NeedsGrant off is part of
// that decision rather than an oversight — a grant that can never be exercised
// over the one surface grants gate is an entry in `grant list` meaning nothing.
//
// It matters more here than in most places: a Kubernetes cluster keeps its
// Secrets in etcd base64-encoded rather than encrypted, unless encryption at
// rest was turned on.
//
// The table fails in both directions, so a new capability that is not
// accounted for fails, and an entry naming one that no longer exists fails too.
func TestTheWriteTierIsDisclosureAndOnlyOneHalfIsGrantable(t *testing.T) {
	want := map[string]struct {
		safety plugin.Safety
		grant  bool
	}{
		"etcd.overview":    {plugin.Read, false},
		"etcd.member.list": {plugin.Read, false},
		"etcd.lease.list":  {plugin.Read, false},
		"etcd.kv.list":     {plugin.Read, false},
		"etcd.kv.tree":     {plugin.Read, false},
		"etcd.kv.get":      {plugin.Write, true},
		"etcd.snapshot":    {plugin.Write, false},
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		seen[c.ID] = true
		expect, ok := want[c.ID]
		if !ok {
			t.Errorf("%s: not accounted for in this test's table", c.ID)
			continue
		}
		if c.Safety != expect.safety {
			t.Errorf("%s: Safety = %s, want %s", c.ID, c.Safety, expect.safety)
		}
		if c.NeedsGrant != expect.grant {
			t.Errorf("%s: NeedsGrant = %v, want %v", c.ID, c.NeedsGrant, expect.grant)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: declared in this test's table but not in Plugin()", id)
		}
	}
}

// The value is the whole point of etcd.kv.get and must still not land in a
// terminal scrollback or a log by accident. Redacted is what makes every
// renderer mask it unless somebody asked for it.
func TestTheValueIsDeclaredRedacted(t *testing.T) {
	v := kvGetResult("/registry/secrets/default/api-token", []byte("s3cret"), 1, 1, 1, 0)
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	if !slices.Contains(kv.Redacted, "value") {
		t.Errorf("etcd.kv.get does not declare `value` redacted: %v", kv.Redacted)
	}
	// The key is not redacted, and should not be: knowing a secret exists is
	// the read tier's job and is already available from etcd.kv.list.
	if slices.Contains(kv.Redacted, "key") {
		t.Error("the key is redacted — that hides which secret was read from the record")
	}
}

// Every capability must be reachable and describable, and namespaced by the
// plugin's own name.
func TestEveryCapabilityIsRunnableAndDescribed(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Run == nil {
			t.Errorf("%s: no Run", c.ID)
		}
		if strings.TrimSpace(c.Description) == "" {
			t.Errorf("%s: no Description — `rta explain` has nothing to print", c.ID)
		}
		if !strings.HasPrefix(c.ID, Plugin().Name+".") {
			t.Errorf("%s: capability IDs must be namespaced by %q", c.ID, Plugin().Name)
		}
	}
}

// Half of an mTLS pair is not a partial configuration — it is a connection
// that fails at handshake with an error naming neither file.
func TestHalfAnMTLSPairIsRefusedWithTheMissingHalfNamed(t *testing.T) {
	_, verr := tlsConfig(req(t, "etcd.overview", map[string]any{"cert-file": "/tmp/c.pem"}))
	if verr == nil || !strings.Contains(verr.Code, "key.missing") {
		t.Errorf("a certificate with no key was accepted: %v", verr)
	}
	_, verr = tlsConfig(req(t, "etcd.overview", map[string]any{"key-file": "/tmp/k.pem"}))
	if verr == nil || !strings.Contains(verr.Code, "cert.missing") {
		t.Errorf("a key with no certificate was accepted: %v", verr)
	}
	// Neither is a working configuration too, but it is a valid one: a cluster
	// with server-only TLS needs no client pair at all.
	if _, verr := tlsConfig(req(t, "etcd.overview", map[string]any{})); verr != nil {
		t.Errorf("server-only TLS was refused: %v", verr)
	}
}

// etcd speaks gRPC, so most failures arrive as a status code rather than a
// typed error, and the codes are the stable part. Each must produce a distinct
// code and a hint naming the next step.
func TestGRPCFailuresAreClassifiedByCode(t *testing.T) {
	r := req(t, "etcd.overview", map[string]any{"endpoint": "etcd-0.internal:2379"})
	cases := []struct {
		code codes.Code
		want string
	}{
		{codes.Unauthenticated, "etcd.auth.failed"},
		{codes.PermissionDenied, "etcd.denied"},
		{codes.Unavailable, "etcd.unavailable"},
		{codes.DeadlineExceeded, "etcd.timeout"},
	}
	for _, c := range cases {
		got := classify(status.Error(c.code, "server text"), r)
		if got.Code != c.want {
			t.Errorf("%s classified as %q, want %q", c.code, got.Code, c.want)
		}
		if strings.TrimSpace(got.Hint) == "" {
			t.Errorf("%s has no hint — the code alone does not say what to do next", c.code)
		}
	}
}

// The failure people actually hit: pointing this at 2380, which is the peer
// port and will never answer a client. The hint has to say so, because nothing
// about the error does.
func TestTheWrongPortIsNamedInTheHint(t *testing.T) {
	r := req(t, "etcd.overview", map[string]any{"endpoint": "etcd-0.internal:2380"})
	got := classify(clientv3.ErrNoAvailableEndpoints, r)
	if got.Code != "etcd.unreachable" {
		t.Errorf("code = %q, want etcd.unreachable", got.Code)
	}
	if !strings.Contains(got.Hint, "2380") {
		t.Errorf("the hint does not mention the peer port: %q", got.Hint)
	}
}

// A cluster that has lost quorum accepts connections and answers nothing, so a
// timeout must not be reported as if the network were down.
func TestATimeoutPointsAtQuorumRatherThanTheNetwork(t *testing.T) {
	r := req(t, "etcd.overview", map[string]any{})
	got := classify(context.DeadlineExceeded, r)
	if got.Code != "etcd.timeout" {
		t.Errorf("code = %q, want etcd.timeout", got.Code)
	}
	if !strings.Contains(got.Hint, "quorum") {
		t.Errorf("the hint does not mention quorum: %q", got.Hint)
	}
}

// A classified error must not be re-wrapped, or the specific answer is buried
// under a generic one.
func TestClassifyReturnsAlreadyClassifiedErrorsUnchanged(t *testing.T) {
	original := view.Errorf("etcd.something.specific", "a precise message")
	got := classify(errors.New("wrapper: "+original.Error()), req(t, "etcd.overview", map[string]any{}))
	if got.Code == "etcd.something.specific" {
		t.Fatal("the fixture does not actually wrap — this test proves nothing")
	}
	got = classify(original, req(t, "etcd.overview", map[string]any{}))
	if got.Code != "etcd.something.specific" {
		t.Errorf("a classified error was re-wrapped as %q", got.Code)
	}
}
