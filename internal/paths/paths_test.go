package paths

import (
	"path/filepath"
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
