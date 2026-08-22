package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	rtav1 "github.com/this-is-tobi/rule-them-all/proto/rta/v1"
)

// The point of the cache: Open answers what a plugin declares without
// launching it. Measured before it existed, 42ms per plugin per invocation on
// an 18MB binary — and shell completion runs rta on every tab press.
func TestACachedDeclarationCostsNoProcess(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	// First open pays for the launch and records the answer.
	warm := New(nil)
	first, err := warm.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if first.cmd == nil {
		t.Fatal("the first open did not launch anything, so nothing was cached")
	}
	warm.CloseAll()

	// Second, in a fresh host: same declaration, no process.
	cold := New(nil)
	t.Cleanup(cold.CloseAll)
	second, err := cold.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if second.cmd != nil {
		t.Error("a cached declaration still launched a process")
	}
	if second.Declared.Name != first.Declared.Name ||
		len(second.Declared.Capabilities) != len(first.Declared.Capabilities) {
		t.Errorf("the cached declaration differs: %+v vs %+v", second.Declared, first.Declared)
	}

	// And it is usable: the process starts on the call, not on the open.
	if _, err := greetWith(t, second, "cached"); err != nil {
		t.Fatalf("calling a plugin opened from cache: %v", err)
	}
	if second.cmd == nil {
		t.Error("the call did not start a process")
	}
}

// Keyed by content digest, so a changed binary cannot hit a stale entry.
// That is what removes the invalidation question entirely: there is no mtime
// to forge, no size to collide, and no rule to get subtly wrong.
//
// Asserted at the cache rather than by running two binaries. The obvious
// fixture — append a byte to the plugin and exec it — does not work on macOS
// and fails for the wrong reason: modifying a Mach-O invalidates its
// signature, so the test would pass on "the kernel refused it" while proving
// nothing about the cache.
func TestTheCacheIsKeyedByContent(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	const a = "1111111111111111111111111111111111111111111111111111111111111111"
	const b = "2222222222222222222222222222222222222222222222222222222222222222"

	writeCache(a, &rtav1.Plugin{Name: "hello", Version: "1"})
	got, ok := readCache(a)
	if !ok || got.GetVersion() != "1" {
		t.Fatalf("entry for its own digest: %v %v", got, ok)
	}
	if _, ok := readCache(b); ok {
		t.Error("a different digest hit an existing entry")
	}

	// Two digests are two entries; recording one never overwrites the other.
	writeCache(b, &rtav1.Plugin{Name: "hello", Version: "2"})
	first, _ := readCache(a)
	second, _ := readCache(b)
	if first.GetVersion() != "1" || second.GetVersion() != "2" {
		t.Errorf("entries collided: %q and %q", first.GetVersion(), second.GetVersion())
	}
}

// A damaged entry must cost a process launch and nothing else. Treating it as
// an error would let one corrupt file break a plugin that is perfectly fine.
func TestADamagedCacheEntryIsAMissNotAFailure(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	id, err := Identify(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(paths.Data(), cacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, junk := range [][]byte{
		{}, // empty: proto unmarshals this cleanly
		[]byte("not a protobuf at all"),
		{0xff, 0xff, 0xff},
	} {
		if err := os.WriteFile(cachePath(id.Digest), junk, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := readCache(id.Digest); ok {
			t.Errorf("%q was accepted as a declaration", junk)
		}
		h := New(nil)
		c, err := h.Open(context.Background(), hello(t))
		if err != nil {
			h.CloseAll()
			t.Fatalf("a damaged cache entry broke the plugin: %v", err)
		}
		if c.Declared.Name != "hello" {
			t.Errorf("declared name = %q", c.Declared.Name)
		}
		h.CloseAll()
	}
}

// The declaration that comes back from the cache is the one that went in,
// including the flags that do not survive into plugin.Capability — a plugin
// whose Suggest silently stopped working after the first run would be a
// miserable thing to debug.
func TestTheCacheRoundTripsHandlerPresence(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	warm := New(nil)
	live, err := warm.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	warm.CloseAll()

	cold := New(nil)
	t.Cleanup(cold.CloseAll)
	cached, err := cold.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}

	suggests := func(c *Client) int {
		n := 0
		for _, cap := range c.Declared.Capabilities {
			for _, f := range cap.Inputs {
				if f.Suggest != nil {
					n++
				}
			}
		}
		return n
	}
	if got, want := suggests(cached), suggests(live); got != want || want == 0 {
		t.Errorf("cached client has %d Suggest handlers, live had %d", got, want)
	}
	// And it actually reaches the process.
	for _, cap := range cached.Declared.Capabilities {
		for _, f := range cap.Inputs {
			if f.Suggest == nil {
				continue
			}
			if got := f.Suggest(context.Background(), plugin.NewRequest(nil, false, false)); len(got) == 0 {
				t.Error("a Suggest restored from cache returned nothing")
			}
			return
		}
	}
}

func TestPruneKeepsTheDirectoryBounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < cacheEntries+20; i++ {
		name := filepath.Join(dir, string(rune('a'+i%26))+string(rune('a'+i/26))+".pb")
		if err := os.WriteFile(name, []byte{1}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneCache(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > cacheEntries {
		t.Errorf("%d entries left, cap is %d", len(entries), cacheEntries)
	}
}

func TestWriteCacheSurvivesAnUnwritableDirectory(t *testing.T) {
	// A path that exists as a *file*, so MkdirAll under it cannot succeed.
	base := t.TempDir()
	blocked := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", blocked)
	// Must not panic and must not fail: a cache that cannot be written is a
	// slower rta, not a broken one.
	writeCache("deadbeef", &rtav1.Plugin{Name: "x"})
}

// A forged entry is not honoured.
//
// This is the attack the seal exists for, in the form it was reproduced. The
// attacker never touches the plugin binary — its digest is what rta hashes on
// every Open and is the anchor for every authorisation (ADR 0015) — and never
// reads the data directory, because the proto shape is public. One file write
// re-authors what rta believes, and the declaration carries the Safety class
// that decides whether an agent needs --allow-destructive and a human-issued
// grant, plus the Summary and Description that reach a model verbatim.
//
// Read to Destructive is asserted here because the example plugin has no
// destructive capability to downgrade. It is the same write in the harmless
// direction: what the test proves is that the field is attacker-controlled.
func TestAForgedCacheEntryIsRefused(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	warm := New(nil)
	honest, err := warm.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatal(err)
	}
	warm.CloseAll()
	if honest.Declared.Capabilities[0].Safety != plugin.Read {
		t.Fatalf("fixture changed: greet is %q, the test needs Read", honest.Declared.Capabilities[0].Safety)
	}

	id, err := Identify(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	forged, ok := readCache(id.Digest)
	if !ok {
		t.Fatal("the honest run recorded nothing to forge")
	}
	forged.Capabilities[0].Safety = rtav1.Safety_SAFETY_DESTRUCTIVE
	forged.Capabilities[0].Summary = "attacker controlled"
	body, err := proto.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	// Written the way an attacker can write it: the real bytes, with a MAC
	// they cannot compute. Anything in that 32-byte slot is a guess.
	entry := append(bytes.Repeat([]byte{0}, sha256.Size), body...)
	if err := os.WriteFile(cachePath(id.Digest), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readCache(id.Digest); ok {
		t.Error("an entry with a forged seal was accepted")
	}

	// And end to end: rta serves what the plugin says, not what the file says.
	cold := New(nil)
	t.Cleanup(cold.CloseAll)
	got, err := cold.Open(context.Background(), hello(t))
	if err != nil {
		t.Fatalf("a forged entry broke the plugin instead of being ignored: %v", err)
	}
	c := got.Declared.Capabilities[0]
	if c.Safety != plugin.Read {
		t.Errorf("safety = %q: the forged declaration was served", c.Safety)
	}
	if c.Summary == "attacker controlled" {
		t.Error("the forged summary was served, and it reaches models verbatim")
	}
}

// An authentic entry moved to another digest's filename is refused.
//
// The subtler half, and the reason the MAC covers the digest rather than the
// declaration alone. Every byte here is genuine and the signature verifies —
// it is simply an answer to a different question. Without the digest in the
// MAC, an attacker with two installed plugins can serve either one's
// capabilities under the other's name, using nothing rta did not write.
func TestASealedEntryDoesNotTransferBetweenDigests(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	const other = "1111111111111111111111111111111111111111111111111111111111111111"

	id, err := Identify(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	h := New(nil)
	if _, err := h.Open(context.Background(), hello(t)); err != nil {
		t.Fatal(err)
	}
	h.CloseAll()

	authentic, err := os.ReadFile(cachePath(id.Digest))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath(other), authentic, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readCache(other); ok {
		t.Error("a sealed entry was honoured under a digest it was not sealed for")
	}
	// The original still works, so this is a binding check and not a
	// verification that fails on everything.
	if _, ok := readCache(id.Digest); !ok {
		t.Error("the authentic entry stopped verifying under its own digest")
	}
}

// The key is not in the directory pruneCache empties.
func TestTheCacheKeySurvivesPruning(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if sealKey(true) == nil {
		t.Fatal("no key was created")
	}
	dir := filepath.Join(paths.Data(), cacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cacheEntries+20; i++ {
		writeCache(fmt.Sprintf("%064x", i), &rtav1.Plugin{Name: "x"})
	}
	if sealKey(false) == nil {
		t.Error("pruning the cache destroyed the key that authenticates it")
	}
}

// Concurrent first runs must agree on a key, or each invalidates the other's
// entries for the life of the machine.
func TestTheCacheKeyIsCreatedOnce(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	keys := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func(i int) { defer wg.Done(); keys[i] = sealKey(true) }(i)
	}
	wg.Wait()
	for i, k := range keys {
		if k == nil {
			t.Fatalf("goroutine %d got no key", i)
		}
		if !bytes.Equal(k, keys[0]) {
			t.Errorf("goroutine %d got a different key", i)
		}
	}
}
