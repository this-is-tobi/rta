package plugindist

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A forced exit taken while a verification launch is still under way leaves
// no plugin process behind. Describe launches through a host of its own, which
// the exit's closeAll does not know — it closes the application's — and the
// launch waits out the plugin's handshake whatever its context says, so a
// plugin that never answers is still starting when the exit lands. Plugins run
// in process groups of their own, so rta exiting did not end it: it went on
// running, sandboxed and unattended, for as long as it liked.
func TestAnExitDuringAVerificationLaunchLeavesNoPluginRunning(t *testing.T) {
	testData(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "rta-plugin-silent")
	body := "#!/bin/sh\necho $$ > " + pidFile + "\nexec sleep 60\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, signalled := context.WithCancel(context.Background())
	defer signalled()
	described := make(chan *view.Error, 1)
	go func() {
		_, verr := Describe(ctx, script)
		described <- verr
	}()
	pid := pidWritten(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	// The first signal cancels the command's context; the exit that follows
	// the grace settles the process and runs its exit hooks.
	signalled()
	resume := shutdown.Settle()
	shutdown.Exiting()
	running := stillRunning(pid)
	resume()
	if running {
		t.Errorf("pid %d was still running once the exit had run its hooks", pid)
	}
	if verr := <-described; verr == nil {
		t.Error("a plugin that never answered was described")
	}
}

func pidWritten(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(raw), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatalf("the plugin wrote %q for its pid", raw)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the plugin never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stillRunning reports whether pid is alive a moment after it was killed: a
// killed process is a zombie until its parent's wait collects it, and a signal
// 0 to a zombie succeeds.
func stillRunning(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
