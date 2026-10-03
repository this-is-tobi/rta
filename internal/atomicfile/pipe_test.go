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

// The other ways a file rta wrote is opened refuse a pipe as ReadCapped does:
// the record's segments are read a line at a time, the stores whole, and the
// record is appended to, which waits for a reader where a read waits for a
// writer.
func TestEveryOpenOfAStateFileRefusesANamedPipe(t *testing.T) {
	for name, open := range map[string]func(string) error{
		"Open":       func(p string) error { f, err := Open(p); closeIf(f); return err },
		"ReadFile":   func(p string) error { _, err := ReadFile(p); return err },
		"OpenAppend": func(p string) error { f, err := OpenAppend(p, 0o600); closeIf(f); return err },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skipf("no named pipes here: %v", err)
			}
			done := make(chan error, 1)
			go func() { done <- open(path) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a named pipe was opened as a file")
				}
			case <-time.After(3 * time.Second):
				// Let the blocked open go, so the goroutine does not outlive the test.
				for _, flag := range []int{os.O_WRONLY, os.O_RDONLY} {
					if f, err := os.OpenFile(path, flag|syscall.O_NONBLOCK, 0); err == nil {
						_ = f.Close()
					}
				}
				t.Fatalf("%s waited on a named pipe", name)
			}
		})
	}
}

func closeIf(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
