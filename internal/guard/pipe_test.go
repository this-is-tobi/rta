package guard

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The guard's state is not there on a machine that never enabled it, so the
// name is one a process that can only add files can take: a named pipe at it
// held `rta mcp serve` before it served anything, since the pin it takes at
// startup reads the state, and Enabled had already said the guard was on.
func TestANamedPipeWhereTheGuardStateGoesDoesNotHoldTheServerUp(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(Path(), 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}

	taken := make(chan Pin, 1)
	go func() { taken <- TakePin() }()
	select {
	case p := <-taken:
		if verr := p.Check(); verr != nil {
			t.Fatalf("a pin taken over the pipe refused at once: %v", verr)
		}
	case <-time.After(3 * time.Second):
		if w, err := os.OpenFile(Path(), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("TakePin waited on a named pipe for a writer")
	}
}
