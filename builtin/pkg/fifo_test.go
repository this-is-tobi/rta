package pkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A named pipe in GOBIN is no tool, and go opens what it is handed blocking:
// `go version -m` on one waited until the list timed out, and the whole go
// row failed with it. Only a regular file, or a link to one, is asked about.
func TestAGoBinEntryThatIsNoFileIsNotHandedToGo(t *testing.T) {
	gopath := t.TempDir()
	f := &fake{bins: map[string]bool{"go": true}, answers: map[string]fakeAnswer{
		"go env GOBIN":  {out: "\n"},
		"go env GOPATH": {out: gopath + "\n"},
	}}
	install(t, f)
	bin := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(bin, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := collect(context.Background(), newRegistryClient(), "go")
	if len(l.failed) != 0 {
		t.Fatalf("failed: %v", l.failed)
	}
	for _, ran := range f.ran {
		if strings.Contains(ran, "pipe") {
			t.Errorf("go was handed the pipe: %s", ran)
		}
	}
}
