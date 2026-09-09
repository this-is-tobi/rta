package app

import (
	"os"
	"path/filepath"
	"testing"
)

// **A readiness probe that waits for traffic is a deadlock.**
//
// rta creates its data directory lazily, on the first record write — which is
// the first call an agent makes. A readiness check that merely tested for a
// writable directory therefore answered "not ready" on a server that had just
// started and was working perfectly, forever: Kubernetes would not send it
// traffic, no agent would call it, no call would create the directory, and
// nothing would ever change. Found by starting a real server and polling, not
// by reading the code, which looked fine.
func TestReadinessDoesNotWaitForTheFirstAgentCall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created-yet")
	t.Setenv("RTA_DATA_DIR", dir)

	if err := recordWritable(); err != nil {
		t.Fatalf("a freshly started server reported not ready: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the data directory was not created: %v", err)
	}
	// The same mode the ledger creates it with. A directory this readiness
	// check created at 0755 would be one `rta doctor` then reports on.
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("data directory mode = %o, want 700", perm)
	}
}

// And the probe leaves nothing behind: a file per poll, forever, in the
// directory holding the record would be its own kind of failure.
func TestReadinessLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)

	for range 3 {
		if err := recordWritable(); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the readiness probe left files behind: %v", names)
	}
}

// The case the endpoint exists for: a volume that went read-only under a
// running server. It still accepts connections and still authenticates
// callers, while nothing an agent does can be recorded.
func TestReadinessFailsWhenTheRecordCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory bit")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	t.Setenv("RTA_DATA_DIR", dir)

	err := recordWritable()
	if err == nil {
		t.Fatal("a read-only data directory reported ready")
	}
	// The body of a 503 is read by a person looking at why the pod stopped
	// taking traffic, so it has to say which directory and what about it.
	if !contains(err.Error(), dir) || !contains(err.Error(), "recorded") {
		t.Errorf("the reason does not say what is wrong: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
