//go:build !windows

package plugindist

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A forced exit taken while an install fetches leaves no staged download
// behind. The fetch and the verification launch are not held off an exit, so
// one can land in either, and the deferred removal of the staging directory is
// a defer os.Exit skips: the partial artifact stayed in a dot-directory under
// plugins/ that nothing lists and nothing removed. A named pipe stands in for
// the artifact, so the fetch is under way for as long as the test says.
func TestAnExitDuringAnInstallsFetchRemovesWhatItStaged(t *testing.T) {
	testData(t)
	fifo := filepath.Join(t.TempDir(), "artifact")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	attach(t, fmt.Sprintf(`name: hello
version: 0.1.0
summary: the example plugin
platforms:
  - os: %s
    arch: %s
    url: %s
    sha256: %s
capabilities:
  - id: hello.greet
    summary: greet someone
    safety: read
`, runtimeGOOS(), runtimeGOARCH(), fileURL(fifo), strings.Repeat("ab", 32)))

	installed := make(chan *view.Error, 1)
	go func() {
		_, verr := Install(context.Background(), "hello", io.Discard)
		installed <- verr
	}()
	// Opened once the install opens it to read, which it does after staging.
	feed, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if _, err := feed.Write([]byte("the first bytes of an artifact")); err != nil {
		t.Fatal(err)
	}
	if staged := stagingDirs(t); len(staged) != 1 {
		t.Fatalf("staging directories during the fetch: %v, want one", staged)
	}

	settled := make(chan func(), 1)
	go func() { settled <- shutdown.Settle() }()
	var resume func()
	select {
	case resume = <-settled:
	case <-time.After(time.Second):
		_ = feed.Close()
		(<-settled)()
		t.Fatal("the process waited for a fetch still under way")
	}
	shutdown.Exiting()
	staged := stagingDirs(t)
	resume()
	if len(staged) != 0 {
		t.Errorf("the exit left %v behind", staged)
	}
	_ = feed.Close()
	if verr := <-installed; verr == nil {
		t.Error("an artifact whose checksum nobody claimed was installed")
	}
}

func stagingDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(paths.Data(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			out = append(out, e.Name())
		}
	}
	return out
}
