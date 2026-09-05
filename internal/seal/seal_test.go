package seal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
)

// Direct tests for the primitive itself, rather than only through
// internal/grant, internal/consent and internal/agentlog's own suites — a
// behavior none of those three happens to exercise (the ErrMissing/ErrShort
// split on the read path, before this file existed) went untested by all
// three at once, which is exactly the gap a package's own test file exists
// to close.

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func TestPathIsInsideTheDataDirectory(t *testing.T) {
	isolate(t)
	got := Path("grants.key")
	want := filepath.Join(paths.Data(), "grants.key")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestCreateWritesAThirtyTwoByteKeyAtModeSixHundred(t *testing.T) {
	isolate(t)
	key, err := Key("probe.key", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key is %d bytes, want 32", len(key))
	}
	info, err := os.Stat(Path("probe.key"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %s, want 0600", perm)
	}
}

func TestReadWithNoKeyReturnsErrMissing(t *testing.T) {
	isolate(t)
	if _, err := Key("probe.key", false); err != ErrMissing {
		t.Errorf("err = %v, want ErrMissing", err)
	}
}

func TestReadReturnsTheSameKeyCreateWrote(t *testing.T) {
	isolate(t)
	created, err := Key("probe.key", true)
	if err != nil {
		t.Fatal(err)
	}
	read, err := Key("probe.key", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != string(created) {
		t.Error("the read path returned different bytes than create just wrote")
	}
}

// The distinction the package doc comment states as its whole reason for
// having two errors: a missing key and a truncated one must not read the
// same to a caller, because a truncated key beside a sealed file is a
// corruption to investigate, not an absent key to regenerate.
func TestReadWithATruncatedKeyReturnsErrShortNotErrMissing(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("probe.key"), []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Key("probe.key", false)
	if err == nil {
		t.Fatal("a 9-byte file was accepted as a key")
	}
	if err == ErrMissing {
		t.Fatal("a truncated key was reported as ErrMissing — indistinguishable from no key at all")
	}
	if err != ErrShort {
		t.Errorf("err = %v, want ErrShort", err)
	}
}

// The create path's own refusal to paper over the same corruption: this one
// is already exercised indirectly by internal/grant/seal_test.go's
// TestASealKeyTooShortIsRefusedRatherThanReplaced, but the package that owns
// the behavior should assert it too rather than relying on a caller to.
func TestCreateWithATruncatedKeyRefusesRatherThanReplacingIt(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("probe.key"), []byte("too-short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Key("probe.key", true); err != ErrShort {
		t.Errorf("err = %v, want ErrShort — a short key must never be silently regenerated", err)
	}
	// And it is left exactly as found, not overwritten by the failed attempt.
	raw, err := os.ReadFile(Path("probe.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "too-short" {
		t.Errorf("the truncated key was rewritten: %q", raw)
	}
}

func TestMACIsDeterministicAndDependsOnBothKeyAndData(t *testing.T) {
	key1 := []byte("a-key-that-is-not-really-32-byte")
	key2 := []byte("a-different-key-32-bytes-or-more")
	a := MAC(key1, []byte("hello"))
	again := MAC(key1, []byte("hello"))
	if a != again {
		t.Fatal("MAC is not deterministic for the same key and data")
	}
	if b := MAC(key1, []byte("goodbye")); b == a {
		t.Error("different data produced the same MAC")
	}
	if c := MAC(key2, []byte("hello")); c == a {
		t.Error("different keys produced the same MAC")
	}
}

func TestEqualComparesMACsCorrectly(t *testing.T) {
	if !Equal("abc123", "abc123") {
		t.Error("identical MACs compared unequal")
	}
	if Equal("abc123", "abc124") {
		t.Error("different MACs compared equal")
	}
	if Equal("abc123", "abc12") {
		t.Error("MACs of different lengths compared equal")
	}
}
