package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func realModel(t *testing.T, w, h int) (Model, *registry.Registry) {
	t.Helper()
	// The model reads the configuration and the data directory on its own
	// (the switched-on environment, the profiles pane, the theme), so a test
	// that did not point them somewhere would pass or fail with what is in the
	// home directory of whoever runs it.
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg, err := all.Registry(nil)
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

// documentedKeys are the keys the TUI page's table under heading puts in code in
// its first column, one row each: what a reader looking a key up finds.
func documentedKeys(t *testing.T, heading string) map[string]bool {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
	raw, err := os.ReadFile(filepath.Join(dir, "docs", "20-using", "20-tui.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(raw), heading)
	if !found {
		t.Fatalf("docs/20-using/20-tui.md no longer has a section called %q", heading)
	}
	section, _, _ := strings.Cut(after, "\n##")
	documented := map[string]bool{}
	code := regexp.MustCompile("`([^`]+)`")
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
		for _, m := range code.FindAllStringSubmatch(cell, -1) {
			documented[m[1]] = true
		}
	}
	if len(documented) < 5 {
		t.Fatalf("found only %d keys in the table under %q; the test no longer reads it", len(documented), heading)
	}
	return documented
}

// The docs' table of the dashboard's keys named `c` as "Configure" — which is
// the plugin inventory's key, one screen over; on the dashboard it copies a
// tile's value — and left out `f` and `b`, the two keys that open the profiles
// pane and the catalogue. A reader learning the screen from the page was told
// of a key that does something else and not told of two that do.
//
// The footer is what the screen answers, so the table is held to it: every key
// in the footer's entries for the dashboard, the arrows aside, is a row of the
// table.
func TestTheDocsTableOfTheDashboardsKeysHasEveryKeyTheFooterOffers(t *testing.T) {
	documented := documentedKeys(t, "## The landing dashboard")

	m, _ := realModel(t, 200, 40)
	for _, it := range m.footerItems(modeDashboard) {
		if it.typed {
			continue
		}
		for _, key := range strings.Fields(it.display) {
			if strings.ContainsAny(key, "↑↓←→") || documented[key] {
				continue
			}
			t.Errorf("the dashboard offers %q (%s) and the docs' table of its keys does not list it", key, it.label)
		}
	}
	// The arrows stand for a dozen keys in the footer and the table never lists
	// them, but the named keys among them — tab, shift+tab, home, end — are keys a
	// hand has to be told about, and the footer's glyph does not.
	for _, key := range bindSelect.keys {
		if len([]rune(key)) == 1 || slices.Contains([]string{"up", "down", "left", "right"}, key) || documented[key] {
			continue
		}
		t.Errorf("the dashboard selects with %q and the docs' table of its keys does not list it", key)
	}
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
		// The bar holds the letters while it holds the selection, so what
		// moves or hides a tile is not among its keys until a tile is selected.
		wants := []string{"move", "hide", "select", "plugins", "quit"}
		if m.tiles[i].search {
			wants = []string{"select", "plugins", "quit"}
		}
		for _, want := range wants {
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
		// The actions the dashboard actually offers, not every action the
		// capability declares: one whose key a pane key already claims is
		// deliberately absent, because pressing it opens the pane instead.
		// TestNoScreenAdvertisesOneKeyAsTwoVerbs is what holds that line.
		for _, a := range m.offeredTileActions(idx) {
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

// advertisedScreens are the screens whose keys rta writes itself, shared by
// the two tests that hold a footer to what its screen does.
//
// The form-driven modes — modeForm, modeTheme, modeCopyPick,
// modeBrowse-while-filtering — hand every key to huh or to the bubbles list,
// which answer to the whole alphabet by typing it into a field; advertising
// that is not a thing a footer can do or should try to.
func advertisedScreens() map[string]func(*testing.T) (Model, mode) {
	return map[string]func(*testing.T) (Model, mode){
		"dashboard": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			m.selected = 1 // a real tile, so its own actions are in play
			return m, modeDashboard
		},
		// The landing screen before anything is selected: the search line holds
		// the selection, and what it advertises is what it answers, which is not
		// move and not hide (typing.go, grid.go dashOwnItems).
		"search bar": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			return m, modeDashboard
		},
		// A question over the dashboard: modal, so its own few keys and nothing of
		// the screen underneath (dialog.go).
		"dialog": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			return m.openDialog(lockStoreDialog(time.Hour)), modeDashboard
		},
		// A dashboard with sections: the keys that fold are on its bar, and the
		// selection can be on a tile of one or on a header, which is a different
		// screen to the keys — enter folds there, and H is not offered.
		"dashboard with sections": func(t *testing.T) (Model, mode) {
			m := sectioned(t, grouped(), 120, 40)
			m.selected = 1
			return m, modeDashboard
		},
		"dashboard on a header": func(t *testing.T) (Model, mode) {
			m := sectioned(t, grouped(), 120, 40)
			m.selectStop(stop{header: "databases"})
			return m, modeDashboard
		},
		"plugins": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			m.selected = 1
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
		// rta's own key-by-key dispatch with capability-declared and
		// row-conditional keys layered on top of the shared vocabulary —
		// exactly the shape most prone to the drift this test exists to
		// catch (see the "e" key that used to silently shadow "edit
		// inputs" on several capabilities at once). An interactive,
		// at-top result so the row-navigation and capAction branches are
		// actually in play, not skipped.
		"result": func(t *testing.T) (Model, mode) {
			var doneLog []int
			m := listResult(t, listRegistry(t, &doneLog))
			return m, modeResult
		},
		// An answer taller than the pane, as a page and as a list: the pane is a
		// pager and answers to the pager's keys, and a list's cursor to the ends.
		// What they move is the pane's scroll and the cursor's row, which the
		// fingerprint reads.
		"a long result": func(t *testing.T) (Model, mode) {
			return longModel(t, 100, 14, 40), modeResult
		},
		"a long list": func(t *testing.T) (Model, mode) {
			return longList(t, 100, 14, 30), modeResult
		},
		// The two screens where something is answered, against the real
		// catalogue: the capitals beside their lower-case keys, and what each
		// does without a form.
		"queue": func(t *testing.T) (Model, mode) {
			parkCall(t, "note.rm", "2", "would remove note 2")
			return screenOf(t, "agent.pending", nil), modeResult
		},
		"roster": func(t *testing.T) (Model, mode) {
			return twoGrants(t), modeResult
		},
		// The catalogue, whole and with a filter applied: the bubbles list answers
		// to keys of its own (pages, the ends, hjkl) that nothing in rta writes,
		// which is exactly how they went unadvertised. Typing a filter is the one
		// state left out, for the reason above.
		"catalogue": func(t *testing.T) (Model, mode) {
			m, _ := realModel(t, 120, 40)
			m.mode = modeBrowse
			return m, modeBrowse
		},
		"catalogue with a filter applied": func(t *testing.T) (Model, mode) {
			m := filterFor(t, browsing(t), "demo")
			return pump(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}), modeBrowse
		},
		"profilePlugins": func(t *testing.T) (Model, mode) {
			m := profileModel(t, twoProfileConfig())
			m.profileOpen = "staging"
			m.mode = modeProfilePlugins
			return m, modeProfilePlugins
		},
		// The destructive gate: enter runs, e reopens the inputs, esc
		// cancels — reached here as `x` on a list reaches it, with the dry
		// run fed back in as the confirmation body.
		"confirm": func(t *testing.T) (Model, mode) {
			var doneLog []int
			m := listResult(t, listRegistry(t, &doneLog))
			removed, _ := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			rm := removed.(Model)
			shown, _ := rm.Update(previewMsg{cap: rm.current, view: view.Text{Body: "would remove"}, seq: rm.runSeq})
			return shown.(Model), modeConfirm
		},
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
	for name, build := range advertisedScreens() {
		t.Run(name, func(t *testing.T) {
			base, screen := build(t)
			advertised := map[string]bool{}
			typed := false
			for _, it := range base.footerItems(screen) {
				typed = typed || it.typed
				for _, k := range it.keys {
					advertised[k] = true
				}
			}
			if len(advertised) == 0 && !typed {
				t.Fatalf("%s advertises nothing at all", name)
			}
			for _, key := range everyKey() {
				// What a screen that says "type" does with a printable key is
				// type it, which is the whole of what that hint teaches.
				if advertised[key] || (typed && len([]rune(key)) == 1) {
					continue
				}
				// Rebuilt per key: `d` deletes a profile, and a screen that
				// carried the last press's side effects into the next one would
				// be testing something else by the third letter.
				fresh, _ := build(t)
				before := fingerprint(fresh)
				after, cmd := fresh.Update(keyMsg(key))
				// A refusal is a key that did nothing and said why — H on a section's
				// header, where it would take several entries down at once — so it is
				// not an action that has to be taught in the footer.
				done := after.(Model)
				if done.flash != "" && done.flash == done.flashBad {
					done.flash = ""
				}
				if fingerprint(done) == before && cmd == nil {
					continue
				}
				t.Errorf("%s answers to %q but its footer never says so:\n  %s",
					name, key, plain(fitHintBar(0, footerMaxLines, base.footerItems(screen)...)))
			}
		})
	}
}

// The converse of the test above: a key the footer explains twice.
//
// A hint bar can hide a collision when the two entries land eight slots and a
// line-wrap apart, and only one of them runs. The dashboard printed `t
// to-do/note` among the note tile's actions and `t theme` among its own pane
// keys, on two lines of the same bar — and dashboardKeys answers `t` in its
// own switch and returns handled, so the theme form opened and the tile's
// declared action was unreachable. One key, two verbs on screen, one of them
// a lie.
//
// Over the same screens as the advertisement test, because a collision is
// only a collision on a screen that shows both halves.
func TestNoScreenAdvertisesOneKeyAsTwoVerbs(t *testing.T) {
	for name, build := range advertisedScreens() {
		t.Run(name, func(t *testing.T) {
			m, screen := build(t)
			verbs := map[string]string{}
			for _, it := range m.footerItems(screen) {
				for _, k := range it.keys {
					if prev, seen := verbs[k]; seen && prev != it.label {
						t.Errorf("%s advertises %q as both %q and %q:\n  %s",
							name, k, prev, it.label,
							plain(fitHintBar(0, footerMaxLines, m.footerItems(screen)...)))
						continue
					}
					verbs[k] = it.label
				}
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
	out = append(out,
		"[", "]", "<", ">", "/", ":", ".", ",", "-", "=", "?", "!", "@", "#", "$", "%",
		"up", "down", "left", "right", "enter", "esc", "tab", "home", "end", " ", "ctrl+c",
	)
	return out
}

// keyMsg builds the press bubbletea would deliver for a key name.
func keyMsg(key string) tea.KeyPressMsg {
	if code := keyCodeFor(key); code != 0 {
		return tea.KeyPressMsg{Code: code}
	}
	if letter, ok := strings.CutPrefix(key, "ctrl+"); ok && len(letter) == 1 {
		return tea.KeyPressMsg{Code: rune(letter[0]), Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
}

// fingerprint is everything a key press could visibly move. Compared rather
// than inspected: the question is only "did that do anything", and listing
// what each key is allowed to change would be the same drift this test exists
// to catch, written twice.
func fingerprint(m Model) string {
	asked := "-"
	if m.dialog != nil {
		asked = fmt.Sprintf("%d:%d", m.dialog.id, m.dialog.focus)
	}
	return fmt.Sprintf("%v|%d|%d|%d|%d|%d|%d|%q|%v|%v|%v|%v|%d|%q|%d|%q|%v|%v|%q|%v|%s|%d|%v|%d",
		m.mode, m.selected, m.pluginSel, m.pluginScroll, m.profileSel, m.profileScroll,
		m.row, m.query, m.searchEditing, m.form != nil, m.themeForm != nil,
		m.copyPick != nil, len(m.trail), m.flash, m.tickGen, m.armedDelete, m.armed != nil, m.previewing,
		m.header, m.folds, asked, m.list.Index(), m.list.FilterState(), m.viewport.YOffset())
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
		// On a tile, where every one of these keys has something to act on.
		base.selected = 1
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
	base.selected = 1
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
		cap: plugin.Capability{ID: "gen.password", Safety: plugin.Read, Copy: "Password"},
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
	base.selected = 1
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

// Tab is advertised as a way to select a tile, and shift+tab did nothing: a
// hand that reaches for the reverse of a key the footer teaches got silence, and
// the way back to the search bar from the last tile of a long dashboard was one
// arrow at a time. It walks the same stops backwards and ends on the bar.
func TestShiftTabWalksBackWhatTabWalks(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	step := func(m Model, k tea.KeyPressMsg) Model {
		out, _ := m.Update(k)
		return out.(Model)
	}
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	back := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

	one := step(m, tab)
	two := step(one, tab)
	if one.currentStop() == m.currentStop() || two.currentStop() == one.currentStop() {
		t.Fatalf("tab did not move the selection: %v, %v, %v", m.currentStop(), one.currentStop(), two.currentStop())
	}
	if got := step(two, back); got.currentStop() != one.currentStop() {
		t.Errorf("shift+tab from the second stop is on %v, want the first, %v", got.currentStop(), one.currentStop())
	}
	if got := step(one, back); got.selected != 0 {
		t.Errorf("shift+tab from the first stop is on %v, want the search bar", got.currentStop())
	}
	if got := step(m, back); got.selected != 0 {
		t.Errorf("shift+tab on the search bar moved the selection to %v", got.currentStop())
	}
}

// Home and end are the first and the last place the selection rests: what a hand
// reaches for on a dashboard longer than the screen, where the last tile was one
// arrow per row away. They answer from the search bar as tab does, scroll the
// window to the tile they select, and are no letters of a query, so the box that
// holds the keyboard leaves them alone.
func TestHomeAndEndSelectTheFirstAndTheLastTile(t *testing.T) {
	m, _ := realModel(t, 60, 24)
	step := func(m Model, code rune) Model {
		out, _ := m.Update(tea.KeyPressMsg{Code: code})
		return out.(Model)
	}
	if m.selected != 0 || len(m.tiles) < 5 {
		t.Fatalf("the test needs the cold start over several tiles: selected %d of %d", m.selected, len(m.tiles))
	}
	if rows, fit := m.dashRows(), m.dashRowsVisible(); rows <= fit {
		t.Fatalf("all %d rows are on screen, so nothing here scrolls", rows)
	}

	last := step(m, tea.KeyEnd)
	if want := len(m.tiles) - 1; last.selected != want {
		t.Fatalf("end from the search bar selected %d, want the last tile, %d", last.selected, want)
	}
	if row, _ := last.selectedRow(); row < last.scroll || row >= last.scroll+last.dashRowsVisible() {
		t.Errorf("the last tile is on row %d and the window shows the %d from row %d", row, last.dashRowsVisible(), last.scroll)
	}
	if again := step(last, tea.KeyEnd); again.selected != last.selected || again.scroll != last.scroll {
		t.Errorf("end on the last tile moved the selection to %d, scroll %d", again.selected, again.scroll)
	}

	first := step(last, tea.KeyHome)
	if first.selected != 1 || first.scroll != 0 {
		t.Errorf("home from the last tile selected %d with scroll %d, want the first tile with the window at the top", first.selected, first.scroll)
	}
	middle := step(step(m, tea.KeyDown), tea.KeyDown)
	if got := step(middle, tea.KeyEnd); got.selected != len(m.tiles)-1 {
		t.Errorf("end from tile %d selected %d, want the last", middle.selected, got.selected)
	}

	typing := m
	typing.searchEditing, typing.query = true, "ab"
	for _, code := range []rune{tea.KeyHome, tea.KeyEnd} {
		if got := step(typing, code); got.selected != 0 || got.query != "ab" || !got.searchEditing {
			t.Errorf("a key that is no letter changed the search being typed: selected %d, query %q, editing %v", got.selected, got.query, got.searchEditing)
		}
	}
}

// On a dashboard that groups its tiles the places the selection rests include
// the headers, and a folded section at the end holds no tile to rest on.
func TestHomeAndEndRestOnTheHeadersOfSectionsAtTheEdges(t *testing.T) {
	dash := config.Dashboard{Tiles: []config.Tile{
		{ID: "alpha.status", Section: "databases"}, {ID: "beta.status", Section: "databases"}, {ID: "gamma.status", Section: "edge"},
	}}
	m := sectioned(t, dash, 120, 40)
	step := func(m Model, code rune) Model {
		out, _ := m.Update(tea.KeyPressMsg{Code: code})
		return out.(Model)
	}
	if got := step(m, tea.KeyEnd); got.selected != len(m.tiles)-1 {
		t.Errorf("end selected %d with every section open, want the last tile, %d", got.selected, len(m.tiles)-1)
	}
	folded := press(t, step(m, tea.KeyEnd), "Z")
	if end := step(folded, tea.KeyEnd); end.selected != -1 || end.header != "edge" {
		t.Errorf("end with every section folded rests on tile %d, header %q, want the header of the last section", end.selected, end.header)
	}
	if home := step(folded, tea.KeyHome); home.selected != -1 || home.header != "databases" {
		t.Errorf("home rests on tile %d, header %q, want the header of the first section", home.selected, home.header)
	}
}

func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	var msg tea.KeyPressMsg
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
	case "home":
		return tea.KeyHome
	case "end":
		return tea.KeyEnd
	case "pgup":
		return tea.KeyPgUp
	case "pgdown":
		return tea.KeyPgDown
	}
	return 0
}
