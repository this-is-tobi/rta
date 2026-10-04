package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fs.hash opened a named pipe blocking: with nothing writing to it, open(2)
// never returned, no context reached it, and every call held an OS thread
// until the runtime aborted the process at ten thousand. Over MCP it is
// refused, and the deadline is the assertion — the failure was a call that
// never answers.
func TestHashRefusesAFIFOOverMCPAtOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runHash(context.Background(), plugin.NewRequest(
			map[string]any{"path": fifo, "algo": "sha256"}, false, false).WithSurface(plugin.SurfaceMCP))
		done <- err
	}()
	select {
	case err := <-done:
		verr, ok := err.(*view.Error)
		if !ok || verr.Code != "fs.hash.notafile" {
			t.Fatalf("err = %v, want fs.hash.notafile", err)
		}
	case <-time.After(5 * time.Second):
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("fs.hash on a FIFO with no writer did not return")
	}
}

// A tree or a scan of a named pipe is refused as one: "is a file" was said
// of anything that was not a directory.
func TestTreeAndUsageNameAPipeForWhatItIs(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for id, run := range map[string]plugin.Handler{"fs.tree": runTree, "fs.usage": runUsage} {
		_, err := run(context.Background(), plugin.NewRequest(map[string]any{"path": fifo}, false, false))
		verr, ok := err.(*view.Error)
		if !ok || verr.Code != id+".notadir" || !strings.Contains(verr.Message, "is a named pipe, not a directory") {
			t.Errorf("%s: %v, want it named a named pipe", id, err)
		}
	}
}
