package paths

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// readOnlyDir is a directory the test cannot create anything in. Running as
// root, every directory is writable, and nothing here can be shown.
func readOnlyDir(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can write a directory whatever its mode")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return dir
}

// **A directory rta cannot write is one error, whoever tripped over it.** The
// data directory of a pod with its volume missing or owned by another uid
// failed six ways — `note.add.failed`, `core.grant.lock`, `config.lock` — each
// saying "acquiring the X lock: creating <dir>: mkdir <dir>: permission
// denied": the path twice, a permission problem called a lock, and nothing
// about whose directory it was or how to point rta somewhere else.
func TestAnUnwritableDataDirectoryNamesItsOwnerAndTheWaysToMoveIt(t *testing.T) {
	parent := readOnlyDir(t)
	dir := filepath.Join(parent, "rta")
	t.Setenv("RTA_DATA_DIR", dir)

	err := os.MkdirAll(dir, 0o700)
	verr := Unwritable(dir, err)
	if verr == nil {
		t.Fatalf("a permission refusal (%v) was not recognised", err)
	}
	if verr.Code != "core.dir.unwritable" {
		t.Errorf("code = %q", verr.Code)
	}
	for _, want := range []string{"data directory", dir, "permission denied", parent, "belongs to", "its mode is 0500"} {
		if !strings.Contains(verr.Message, want) {
			t.Errorf("the message %q does not say %q", verr.Message, want)
		}
	}
	if strings.Count(verr.Message, dir) != 1 {
		t.Errorf("the directory is named more than once: %q", verr.Message)
	}
	for _, want := range []string{"RTA_DATA_DIR", "XDG_DATA_HOME"} {
		if !strings.Contains(verr.Hint, want) {
			t.Errorf("the hint %q does not name %s", verr.Hint, want)
		}
	}
	if strings.Contains(verr.Hint, "RTA_CONFIG") {
		t.Errorf("the hint for the data directory names the config file's variable: %q", verr.Hint)
	}
}

func TestAnUnwritableConfigDirectoryNamesTheConfigVariables(t *testing.T) {
	parent := readOnlyDir(t)
	dir := filepath.Join(parent, "rta")
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))

	verr := Unwritable(dir, os.MkdirAll(dir, 0o700))
	if verr == nil {
		t.Fatal("a permission refusal was not recognised")
	}
	if !strings.Contains(verr.Message, "config directory") {
		t.Errorf("the message %q does not call it the config directory", verr.Message)
	}
	for _, want := range []string{"RTA_CONFIG", "XDG_CONFIG_HOME"} {
		if !strings.Contains(verr.Hint, want) {
			t.Errorf("the hint %q does not name %s", verr.Hint, want)
		}
	}
	if strings.Contains(verr.Hint, "RTA_DATA_DIR") {
		t.Errorf("the hint for the config directory names the data directory's variable: %q", verr.Hint)
	}
}

// A directory that is there and cannot be written is named as that, and the
// account that owns it and is locked out of it is told its mode, not that the
// directory belongs to the account that is running.
func TestAnExistingDirectoryItsOwnerCannotWriteIsToldItsMode(t *testing.T) {
	dir := readOnlyDir(t)
	t.Setenv("RTA_DATA_DIR", dir)

	_, err := os.CreateTemp(dir, "x")
	verr := Unwritable(dir, err)
	if verr == nil {
		t.Fatal("a permission refusal was not recognised")
	}
	if !strings.Contains(verr.Message, "cannot write to its data directory "+dir) {
		t.Errorf("the message %q does not name the directory it cannot write to", verr.Message)
	}
	if !strings.Contains(verr.Message, "its mode is 0500") || strings.Contains(verr.Message, "running as") {
		t.Errorf("the message %q does not say the mode that keeps its own owner out", verr.Message)
	}
}

// Only the system's refusal to let rta write is this error: a disk that is
// full, or a file in the way, are other problems with other fixes.
func TestOnlyAPermissionOrAReadOnlyRefusalIsAnUnwritableDirectory(t *testing.T) {
	if verr := Unwritable("/x", syscall.ENOSPC); verr != nil {
		t.Errorf("a full disk was called unwritable: %v", verr)
	}
	if verr := Unwritable("/x", nil); verr != nil {
		t.Errorf("no error was called unwritable: %v", verr)
	}
	verr := Unwritable("/x", syscall.EROFS)
	if verr == nil || !strings.Contains(verr.Message, "read-only file system") {
		t.Errorf("a read-only file system was not named: %v", verr)
	}
}

func TestCheckWritableAnswersForAnExistingAndForAMissingDirectory(t *testing.T) {
	ok := t.TempDir()
	if verr := CheckWritable(ok); verr != nil {
		t.Errorf("a directory this account owns is unwritable: %v", verr)
	}
	if verr := CheckWritable(filepath.Join(ok, "not", "yet")); verr != nil {
		t.Errorf("a directory that would be made under a writable one is unwritable: %v", verr)
	}

	ro := readOnlyDir(t)
	if verr := CheckWritable(ro); verr == nil || verr.Code != CodeUnwritable {
		t.Errorf("a read-only directory is writable: %v", verr)
	}
	if verr := CheckWritable(filepath.Join(ro, "rta")); verr == nil || verr.Code != CodeUnwritable {
		t.Errorf("a directory that would be made under a read-only one is writable: %v", verr)
	}
}
