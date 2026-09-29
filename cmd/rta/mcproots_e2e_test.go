//go:build !windows

package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A server started with a root it can search and not read says so once on
// stderr, naming the root, and goes on serving: every read bounded under
// such a root was refused, call after call, with nothing at the start to
// say why.
func TestMCPServeSaysOnceWhichRootItCannotRead(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	// Resolved, as the server resolves a root before it names one: macOS's
	// temporary directory is reached through a link.
	resolved := func(dir string) string {
		target, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	readable := resolved(t.TempDir())
	searchOnly := filepath.Join(resolved(t.TempDir()), "search-only")
	if err := os.Mkdir(searchOnly, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(searchOnly, 0o700) })

	cmd := exec.Command(binary, "mcp", "serve", "--as", "probe", "--root", searchOnly, "--root", readable)
	cmd.Env = append(os.Environ(), "RTA_DATA_DIR="+t.TempDir(),
		"RTA_CONFIG="+filepath.Join(t.TempDir(), "config.yaml"), "NO_COLOR=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errBuf lockedBuffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	said := "rta: the root " + searchOnly + " cannot be read"
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(errBuf.String(), "team policy") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stderr := errBuf.String()
	if n := strings.Count(stderr, said); n != 1 {
		t.Fatalf("the unreadable root was said %d times, want once:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "the root "+readable) {
		t.Errorf("the readable root was said to be unreadable:\n%s", stderr)
	}
	if cmd.ProcessState != nil {
		t.Errorf("the server stopped rather than serving the root it can read:\n%s", stderr)
	}
}
