package agentlog

import (
	"os"
	"path/filepath"
	"testing"
)

// Writable is asked before a call that spends authority, so it has to say yes
// to a record that takes a write and leave nothing behind but a record: no
// scratch file, and no entry — the chain is the calls', not the checks'.
func TestWritableSaysYesAndLeavesNothingBehind(t *testing.T) {
	dir := isolate(t)
	if Started() {
		t.Fatal("a directory with no record reports one")
	}
	if err := Writable(); err != nil {
		t.Fatalf("a fresh data directory is not writable: %v", err)
	}
	write(t, Entry{Cap: "sys.cpu", Tool: "sys_cpu", Outcome: Ran, Auth: Open})
	if err := Writable(); err != nil {
		t.Fatalf("a record that takes writes is not writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, probeFile)); err == nil {
		t.Error("the scratch file was left in the data directory")
	}
	rep, err := Verify()
	if err != nil || rep.Broken != 0 || rep.Entries != 1 {
		t.Fatalf("asking changed the record: %+v, %v", rep, err)
	}
	if !Started() {
		t.Error("a directory with a record reports none")
	}
}

// And no to each way a record stops taking writes, with the reason: where the
// file should be there is something else, the key is gone from beside a record
// that is not empty, or there is no room for the scratch row.
func TestWritableSaysWhyNot(t *testing.T) {
	t.Run("the record is not a file", func(t *testing.T) {
		isolate(t)
		if err := os.Mkdir(Path(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Writable(); err == nil {
			t.Fatal("a directory where the record goes was called writable")
		}
	})
	t.Run("the key is gone", func(t *testing.T) {
		isolate(t)
		write(t, Entry{Cap: "sys.cpu", Tool: "sys_cpu", Outcome: Ran, Auth: Open})
		if err := os.Remove(filepath.Join(filepath.Dir(Path()), keyFile)); err != nil {
			t.Fatal(err)
		}
		if err := Writable(); err == nil {
			t.Fatal("a record with no key beside it was called writable")
		}
	})
	t.Run("the scratch row cannot be written", func(t *testing.T) {
		isolate(t)
		if err := os.Mkdir(filepath.Join(filepath.Dir(Path()), probeFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := Writable(); err == nil {
			t.Fatal("a data directory that takes no new file was called writable")
		}
	})
}
