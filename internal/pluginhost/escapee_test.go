package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A launch that failed is not held for as long as a descendant of the plugin
// lives. The plugin's own process group is taken whole (reap), and go-plugin
// collects what it started only once the plugin's output has been read to its
// end, so a descendant that left the group and kept the plugin's stderr open
// held the collection, and the failed launch with it, for as long as it ran:
// a plugin that printed something other than its handshake and handed a
// background process its stderr hung every rta that found it for the
// background process's whole life.
//
// The launch is the same under the sandbox on macOS and unwrapped, so one
// script serves both; setsid is how a descendant leaves the group, and is not
// on every system.
func TestADescendantHoldingThePipesDoesNotHoldAFailedLaunch(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("no setsid to leave the process group with")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "rta-plugin-escapee")
	pidFile := filepath.Join(dir, "pid")
	script := "#!/bin/sh\necho not a handshake\n" + setsid + " sleep 60 &\necho $! > " + pidFile + "\nexit 1\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	h := New()
	t.Cleanup(h.CloseAll)

	start := time.Now()
	_, err = h.Open(context.Background(), p)
	opened := time.Since(start)
	if err == nil {
		t.Fatal("a script was accepted as a plugin")
	}
	// Two bounded waits at most, each the wait a close already accepts, and
	// far under the minute the descendant sleeps: what is being kept from
	// happening is waiting on it.
	if limit := 3 * killTimeout; opened > limit {
		t.Errorf("the failed launch took %s with a descendant holding its pipes, want under %s", opened, limit)
	}
	if !strings.Contains(err.Error(), "not a handshake") {
		t.Errorf("the launch did not say what the plugin printed: %v", err)
	}
}
