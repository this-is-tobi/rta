package tunnel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperEnv names the script the helper process below runs as its kubectl.
const helperEnv = "RTA_TUNNEL_ORPHAN_HELPER"

// TestOrphanHelperProcess is the process the test below kills: it opens a
// forward through the real Open and then waits to be killed. It is a test only
// so the test binary can be its own helper, and does nothing unless it is
// asked to.
func TestOrphanHelperProcess(t *testing.T) {
	script := os.Getenv(helperEnv)
	if script == "" {
		t.Skip("a helper of the test below")
	}
	kubectl = script
	if _, verr := Open(context.Background(), "homelab-pg", Target{Kube: homelab}); verr != nil {
		fmt.Fprintln(os.Stderr, "open:", verr)
		os.Exit(3)
	}
	time.Sleep(time.Hour)
}

// running is whether pid is a process that is still doing anything: a zombie
// still answers signal 0, and in a container with nothing to reap it, the
// reparented child of a killed server stays one.
func running(pid int) bool {
	if !alive(pid) {
		return false
	}
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if _, after, found := strings.Cut(string(raw), ") "); found && strings.HasPrefix(after, "Z") {
			return false
		}
	}
	return true
}

func waitDead(pid int, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !running(pid) {
			return true
		}
	}
	return !running(pid)
}

// A port-forward is a listener on a loopback port into the operator's
// cluster, and a server killed outright — SIGKILL, the OOM killer — could not
// close it: the kubectl was in a process group of its own with nothing to tell
// it its parent was gone, and it went on forwarding with nobody watching. On
// Linux the kernel ends it with its parent. Where it cannot (macOS), the next
// start finds it and does.
func TestAKilledServersForwardDoesNotOutliveIt(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "kubectl")
	body := fmt.Sprintf("#!/bin/sh\necho 'Forwarding from 127.0.0.1:1 -> 5432'\necho $$ > %s\nwhile true; do sleep 0.05; done\n", marker)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", filepath.Join(dir, "data"))

	helper := exec.Command(os.Args[0], "-test.run=^TestOrphanHelperProcess$")
	helper.Env = append(os.Environ(), helperEnv+"="+script)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	forwardPID := 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if raw, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(raw)) != "" {
			forwardPID, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			break
		}
	}
	if forwardPID == 0 {
		_ = helper.Process.Kill()
		t.Fatal("the helper never opened its forward, so this proves nothing")
	}
	t.Cleanup(func() { _ = syscall.Kill(-forwardPID, syscall.SIGKILL); _ = syscall.Kill(forwardPID, syscall.SIGKILL) })

	// The script writes its marker the moment it runs, and the helper writes
	// the forward down just after starting it: a server killed between the two
	// leaves nothing for the next start to find, which is a window this test is
	// not about. It waits for the record, where one is kept, so what it kills
	// is a server with a forward in flight.
	if runtime.GOOS == "darwin" {
		record := filepath.Join(dir, "data", "forwards", strconv.Itoa(forwardPID)+".json")
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(record); err == nil {
				break
			}
		}
	}

	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()

	if runtime.GOOS == "darwin" {
		if !running(forwardPID) {
			t.Skip("this kernel ended the forward by itself")
		}
		if n := ReapOrphans(); n != 1 {
			t.Fatalf("the next start stopped %d forwards, want the one a killed server left", n)
		}
	}
	if !waitDead(forwardPID, 5*time.Second) {
		t.Fatal("the forward of a server that was killed is still running: a listener into the cluster nobody watches")
	}
}
