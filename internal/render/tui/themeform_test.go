package tui

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// resetTheme restores the built-in palette after a test that calls
// theme.Apply. Package state shared across every test in this binary,
// including panel_test.go's assertions about Label's exact relationship to
// Primary and the status colors — a theme test that leaves an override in
// place would make those fail depending on what ran before them, which is
// exactly the kind of order-dependent failure this exists to rule out.
func resetTheme(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { theme.Apply(nil) })
}

// The order this form asks fields in is a separate list from
// theme.Fields()'s alphabetical one, kept only for presentation — and a
// separate list is exactly the kind of thing that silently stops matching
// the source it was copied from.
func TestThemeFieldOrderNamesExactlyWhatThemeFieldsDoes(t *testing.T) {
	got := append([]string{}, themeFieldOrder...)
	sort.Strings(got)
	want := theme.Fields()
	if len(got) != len(want) {
		t.Fatalf("themeFieldOrder has %d fields, theme.Fields() has %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("themeFieldOrder sorted = %v, theme.Fields() = %v", got, want)
			break
		}
	}
}

// The property that makes seeding safe: an untouched form must not write
// every built-in into the config file as though the operator had chosen it.
// theme.Current() always answers every field, built-in or not, so seeding
// from it would start every box non-empty — this checks the form is seeded
// from what is actually on disk instead.
func TestThemeFormStartsEmptyForAFieldWithNoStoredOverride(t *testing.T) {
	resetTheme(t)
	theme.Apply(map[string]string{"primary": "#ABCDEF"}) // live, but not on disk
	tf := newThemeForm(map[string]string{})              // nothing stored
	if got := *tf.bindings["primary"]; got != "" {
		t.Errorf("primary binding = %q, want empty — seeded from the live palette instead of disk", got)
	}
}

func TestThemeFormSeedsWhatIsActuallyStored(t *testing.T) {
	resetTheme(t)
	tf := newThemeForm(map[string]string{"primary": "#123456"})
	if got := *tf.bindings["primary"]; got != "#123456" {
		t.Errorf("primary binding = %q, want #123456", got)
	}
	if got := *tf.bindings["accent"]; got != "" {
		t.Errorf("accent binding = %q, want empty — nothing stored for it", got)
	}
}

func TestThemeOverridesCollectsOnlyNonEmptyFields(t *testing.T) {
	tf := newThemeForm(map[string]string{"primary": "#123456"})
	*tf.bindings["good"] = "  #00FF00  " // whitespace, as a person might leave it
	got := tf.overrides()
	want := map[string]string{"primary": "#123456", "good": "#00FF00"}
	if len(got) != len(want) {
		t.Fatalf("overrides = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("overrides[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestHexOrEmptyAcceptsEmptyAndRejectsGarbage(t *testing.T) {
	for _, ok := range []string{"", "  ", "#000000", "#ABCDEF"} {
		if err := hexOrEmpty(ok); err != nil {
			t.Errorf("hexOrEmpty(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"red", "#GGGGGG", "#12345", "000000"} {
		if err := hexOrEmpty(bad); err == nil {
			t.Errorf("hexOrEmpty(%q) = nil, want an error", bad)
		}
	}
}

// The full path a submit takes: written to the file an operator would open,
// and applied to this process immediately rather than waiting for a
// restart.
func TestSaveThemeWritesToDiskAndAppliesLive(t *testing.T) {
	resetTheme(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	m := New(testRegistry(t), config.Dashboard{}, nil)
	m.themeForm = newThemeForm(map[string]string{})
	*m.themeForm.bindings["primary"] = "#654321"

	next, _ := m.saveTheme()
	nm := next.(Model)

	if nm.mode != modeDashboard {
		t.Errorf("mode = %v, want modeDashboard", nm.mode)
	}
	if nm.themeForm != nil {
		t.Error("themeForm still set after saving")
	}
	if nm.flash == "" {
		t.Error("no flash message after saving")
	}

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Theme["primary"] != "#654321" {
		t.Errorf("on disk: theme.primary = %q, want #654321", onDisk.Theme["primary"])
	}
	if got := theme.Current()["primary"]; got != "#654321" {
		t.Errorf("theme.Current()[primary] = %q, want #654321 — saveTheme must apply live, "+
			"not just write the file", got)
	}
}

// A malformed value never reaches Apply as a crash or a silently-dropped
// field — it is reported, in the flash, and the field it named keeps its
// built-in. saveTheme itself never validates; this is checking that when
// something invalid does get through to it (a config file edited by hand
// between opening the form and submitting it, say), the operator is told.
func TestSaveThemeReportsWhatApplyCouldNotHonour(t *testing.T) {
	resetTheme(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))

	m := New(testRegistry(t), config.Dashboard{}, nil)
	m.themeForm = newThemeForm(map[string]string{})
	*m.themeForm.bindings["primary"] = "definitely-not-hex"

	next, _ := m.saveTheme()
	nm := next.(Model)
	if nm.flash == "" {
		t.Fatal("no flash message")
	}
	// theme.Apply's own tests cover the fallback in detail (apply_test.go);
	// what this test owns is that saveTheme surfaces the problem rather than
	// swallowing it, and does not let the malformed string itself reach the
	// live palette.
	if got := theme.Current()["primary"]; got == "definitely-not-hex" {
		t.Errorf("an invalid value reached the live palette: %q", got)
	}
}

// The box being typed into is never what a short window cuts off. The swatch
// strip took three lines at sixty columns, so under fourteen rows the first
// field's box fell below the panel and the editor showed a palette and nothing
// to type into.
func TestTheThemeBoxIsVisibleOnAShortTerminal(t *testing.T) {
	resetTheme(t)
	for _, height := range []int{20, 14, 13, 12, 10, 9} {
		m := New(registry.New(), config.Dashboard{}, nil)
		m.width, m.height = 60, height
		m.themeForm, m.mode = newThemeForm(nil), modeTheme
		m.themeForm.form.Init()
		m.fitThemeForm()
		out := plain(m.themeView())
		if !strings.Contains(out, "┃ >") {
			t.Errorf("height %d: the focused box is not on screen:\n%s", height, out)
		}
		if got := strings.Count(out, "\n") + 1; got > height {
			t.Errorf("height %d: the editor is %d lines tall", height, got)
		}
	}
}

// A swatch and its name are one unit however narrow the terminal: a plain
// soft wrap broke the strip between "████" and the word beside it, leaving a
// line that began with a name and read as the swatch of the line above.
func TestThePreviewStripNeverPartsASwatchFromItsName(t *testing.T) {
	resetTheme(t)
	for _, width := range []int{30, 40, 60, 200} {
		m := New(registry.New(), config.Dashboard{}, nil)
		m.width, m.height = width, 40
		m.themeForm, m.mode = newThemeForm(nil), modeTheme
		m.fitThemeForm()
		rows := strings.Split(plain(m.themeView()), "\n")
		seen := 0
		// The strip is the first block of the panel's body, after its blank
		// first line and up to the next blank one.
		for _, row := range rows[2:] {
			row = strings.TrimSuffix(strings.TrimPrefix(row, "│"), "│")
			words := strings.Fields(row)
			if len(words) == 0 {
				break
			}
			if len(words)%2 != 0 {
				t.Errorf("width %d: line %q does not hold whole swatch-and-name pairs", width, row)
				continue
			}
			for i := 0; i < len(words); i += 2 {
				if words[i] != "████" && words[i] != "××××" {
					t.Errorf("width %d: line %q has %q where a swatch belongs", width, row, words[i])
				}
				seen++
			}
		}
		if seen != len(themeFieldOrder) {
			t.Errorf("width %d: %d swatches shown, want %d", width, seen, len(themeFieldOrder))
		}
	}
}
