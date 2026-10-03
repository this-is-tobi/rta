//go:build unix

package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A file under the data directory is one something other than rta can put a
// name on, and a named pipe put there is an open(2) that waits for a writer
// which never comes: no context reaches into the syscall, and the file may be
// the lock list or the grants, read before every call an agent makes. Refused
// at once instead, and not retried the way a refusal that clears is.
func TestANamedPipeWhereAStateFileGoesIsRefusedNotWaitedOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lockdown.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}

	type result struct {
		data []byte
		err  error
	}
	read := make(chan result, 1)
	go func() {
		data, err := ReadCapped(path, 1<<10)
		read <- result{data, err}
	}()

	select {
	case r := <-read:
		if r.err == nil {
			t.Fatalf("a named pipe read as a file: %q", r.data)
		}
	case <-time.After(3 * time.Second):
		// Let the blocked open go, so the goroutine does not outlive the test.
		if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("ReadCapped waited on a named pipe for a writer")
	}
}
