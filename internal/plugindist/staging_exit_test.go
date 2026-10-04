package plugindist

import (
	"context"
	"errors"
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
		t.Fatal(err)
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
	feed := openFeed(t, fifo, installed)
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

// openFeed opens the write end of fifo as soon as the install under way opens
// its read end, and fails the test, saying how the install ended, if it ends
// without ever having.
//
// A write open on a named pipe blocks until something opens the other end, so
// a plain os.OpenFile here waits for as long as the install never will: an
// install that failed before it reached the pipe, a verification launch that
// outlasted its handshake bound on a machine too busy to start the plugin in
// time, left the test blocked until the package's timeout killed the whole
// run, ten minutes on with a stack that names the open and not the install
// that failed. Opened without blocking, the open fails with ENXIO while
// nothing reads, which turns the wait into a poll that can also hear the
// install end.
func openFeed(t *testing.T, fifo string, installed <-chan *view.Error) *os.File {
	t.Helper()
	for {
		feed, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return feed
		}
		if !errors.Is(err, syscall.ENXIO) {
			t.Fatal(err)
		}
		select {
		case verr := <-installed:
			t.Fatalf("the install ended before it read %s: %v", filepath.Base(fifo), verr)
		case <-time.After(5 * time.Millisecond):
		}
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

// Nor does one taken while it fetches a signature. The signature and its key
// were fetched into a temporary directory of their own, whose deferred removal
// an exit skips, and a file:// signature that never ends keeps that fetch
// under way for as long as it likes: the directory stayed in $TMPDIR. They are
// fetched into the staging directory the exit removes, which is what this
// looks for while the fetch is under way.
func TestAnExitDuringASignaturesFetchLeavesNothingBehind(t *testing.T) {
	testData(t)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "artifact.sig")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key.pub")
	cosign := filepath.Join(dir, "cosign")
	if err := os.WriteFile(key, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cosign, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := cosignBin
	cosignBin = cosign
	t.Cleanup(func() { cosignBin = saved })
	attach(t, helloManifest(t, hello(t), "")+fmt.Sprintf("signature:\n  sig: %s\n  key: %s\n",
		fileURL(fifo), fileURL(key)))

	installed := make(chan *view.Error, 1)
	go func() {
		_, verr := Install(context.Background(), "hello", io.Discard)
		installed <- verr
	}()
	// Opened once the install opens it to read, which it does once the
	// plugin it signs has been verified.
	feed := openFeed(t, fifo, installed)
	defer func() { _ = feed.Close() }()
	if _, err := feed.Write([]byte("the first bytes of a signature")); err != nil {
		t.Fatal(err)
	}
	staged := stagingDirs(t)
	if len(staged) != 1 {
		t.Fatalf("staging directories during the fetch: %v, want one", staged)
	}
	if _, err := os.Stat(filepath.Join(paths.Data(), "plugins", staged[0], "artifact.sig")); err != nil {
		t.Errorf("the signature is fetched somewhere the exit does not remove: %v", err)
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
	staged = stagingDirs(t)
	resume()
	if len(staged) != 0 {
		t.Errorf("the exit left %v behind", staged)
	}
	_ = feed.Close()
	<-installed
}
