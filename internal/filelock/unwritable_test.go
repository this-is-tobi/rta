package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

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

// **The lock is the first thing to touch the data directory of most commands,
// so it is where an unwritable one is found out.** Taking the lock said
// `creating <dir>: mkdir <dir>: permission denied`, and six callers each put
// their own code and "acquiring the X lock" around it.
func TestALockInADirectoryNobodyCanWriteIsTheUnwritableDirectoryError(t *testing.T) {
	ro := readOnlyDir(t)
	t.Setenv("RTA_DATA_DIR", filepath.Join(ro, "rta"))

	for name, path := range map[string]string{
		"a directory to make":       filepath.Join(ro, "rta", "notes.json.lock"),
		"a directory that is there": filepath.Join(ro, "x.lock"),
	} {
		_, err := Acquire(path, DefaultStale, DefaultRetry, 100*time.Millisecond)
		var verr *view.Error
		if !errors.As(err, &verr) || verr.Code != "core.dir.unwritable" {
			t.Errorf("%s: err = %v, want core.dir.unwritable", name, err)
			continue
		}
		if verr.Hint == "" {
			t.Errorf("%s: the refusal has no hint", name)
		}
	}
}

// A caller that wraps the failure in its own code would hide it, so Failed hands
// the unwritable error back as it is and gives every other failure the caller's
// code and sentence.
func TestFailedPassesTheUnwritableDirectoryThroughAndCodesTheRest(t *testing.T) {
	ro := readOnlyDir(t)
	_, err := Acquire(filepath.Join(ro, "x.lock"), DefaultStale, DefaultRetry, 100*time.Millisecond)
	verr := Failed(err, "core.grant.lock", "acquiring the grant file lock")
	if verr.Code != "core.dir.unwritable" || strings.Contains(verr.Message, "acquiring") {
		t.Errorf("an unwritable directory was wrapped: %s %q", verr.Code, verr.Message)
	}

	other := Failed(errors.New("timed out waiting for another call"), "core.grant.lock", "acquiring the grant file lock")
	if other.Code != "core.grant.lock" || other.Message != "acquiring the grant file lock: timed out waiting for another call" {
		t.Errorf("another failure became %s %q", other.Code, other.Message)
	}
}
