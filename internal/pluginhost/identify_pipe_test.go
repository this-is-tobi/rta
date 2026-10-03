//go:build unix

package pluginhost

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Identify is the first thing every start of rta does with a plugin it finds,
// and what it finds is a name in a $PATH directory or the store with the
// execute bit — a bit a named pipe can be given as easily as a file. open(2)
// on a pipe waits for a writer, so one planted in a directory something other
// than rta can write to held the discovery of every plugin, and with it every
// command that loads the catalogue.
func TestANamedPipeWithTheExecuteBitDoesNotHoldIdentify(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), BinaryName("rta-plugin-fifo"))
	if err := syscall.Mkfifo(pipe, 0o755); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Identify(pipe)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a named pipe was identified as a plugin")
		}
	case <-time.After(5 * time.Second):
		// Let the blocked open go, so the goroutine does not outlive the test.
		if w, err := os.OpenFile(pipe, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("Identify waited on a named pipe for a writer")
	}
}
