//go:build !windows

package pathin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A named pipe nothing writes to held open(2) for good, where no context
// reaches, and each call pinned an OS thread. Off the CLI it is refused, and
// promptly: the deadline is the assertion, because the failure is a call
// that never returns rather than one that returns wrong.
func TestAFIFOIsRefusedAtOnceOffTheCLI(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI, plugin.SurfaceCompletion} {
		done := make(chan error, 1)
		go func() {
			_, err := Read(sf, fifo, 64)
			done <- err
		}()
		select {
		case err := <-done:
			var notAFile *NotAFileError
			if !errors.As(err, &notAFile) || !strings.Contains(err.Error(), "a named pipe") {
				t.Errorf("%s: err = %v, want a NotAFileError saying it is a named pipe", sf, err)
			}
		case <-time.After(5 * time.Second):
			// Unblock the open this test leaked, so the process can exit.
			if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
			t.Fatalf("%s: reading a FIFO with no writer did not return", sf)
		}
	}
}

// The CLI reads a pipe, as its guide says every Path input does: a person
// naming one at a terminal started its writer.
func TestTheCLIReadsAPipeItsWriterFeeds(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = w.WriteString("piped")
		_ = w.Close()
	}()
	got, err := Read(plugin.SurfaceCLI, fifo, 64)
	if err != nil || string(got) != "piped" {
		t.Errorf("got %q, %v", got, err)
	}
}
