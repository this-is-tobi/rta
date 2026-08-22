package atomicfile

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The property the whole package exists for: a reader either sees the file
// as it was or as it becomes, never a prefix of the new contents.
//
// Checked by racing readers against writers under -race and asserting every
// read landed on a complete version. os.WriteFile — what config.Write used
// to do — truncates first, so this fails against it: a reader that lands in
// the gap gets zero bytes, and zero bytes is a config file rta reports as
// invalid on every subsequent run.
func TestAReaderNeverSeesAPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	old := []byte(strings.Repeat("a", 64*1024))
	fresh := []byte(strings.Repeat("b", 64*1024))
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := Write(path, fresh, 0o600); err != nil {
				t.Error(err)
				break
			}
		}
		close(stop)
	}()

	bad := 0
	for {
		select {
		case <-stop:
			wg.Wait()
			if bad > 0 {
				t.Fatalf("%d reads saw neither the old file nor the new one", bad)
			}
			return
		default:
		}
		got, err := os.ReadFile(path)
		if err != nil {
			continue // the path always exists, but a rename can race an open
		}
		if !bytes.Equal(got, old) && !bytes.Equal(got, fresh) {
			bad++
		}
	}
}

// A failed write must cost nothing. The previous contents are the user's
// data; leaving a truncated file behind because the new one could not be
// produced turns a transient error into permanent loss.
func TestAFailedWriteLeavesTheOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nothing can be created in a directory that is not writable, so the
	// temporary file never appears and the rename never happens.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := Write(path, []byte("replacement"), 0o600); err == nil {
		t.Fatal("Write into an unwritable directory reported success")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("original contents = %q, want %q — a failed write destroyed them", got, "original")
	}
}

// Nothing is left in the directory afterwards, on either path. A temp file
// per keystroke that never got cleaned up would fill the config directory
// with .config.yaml-*.tmp, and the grant directory with something worse:
// files holding what an agent was allowed to do, at whatever mode they were
// created with.
func TestNoTemporaryFileSurvives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := Write(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A doomed write, for the failure path.
	_ = Write(filepath.Join(dir, "nested", "deep.yaml"), []byte("x"), 0o600)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.yaml" {
			t.Errorf("leftover in the target directory: %s", e.Name())
		}
	}
}

// The mode is enforced on every write, not just on creation. The same path
// deliberately, so each iteration is a rewrite: os.WriteFile's mode argument
// applies only when the file is created, so under it a store that started
// life 0644 stays 0644 no matter how many times rta writes 0600 to it.
//
// Not covered here, and honestly: the chmod happening before the rename
// rather than after. There is no observable a test on a working machine can
// reach, because CreateTemp already sets the mode the callers that care ask
// for — what the ordering buys is that a mode is never *widened* on a path
// something else can already open by name.
func TestTheModeIsExactAndArrivesWithTheFile(t *testing.T) {
	dir := t.TempDir()
	for _, perm := range []fs.FileMode{0o600, 0o644, 0o640} {
		path := filepath.Join(dir, "m.yaml")
		if err := Write(path, []byte("x"), perm); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != perm {
			t.Errorf("mode = %o, want %o", got, perm)
		}
	}
}

// A rewrite replaces the contents rather than appending to or overlaying
// them, which a same-name reuse of an existing longer file could otherwise
// hide.
func TestAShorterWriteReplacesTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.yaml")
	if err := Write(path, []byte("a much longer original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "short" {
		t.Fatalf("contents = %q, want %q", got, "short")
	}
}

// Publish never overwrites. The whole reason a key file uses it instead of
// Write: a second key silently replacing the first invalidates every seal
// made with the first, so the second writer has to be told it lost.
func TestPublishNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seal.key")
	first, err := Publish(path, []byte("first-writer"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != "first-writer" {
		t.Fatalf("creator got %q back, want its own bytes", first)
	}
	second, err := Publish(path, []byte("second-writer"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != "first-writer" {
		t.Errorf("loser got %q, want the winner's bytes", second)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != "first-writer" {
		t.Errorf("file holds %q — the second writer overwrote the first", onDisk)
	}
}

// A symlink sitting at the target path — planted deliberately, left over
// from a botched restore, or created by some unrelated dotfile-sync tool —
// is not a prior publication, even though Link's EEXIST looks identical to
// the real thing. The old fallback read straight through it with
// os.ReadFile and handed back whatever it resolved to as though it were
// this path's own validated contents; the actual callers (the grant seal
// key, the plugin cache seal key) only check the length of what comes back,
// so a foreign file long enough to look like a key would have been adopted
// outright. Found by review, not observed in the field.
func TestPublishRefusesASymlinkAtTheTargetPath(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "foreign.key")
	if err := os.WriteFile(foreign, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "seal.key")
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}

	got, err := Publish(path, []byte(strings.Repeat("y", 32)), 0o600)
	if err == nil {
		t.Fatalf("Publish through a symlink returned %q with no error — a foreign file was accepted as a prior publication", got)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want it to name the symlink as the reason", err)
	}
}

// The same guard has to fire for a dangling symlink too — Lstat sees it
// without following it, so this takes the identical refusal path rather
// than falling into os.ReadFile's ENOENT, which the pre-fix code treated as
// ordinary lock contention and retried until it gave up with a misleading
// "gave up after repeatedly losing and re-losing the race", even though
// nothing real ever occupied the path.
func TestPublishRefusesADanglingSymlinkAtTheTargetPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seal.key")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), path); err != nil {
		t.Fatal(err)
	}

	_, err := Publish(path, []byte(strings.Repeat("y", 32)), 0o600)
	if err == nil {
		t.Fatal("Publish through a dangling symlink returned no error")
	}
	if strings.Contains(err.Error(), "gave up after repeatedly losing") {
		t.Errorf("error = %q — fell through to the retry-exhaustion path instead of refusing the symlink directly", err)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want it to name the symlink as the reason", err)
	}
}

// Racing publishers agree on one answer. Everybody generating a key at once
// must end up using the same one, or two processes disagree about what
// authenticates a file they both write.
func TestRacingPublishersAllReturnTheWinnersBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seal.key")
	const n = 8
	got := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Distinct payloads, so "they all agree" cannot be true by
			// accident the way it would be with identical ones.
			b, err := Publish(path, []byte(strings.Repeat(string(rune('a'+i)), 32)), 0o600)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = b
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !bytes.Equal(got[0], got[i]) {
			t.Fatalf("publisher 0 got %q and publisher %d got %q — they disagree about the key", got[0], i, got[i])
		}
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, got[0]) {
		t.Errorf("file holds %q but every publisher returned %q", onDisk, got[0])
	}
}

// A reader racing a publisher sees the path as absent or as complete, never
// as a prefix. This is the failure that made rta accuse itself of forgery:
// a seal key caught mid-write reads short, and short is indistinguishable
// from "this file was not written by rta", which is what the operator gets
// told — along with a hint to delete every grant they hold.
//
// One path, removed and republished, because that is the shape the real case
// has: a key file that went missing is recreated under the name every reader
// is already watching.
func TestAPublishedFileIsNeverObservablyPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seal.key")
	body := []byte(strings.Repeat("k", 64*1024))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 300 {
			_ = os.Remove(path)
			if _, err := Publish(path, body, 0o600); err != nil {
				t.Error(err)
				break
			}
		}
		close(stop)
	}()

	short := 0
	for {
		select {
		case <-stop:
			wg.Wait()
			if short > 0 {
				t.Fatalf("%d reads saw a partially written file", short)
			}
			return
		default:
		}
		if raw, err := os.ReadFile(path); err == nil && len(raw) != len(body) {
			short++
		}
	}
}

// A published file that its owner then removes is a lock, and Publish has to
// survive it.
//
// The first version returned an error whenever Link said the path existed and
// the read that followed said it did not — which is what every contended lock
// acquire looks like, because the winner releases. Every acquire under
// contention became "acquiring the grant file lock failed" instead of "go
// round again", and the grant suite's concurrency test caught it immediately.
func TestPublishSurvivesTheWinnerLettingGo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	token := []byte("holder")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// A holder that keeps taking the path and giving it back.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if held, err := Publish(path, token, 0o600); err == nil && bytes.Equal(held, token) {
				_ = os.Remove(path)
			}
		}
	}()

	for range 2000 {
		if _, err := Publish(path, []byte("contender"), 0o600); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("a contended publish failed rather than retrying: %v", err)
		}
		_ = os.Remove(path)
	}
	close(stop)
	wg.Wait()
}
