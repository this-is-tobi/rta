//go:build unix

package agentlog

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Append is called after every call an agent makes, and it reads the files of
// the record to chain onto them. A segment's name is the next number up, a
// name nothing is at until the record rolls, so something that can add files
// to the data directory can put a named pipe at one — and open(2) on a pipe
// waits for a writer. Every call, the free reads among them, and every
// command that reads the record then waited with it.
func TestANamedPipeAmongTheRecordsFilesDoesNotHoldEveryCall(t *testing.T) {
	dir := isolate(t)
	write(t, Entry{Cap: "sys.cpu", Tool: "sys_cpu", Outcome: Ran, Auth: Open})
	pipe := filepath.Join(dir, filepath.Base(segmentPath(7)))
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Append(Entry{Cap: "sys.cpu", Tool: "sys_cpu", Outcome: Ran, Auth: Open})
		_, _ = Verify()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Let the blocked open go, so the goroutine does not outlive the test.
		if w, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("the record waited on a named pipe among its files for a writer")
	}
}
