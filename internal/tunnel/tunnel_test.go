package tunnel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// fakeKubectl installs a script that behaves like `kubectl port-forward` in
// whichever way a test needs.
//
// A stub rather than a cluster, because what is being tested is the
// resolver's lifecycle — does it wait for the listener, does it classify the
// failure, does it reap the process — and none of that is about Kubernetes.
// The parts that are about Kubernetes are the ones only a real cluster can
// answer, and they are listed at the bottom of this file.
func fakeKubectl(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := kubectl
	kubectl = path
	t.Cleanup(func() { kubectl = saved })
}

const homelab = "homelab/databases/svc/postgres:5432"

// The happy path: a real listener, and an endpoint that can actually be
// dialled. Asserting on the parsed port alone would pass against a resolver
// that returned a number nothing was listening on.
func TestOpenReturnsAnAddressThatAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	fakeKubectl(t, fmt.Sprintf(
		"echo 'Forwarding from 127.0.0.1:%d -> 5432'\nwhile true; do sleep 1; done\n", port))

	tun, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr != nil {
		t.Fatalf("open: %v", verr)
	}
	defer tun.Close()

	if tun.Host != "127.0.0.1" || tun.Port != port {
		t.Fatalf("endpoint = %s:%d, want 127.0.0.1:%d", tun.Host, tun.Port, port)
	}
	conn, err := net.DialTimeout("tcp",
		fmt.Sprintf("%s:%d", tun.Host, tun.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("the endpoint rta handed back does not answer: %v", err)
	}
	_ = conn.Close()
}

// A port-forward that outlives its call is a hole in a cluster's network
// boundary with nobody watching. Close must actually end the process, not
// only stop reading from it.
func TestCloseEndsTheForward(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "alive")
	fakeKubectl(t, fmt.Sprintf(
		"echo 'Forwarding from 127.0.0.1:1 -> 5432'\ntouch %s\ntrap 'rm -f %s; exit 0' TERM\nwhile true; do sleep 0.05; done\n",
		marker, marker))

	tun, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr != nil {
		t.Fatalf("open: %v", verr)
	}
	// Waited for rather than assumed. If the forward has not written its
	// marker yet — which a loaded machine makes ordinary — the check below
	// finds no marker and reports success without a forward ever having run,
	// so the test would pass hardest exactly when it should fail.
	if !awaitMarker(marker, true) {
		t.Fatal("the forward never started, so this proves nothing about Close")
	}
	tun.Close()

	// The trap removes the marker on SIGTERM; if it is still there, the
	// forward survived the call.
	//
	// Patient on purpose. A shell trap runs between commands, so the signal
	// waits out whatever `sleep` is in flight, and under load that is not
	// the 0.05s the script asks for. The bug this guards against is a forward
	// that never dies at all, and no amount of waiting turns that green.
	if !awaitMarker(marker, false) {
		t.Error("the forward was still running after Close: a tunnel outlived its call")
	}
}

// awaitMarker waits for the marker to exist, or to be gone.
func awaitMarker(path string, want bool) bool {
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := os.Stat(path)
		if (err == nil) == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	fakeKubectl(t, "echo 'Forwarding from 127.0.0.1:1 -> 5432'\nwhile true; do sleep 1; done\n")
	tun, verr := Open(context.Background(), "x", Target{Kube: homelab})
	if verr != nil {
		t.Fatal(verr)
	}
	tun.Close()
	tun.Close() // must not panic or block
	(*Tunnel)(nil).Close()
}

// kubectl's failures are stable and specific, which is most of why shelling
// out is tolerable: the message an operator sees is one they already know how
// to read. Each still needs a code and a next step.
func TestEveryKubectlFailureIsClassified(t *testing.T) {
	cases := []struct {
		name, stderr, code string
	}{
		{"unknown context", `error: context "nope" does not exist`, "tunnel.context.unknown"},
		{"no such service", `Error from server (NotFound): services "pg" not found`, "tunnel.service.missing"},
		{"rbac", `Error from server (Forbidden): pods is forbidden`, "tunnel.denied"},
		// **401 and 403 were one answer, and only one of them was about
		// permissions.** On any cluster behind an exec credential plugin —
		// Teleport, aws eks get-token, gke-gcloud-auth-plugin, an OIDC
		// refresh — an expired login is the ordinary daily failure, and it
		// was reported as "not allowed to port-forward there, the verb is
		// create on pods/portforward", which is an afternoon in the wrong
		// place.
		{"expired login", `error: You must be logged in to the server (Unauthorized)`,
			"tunnel.unauthenticated"},
		{"401 from the api server", `Error from server (Unauthorized): unknown`,
			"tunnel.unauthenticated"},
		// The helper failing before a request exists at all: whatever it
		// says, it cannot be a permissions answer, because nothing asked.
		{"credential plugin failed",
			`Unable to connect to the server: getting credentials: exec: executable tsh failed`,
			"tunnel.unauthenticated"},
		{"port taken", `Unable to listen on port: address already in use`, "tunnel.port.taken"},
		{"silent death", ``, "tunnel.open.failed"},
		{"anything else", `error: something new`, "tunnel.open.failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeKubectl(t, fmt.Sprintf("echo %q >&2\nexit 1\n", tc.stderr))
			_, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
			if verr == nil {
				t.Fatal("a failing kubectl produced no error")
			}
			if verr.Code != tc.code {
				t.Errorf("code = %q, want %q (stderr %q)", verr.Code, tc.code, tc.stderr)
			}
			if verr.Hint == "" {
				t.Error("no hint: this is the error somebody is stuck on")
			}
		})
	}
}

// **The hint names the command, because the generic one is useless here.**
//
// "Log in again" leaves somebody to work out which of ten contexts, which
// tool, and which cluster flag. `kubectl config view` is a local read of a
// file that contacts nothing and answers all three, and this is a path where
// something has already stopped, so it can afford one question.
func TestAnExpiredLoginIsToldWhichCommandRenewsIt(t *testing.T) {
	// A kubectl that fails the forward and, asked about the context, reports
	// the exec plugin a Teleport-issued kubeconfig carries — the shape read
	// off a real one: `tsh kube credentials --kube-cluster=… --teleport-cluster=…`.
	fakeKubectl(t, `case "$1" in
  config) echo 'tsh [kube credentials --kube-cluster=dso --teleport-cluster=rke.example.com]' ;;
  *) echo 'error: You must be logged in to the server (Unauthorized)' >&2; exit 1 ;;
esac
`)
	_, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr == nil {
		t.Fatal("a failing kubectl produced no error")
	}
	if verr.Code != "tunnel.unauthenticated" {
		t.Fatalf("code = %q", verr.Code)
	}
	if !strings.Contains(verr.Hint, "tsh kube login dso") {
		t.Errorf("hint = %q, want the exact command that renews this context", verr.Hint)
	}
}

// And falls back to the general sentence when it cannot tell, because a wrong
// command in a hint is worse than no command.
func TestAnUnknownCredentialHelperIsNotGuessedAt(t *testing.T) {
	fakeKubectl(t, `case "$1" in
  config) echo '' ;;
  *) echo 'error: You must be logged in to the server (Unauthorized)' >&2; exit 1 ;;
esac
`)
	_, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr == nil {
		t.Fatal("no error")
	}
	if strings.Contains(verr.Hint, "tsh") {
		t.Errorf("hint invented a tool: %q", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "homelab") {
		t.Errorf("hint = %q, want it to name the context that failed", verr.Hint)
	}
}

// A caller may select a target, never describe one, so a malformed
// coordinate is the operator's typo and the message is for them.
func TestMalformedTargetsAreRefusedWithTheForm(t *testing.T) {
	for _, spec := range []string{
		"homelab/databases/svc/postgres",  // no port
		"homelab/databases/postgres:5432", // three segments
		"homelab/databases/svc/postgres:0",
		"homelab/databases/svc/postgres:70000",
		"homelab//svc/postgres:5432",
		"/databases/svc/postgres:5432",
	} {
		_, verr := Open(context.Background(), "t", Target{Kube: spec})
		if verr == nil {
			t.Errorf("%q was accepted", spec)
			continue
		}
		if verr.Code != "tunnel.target.malformed" {
			t.Errorf("%q: code = %q", spec, verr.Code)
		}
		if !strings.Contains(verr.Hint, "context/namespace/kind/name:port") {
			t.Errorf("%q: the hint does not show the form: %s", spec, verr.Hint)
		}
	}
}

// The dependency argument is only honest if the absence of
// kubectl is a legible message rather than "exec: not found".
func TestAMissingKubectlSaysWhatToDo(t *testing.T) {
	saved := kubectl
	kubectl = "kubectl-that-is-not-installed-anywhere"
	t.Cleanup(func() { kubectl = saved })

	_, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr == nil || verr.Code != "tunnel.kubectl.missing" {
		t.Fatalf("verr = %v, want tunnel.kubectl.missing", verr)
	}
	if !strings.Contains(verr.Hint, "client-go") {
		t.Errorf("the hint does not explain why rta needs kubectl at all: %s", verr.Hint)
	}
}

// A kubectl that starts and never forwards must not hang the call forever.
func TestAForwardThatNeverComesUpTimesOut(t *testing.T) {
	fakeKubectl(t, "while true; do sleep 1; done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, verr := Open(ctx, "homelab-pg", Target{Kube: homelab})
	if verr == nil || verr.Code != "tunnel.open.timeout" {
		t.Fatalf("verr = %v, want tunnel.open.timeout", verr)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v to give up", time.Since(start))
	}
}

// A regression test for a real bug review found: Open had
// no fallback ceiling of its own, so a caller passing a context with no
// deadline — context.Background(), which every call site in this package's
// own tests used until this one — combined with a kubectl that neither
// prints the listener line nor exits (stuck behind an interactive exec
// credential plugin, for instance) hung forever. openCeiling is overridden
// to a small value here, the same way kubectl itself is swapped for a
// fixture, so proving this does not itself take openCeiling's real minute.
func TestOpenNeverHangsForeverWithNoCallerDeadline(t *testing.T) {
	fakeKubectl(t, "while true; do sleep 1; done\n")
	saved := openCeiling
	openCeiling = 200 * time.Millisecond
	t.Cleanup(func() { openCeiling = saved })

	start := time.Now()
	_, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr == nil || verr.Code != "tunnel.open.timeout" {
		t.Fatalf("verr = %v, want tunnel.open.timeout", verr)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v to give up despite no caller deadline", time.Since(start))
	}
}

// A failed open must not stall. awaitForwarding and Close both wait for
// kubectl to exit, and the first version signalled that with a send on a
// buffered channel — which awaitForwarding consumed, so Close then waited its
// full two-second timeout on a channel nothing would ever write to again.
// Every failing target cost two seconds, and every test still passed.
func TestAFailedOpenDoesNotWaitForASignalItAlreadyConsumed(t *testing.T) {
	// awaitForwarding and Close both wait for kubectl to exit. The first
	// version signalled that with a send on a buffered channel, which
	// awaitForwarding consumed — so Close then waited its full two-second
	// timeout on a channel nothing would write to again. Every failing target
	// cost two seconds and every test still passed.
	//
	// Asserted on the mechanism rather than on elapsed time: the first
	// version of this test bounded the wall clock, passed alone, and failed
	// inside the full -race -shuffle suite, where it was measuring the
	// machine rather than the code.
	fakeKubectl(t, "echo 'error: context \"nope\" does not exist' >&2\nexit 1\n")

	tun, verr := openForTest(t, "homelab-pg", Target{Kube: homelab})
	if verr == nil {
		t.Fatal("expected a failure")
	}
	if tun.TimedOut() {
		t.Error("a wait fell through to its timeout: something is waiting for a signal " +
			"another waiter already consumed")
	}
}

// Measured, because one tunnel per call is the rule and whether that is
// tolerable is a number rather than an opinion. Against a stub this is the
// resolver's own overhead with the cluster round-trip removed — the floor,
// not the real cost.
//
// Reported, not asserted. This test used to fail if an open took more than
// 250 ms, and it fired for real during a `-race -shuffle` run of the whole
// suite: 282 ms, on a machine that was busy running everything else. Five
// process spawns on a loaded box is a measurement of the box. The number that
// decides here is the one against a real cluster — 54 ms median, attributed
// against kubectl's own startup — and it lives in live_test.go where it can
// be taken on a machine doing nothing else. What is asserted here is what is
// deterministic: five opens in a row all work, and none of them falls through
// to a timeout.
func TestSetupOverheadIsReported(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	fakeKubectl(t, fmt.Sprintf(
		"echo 'Forwarding from 127.0.0.1:%d -> 5432'\nwhile true; do sleep 1; done\n", port))

	// Split, because open and close are paid at different moments and a
	// single figure cannot say which one is expensive. The first version
	// measured them together, reported ~1s, and that number was mostly the
	// stub: a `while true; do sleep 1; done` shell does not die until its
	// current sleep returns, so it was timing SIGTERM latency in a fake.
	const n = 5
	var opening, closing time.Duration
	for range n {
		t0 := time.Now()
		tun, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab})
		if verr != nil {
			t.Fatal(verr)
		}
		opening += time.Since(t0)
		t1 := time.Now()
		tun.Close()
		closing += time.Since(t1)
		if tun.TimedOut() {
			t.Fatal("a wait fell through to its timeout")
		}
	}
	t.Logf("stubbed: open %v, close %v (close includes the stub's own SIGTERM latency, "+
		"not rta's; open excludes the cluster round-trip)", opening/n, closing/n)
}

// --- What a stub cannot answer -------------------------------------------
//
// Recorded here rather than claimed as covered, because five
// questions and a fake kubectl answers three of them:
//
//   - ANSWERED: how the local port is chosen (parsed off stdout, and the
//     endpoint is dialled in TestOpenReturnsAnAddressThatAnswers rather than
//     merely parsed);
//   - ANSWERED: what a dying forward looks like (TestEveryKubectlFailureIs-
//     Classified, from kubectl's real message shapes);
//   - ANSWERED: teardown (TestCloseEndsTheForward, via a SIGTERM trap).
//
//   - OPEN: real setup cost against a cluster. The stub measures rta's
//     overhead with the round-trip removed, which is a floor. If the true
//     figure is around a second, one-tunnel-per-call is wrong
//     for the TUI's five-second refresh and caching reopens.
//   - OPEN: whether "Forwarding from" means the tunnel carries traffic yet.
//     If a connection can be refused after that line, the resolver needs a
//     dial-check loop and the cost above goes up.
//
// Both need a cluster. `kind create cluster` is enough for the second.

// openForTest calls Open and hands back the Tunnel even on failure, so a test
// can inspect how the failure was reached. Open itself returns nil on error,
// because a caller has nothing to do with a dead tunnel.
func openForTest(t *testing.T, name string, tgt Target) (*Tunnel, *view.Error) {
	t.Helper()
	tun, verr := openInstrumented(context.Background(), name, tgt)
	return tun, verr
}
