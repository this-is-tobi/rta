package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// The theme editor has one box for every key of the `theme:` block the operator
// can state, and `status` is the one that takes a word: the three ways to draw the
// colour that grades, completed by tab like a colour, and saved with the colours
// beside it. Only the operator's own block has it.

func TestTheOperatorsOwnEditorEndsOnAStatusBoxThatTakesTheThreeModes(t *testing.T) {
	resetTheme(t)
	tf := newThemeForm(globalTarget(map[string]string{}))
	fields := globalTarget(nil).fields()
	if last := fields[len(fields)-1]; last != theme.GradesKey {
		t.Fatalf("the editor's last box is %q, want %q", last, theme.GradesKey)
	}
	if _, ok := tf.bindings[theme.GradesKey]; !ok {
		t.Fatal("the editor has no box for status")
	}
	if got := tf.offers[theme.GradesKey]; strings.Join(got, ",") != "all,alerts,off" {
		t.Errorf("tab on the status box completes %v, want the three modes", got)
	}
	if got := *tf.bindings[theme.GradesKey]; got != "" {
		t.Errorf("the status box starts as %q with nothing stored: an empty box is no override", got)
	}
	// What the box says it is "currently" is what the place would draw.
	if now, _ := tf.target.palette(nil); now[theme.GradesKey] != "all" {
		t.Errorf("the editor says status is %q until something states it, want all", now[theme.GradesKey])
	}
	if help := fieldHelp(theme.GradesKey); !strings.Contains(help, "all, alerts") || !strings.Contains(help, "off") {
		t.Errorf("the line under the box does not name the modes: %q", help)
	}
	for _, mode := range append(theme.GradeModes(), "") {
		if err := tf.validators[theme.GradesKey](mode); err != nil {
			t.Errorf("the box refuses %q: %v", mode, err)
		}
	}
	for _, bad := range []string{"loud", "Off", "#112233", "false"} {
		err := tf.validators[theme.GradesKey](bad)
		if err == nil || !strings.Contains(err.Error(), "all, alerts, off") {
			t.Errorf("the box accepts %q, or refuses it without the modes: %v", bad, err)
		}
	}
	if err := tf.validators["accent"]("alerts"); err == nil {
		t.Error("a colour box accepts a mode")
	}
}

// The preview is the palette's colours and a word is not one: no swatch is drawn
// for it, and a box that is empty or holds a word costs the strip nothing.
func TestTheStatusBoxIsNoSwatchInThePreview(t *testing.T) {
	resetTheme(t)
	tf := newThemeForm(globalTarget(map[string]string{"status": "alerts"}))
	strip := plain(tf.preview(200))
	if strings.Contains(strip, theme.GradesKey) {
		t.Errorf("the preview strip draws status:\n%s", strip)
	}
	if got := strings.Count(strip, "████"); got != len(themeFieldOrder) {
		t.Errorf("%d swatches in the strip, want one per colour (%d):\n%s", got, len(themeFieldOrder), strip)
	}
	if now, _ := tf.target.palette(tf.target.stored); now[theme.GradesKey] != "alerts" {
		t.Errorf("the editor says status is %q with alerts stored, want alerts", now[theme.GradesKey])
	}
}

// A profile's editor and the editor of a file nobody named offer no such box: an
// environment cannot decide how failures are drawn on the operator's screen.
func TestOnlyTheOperatorsOwnBlockIsOfferedTheStatusBox(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	staging := editorOn(t, m, "staging").themeForm
	if _, ok := staging.bindings[theme.GradesKey]; ok {
		t.Error("a profile's editor has a box for status")
	}
	if _, ok := staging.inputs[theme.GradesKey]; ok {
		t.Error("a profile's editor draws a field for status")
	}
	if _, ok := editorOn(t, m, "").themeForm.bindings[theme.GradesKey]; !ok {
		t.Error("the operator's own editor has no box for status")
	}
	if fields := (themeTarget{guarded: true}).fields(); len(fields) != 5 {
		t.Errorf("a guarded block is offered %v, want the five text colours", fields)
	}
}

// The word is saved beside the colours, applied at once, and changing a colour
// does not take it away.
func TestSavingCarriesTheStatusWordWithTheColoursBesideIt(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m = editorOn(t, m, "")
	*m.themeForm.bindings["accent"] = "#6688AA"
	*m.themeForm.bindings[theme.GradesKey] = "alerts"
	next, _ := m.saveTheme()
	nm := next.(Model)

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Theme[theme.GradesKey] != "alerts" || onDisk.Theme["accent"] != "#6688AA" {
		t.Errorf("on disk the theme is %v, want the colour and the word", onDisk.Theme)
	}
	if theme.Grading() != theme.GradesAlerts {
		t.Errorf("status is drawn %s after the save, want alerts at once", theme.Grading())
	}
	if !strings.Contains(nm.flash, "saved 2 theme overrides") {
		t.Errorf("flash = %q", nm.flash)
	}

	// And again, opened on what was stored: an untouched form writes the same block.
	m = editorOn(t, nm, "")
	if got := *m.themeForm.bindings[theme.GradesKey]; got != "alerts" {
		t.Fatalf("the box is seeded with %q, want what is stored", got)
	}
	before := fileText(t)
	next, _ = m.saveTheme()
	if after := fileText(t); after != before {
		t.Errorf("an untouched form changed the file:\n%s\n--- to ---\n%s", before, after)
	}
	if next.(Model).themeForm != nil || theme.Grading() != theme.GradesAlerts {
		t.Errorf("status is drawn %s after an untouched save, want alerts", theme.Grading())
	}

	// Emptying the box is taking the line out, and the colours stay.
	m = editorOn(t, next.(Model), "")
	*m.themeForm.bindings[theme.GradesKey] = ""
	_, _ = m.saveTheme()
	onDisk, err = config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, has := onDisk.Theme[theme.GradesKey]; has || onDisk.Theme["accent"] != "#6688AA" {
		t.Errorf("after emptying the box the theme is %v, want the colour and no status", onDisk.Theme)
	}
	if theme.Grading() != theme.GradesAll {
		t.Errorf("status is drawn %s after taking it out, want all", theme.Grading())
	}
}

// A colour saved from the editor of a file that states the word by hand must not
// delete it: the editor writes the block it holds, and the block holds the word.
func TestSavingAColourKeepsAStatusStatedInTheFile(t *testing.T) {
	resetTheme(t)
	cfg := twoProfileConfig()
	cfg.Theme = map[string]string{"status": "off", "faint": "#00aa66"}
	m := profileModel(t, cfg)
	m = editorOn(t, m, "")
	*m.themeForm.bindings["label"] = "#C792EA"
	m.saveTheme()
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Theme["status"] != "off" || onDisk.Theme["label"] != "#C792EA" || onDisk.Theme["faint"] != "#00aa66" {
		t.Errorf("the theme on disk is %v: saving a colour lost the status or a colour", onDisk.Theme)
	}
}

// A profile's block that carries the word by hand has no box for it, and the save
// leaves it behind and says so, as it does a status colour.
func TestSavingAProfilesThemeDropsAStatusWordItHasNoBoxFor(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	if err := config.Mutate(func(c config.Config) (config.Config, bool) {
		p := c.Profiles["staging"]
		p.Theme = map[string]string{"faint": "#00aa66", "status": "off"}
		c.Profiles["staging"] = p
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	m = editorOn(t, m, "staging")
	*m.themeForm.bindings["label"] = "#C792EA"
	next, _ := m.saveTheme()
	nm := next.(Model)
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Theme; len(got) != 2 || got["faint"] != "#00aa66" || got["label"] != "#C792EA" {
		t.Errorf("staging's theme = %v, want the two colours the editor holds and no status", got)
	}
	if !strings.Contains(nm.flash, "dropped status") || !strings.Contains(nm.flash, "your own config file") {
		t.Errorf("flash = %q, want it to say the word went and whose it is", nm.flash)
	}
}
