package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// helloOnce builds examples/plugin-hello once for the whole package.
//
// A real binary, launched through the real spawn path, is the only test worth
// writing here. Everything this package does that could break — the sandbox
// wrapper, the environment allowlist, mTLS negotiation, the handshake, the
// descriptor handling — happens between fork and the first RPC, and a fake
// that skips the fork skips all of it. It also makes the test the same thing
// the M2 acceptance gate measures: build a plugin, put it somewhere, use it.
var (
	helloOnce sync.Once
	helloPath string
	helloErr  error
)

func hello(t *testing.T) string {
	t.Helper()
	helloOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rta-plugin-hello-*")
		if err != nil {
			helloErr = err
			return
		}
		helloPath = filepath.Join(dir, "rta-plugin-hello")
		cmd := exec.Command("go", "build", "-o", helloPath, "../../examples/plugin-hello")
		if out, err := cmd.CombinedOutput(); err != nil {
			helloErr = err
			t.Logf("building the example plugin: %s", out)
		}
	})
	if helloErr != nil {
		t.Fatalf("building the example plugin: %v", helloErr)
	}
	return helloPath
}

func open(t *testing.T) (*Host, *Client) {
	t.Helper()
	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatalf("opening the plugin: %v", err)
	}
	return h, c
}

// The whole bet in one test: what comes back from a subprocess is a
// plugin.Plugin, identical in type to what builtin/fs returns, so no surface
// needs a notion of "remote".
func TestAPluginProcessLooksLikeABuiltIn(t *testing.T) {
	_, c := open(t)

	if c.Declared.Name != "hello" {
		t.Errorf("name = %q, want hello", c.Declared.Name)
	}
	if len(c.Declared.Capabilities) != 2 {
		t.Fatalf("capabilities = %d, want 2", len(c.Declared.Capabilities))
	}
	if len(c.Unknown) != 0 {
		t.Errorf("host could not interpret parts of the declaration: %v", c.Unknown)
	}
	// The declaration passed the same Validate a built-in passes. If it had
	// not, describe would have refused it.
	if err := c.Declared.Validate(); err != nil {
		t.Errorf("the assembled declaration does not validate: %v", err)
	}

	var greet plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			greet = cap
		}
	}
	if greet.Run == nil {
		t.Fatal("hello.greet came back with no Run handler")
	}
	v, err := greet.Run(context.Background(),
		plugin.NewRequest(map[string]any{"name": "world", "lang": "fr"}, false, false))
	if err != nil {
		t.Fatalf("running hello.greet: %v", err)
	}
	text, ok := v.(view.Text)
	if !ok {
		t.Fatalf("hello.greet returned %T, want view.Text", v)
	}
	if text.Body != "Bonjour, world!" {
		t.Errorf("body = %q", text.Body)
	}
}

// A capability's own failure is not a transport failure, and the distinction
// has to survive the process boundary: the code and the hint are what every
// surface renders, and a gRPC status carries neither.
func TestAPluginErrorCrossesWithItsCodeAndHint(t *testing.T) {
	_, c := open(t)
	var greet plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			greet = cap
		}
	}
	_, err := greet.Run(context.Background(),
		plugin.NewRequest(map[string]any{"name": "x", "lang": "zz"}, false, false))
	if err == nil {
		t.Fatal("an unknown language was accepted")
	}
	ve, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("error is %T, want *view.Error", err)
	}
	if ve.Code != "hello.unknownlang" {
		t.Errorf("code = %q", ve.Code)
	}
	if !strings.Contains(ve.Hint, "en") {
		t.Errorf("hint did not survive: %q", ve.Hint)
	}
}

// Suggest is fetched on demand rather than shipped with the declaration, so
// the values have to come back over a second RPC.
func TestSuggestReachesThePluginProcess(t *testing.T) {
	_, c := open(t)
	var greet plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			greet = cap
		}
	}
	for _, f := range greet.Inputs {
		if f.Name != "lang" {
			continue
		}
		if f.Suggest == nil {
			t.Fatal("lang declared has_suggest but no handler was attached")
		}
		got := f.Suggest(context.Background(), plugin.NewRequest(nil, false, false))
		if len(got) != 4 {
			t.Fatalf("suggest returned %v, want the four languages", got)
		}
		// The tab-separated description survives, because shell completion
		// renders it and the other surfaces strip it.
		if !strings.Contains(got[0], "\t") {
			t.Errorf("the description was lost: %q", got[0])
		}
		return
	}
	t.Fatal("no lang input")
}

// The process cache: one binary, one spec, one process, however many callers.
// The dashboard runs a tile per plugin on a refresh timer, so a spawn per call
// would be a process launch every few seconds for every plugin installed.
func TestTheSameBinaryIsNotLaunchedTwice(t *testing.T) {
	h := New(nil)
	t.Cleanup(h.CloseAll)
	first, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("a second Open produced a second client for the same binary")
	}
	// Force the process into existence — Open does not launch one when the
	// declaration is cached — and check both views of it agree.
	if _, err := greetWith(t, first, "one"); err != nil {
		t.Fatal(err)
	}
	pid := first.cmd.Process.Pid
	if _, err := greetWith(t, second, "two"); err != nil {
		t.Fatal(err)
	}
	if second.cmd.Process.Pid != pid {
		t.Errorf("pids differ: %d and %d", pid, second.cmd.Process.Pid)
	}
}

// greet runs the example plugin's greet capability through a client, which is
// also the shortest way to force a lazily-started process to exist.
func greetWith(t *testing.T, c *Client, name string) (view.View, error) {
	t.Helper()
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			return cap.Run(context.Background(),
				plugin.NewRequest(map[string]any{"name": name, "lang": "en"}, false, false))
		}
	}
	t.Fatal("no hello.greet capability")
	return nil, nil
}

// A killed plugin must not leave the host handing out a dead client forever.
func TestADeadProcessIsReplacedOnTheNextOpen(t *testing.T) {
	h := New(nil)
	t.Cleanup(h.CloseAll)
	first, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := greetWith(t, first, "x"); err != nil {
		t.Fatal(err)
	}
	firstPid := first.cmd.Process.Pid
	first.Close()
	// Kill is asynchronous in go-plugin; wait for it to be observable rather
	// than sleeping a guessed interval.
	deadline := time.Now().Add(5 * time.Second)
	for !first.client.Exited() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	second, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatalf("reopening after a kill: %v", err)
	}
	if _, err := greetWith(t, second, "y"); err != nil {
		t.Fatalf("calling after a kill: %v", err)
	}
	if second.cmd.Process.Pid == firstPid {
		t.Error("the dead process was handed back")
	}
}

// The identity is the artifact, not the name. A digest that changed with the
// path — or did not change when the contents did — would make any
// digest-keyed authorisation meaningless.
func TestIdentityIsContentAddressed(t *testing.T) {
	path := hello(t)
	a, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest != b.Digest {
		t.Error("hashing the same file twice gave two answers")
	}
	if len(a.Digest) != 64 {
		t.Errorf("digest is %d chars, want a hex sha256", len(a.Digest))
	}

	// Same bytes at another path: same identity.
	copied := filepath.Join(t.TempDir(), "renamed")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, data, 0o755); err != nil {
		t.Fatal(err)
	}
	moved, err := Identify(copied)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Digest != a.Digest {
		t.Error("the same bytes at a different path hashed differently")
	}
	if moved.Path == a.Path {
		t.Error("the resolved path was not recorded, so a swap would be invisible")
	}

	// One byte different: different identity.
	changed := filepath.Join(t.TempDir(), "changed")
	if err := os.WriteFile(changed, append(data, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}
	other, err := Identify(changed)
	if err != nil {
		t.Fatal(err)
	}
	if other.Digest == a.Digest {
		t.Error("a changed binary kept its identity")
	}
}

// Anything executable on $PATH whose name matches the plugin prefix gets
// launched, and not all of it is a plugin. Two shapes have to stay bounded,
// because both are paid on every rta invocation:
//
//   - a binary that prints something that is not a handshake
//   - a binary that prints nothing at all
//
// The second is the one that cost five minutes. go-plugin's StartTimeout
// defaults to one minute, and its Kill then blocks on clientWaitGroup.Wait(),
// which waits on the goroutines reading the child's stdout and stderr — pipes
// a grandchild holds open long after runner.Kill has signalled the direct
// child. A plugin whose entire body was `sleep 300` hung rta for the whole
// three hundred seconds with the reap that would free it one line further on.
func TestABinaryThatIsNotAPluginFailsQuickly(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"prints nonsense", "#!/bin/sh\necho not a plugin\n"},
		{"prints nothing and hangs", "#!/bin/sh\nsleep 300\n"},
		{"exits immediately", "#!/bin/sh\nexit 1\n"},
		{"forks and exits, holding the pipes", "#!/bin/sh\nsleep 300 &\nexit 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "rta-plugin-notreally")
			if err := os.WriteFile(p, []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}

			h := New(nil)
			start := time.Now()
			_, err := h.Open(context.Background(), p)
			opened := time.Since(start)
			if err == nil {
				h.CloseAll()
				t.Fatal("a script was accepted as a plugin")
			}
			// Generous against the 5s handshake bound, and far under the
			// minute go-plugin would otherwise spend.
			if opened > 20*time.Second {
				t.Errorf("Open took %s; every rta invocation pays this", opened)
			}

			start = time.Now()
			h.CloseAll()
			if closing := time.Since(start); closing > 20*time.Second {
				t.Errorf("CloseAll took %s, so ctrl-c could not escape either", closing)
			}
		})
	}
}

// argv is one of the three launch levers this package exposes, so it has to
// be in the process cache key. A lever that changes the process but not its
// key hands a caller a process somebody else configured.
func TestArgumentsAreProcessIdentity(t *testing.T) {
	id, err := Identify(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	deny := DenySet{NoAccess: []string{"/x"}}
	base := cacheKey(id, deny, nil)
	if base != cacheKey(id, deny, nil) {
		t.Fatal("the same launch hashed twice gave two answers")
	}
	for _, args := range [][]string{
		{"--verbose"},
		{"--verbose", "--extra"},
		{"--verbose=1"},
		{""},
	} {
		if cacheKey(id, deny, args) == base {
			t.Errorf("argv %v shares a cache key with no arguments", args)
		}
	}
	// And length-prefixed, so shuffling a separator between arguments cannot
	// collide two different argument lists.
	if cacheKey(id, deny, []string{"a", "b"}) == cacheKey(id, deny, []string{"a\nb"}) {
		t.Error("two different argument lists collided")
	}
}

// A crashed plugin used to leave every capability the registry had indexed
// pointing at a dead stub forever, while the error promised it would be
// restarted on the next call. On the CLI that barely shows; in the TUI it is
// a tile that stays dead until rta is restarted.
func TestACrashedPluginIsRestartedForTheNextCall(t *testing.T) {
	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	var greet plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			greet = cap
		}
	}
	req := plugin.NewRequest(map[string]any{"name": "again", "lang": "en"}, false, false)
	if _, err := greet.Run(context.Background(), req); err != nil {
		t.Fatalf("the first call failed: %v", err)
	}
	firstPid := c.cmd.Process.Pid

	// Kill the process out from under the registered handler, the way a
	// crashing plugin would.
	reap(c.cmd)
	deadline := time.Now().Add(5 * time.Second)
	for !c.client.Exited() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !c.client.Exited() {
		t.Fatal("could not kill the plugin, so this test proves nothing")
	}

	// The very same capability value the registry holds must work again.
	v, err := greet.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("the call after a crash failed: %v", err)
	}
	if text, _ := v.(view.Text); text.Body != "Hello, again!" {
		t.Errorf("body = %q", text.Body)
	}
	if c.cmd.Process.Pid == firstPid {
		t.Error("the pid did not change, so nothing was actually restarted")
	}
}

// A binary replaced on disk is a different artifact, and the declaration
// every surface already published came from the old one. Serving the new one
// silently would leave the catalogue and the process disagreeing about what
// exists — the exact condition describe validates against at startup.
func TestAChangedBinaryIsNotSilentlyRestarted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rta-plugin-hello")
	data, err := os.ReadFile(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}

	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var greet plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			greet = cap
		}
	}
	// Force the lazily-started process into existence before killing it.
	if _, err := greetWith(t, c, "x"); err != nil {
		t.Fatal(err)
	}

	reap(c.cmd)
	deadline := time.Now().Add(5 * time.Second)
	for !c.client.Exited() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Swap the binary for different bytes before the next call.
	if err := os.WriteFile(path, append(data, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = greet.Run(context.Background(),
		plugin.NewRequest(map[string]any{"name": "x"}, false, false))
	if err == nil {
		t.Fatal("a swapped binary was silently restarted and served")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("error does not explain what happened: %v", err)
	}
}

// Shutting down while a call is restarting the process must be safe.
//
// live() replaces c.client, c.stub, c.cmd and c.logger in place — that is
// what lets a restarted plugin keep serving handlers registered against the
// dead one — and Close read three of those without holding the lock that
// guards them. Host.cached and Host.Open read c.client under h.mu, which is a
// different lock, and a different lock is no lock.
//
// The race detector never caught it, here or in two targeted probes, which is
// worth saying plainly: -race reports races it observes, and an unsynchronised
// read that happens not to interleave during a test run is still undefined
// behaviour. The fix is structural — every read of those fields now happens
// under c.mu — and this test exercises the interleaving rather than claiming
// to prove its absence.
func TestShutdownDuringRestartIsSafe(t *testing.T) {
	if testing.Short() {
		t.Skip("launches a binary")
	}
	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.live(context.Background()); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); <-start; _, _ = c.live(context.Background()) }()
		go func() { defer wg.Done(); <-start; c.Close() }()
		go func() { defer wg.Done(); <-start; _ = c.usable() }()
	}
	close(start)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("close and restart deadlocked against each other")
	}
}

// Restarting a plugin used to leak the process it replaced.
//
// live() swaps four fields in place — that is what lets a restarted plugin
// keep serving handlers registered against the dead one — and it dropped the
// old *goplugin.Client without killing it, so nothing ever closed that
// client's gRPC ClientConn. The connection outlives the process it was
// dialled to, and gRPC keeps its callback serializers, its HTTP/2 transport
// and yamux's session goroutine alive around it.
//
// Measured before the fix: four goroutines per restart, none of them ever
// collected. On the CLI that is invisible because the process exits. In `rta
// mcp serve`, which is long-lived and is the whole reason live() exists, a
// crash-looping plugin accumulates them for as long as the server runs.
//
// Counting goroutines rather than asserting on internals because the leak is
// only observable as goroutines: rta passes Cmd rather than RunnerFunc, so
// go-plugin never allocates the socket directory Kill would have removed, and
// the process itself is already dead by the time live() runs. The thing that
// survives is the connection.
func TestRestartingAPluginDoesNotLeakTheOneItReplaced(t *testing.T) {
	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	var greet plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "hello.greet" {
			greet = cap
		}
	}
	req := plugin.NewRequest(map[string]any{"name": "again", "lang": "en"}, false, false)
	call := func() error {
		_, err := greet.Run(context.Background(), req)
		return err
	}
	if err := call(); err != nil {
		t.Fatalf("the first call failed: %v", err)
	}

	// gRPC tears down asynchronously, so a count taken immediately measures
	// scheduling rather than leakage.
	settle := func() {
		for range 5 {
			runtime.GC()
			time.Sleep(120 * time.Millisecond)
		}
	}
	settle()
	before := runtime.NumGoroutine()

	const restarts = 8
	for range restarts {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
		// The failed call is not retried by design, so the next one is what
		// gets the live process.
		for call() != nil {
			time.Sleep(20 * time.Millisecond)
		}
	}
	settle()

	// A generous bound: the leak was four per restart, so eight restarts left
	// thirty-two behind, and the fix leaves none. Anything under one per
	// restart is scheduling noise rather than a connection nobody closed.
	if grew := runtime.NumGoroutine() - before; grew > restarts {
		t.Errorf("%d goroutines survived %d restarts (%.1f per restart) — "+
			"the replaced client's connection is not being closed", grew, restarts, float64(grew)/restarts)
	}
}

// The managed store's shape end to end: the binary lives inside rta's own
// data dir — which the sandbox denies file-read* on — and is launched through
// the bin/ symlink. This exact spawn failed with EPERM before Identify
// resolved symlinks, because SBPL blocks reading a link in a denied subtree
// even though executing its target is a separate, allowed operation. Found by
// running an installed plugin; this is that run, as a test.
func TestAManagedStoreSymlinkSpawns(t *testing.T) {
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	store := filepath.Join(data, "plugins", "store", "hello", "d1")
	bin := filepath.Join(data, "plugins", "bin")
	for _, d := range []string{store, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(store, "rta-plugin-hello")
	if err := os.WriteFile(real, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "rta-plugin-hello")
	if err := os.Symlink(filepath.Join("..", "store", "hello", "d1", "rta-plugin-hello"), link); err != nil {
		t.Fatal(err)
	}

	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), link)
	if err != nil {
		t.Fatalf("opening through the managed bin link: %v", err)
	}
	if c.Declared.Name != "hello" {
		t.Fatalf("declared %q", c.Declared.Name)
	}
}
