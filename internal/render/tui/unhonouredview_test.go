package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
)

// A config file found in the working directory arranges no screen. V cycled
// through its views anyway, drew the automatic dashboard under each name, and
// said "view: ops" over tiles that were not ops's: a menu in which every entry
// is the same screen.
func TestCyclingViewsOfAnUnhonouredFileSaysThereAreNone(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_CONFIG", "")
	t.Chdir(t.TempDir())
	if config.TrustedPath() {
		t.Skip("this machine has a config directory")
	}
	file := "dashboard:\n  views:\n    ops:\n      add:\n      - id: sys.overview\n"
	if err := os.WriteFile(".rta.yaml", []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []int{1, -1} {
		m := New(registry.New(), config.Dashboard{}, nil)
		cmd := m.cycleView(step)
		if !strings.Contains(m.flash, "arranges no screen") {
			t.Errorf("step %d: flash = %q, want the statement that this file arranges nothing", step, m.flash)
		}
		if m.view != "" || cmd != nil {
			t.Errorf("step %d: a view was switched to (%q) from a file that arranges nothing", step, m.view)
		}
	}
}
