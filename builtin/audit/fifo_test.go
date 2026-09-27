//go:build !windows

package audit

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// mkfifo makes a named pipe at path, skipping where the platform has none.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
}

// answersInTime runs call and fails the test if it does not return: open(2)
// on a named pipe with no writer blocks where no context reaches, so the
// failure is a call that never answers, and the deadline is the assertion.
// The pipes are opened for writing on the way out to free a leaked open.
func answersInTime[T any](t *testing.T, call func() T, fifos ...string) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- call() }()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Second):
		for _, f := range fifos {
			if w, err := os.OpenFile(f, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = w.Close()
			}
		}
		t.Fatal("the call on a named pipe with no writer did not return")
	}
	var zero T
	return zero
}

// audit_deps named a lockfile and read it with fs.ReadFile, which opens a
// named pipe blocking: over MCP the call never answered. It is refused by
// name before anything opens it.
func TestDepsRefusesANamedPipeOverMCPAtOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "package-lock.json")
	mkfifo(t, fifo)
	for _, run := range []struct {
		name string
		call func() error
	}{
		{"audit.deps", func() error {
			_, err := runDeps(t.Context(), req(map[string]any{"path": fifo, "offline": true}).WithSurface(plugin.SurfaceMCP))
			return err
		}},
		{"audit.why", func() error {
			_, err := runWhy(t.Context(), req(map[string]any{"package": "left-pad", "path": fifo}).WithSurface(plugin.SurfaceMCP))
			return err
		}},
	} {
		err := answersInTime(t, run.call, fifo)
		verr, ok := err.(*view.Error)
		if !ok || !strings.HasSuffix(verr.Code, ".notafile") || !strings.Contains(verr.Message, "a named pipe") {
			t.Errorf("%s: err = %v, want a notafile refusal naming the pipe", run.name, err)
		}
	}
}

// A directory scan finds a lockfile nobody named, so no surface reads a
// pipe in its place, the CLI's included: the pipe is named as a manifest
// that could not be read, and the rest of the directory is still audited.
func TestADirectoryScanNamesAPipeInPlaceOfALockfile(t *testing.T) {
	dir := t.TempDir()
	mkfifo(t, filepath.Join(dir, "package-lock.json"))
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("django==3.2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceMCP} {
		proj, verr := openProject(t.Context(), req(map[string]any{}).WithSurface(sf), dir)
		if verr != nil {
			t.Fatal(verr)
		}
		names, shown, _, err := proj.manifests(false)
		if err != nil {
			t.Fatal(err)
		}
		inv := answersInTime(t, func() inventory { return read(proj.fsys, names, shown) },
			filepath.Join(dir, "package-lock.json"))
		if len(inv.all) != 1 {
			t.Errorf("%s: read %d components, want requirements.txt's one", sf, len(inv.all))
		}
		if len(inv.unreadable) != 1 || !strings.Contains(inv.unreadable[0].reason, "a named pipe") {
			t.Errorf("%s: unreadable = %+v, want the lockfile named as a pipe", sf, inv.unreadable)
		}
	}
}

// audit clients reads a project's .mcp.json from the working directory, a
// file a cloned repository chooses, with os.ReadFile: a pipe there held the
// audit at the terminal for good. It is graded as a file that could not be
// read, and the rest of the report still arrives.
func TestClientsNamesAPipeInPlaceOfAProjectConfig(t *testing.T) {
	fakeHome(t, nil)
	wd := t.TempDir()
	fifo := filepath.Join(wd, ".mcp.json")
	mkfifo(t, fifo)
	t.Chdir(wd)
	rows := answersInTime(t, func() map[string][]string { return agentRows(t, plugin.SurfaceCLI) }, fifo)
	var named bool
	for _, row := range rows {
		if strings.Contains(strings.Join(row, " "), "a named pipe") {
			named = true
		}
	}
	if !named {
		t.Errorf("no row names .mcp.json as a pipe: %v", rows)
	}
}

// A manifest is read up to maxManifestBytes and no further: one named on its
// own and past it is refused by name, and one a scan finds is named as a
// manifest it could not read, rather than either being read whole.
func TestAManifestPastItsCapIsNotReadWhole(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "go.mod")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxManifestBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_, verr := openProject(t.Context(), req(map[string]any{}).WithSurface(plugin.SurfaceMCP), big)
	if verr == nil || verr.Code != "audit.deps.toolarge" || !strings.Contains(verr.Message, big) {
		t.Errorf("named: err = %v, want audit.deps.toolarge naming the file", verr)
	}
	proj, verr := openProject(t.Context(), req(map[string]any{}).WithSurface(plugin.SurfaceMCP), dir)
	if verr != nil {
		t.Fatal(verr)
	}
	names, shown, _, err := proj.manifests(false)
	if err != nil {
		t.Fatal(err)
	}
	inv := read(proj.fsys, names, shown)
	if len(inv.unreadable) != 1 || !strings.Contains(inv.unreadable[0].reason, "larger than") {
		t.Errorf("scanned: unreadable = %+v, want go.mod named as past the cap", inv.unreadable)
	}
}
