//go:build !windows

package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// `rta plugin dev <dir> -- mcp serve` stops on SIGTERM as `rta mcp serve`
// does. The command after `--` ran in a nested root executed without the
// outer command's context, which is the one a signal cancels, and mcp serve
// owns its shutdown, so no grace cut it short either: it went on serving
// until its standard input closed, and only a second signal ended it.
//
// Through the real binary and a real plugin, because what is under test is
// which context reaches a command two roots down, and the signal handling
// that cancels it is main's.
func TestPluginDevStopsTheCommandItRunsOnSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	env := append(os.Environ(),
		"RTA_DATA_DIR="+data,
		"RTA_CONFIG="+filepath.Join(data, "config.yaml"),
		"NO_COLOR=1",
		// The scaffold is a new module whose go.sum starts empty; its tidy
		// resolves from the module cache rather than asking the checksum
		// database for every entry (internal/app's goFromCache says more).
		"GOSUMDB=off",
	)
	src := filepath.Join(t.TempDir(), "rta-plugin-probe")
	scaffold := exec.Command(binary, "plugin", "new", "probe", "--dir", src, "--rta-source", root)
	scaffold.Env = env
	if out, err := scaffold.CombinedOutput(); err != nil {
		t.Fatalf("plugin new: %v\n%s", err, out)
	}

	cmd := exec.Command(binary, "plugin", "dev", src, "--", "mcp", "serve", "--as", "probe")
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Compiling the plugin comes first, and with a cold build cache that is
	// the slow part of this test by far.
	deadline := time.Now().Add(5 * time.Minute)
	for !strings.Contains(stderr.String(), "listening") {
		select {
		case err := <-done:
			t.Fatalf("plugin dev ended (%v) before the server listened: %q", err, stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never said it was listening: %q", stderr.String())
		}
	}
	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		took := time.Since(sent)
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != 0 || took > 2*time.Second {
			t.Errorf("exit %d %s after SIGTERM, want 0 at once (stderr %q)", code, took, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("still serving 30s after SIGTERM (stderr %q)", stderr.String())
	}
}
