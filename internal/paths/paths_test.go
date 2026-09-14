package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// clear points every source Data consults at nothing, so each test below
// starts from "none of them are set" rather than whatever happens to be in
// the environment the suite runs in.
func clear(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
}

// RTA_DATA_DIR is the override tests and portable setups rely on, and it
// has to win outright — over XDG_DATA_HOME too, not only over the bare home
// fallback, or a machine with both set would silently ignore the override.
func TestDataPrefersRTADataDirOverEverything(t *testing.T) {
	clear(t)
	t.Setenv("XDG_DATA_HOME", "/xdg")
	t.Setenv("RTA_DATA_DIR", "/explicit")

	if got := Data(); got != "/explicit" {
		t.Errorf("Data() = %q, want the RTA_DATA_DIR override verbatim", got)
	}
}

// Without an explicit override, XDG's own data directory convention wins
// over guessing from $HOME — "rta" is appended, not assumed already there.
func TestDataFallsBackToXDGDataHome(t *testing.T) {
	clear(t)
	t.Setenv("XDG_DATA_HOME", "/xdg")

	want := filepath.Join("/xdg", "rta")
	if got := Data(); got != want {
		t.Errorf("Data() = %q, want %q", got, want)
	}
}

// With neither set, the XDG default itself: ~/.local/share, plus rta's own
// name under it.
func TestDataFallsBackToHomeWhenNeitherIsSet(t *testing.T) {
	clear(t)
	t.Setenv("HOME", "/home/someone")

	want := filepath.Join("/home/someone", ".local", "share", "rta")
	if got := Data(); got != want {
		t.Errorf("Data() = %q, want %q", got, want)
	}
}

// The directory holds the grant file and its seal key, the record, the store
// and every parked request, all written 0600 — and until EnsureData was the
// one creator, six writers created the directory itself 0755 through
// MkdirAll's parent creation, so its mode depended on which command a
// machine happened to run first. Owner-only here, or every one of those
// filenames is listable by any account.
func TestEnsureDataCreatesTheDirectoryOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	clear(t)
	nested := filepath.Join(t.TempDir(), "xdg", "rta")
	t.Setenv("RTA_DATA_DIR", nested)

	dir, err := EnsureData()
	if err != nil {
		t.Fatal(err)
	}
	if dir != nested {
		t.Fatalf("EnsureData() = %q, want %q", dir, nested)
	}
	for _, p := range []string{nested, filepath.Dir(nested)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s is mode %04o, want 0700", p, perm)
		}
	}
}

// An existing directory is the operator's: its mode is reported by `rta
// doctor`, never rewritten behind their back.
func TestEnsureDataLeavesAnExistingDirectoryAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	clear(t)
	dir := filepath.Join(t.TempDir(), "rta")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", dir)
	if _, err := EnsureData(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("EnsureData changed an existing directory to %04o", perm)
	}
}

// The system root is an explicit override first, and "set but empty" is the
// one spelling that means "there is none" — the difference between an
// operator who never heard of it and one who turned it off.
func TestSystemIsTheOverrideAndSetEmptyMeansNone(t *testing.T) {
	t.Setenv("RTA_SYSTEM_DIR", "/opt/rta")
	if got := System(); got != "/opt/rta" {
		t.Fatalf("System() = %q, want the override", got)
	}
	t.Setenv("RTA_SYSTEM_DIR", "")
	if got := System(); got != "" {
		t.Fatalf("System() = %q with RTA_SYSTEM_DIR set empty, want none", got)
	}
}

// Unset, the root exists on Linux only: that is where images and packages
// put things, and a default on every platform would have discovery reading a
// directory nothing on a Mac or Windows machine fills.
func TestSystemDefaultsOnLinuxOnly(t *testing.T) {
	os.Unsetenv("RTA_SYSTEM_DIR")
	t.Setenv("RTA_SYSTEM_DIR", "x")
	os.Unsetenv("RTA_SYSTEM_DIR")
	got := System()
	if runtime.GOOS == "linux" && got != "/usr/local/lib/rta" {
		t.Fatalf("System() = %q on linux, want /usr/local/lib/rta", got)
	}
	if runtime.GOOS != "linux" && got != "" {
		t.Fatalf("System() = %q on %s, want none", got, runtime.GOOS)
	}
}
