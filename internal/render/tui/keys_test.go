package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/builtin/all"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func realModel(t *testing.T, w, h int) (Model, *registry.Registry) {
	t.Helper()
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return um.(Model), reg
}

// footerOf returns the hint bar, which may occupy more than one line.
func footerOf(view string, lines int) string {
	all := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if lines > len(all) {
		lines = len(all)
	}
	return plain(strings.Join(all[len(all)-lines:], "\n"))
}

// The bug this pins down, in the reporter's words: "why don't all tiles get
// the [ ] move?". Nothing about moving differed between tiles — the footer
// measured itself whole, and any tile carrying actions of its own pushed it
// over, at which point the old code swapped the entire navigation half for a
// hardcoded three-hint stub. Five hints vanished at once, on a terminal with
// columns to spare.
func TestEveryTileOffersTheSameNavigation(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	if len(m.tiles) < 3 {
		t.Fatalf("need several tiles to compare, got %d", len(m.tiles))
	}
	for i := range m.tiles {
		m.selected = i
		footer := footerOf(m.dashboardView(), footerMaxLines)
		id := "search"
		if !m.tiles[i].search {
			id = m.tiles[i].cap.ID
		}
		for _, want := range []string{"move", "hide", "select", "plugins", "quit"} {
			if !strings.Contains(footer, want) {
				t.Errorf("tile %s: footer is missing %q\n  %s", id, want, footer)
			}
		}
	}
}

// A tile's own actions are the one thing on the screen that cannot be
// learned from any other screen, so they must be the last thing dropped.
func TestATilesOwnActionsSurviveANarrowFooter(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	idx := -1
	for i, ti := range m.tiles {
		if len(ti.actions) > 2 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Skip("no tile with several actions")
	}
	m.selected = idx
	for _, width := range []int{200, 120, 90, 60, 40} {
		m.width = width
		footer := footerOf(m.dashboardView(), footerMaxLines)
		for _, a := range m.tiles[idx].actions {
			if a.key == "enter" {
				continue
			}
			if !strings.Contains(footer, a.label) {
				t.Errorf("width %d: action %q (%s) was dropped before navigation\n  %s",
					width, a.label, a.key, footer)
			}
		}
	}
}

// A footer that runs past the edge of the terminal is the same class of bug
// as one that gives up too early: what it says is not what the reader sees.
func TestNoFooterOverflowsTheTerminal(t *testing.T) {
	for _, width := range []int{200, 120, 100, 80, 60, 40} {
		m, _ := realModel(t, width, 40)
		for i := range m.tiles {
			m.selected = i
			if got := lipgloss.Width(footerOf(m.dashboardView(), footerMaxLines)); got > width {
				t.Errorf("dashboard tile %d at width %d: footer is %d cells", i, width, got)
			}
		}
		if got := lipgloss.Width(footerOf(m.pluginsView(), footerMaxLines)); got > width {
			t.Errorf("plugins at width %d: footer is %d cells", width, got)
		}
	}
}

// When a hint genuinely cannot fit, the bar has to admit it. A footer that
// silently shows less than the truth is how a key becomes undiscoverable —
// which is the whole failure this file exists to prevent.
func TestATruncatedFooterSaysSo(t *testing.T) {
	items := []hintItem{
		action("a", "add"), item(bindSelect), item(bindMove), item(bindHide),
		item(bindPlugin), item(bindBrowse), item(bindSearch), item(bindQuit),
	}
	full := plain(fitHintBar(0, footerMaxLines, items...))
	if strings.Contains(full, "…") {
		t.Errorf("an unconstrained bar must not claim truncation: %s", full)
	}
	// Two lines of 30 cells cannot hold eight hints, so something has to go —
	// and the bar has to say that it did.
	short := plain(fitHintBar(30, footerMaxLines, items...))
	if !strings.Contains(short, "…") {
		t.Errorf("a truncated bar must end in an ellipsis: %s", short)
	}
	if !strings.Contains(short, "add") {
		t.Errorf("the tile's own action should outlast generic navigation: %s", short)
	}
	// The same hints in a bar allowed to wrap keep everything, on two lines.
	wrapped := plain(fitHintBar(70, footerMaxLines, items...))
	if strings.Contains(wrapped, "…") {
		t.Errorf("two lines of 70 cells should hold every hint: %q", wrapped)
	}
	if got := strings.Count(wrapped, "\n") + 1; got != 2 {
		t.Errorf("wrapped bar took %d lines, want 2: %q", got, wrapped)
	}
}

// The reported bug, in the reporter's words: "I don't see the profile action".
//
// `f` opened the profiles pane from the day it was written and the dashboard's
// footer never learned to say so, because each screen built its own bar at the
// place it painted it and a new pane touched neither. A key nobody can see is a
// key nobody has.
//
// So this is the general form rather than an assertion about `f`: put the model
// in a screen, press everything a keyboard offers, and fail on anything that
// does something the footer never mentions. The next pane to arrive gets the
// same treatment for free.
func TestEveryKeyAScreenAnswersToIsAdvertised(t *testing.T) {
	// Only the screens whose keys rta writes itself. The form-driven modes —
	// modeForm, modeTheme, modeCopyPick, modeBrowse-while-filtering — hand
	// every key to huh or to the bubbles list, which answer to the whole
	// alphabet by typing it into a field; advertising that is not a thing a
	// footer can do or should try to.
	screens := map[string]func(*testing.T) (Model, mode){
		"dashboard": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			m.selected = 1 // a real tile, so its own actions are in play
			return m, modeDashboard
		},
		"plugins": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			return press(t, m, "p"), modePlugins
		},
		"profiles": func(t *testing.T) (Model, mode) {
			m := profileModel(t, twoProfileConfig())
			m.mode = modeProfiles
			return m, modeProfiles
		},
		"running": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			m.mode = modeRunning
			return m, modeRunning
		},
	}
	for name, build := range screens {
		t.Run(name, func(t *testing.T) {
			base, screen := build(t)
			advertised := map[string]bool{}
			for _, it := range base.footerItems(screen) {
				for _, k := range it.keys {
					advertised[k] = true
				}
			}
			if len(advertised) == 0 {
				t.Fatalf("%s advertises nothing at all", name)
			}
			for _, key := range everyKey() {
				if advertised[key] {
					continue
				}
				// Rebuilt per key: `d` deletes a profile, and a screen that
				// carried the last press's side effects into the next one would
				// be testing something else by the third letter.
				fresh, _ := build(t)
				before := fingerprint(fresh)
				after, cmd := fresh.Update(keyMsg(key))
				if fingerprint(after.(Model)) == before && cmd == nil {
					continue
				}
				t.Errorf("%s answers to %q but its footer never says so:\n  %s",
					name, key, plain(fitHintBar(0, footerMaxLines, base.footerItems(screen)...)))
			}
		})
	}
}

// everyKey is what a person can press without contorting: the alphabet in both
// cases, the punctuation this app has ever bound, and the named keys.
func everyKey() []string {
	var out []string
	for c := 'a'; c <= 'z'; c++ {
		out = append(out, string(c), strings.ToUpper(string(c)))
	}
	for _, k := range []string{
		"[", "]", "<", ">", "/", ":", ".", ",", "-", "=", "?", "!", "@", "#", "$", "%",
		"up", "down", "left", "right", "enter", "esc", "tab", " ", "ctrl+c",
	} {
		out = append(out, k)
	}
	return out
}

// keyMsg builds the press bubbletea would deliver for a key name.
func keyMsg(key string) tea.KeyPressMsg {
	if code := keyCodeFor(key); code != 0 {
		return tea.KeyPressMsg{Code: code}
	}
	if key == "ctrl+c" {
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
}

// fingerprint is everything a key press could visibly move. Compared rather
// than inspected: the question is only "did that do anything", and listing
// what each key is allowed to change would be the same drift this test exists
// to catch, written twice.
func fingerprint(m Model) string {
	return fmt.Sprintf("%v|%d|%d|%d|%d|%d|%d|%q|%v|%v|%v|%v|%d|%q|%d",
		m.mode, m.selected, m.pluginSel, m.pluginScroll, m.profileSel, m.profileScroll,
		m.row, m.query, m.searchEditing, m.form != nil, m.themeForm != nil,
		m.copyPick != nil, len(m.trail), m.flash, m.tickGen)
}

// One idea, one word, one notation — everywhere. Three spellings of "the
// arrow keys move the cursor" (↑↓←→, ↑↓, ↑/↓) is the kind of small
// incoherence that makes a person check the documentation for something they
// already knew.
func TestOneNotationForMovingACursor(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	seen := map[string]bool{}
	collect := func(footer string) {
		for _, f := range strings.Fields(footer) {
			if strings.ContainsAny(f, "↑↓←→") {
				seen[f] = true
			}
		}
	}
	collect(footerOf(m.dashboardView(), footerMaxLines))
	collect(footerOf(m.pluginsView(), footerMaxLines))
	for glyph := range seen {
		if glyph != bindSelect.display && glyph != bindScroll.display {
			t.Errorf("a third arrow notation appeared: %q (vocabulary has %q and %q)",
				glyph, bindSelect.display, bindScroll.display)
		}
	}
}

// The vocabulary is only worth having if it is unambiguous: two bindings
// answering to the same key would make "what does this key do" depend on
// which switch statement happened to see it first.
func TestNoTwoBindingsClaimTheSameKeyWithDifferentWords(t *testing.T) {
	all := map[string]binding{
		"quit": bindQuit, "back": bindBack, "open": bindOpen, "rerun": bindRerun,
		"edit": bindEdit, "copy": bindCopy, "move": bindMove, "hide": bindHide,
		"plugins": bindPlugin, "browse": bindBrowse, "search": bindSearch,
		"toggle": bindToggle,
	}
	owner := map[string]string{}
	for name, b := range all {
		if b.display == "" || b.label == "" {
			t.Errorf("%s has no display or label", name)
		}
		for _, k := range b.keys {
			if prev, dup := owner[k]; dup {
				t.Errorf("key %q is claimed by both %s and %s", k, prev, name)
			}
			owner[k] = name
		}
	}
}

// Every alias the vocabulary advertises has to actually work, or the table
// is documentation that lies. These are the habits people arrive with — vim
// motions, tab, the shifted bracket keys — and they were all implemented
// without any screen or comment saying so.
func TestDeclaredAliasesActuallyDoTheSameThing(t *testing.T) {
	cases := []struct {
		name     string
		primary  string
		aliases  []string
		observe  func(Model) any
		setupNav bool
	}{
		{
			name: "move selection right", primary: "right", aliases: []string{"l", "tab"},
			observe: func(m Model) any { return m.selected },
		},
		{
			name: "move selection down", primary: "down", aliases: []string{"j"},
			observe: func(m Model) any { return m.selected },
		},
		{
			name: "open the catalogue", primary: "b", aliases: []string{":"},
			observe: func(m Model) any { return m.mode },
		},
	}
	for _, tc := range cases {
		base, _ := realModel(t, 120, 40)
		want := tc.observe(press(t, base, tc.primary))
		for _, alias := range tc.aliases {
			if got := tc.observe(press(t, base, alias)); got != want {
				t.Errorf("%s: %q gave %v but its alias %q gave %v",
					tc.name, tc.primary, want, alias, got)
			}
		}
	}
}

// Leaving works the same way from every screen that has somewhere to go
// back to. A person who learns esc on one screen must not have to re-learn
// it on the next.
func TestEscapeGoesBackFromEveryScreenThatHasABack(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	plugins := press(t, base, "p")
	if plugins.mode != modePlugins {
		t.Fatalf("p did not open the plugin inventory: %v", plugins.mode)
	}
	if back := press(t, plugins, "esc"); back.mode != modeDashboard {
		t.Errorf("esc from plugins went to %v, want the dashboard", back.mode)
	}
	browse := press(t, base, "b")
	if browse.mode != modeBrowse {
		t.Fatalf("b did not open the catalogue: %v", browse.mode)
	}
	if back := press(t, browse, "esc"); back.mode != modeDashboard {
		t.Errorf("esc from the catalogue went to %v, want the dashboard", back.mode)
	}
	th := press(t, base, "t")
	if th.mode != modeTheme {
		t.Fatalf("t did not open the theme editor: %v", th.mode)
	}
	if back := press(t, th, "esc"); back.mode != modeDashboard {
		t.Errorf("esc from the theme editor went to %v, want the dashboard", back.mode)
	}

	// The copy picker returns to the result it was opened from, not the
	// dashboard — unlike the screens above, it is reached from modeResult,
	// never from base directly.
	withResult, _ := base.Update(resultMsg{
		cap: plugin.Capability{ID: "gen.password", Safety: plugin.Read},
		view: view.Table{
			Columns: []view.Column{{Name: "Password"}},
			Rows:    [][]string{{"a"}, {"b"}},
		},
	})
	pick := press(t, withResult.(Model), "c")
	if pick.mode != modeCopyPick {
		t.Fatalf("c did not open the copy picker: %v", pick.mode)
	}
	if back := press(t, pick, "esc"); back.mode != modeResult {
		t.Errorf("esc from the copy picker went to %v, want the result it was opened from", back.mode)
	}
}

// q quits from everywhere, because the one key nobody should have to look
// up is the one that gets you out.
func TestQuitWorksFromEveryScreen(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	screens := map[string]Model{
		"dashboard": base,
		"plugins":   press(t, base, "p"),
		"browse":    press(t, base, "b"),
	}
	for name, m := range screens {
		if _, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil {
			t.Errorf("q on the %s screen did not quit", name)
		}
	}
}

func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	msg := tea.KeyPressMsg{Text: key}
	switch key {
	case "up", "down", "left", "right", "enter", "esc", "tab":
		msg = tea.KeyPressMsg{Code: keyCodeFor(key)}
	default:
		msg = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
	out, _ := m.Update(msg)
	return out.(Model)
}

func keyCodeFor(name string) rune {
	switch name {
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "left":
		return tea.KeyLeft
	case "right":
		return tea.KeyRight
	case "enter":
		return tea.KeyEnter
	case "esc":
		return tea.KeyEscape
	case "tab":
		return tea.KeyTab
	}
	return 0
}
