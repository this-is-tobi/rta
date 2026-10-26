package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// The dashboard's refresh chain re-arms only while the dashboard is up, so the
// tick that fell due while the theme screen was open was dropped, and every way
// back but a save that worked only set the mode: no tile refreshed again, the
// switch was not re-read, and the tiles went dim for good. Every way off the
// screen starts the chain again, as every other screen's way back does.

func TestEveryWayOffTheThemeScreenRestartsTheDashboardRefresh(t *testing.T) {
	onEditor := func(t *testing.T, m Model) Model {
		m = editorOn(t, m, "")
		m.themeForm.form.Init()
		return m
	}
	ways := []struct {
		name  string
		leave func(t *testing.T, m Model) (tea.Model, tea.Cmd)
	}{
		{"esc on the picker", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			return m.Update(keyMsg("esc"))
		}},
		{"the picker aborted", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			m.themePick.form.State = huh.StateAborted
			return m.afterThemePickUpdate(nil)
		}},
		{"esc on an untouched editor", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			return onEditor(t, m).Update(keyMsg("esc"))
		}},
		{"the editor aborted", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			m = onEditor(t, m)
			m.themeForm.form.State = huh.StateAborted
			return m.afterThemeFormUpdate(nil)
		}},
		{"discarding what was typed", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			asked := press(t, strike(onEditor(t, m), "#123456"), "esc")
			if asked.dialog == nil {
				t.Fatal("esc with a colour typed did not ask")
			}
			return asked.Update(keyMsg("y"))
		}},
		{"a save refused for a profile that went", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			m = editorOn(t, m, "prod")
			if err := config.Mutate(func(c config.Config) (config.Config, bool) {
				delete(c.Profiles, "prod")
				return c, true
			}); err != nil {
				t.Fatal(err)
			}
			return m.fastSubmitThemeForm()
		}},
		{"a profile that went before the editor opened", func(t *testing.T, m Model) (tea.Model, tea.Cmd) {
			return m.openThemeEditor("gone")
		}},
	}
	for _, way := range ways {
		t.Run(way.name, func(t *testing.T) {
			resetTheme(t)
			m := openTheme(t, profileModel(t, themedProfiles()))
			if m.themePick == nil {
				t.Fatal("the theme screen did not open on its picker")
			}
			gen := m.tickGen
			dropped, cmd := m.Update(tickMsg{gen: gen})
			if cmd != nil {
				t.Fatal("a tick re-armed the chain under the theme screen")
			}
			next, cmd := way.leave(t, dropped.(Model))
			back := next.(Model)
			if back.mode != modeDashboard || back.themePick != nil || back.themeForm != nil {
				t.Fatalf("mode %v, picker %v, editor %v: not back on the dashboard",
					back.mode, back.themePick != nil, back.themeForm != nil)
			}
			if back.tickGen == gen || cmd == nil {
				t.Errorf("back on the dashboard with tickGen %d (was %d) and a command %v: the refresh chain was not restarted",
					back.tickGen, gen, cmd != nil)
			}
		})
	}
}
