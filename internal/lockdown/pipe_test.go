//go:build unix

package lockdown

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A machine with no locks has no lock file, so a name nothing replaces is one
// something that can only add files can take: a named pipe there held every
// call, the free reads among them, waiting for a writer.
func TestANamedPipeWhereTheLockFileGoesHoldsNobodyUpAndEveryoneBack(t *testing.T) {
	fresh(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(Path(), 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	done := make(chan *Lock, 1)
	go func() {
		l, _ := NewPin().Check("claude", "")
		done <- l
	}()
	select {
	case l := <-done:
		if !held(l) {
			t.Errorf("a named pipe at the lock file let somebody through: %+v", l)
		}
	case <-time.After(3 * time.Second):
		if w, err := os.OpenFile(Path(), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("the check waited on a named pipe for a writer")
	}
}
