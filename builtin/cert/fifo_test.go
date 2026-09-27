//go:build !windows

package cert

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// cert read its target with os.ReadFile, which opens a named pipe blocking:
// with nothing writing to it the call never answered, and held an OS thread
// while it did not. Over MCP it is refused before anything opens it.
func TestInspectRefusesAFIFOOverMCPAtOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runInspect(context.Background(), req(map[string]any{"target": fifo}).WithSurface(plugin.SurfaceMCP))
		done <- err
	}()
	select {
	case err := <-done:
		verr, ok := err.(*view.Error)
		if !ok || verr.Code != "cert.file.notafile" {
			t.Fatalf("err = %v, want cert.file.notafile", err)
		}
	case <-time.After(5 * time.Second):
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("cert.inspect on a FIFO with no writer did not return")
	}
}
