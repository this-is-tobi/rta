package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// The mark beside a footer line follows the line (refuse, guide): a flash that
// went through neither wears the green check of success, and a save the file
// refused read as the confirmation of the save. TestAFlashThatReportsAFailure
// holds the mark to the call; these hold the keys to the call.
func flashedAsRefusal(m Model) bool { return m.flash != "" && m.flash == m.flashBad }
func flashedAsGuide(m Model) bool   { return m.flash != "" && m.flash == m.flashGuide }

// A config file that cannot be written is the plainest failed save: the path
// has a regular file where its directory should be.
func unwritableConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "readonly", "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, "readonly"), nil, 0o400); err != nil {
		t.Fatal(err)
	}
}

// H and ] apply for the session when the file will not take them, and the footer
// said so after a green check. The line leads with what did not happen, so a bar
// that cuts it short cannot cut that off, and wears the cross.
func TestAnArrangementThatCouldNotBeSavedIsFlashedWithTheCross(t *testing.T) {
	for key, name := range map[string]string{"H": "hide", "]": "move"} {
		unwritableConfig(t)
		m := New(multiRegistry(t), config.Dashboard{}, nil)
		sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		sm := sized.(Model)
		sm.selected = 1

		after, _ := sm.Update(keyMsg(key))
		am := after.(Model)
		if !strings.HasPrefix(am.flash, "not saved") || !strings.Contains(am.flash, "session only") {
			t.Errorf("%s (%s): flash = %q, want it to lead with what did not happen and say it holds for the session", key, name, am.flash)
		}
		if !flashedAsRefusal(am) {
			t.Errorf("%s (%s): the flash %q is not drawn as a refusal", key, name, am.flash)
		}
		footer := plain(am.dashFooter())
		if !strings.Contains(footer, "✗ not saved") || strings.Contains(footer, "✓") {
			t.Errorf("%s (%s): the footer reads:\n%s", key, name, footer)
		}
	}
}

// Every key that found nothing to do, or was refused, wears the cross; every key
// that only says what comes next wears the arrow. None of them the check.
func TestAKeyThatDidNothingIsNotFlashedWithTheCheck(t *testing.T) {
	t.Run("hiding a plugin's tile in the inventory on a config that cannot be written", func(t *testing.T) {
		unwritableConfig(t)
		m := New(multiRegistry(t), config.Dashboard{}, nil)
		m.plugins = m.pluginInventory()
		m.mode = modePlugins
		got := sent(t, m, keyMsg("space"))
		if !flashedAsRefusal(got) || !strings.HasPrefix(got.flash, "not saved") {
			t.Errorf("flash = %q (refusal %v)", got.flash, flashedAsRefusal(got))
		}
	})
	t.Run("approving a built-in", func(t *testing.T) {
		t.Setenv("RTA_DATA_DIR", t.TempDir())
		m := paneWithUntrusted(t)
		m.mode, m.pluginSel = modePlugins, 0
		got := sent(t, m, keyMsg("t"))
		if !flashedAsRefusal(got) || !strings.Contains(got.flash, "built into rta") {
			t.Errorf("flash = %q (refusal %v)", got.flash, flashedAsRefusal(got))
		}
	})
	t.Run("a key on a plugin that has not been approved", func(t *testing.T) {
		t.Setenv("RTA_DATA_DIR", t.TempDir())
		m := paneWithUntrusted(t)
		m.mode = modePlugins
		got := sent(t, m, keyMsg("c"))
		if !flashedAsRefusal(got) || !strings.Contains(got.flash, "has not been run") {
			t.Errorf("flash = %q (refusal %v)", got.flash, flashedAsRefusal(got))
		}
	})
	t.Run("switching to an environment that cannot be used", func(t *testing.T) {
		m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
			"broken": {Plugins: map[string]config.Connection{"nosuchplugin": conn(map[string]any{"host": "x"})}},
		}})
		m.mode = modeProfiles
		got := sent(t, m, keyMsg("u"))
		if !flashedAsRefusal(got) || !strings.Contains(got.flash, "cannot be used") {
			t.Errorf("flash = %q (refusal %v)", got.flash, flashedAsRefusal(got))
		}
	})
	t.Run("unlocking a store that is not there", func(t *testing.T) {
		m, _ := kvModel(t)
		got := sent(t, m, keyMsg("U"))
		if !flashedAsRefusal(got) || !strings.Contains(got.flash, "nothing to unlock") {
			t.Errorf("flash = %q (refusal %v)", got.flash, flashedAsRefusal(got))
		}
	})
	t.Run("a key that is not the one the armed answer waits for", func(t *testing.T) {
		m := dashboardModel(t)
		m.armed = &armedAnswer{}
		got := sent(t, m, keyMsg("x"))
		if !flashedAsRefusal(got) || !strings.Contains(got.flash, "nothing approved") {
			t.Errorf("flash = %q (refusal %v)", got.flash, flashedAsRefusal(got))
		}
	})
}

// A fetch under way has reported nothing done: its line points at the box, as a
// hint does, and the check would say the completion had succeeded before it was
// asked for.
func TestAFetchUnderWayPointsAtTheBoxAndIsNotACheck(t *testing.T) {
	noHistory(t)
	clusterFake(t)
	next, cmd := connFormOnKubeField(t).Update(tabKey)
	got := next.(Model)
	if got.flash != "completing…" || cmd == nil {
		t.Fatalf("tab did not start a fetch (flash %q)", got.flash)
	}
	if !flashedAsGuide(got) {
		t.Errorf("the flash %q is not drawn as a hint", got.flash)
	}
}
