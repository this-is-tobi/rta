package cert

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/x509check"
	"github.com/this-is-tobi/rule-them-all/internal/pathguard"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// blackhole is a listener that accepts a TCP connection and then says
// nothing at all — the shape of a host behind a firewall that drops packets
// rather than refusing them. A dial with no deadline waits it out forever,
// which is the case every test below depends on.
func blackhole(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})
	return ln.Addr().String()
}

// startTLS returns a local TLS server's host:port and its leaf cert as PEM.
func startTLS(t *testing.T) (addr string, pemPath string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	addr = strings.TrimPrefix(srv.URL, "https://")

	leaf := srv.TLS.Certificates[0]
	pemPath = filepath.Join(t.TempDir(), "leaf.pem")
	f, err := os.Create(pemPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}); err != nil {
		t.Fatal(err)
	}
	return addr, pemPath
}

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// cert.expiry dials whatever hosts the caller lists with nothing standing
// between an MCP caller and the network — see its declaration comment in
// Plugin() for why it differs from its Path-typed siblings.
func TestExpiryNeedsAGrantScopedToTargets(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID != "cert.expiry" {
			continue
		}
		if !c.NeedsGrant {
			t.Error("cert.expiry.NeedsGrant = false, want true")
		}
		if c.Scope != "targets" {
			t.Errorf("cert.expiry.Scope = %q, want %q", c.Scope, "targets")
		}
		return
	}
	t.Fatal("cert.expiry not registered")
}

func TestInspectLiveHost(t *testing.T) {
	addr, _ := startTLS(t)
	v, err := runInspect(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	keys := map[string]string{}
	for _, p := range kv.Pairs {
		keys[p.Key] = p.Value
	}
	for _, want := range []string{"subject", "issuer", "not-after", "expires-in", "sha256", "chain"} {
		if keys[want] == "" {
			t.Errorf("missing key %q in %v", want, keys)
		}
	}
	// httptest uses a self-signed cert: chain must be reported invalid.
	if !strings.HasPrefix(keys["chain"], "INVALID") {
		t.Errorf("self-signed chain reported as %q", keys["chain"])
	}
}

func TestInspectPEMFile(t *testing.T) {
	_, pemPath := startTLS(t)
	v, err := runInspect(context.Background(), req(map[string]any{"target": pemPath}))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.(view.KeyValue).Pairs) == 0 {
		t.Error("empty inspect result from PEM")
	}
}

func TestChain(t *testing.T) {
	addr, _ := startTLS(t)
	v, err := runChain(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatal(err)
	}
	tree := v.(view.Tree)
	if len(tree.Roots) != 1 {
		t.Fatalf("chain roots = %d", len(tree.Roots))
	}
	if !strings.Contains(tree.Roots[0].Detail, "expires") {
		t.Errorf("node detail = %q", tree.Roots[0].Detail)
	}
}

func TestExpiryTable(t *testing.T) {
	addr, _ := startTLS(t)
	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets":   []string{addr, "closed.invalid:1"},
		"warn-days": 30,
	}))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tbl.Rows))
	}
	// Reachable host resolves to a status; unreachable one reports the error inline.
	if tbl.Rows[0][3] == "" {
		t.Error("empty status for live host")
	}
	if !strings.HasPrefix(tbl.Rows[1][3], "ERROR") {
		t.Errorf("unreachable host status = %q", tbl.Rows[1][3])
	}
}

func TestTLSReport(t *testing.T) {
	addr, _ := startTLS(t)
	v, err := runTLS(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	var version string
	for _, p := range kv.Pairs {
		if p.Key == "version" {
			version = p.Value
		}
	}
	if !strings.HasPrefix(version, "TLS") {
		t.Errorf("version = %q", version)
	}
}

func TestTLSRejectsFileTarget(t *testing.T) {
	_, pemPath := startTLS(t)
	_, err := runTLS(context.Background(), req(map[string]any{"target": pemPath}))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "cert.tls.filetarget" {
		t.Errorf("want cert.tls.filetarget, got %v", err)
	}
}

// cert.inspect/chain/pem/tls carry no NeedsGrant, unlike cert.expiry and
// net.probe/net.port, because today they do not need it: targetField's
// pathguard.Check step (see Plugin()'s comment on targetField) rewrites a
// bare host:port into "<cwd>/host:port" before it ever reaches dialCerts,
// and that string can never split back into a dialable address. This pins
// the mechanism itself rather than leaving it assumed — if pathguard's
// handling of a non-path value ever changes so a live host becomes reachable
// through this field again, this test fails, and these four need the same
// NeedsGrant + Scope: "target" treatment cert.expiry got instead of a
// silently reopened gap.
func TestLiveHostTargetIsUndialableOnceRoutedThroughThePathGate(t *testing.T) {
	addr, _ := startTLS(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	guard, err := pathguard.New(wd)
	if err != nil {
		t.Fatal(err)
	}
	resolved, verr := guard.Check("target", addr)
	if verr != nil {
		return // refused outright is even safer than corrupted
	}
	if _, err := runInspect(context.Background(), req(map[string]any{
		"target": resolved, "timeout": 2,
	})); err == nil {
		t.Fatal("a host:port value rewritten by the path gate still reached a live host — " +
			"cert.inspect/chain/pem/tls need NeedsGrant now")
	}
}

func TestDialFailureIsCodedWithHint(t *testing.T) {
	_, err := runInspect(context.Background(), req(map[string]any{"target": "closed.invalid:1"}))
	ve := view.AsError(err, "x")
	if ve.Code != "cert.dial.failed" || ve.Hint == "" {
		t.Errorf("want coded error with hint, got %+v", ve)
	}
}

// cert was the only network-touching family in the catalogue with no timeout
// input, so every capability that dials has to declare one — and declare the
// same one, since a per-capability guess is how `cert inspect` and `cert tls`
// end up disagreeing about how long a host gets to answer.
func TestEveryCapabilityDeclaresTheSameTimeout(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		var f *plugin.Field
		for i := range c.Inputs {
			if c.Inputs[i].Name == "timeout" {
				f = &c.Inputs[i]
			}
		}
		if f == nil {
			t.Errorf("%s dials but declares no timeout", c.ID)
			continue
		}
		if f.Type != plugin.Int || f.Default != defaultTimeoutSeconds || f.Min != 1 || f.Max != 120 {
			t.Errorf("%s timeout = %+v, want an Int bounded 1..120 defaulting to %d",
				c.ID, *f, defaultTimeoutSeconds)
		}
	}
}

// loadCerts dialed with the caller's bare context, which on the CLI carries
// no deadline and over MCP is unbounded from the client. A host that
// completes the TCP connect and then never speaks TLS therefore hung the
// command with nothing to cancel it but Ctrl-C.
func TestDialIsBoundedByTheDeclaredTimeout(t *testing.T) {
	addr := blackhole(t)
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := runInspect(context.Background(), req(map[string]any{"target": addr, "timeout": 1}))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a host that never completes the handshake returned a certificate")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("dial returned after %s, want about 1s", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runInspect never returned — the dial is not bounded by --timeout")
	}
}

// Two guarantees at once. The walk used to be sequential, so one filtered
// host cost a whole timeout before the next was dialed and `cert expiry`
// across forty hosts took forty timeouts back to back. And the fix must not
// reorder the answer: the row position is the only thing tying a status back
// to the target the caller listed.
func TestExpiryChecksTargetsConcurrentlyAndKeepsInputOrder(t *testing.T) {
	live, _ := startTLS(t)
	targets := []string{live, blackhole(t), blackhole(t), blackhole(t), blackhole(t)}

	start := time.Now()
	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets": targets, "warn-days": 30, "timeout": 1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Four silent targets at a one-second timeout each: sequential is at
	// least four seconds, concurrent is a little over one.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("runExpiry took %s for four one-second timeouts — targets are still walked one at a time", elapsed)
	}

	tbl := v.(view.Table)
	if len(tbl.Rows) != len(targets) {
		t.Fatalf("rows = %d, want %d", len(tbl.Rows), len(targets))
	}
	for i, want := range targets {
		if tbl.Rows[i][0] != want {
			t.Errorf("row %d target = %q, want %q — results are not in input order", i, tbl.Rows[i][0], want)
		}
	}
	if strings.HasPrefix(tbl.Rows[0][3], "ERROR") {
		t.Errorf("live host reported %q", tbl.Rows[0][3])
	}
	for i := 1; i < len(targets); i++ {
		if !strings.HasPrefix(tbl.Rows[i][3], "ERROR") {
			t.Errorf("row %d status = %q, want an inline ERROR", i, tbl.Rows[i][3])
		}
	}
}

// The warn window was written down twice — 30 here, a private 15 inside the
// audit's cert-expiry check — so the same certificate 20 days from expiry
// came back "ok" from `rta audit web` and "WARN <30d" from `rta cert expiry`.
func TestExpiryWarnDaysDefaultsToTheSharedWindow(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID != "cert.expiry" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "warn-days" {
				if f.Default != x509check.DefaultWarnDays {
					t.Errorf("warn-days default = %v, want the shared %d", f.Default, x509check.DefaultWarnDays)
				}
				return
			}
		}
		t.Fatal("cert.expiry declares no warn-days")
	}
	t.Fatal("cert.expiry not registered")
}

// The shared verdict is "" for a chain that validates, which is right for a
// grader deciding pass/fail and wrong for a row a person reads: a blank
// value next to "chain" is a check that did not run, not one that passed.
func TestChainVerdictNamesThePassingCase(t *testing.T) {
	if got := chainVerdict(""); got != "valid" {
		t.Errorf(`chainVerdict("") = %q, want "valid"`, got)
	}
	if got := chainVerdict("INVALID: unknown authority"); got != "INVALID: unknown authority" {
		t.Errorf("chainVerdict rewrote a reason to %q", got)
	}
}
