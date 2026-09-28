//go:build !windows

package pluginhost

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// socketTMPDIR points TMPDIR at a directory of the test's own, short enough
// for a socket, and an empty describe cache beside it so the open launches.
// Not t.TempDir: its name carries the test's, and under macOS's TMPDIR that
// alone takes a socket's path past its limit.
//
// The plugin is built first, because hello builds into TMPDIR once for the
// whole package and a fixture built into this test's directory would be
// removed with it.
func socketTMPDIR(t *testing.T) (bin, tmp string) {
	t.Helper()
	bin = hello(t)
	tmp, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	t.Setenv("TMPDIR", tmp)
	t.Setenv("RTA_DATA_DIR", filepath.Join(tmp, "data"))
	return bin, tmp
}

// leftovers is what a plugin left in TMPDIR: everything but the data
// directory the test put there.
func leftovers(t *testing.T, tmp string) []string {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if e.Name() != "data" {
			left = append(left, e.Name())
		}
	}
	return left
}

// The socket is made in a directory of rta's own, this user's alone, and
// goes with the process: after an ordinary call, after a restart that
// replaced a crashed process, and after a close. go-plugin removed it on
// none of them — with its broker multiplexed, the listener it closes is not
// the one the socket was bound through — and one developer's TMPDIR held
// 74,664 of them.
func TestAPluginLeavesNoSocketBehind(t *testing.T) {
	bin, tmp := socketTMPDIR(t)
	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := greetWith(t, c, "world"); err != nil {
		t.Fatal(err)
	}

	left := leftovers(t, tmp)
	if len(left) != 1 || !strings.HasPrefix(left[0], socketDirPattern) {
		t.Fatalf("a running plugin put %v in TMPDIR, want one %s directory", left, socketDirPattern)
	}
	dir := filepath.Join(tmp, left[0])
	if info, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the socket's directory is %04o, want 0700", perm)
	}
	inside, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(inside) != 1 || inside[0].Type()&fs.ModeSocket == 0 {
		t.Fatalf("the socket's directory holds %v, want the plugin's socket alone", inside)
	}

	// A crash, and the restart the next call makes: the dead process's
	// directory goes with it, and only the new one's is left.
	c.mu.Lock()
	_ = c.cmd.Process.Kill()
	c.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !c.client.Exited() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := greetWith(t, c, "again"); err != nil {
		t.Fatalf("the call after the crash: %v", err)
	}
	if now := leftovers(t, tmp); len(now) != 1 || now[0] == left[0] {
		t.Errorf("after a restart TMPDIR holds %v, want the new process's directory alone", now)
	}

	h.CloseAll()
	if now := leftovers(t, tmp); len(now) != 0 {
		t.Errorf("a closed plugin left %v in TMPDIR", now)
	}
}

// A forced exit closes every host, and the socket's directory goes with the
// process there too: plugin dev ended by a SIGTERM left its socket behind.
func TestAForcedExitLeavesNoSocketBehind(t *testing.T) {
	bin, tmp := socketTMPDIR(t)
	h := New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := greetWith(t, c, "world"); err != nil {
		t.Fatal(err)
	}
	if left := leftovers(t, tmp); len(left) != 1 {
		t.Fatalf("a running plugin put %v in TMPDIR, want its socket's directory", left)
	}

	resume := shutdown.Settle()
	shutdown.Exiting()
	left := leftovers(t, tmp)
	resume()
	if len(left) != 0 {
		t.Errorf("a forced exit left %v in TMPDIR", left)
	}
}
