package pluginhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// envPrinterSource is a plugin-shaped program that prints the environment it
// was started with. It is compiled rather than a copy of env(1), because a
// copy of a platform binary is killed on macOS.
const envPrinterSource = `package main

import (
	"fmt"
	"os"
)

func main() {
	for _, kv := range os.Environ() {
		fmt.Println(kv)
	}
}
`

var (
	envPrinterOnce sync.Once
	envPrinterBin  string
	envPrinterErr  error
)

// envPrinter builds the program once and hands each test a copy of its own, so
// the file a test launches is not shared with another test's staging.
func envPrinter(t *testing.T) string {
	t.Helper()
	envPrinterOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rta-envprinter-*")
		if err != nil {
			envPrinterErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(envPrinterSource), 0o644); err != nil {
			envPrinterErr = err
			return
		}
		envPrinterBin = filepath.Join(dir, BinaryName("rta-plugin-envprinter"))
		if out, err := exec.Command("go", "build", "-o", envPrinterBin, src).CombinedOutput(); err != nil {
			envPrinterErr = fmt.Errorf("%w: %s", err, out)
		}
	})
	if envPrinterErr != nil {
		t.Fatalf("building the environment printer: %v", envPrinterErr)
	}
	body, err := os.ReadFile(envPrinterBin)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), BinaryName("rta-plugin-envprinter"))
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// stagedCmd is buildCmd for a test that runs the command it builds: launch
// stages the file first, and the command names the staged copy.
func stagedCmd(t *testing.T, id Identity, deny DenySet, args []string) *exec.Cmd {
	t.Helper()
	if _, err := stage(id); err != nil {
		t.Fatal(err)
	}
	deny, err := deny.Launching(id.execPath())
	if err != nil {
		t.Fatal(err)
	}
	return buildCmd(id, deny, args)
}

// What a plugin found on $PATH runs from is rta's own copy, with the bytes the
// digest names, in a directory only the operator's own user can write.
func TestAPluginOnThePathRunsFromAPrivateCopy(t *testing.T) {
	id, path := pathPlugin(t)
	exe, err := stage(id)
	if err != nil {
		t.Fatal(err)
	}
	if exe == path || !strings.HasPrefix(exe, ManagedRun()) {
		t.Fatalf("staged at %s, want a file under %s", exe, ManagedRun())
	}
	want, _ := os.ReadFile(path)
	got, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the copy differs from what was hashed: %v", err)
	}
	if info, err := os.Stat(exe); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Errorf("the copy is reachable by others: %v %v", info, err)
	}
	cmd := buildCmd(id, DenySet{}, nil)
	if !strings.Contains(strings.Join(cmd.Args, "\x00"), exe) {
		t.Errorf("the command %v does not run the copy %s", cmd.Args, exe)
	}
}

// A copy that was altered is not trusted for being there.
func TestAStagedCopyThatNoLongerMatchesIsWrittenAgain(t *testing.T) {
	id, _ := pathPlugin(t)
	exe, err := stage(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("not the plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := stage(id); err != nil {
		t.Fatal(err)
	}
	if !matches(exe, id.Digest) {
		t.Fatal("a copy that did not match was run as it was")
	}
}

// An artifact in a directory rta keeps is run where it is: it is not copied,
// and the sandbox's own-directory rule names the directory it sits in.
func TestAnArtifactInTheStoreIsNotCopied(t *testing.T) {
	data, err := os.ReadFile(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ManagedStore(), "staged", "abc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(ManagedStore(), "staged")) })
	path := filepath.Join(dir, BinaryName("rta-plugin-staged"))
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := stage(id)
	if err != nil || exe != id.Path {
		t.Fatalf("stage = %q, %v; want the artifact itself %q", exe, err, id.Path)
	}
}

// A copy that is started again is a copy somebody asked for today, however long
// ago it was made: the sweep another plugin's launch makes judges it by its
// directory's time, and one that was reused without touching it would be
// removed from under a launch that had just found it.
func TestAReusedCopyIsNotSweptByTheNextLaunch(t *testing.T) {
	id, _ := pathPlugin(t)
	exe, err := stage(id)
	if err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Dir(exe), then, then); err != nil {
		t.Fatal(err)
	}
	if _, err := stage(id); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), BinaryName("rta-plugin-another"))
	if err := os.WriteFile(elsewhere, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	other, err := Identify(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stage(other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Errorf("a copy that was started again today was swept: %v", err)
	}
}

// Copies nobody has asked for in a day are not kept: a plugin author rebuilds
// on every run and each build is a digest of its own.
func TestStaleStagedCopiesAreSweptAway(t *testing.T) {
	id, _ := pathPlugin(t)
	old := filepath.Join(ManagedRun(), strings.Repeat("a", 64))
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	if _, err := stage(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("a copy untouched for two days was kept")
	}
}
