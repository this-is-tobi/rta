package recent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Every rta command reads the shortlists to build its completions, and the
// file is not there until a command has been run at a terminal, so a name
// something that can add files to the data directory can take: a named pipe
// at it held every command, `rta mcp serve` among them, waiting for a writer.
// Every failure answers empty (Load), and this is one.
func TestANamedPipeWhereTheShortlistsGoAnswersEmpty(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(Path(), 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}

	loaded := make(chan Values, 1)
	go func() { loaded <- Load() }()
	select {
	case v := <-loaded:
		if len(v) != 0 {
			t.Errorf("a named pipe held shortlists: %v", v)
		}
	case <-time.After(3 * time.Second):
		if w, err := os.OpenFile(Path(), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("Load waited on a named pipe for a writer")
	}
}
