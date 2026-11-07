package tunnel

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether a pid is still a running process. Signal 0 checks for
// existence without delivering anything.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// A splice child that ignores SIGTERM is ended by Close all the same: the
// listener is closed and the connection with it, and a process nobody can
// find would otherwise still hold the path to the far side.
func TestSSHCloseEndsAChildThatIgnoresSIGTERM(t *testing.T) {
	dir := t.TempDir()
	probed, pidFile := filepath.Join(dir, "probed"), filepath.Join(dir, "pid")
	fakeSSH(t, fmt.Sprintf(
		"if [ ! -f %s ]; then touch %s; exit 0; fi\ntrap '' TERM\necho $$ > %s\nwhile true; do sleep 0.05; done\n",
		probed, probed, pidFile))

	tun, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
	if verr != nil {
		t.Fatalf("open: %v", verr)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(tun.Host, strconv.Itoa(tun.Port)), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	var pid int
	for deadline := time.Now().Add(20 * time.Second); pid == 0; time.Sleep(10 * time.Millisecond) {
		raw, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		if pid == 0 && time.Now().After(deadline) {
			t.Fatal("the splice child never started, so this proves nothing about Close")
		}
	}

	tun.Close()

	if alive(pid) {
		t.Errorf("the child (pid %d) was still running after Close: a trapped SIGTERM left it serving", pid)
	}
	if !tun.TimedOut() {
		t.Error("Close that had to kill a child did not say it gave up waiting")
	}
}
