//go:build !windows

package pluginhost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/view"
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
	h := New()
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
	h := New()
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

// A TMPDIR too long for a socket is refused before anything starts, by
// name, with the limit and the fix — from the launch, and from a call that
// finds the declaration cached and has a process to start. It used to reach
// the operator as go-plugin's "Failed to read any lines from plugin's
// stdout" and a list of causes that were not this one.
func TestATMPDIRTooLongForASocketIsRefusedByName(t *testing.T) {
	bin, tmp := socketTMPDIR(t)
	long := filepath.Join(tmp, strings.Repeat("d", maxSocketPath))
	refused := func(t *testing.T, err error) {
		t.Helper()
		var verr *view.Error
		if !errors.As(err, &verr) || verr.Code != "plugin.tmpdir.toolong" {
			t.Fatalf("got %v, want the plugin.tmpdir.toolong refusal", err)
		}
		for _, want := range []string{strconv.Itoa(maxSocketPath), strconv.Itoa(maxSocketPath - socketTail), long} {
			if !strings.Contains(verr.Message, want) {
				t.Errorf("the refusal does not say %q: %s", want, verr.Message)
			}
		}
		if !strings.Contains(verr.Hint, "shorter directory") {
			t.Errorf("the hint does not name the fix: %q", verr.Hint)
		}
	}

	t.Run("launching", func(t *testing.T) {
		t.Setenv("TMPDIR", long)
		h := New()
		defer h.CloseAll()
		_, err := h.Open(context.Background(), bin)
		refused(t, err)
	})

	t.Run("calling with the declaration cached", func(t *testing.T) {
		h := New()
		defer h.CloseAll()
		if _, err := h.Open(context.Background(), bin); err != nil {
			t.Fatal(err)
		}
		h.CloseAll()

		t.Setenv("TMPDIR", long)
		cached := New()
		defer cached.CloseAll()
		c, err := cached.Open(context.Background(), bin)
		if err != nil {
			t.Fatalf("a cached declaration needs no process, and none should be refused: %v", err)
		}
		_, err = greetWith(t, c, "world")
		refused(t, err)
	})

	if left := leftovers(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR holds %v after the runs", left)
	}
}

// A TMPDIR that cannot hold the socket's directory is refused by name too,
// naming TMPDIR: left as os.MkdirTemp's error it read "stat …: no such file
// or directory", as a plugin that had gone.
func TestATMPDIRThatCannotHoldTheSocketIsRefusedByName(t *testing.T) {
	bin, tmp := socketTMPDIR(t)
	// One letter: the test's TMPDIR already takes most of what a socket's
	// path may, and a longer name is refused as too long first.
	missing := filepath.Join(tmp, "m")
	t.Setenv("TMPDIR", missing)
	h := New()
	defer h.CloseAll()
	_, err := h.Open(context.Background(), bin)
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "plugin.tmpdir.unusable" {
		t.Fatalf("got %v, want the plugin.tmpdir.unusable refusal", err)
	}
	if !strings.Contains(verr.Message, missing) || !strings.Contains(verr.Hint, "exists") {
		t.Errorf("the refusal does not name TMPDIR and the fix: %s (%s)", verr.Message, verr.Hint)
	}
	if left := leftovers(t, tmp); len(left) != 0 {
		t.Errorf("TMPDIR's parent holds %v after the refusal", left)
	}
}
