//go:build linux || darwin

package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What a signal does to the real process, whose exit status and whose stderr
// are the whole of the contract: a handler that never reads its context kept
// rta running after SIGTERM, `timeout 120 rta git blame . big.txt` on a large
// history among them.

// blocked starts rta on a command whose handler never reads its context —
// kv set reading a named pipe nobody writes to — and returns once rta has
// opened the pipe: past its startup, with its signal handling in place, and
// blocked in a read no context reaches. Readiness is the open, not a sleep,
// because the first run of a freshly built binary can take seconds to start
// on macOS, and a signal before main has caught it is its default action.
func blocked(t *testing.T) (*exec.Cmd, *lockedBuffer) {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	fifo := filepath.Join(t.TempDir(), "value")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	cmd := exec.Command(binary, "kv", "set", "probe", "--file", fifo, "-o", "json")
	cmd.Env = append(os.Environ(),
		"RTA_DATA_DIR="+dataDir(t),
		"RTA_CONFIG="+filepath.Join(dataDir(t), "config.yaml"),
		"RTA_KV_PASSPHRASE=probe",
		"RTA_KV_IDENTITY=",
		"NO_COLOR=1",
	)
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	deadline := time.Now().Add(startsWithin)
	for {
		// A writer's non-blocking open fails until a reader has the pipe
		// open. Held, and never written to, for as long as the test runs.
		w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			t.Cleanup(func() { _ = w.Close() })
			return cmd, stderr
		}
		if time.Now().After(deadline) {
			t.Fatalf("rta never opened the pipe (stderr %q)", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// exit waits for cmd, and reports the status it exited with — -1 when a
// signal's default action ended it, which is what these tests rule out — and
// how long after since it took.
func exit(t *testing.T, cmd *exec.Cmd, since time.Time) (int, time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, time.Since(since)
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("still running 30s after the signal")
	}
	return 0, 0
}

// The command is given its grace to return on its own, and then rta exits
// without it: 143 for SIGTERM and 130 for SIGINT, as a shell reports a process
// the signal stopped, with the reason as a coded error in the format asked for.
func TestASignalStopsACommandThatNeverReadsItsContext(t *testing.T) {
	for sig, want := range map[syscall.Signal]int{syscall.SIGTERM: 143, syscall.SIGINT: 130} {
		cmd, stderr := blocked(t)
		sent := time.Now()
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		code, took := exit(t, cmd, sent)
		if code != want {
			t.Errorf("%v: exit %d, want %d (stderr %q)", sig, code, want, stderr.String())
		}
		// The grace is three seconds: not exited at once, and not long after.
		if took < 2*time.Second || took > 20*time.Second {
			t.Errorf("%v: exited %s after the signal, want about three seconds", sig, took)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(stderr.String()), &env); err != nil {
			t.Errorf("%v: stderr is not the json asked for (%v): %q", sig, err, stderr.String())
			continue
		}
		if env["code"] != "core.signal" || !strings.Contains(env["message"].(string), "`rta kv set`") {
			t.Errorf("%v: stderr = %v, want core.signal naming the command", sig, env)
		}
	}
}

// A second signal is somebody insisting, and exits at once, in the status of
// the signal that did it.
//
// SIGINT goes first and SIGTERM second, because the order is what holds the
// verdict when the pause between them is not enough. Two signals pending
// together are handed over lowest number first, by the kernel and again by
// the Go runtime, whatever order they were sent in. This sent SIGTERM and
// then SIGINT, with only the pause to let the first be read: a process that
// had not run by the time the second arrived read SIGINT first and exited
// 143 for the 130 asked for, as SIGTERM and SIGINT sent back to back do
// every time. Sent in ascending order, the order they are read in is the
// order they were sent in, read apart or together.
func TestASecondSignalExitsAtOnce(t *testing.T) {
	cmd, stderr := blocked(t)
	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	code, took := exit(t, cmd, sent)
	if code != 143 || took > 2500*time.Millisecond {
		t.Errorf("exit %d %s after the first signal, want 143 well inside the grace (stderr %q)",
			code, took, stderr.String())
	}
}

// `mcp serve` stops on its own terms, as it did before any of this: SIGTERM
// ends the session and it exits 0 at once, with no deadline of the command
// line's in the way.
func TestMCPServeStillStopsCleanlyOnSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	cmd := exec.Command(binary, "mcp", "serve", "--as", "probe")
	cmd.Env = append(os.Environ(), "RTA_DATA_DIR="+t.TempDir(), "NO_COLOR=1")
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
	deadline := time.Now().Add(startsWithin)
	for !strings.Contains(stderr.String(), "listening") {
		if time.Now().After(deadline) {
			t.Fatalf("the server never said it was listening: %q", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	code, took := exit(t, cmd, sent)
	if code != 0 || took > 2*time.Second {
		t.Errorf("exit %d %s after SIGTERM, want 0 at once (stderr %q)", code, took, stderr.String())
	}
}
