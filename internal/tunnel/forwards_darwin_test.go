//go:build darwin

package tunnel

import (
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// sleeper is a stand-in for a forward: a process in a group of its own, which
// the test kills on the way out whatever happened to it.
func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	harden(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// withRecord rewrites the record remember made for cmd, for the cases a test
// has to arrange: an owner that is gone, a pid that is not the one recorded.
func withRecord(t *testing.T, cmd *exec.Cmd, change func(*forward)) {
	t.Helper()
	path := recordPath(cmd.Process.Pid)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("remember wrote nothing: %v", err)
	}
	var f forward
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	change(&f)
	raw, _ = json.Marshal(f)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// deadPID is the pid of a process that has run and been waited for.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func stopped(cmd *exec.Cmd) bool {
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

func TestAForwardWhoseOwnerIsGoneIsStoppedAndForgotten(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	fwd := sleeper(t)
	remember(fwd)
	withRecord(t, fwd, func(f *forward) { f.Owner, f.OwnerStarted = deadPID(t), 1 })

	if n := ReapOrphans(); n != 1 {
		t.Fatalf("stopped %d forwards, want 1", n)
	}
	if !stopped(fwd) {
		t.Fatal("the orphaned forward is still running")
	}
	if _, err := os.Stat(recordPath(fwd.Process.Pid)); err == nil {
		t.Error("the record of a forward that was stopped is still there")
	}
}

// Another server on the machine is alive, and so are its forwards: only the
// ones whose owner is gone are orphans.
func TestAForwardWhoseOwnerIsAliveIsLeftAlone(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	fwd := sleeper(t)
	remember(fwd)
	if n := ReapOrphans(); n != 0 {
		t.Fatalf("stopped %d forwards whose owner is running", n)
	}
	if err := syscall.Kill(fwd.Process.Pid, 0); err != nil {
		t.Fatalf("a live server's forward was stopped: %v", err)
	}
	if _, err := os.Stat(recordPath(fwd.Process.Pid)); err != nil {
		t.Errorf("a live server's record was removed: %v", err)
	}
}

// A pid the kernel has given to something else since is never signalled: the
// start time the kernel reports for it is not the one that was written down.
func TestARecycledPidIsNeverSignalled(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	stranger := sleeper(t)
	remember(stranger)
	withRecord(t, stranger, func(f *forward) {
		f.Owner, f.OwnerStarted = deadPID(t), 1
		f.Started++
	})
	if n := ReapOrphans(); n != 0 {
		t.Fatalf("stopped %d processes that were not the forward recorded", n)
	}
	if err := syscall.Kill(stranger.Process.Pid, 0); err != nil {
		t.Fatalf("a process that only shares a recorded pid was stopped: %v", err)
	}
	if _, err := os.Stat(recordPath(stranger.Process.Pid)); err == nil {
		t.Error("the record of a pid that was recycled was kept")
	}
}

func TestARecordThatNamesNothingTrueIsDiscarded(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(forwardsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"1.json":    `{"pid":1,"started":1,"comm":"launchd","owner":0}`,
		"12.json":   `{"pid":13,"started":1,"owner":0}`,
		"junk.json": `not json`,
	} {
		if err := os.WriteFile(forwardsDir()+"/"+name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n := ReapOrphans(); n != 0 {
		t.Fatalf("stopped %d processes on the word of a record that names none", n)
	}
	if entries, _ := os.ReadDir(forwardsDir()); len(entries) != 0 {
		t.Errorf("records that name nothing true were kept: %v", entries)
	}
}
