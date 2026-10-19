package theme_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/render/theme/themetest"
)

// reset puts the built-in palette back after a test that applied something.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { theme.Apply(nil) })
}

// What each kind of grade is drawn in under each way of drawing status, read from
// the escape sequence a terminal would receive and not from the style that
// produced it: a table of the colour a word is in, where "" is a word drawn in
// none. A style compared with the style the same code chose would pass whatever
// the code chose.
func TestEveryGradeIsDrawnInTheColourItsSettingGivesIt(t *testing.T) {
	green := themetest.ColourOf(theme.GoodText)
	amber := themetest.ColourOf(theme.WarnText)
	red := themetest.ColourOf(theme.BadText)
	grey := themetest.ColourOf(theme.Subtle)
	for _, c := range []string{green, amber, red, grey} {
		if c == "" {
			t.Fatal("a palette colour is drawn in no colour, which would make every row below true")
		}
	}
	if green == amber || amber == red || green == red {
		t.Fatalf("the three grades are not three colours: %s %s %s", green, amber, red)
	}

	// A word of each kind, and a usage cell of each band: the two classifiers the
	// package hands styles out for.
	words := map[string]theme.StatusKind{
		"ok": theme.StatusGood, "warning": theme.StatusWarn, "failed": theme.StatusBad,
		"done": theme.StatusMuted, "anything else": theme.StatusNeutral,
	}
	usage := map[string]theme.StatusKind{
		"31%": theme.StatusGood, "84%": theme.StatusWarn, "96%": theme.StatusBad, "n/a": theme.StatusNeutral,
	}

	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for word, kind := range words {
			want := themetest.Drawn(mode, kind)
			if got := themetest.ColourBefore(t, theme.StatusStyle(word).Render(word), word); got != want {
				t.Errorf("status word %q is drawn in %q, want %q", word, got, want)
			}
			if got, drawn := theme.Coloured(kind), want != "" && kind != theme.StatusMuted; got != drawn {
				t.Errorf("Coloured(%v) = %v under %s, but the word is drawn in %q", kind, got, mode, want)
			}
		}
		for cell, kind := range usage {
			if got, want := themetest.ColourBefore(t, theme.UsageStyle(cell).Render(cell), cell), themetest.Drawn(mode, kind); got != want {
				t.Errorf("usage cell %q is drawn in %q, want %q", cell, got, want)
			}
		}
	})
}

// A safety or trust cue says what a call may do or whether code is trusted, not how
// well anything is, so no setting quiets it: a red `destructive` that went plain
// under `off` would be the wrong way for the setting to fail. Held through
// StatusStyle, which is what a table's status column, a key/value page and a result
// header ask, and through Cue itself.
func TestASafetyOrTrustCueKeepsItsColourUnderEverySetting(t *testing.T) {
	cues := map[string]theme.StatusKind{
		"read": theme.StatusGood, "write": theme.StatusWarn, "destructive": theme.StatusBad,
		"untrusted": theme.StatusWarn, "failed to start": theme.StatusBad,
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for word, kind := range cues {
			want := themetest.Drawn(theme.GradesAll, kind)
			if want == "" {
				t.Fatalf("%v has no colour to keep", kind)
			}
			if got := themetest.ColourBefore(t, theme.StatusStyle(word).Render(word), word); got != want {
				t.Errorf("%q is drawn in %q by StatusStyle under status: %s, want %q", word, got, mode, want)
			}
			style, ok := theme.Cue(word)
			if !ok {
				t.Errorf("%q is not a cue", word)
			}
			if got := themetest.ColourBefore(t, style.Render(word), word); got != want {
				t.Errorf("%q is drawn in %q by Cue under status: %s, want %q", word, got, mode, want)
			}
		}
	})
}

// A cue is the whole word. A status that only begins with one is still a grade and
// is quieted with the rest, and so is a word that grades nothing.
func TestOnlyTheWholeWordOfACueIsOne(t *testing.T) {
	for _, word := range []string{"write pending", "destructive-ish", "readiness", "untrusted by the cluster", "ok", "", "zzz"} {
		if _, ok := theme.Cue(word); ok {
			t.Errorf("%q is taken for a cue", word)
		}
	}
	for _, word := range []string{"Write", " destructive ", "READ", "Failed To Start"} {
		if _, ok := theme.Cue(word); !ok {
			t.Errorf("%q is not taken for a cue", word)
		}
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		if got, want := themetest.ColourBefore(t, theme.StatusStyle("write pending").Render("write pending"), "write pending"),
			themetest.Drawn(mode, theme.StatusWarn); got != want {
			t.Errorf("a status that begins with write is drawn in %q under status: %s, want %q", got, mode, want)
		}
	})
}

// Nothing stated is what rta always drew, and the default is the setting itself:
// every existing test that compares a cell with the palette's own style passes
// because of it.
func TestWithNoSettingEveryGradeIsDrawnAsItAlwaysWas(t *testing.T) {
	reset(t)
	theme.Apply(nil)
	if theme.Grading() != theme.GradesAll {
		t.Fatalf("the default is %s", theme.Grading())
	}
	for word, style := range map[string]string{
		"ok": theme.GoodText.Render("ok"), "warning": theme.WarnText.Render("warning"),
		"failed": theme.BadText.Render("failed"), "done": theme.Subtle.Render("done"),
	} {
		if got := theme.StatusStyle(word).Render(word); got != style {
			t.Errorf("%q is drawn %q, want %q", word, got, style)
		}
	}
	for cell, style := range map[string]string{
		"12%": theme.GoodText.Render("12%"), "85%": theme.WarnText.Render("85%"), "99%": theme.BadText.Render("99%"),
	} {
		if got := theme.UsageStyle(cell).Render(cell); got != style {
			t.Errorf("%q is drawn %q, want %q", cell, got, style)
		}
	}
}

// The setting is applied with the colours and taken off with them: a second Apply
// with a block that states none is a fresh decision, as it is for a colour, or
// switching an environment off would leave the last one's quiet behind.
func TestApplyTakesStatusAndTheNextApplyPutsItBack(t *testing.T) {
	reset(t)
	for _, mode := range theme.GradeModes() {
		if problems := theme.Apply(map[string]string{"status": mode}); len(problems) != 0 {
			t.Fatalf("status: %s: %v", mode, problems)
		}
		if got := theme.Grading().String(); got != mode {
			t.Errorf("status: %s applied, Grading() = %s", mode, got)
		}
		theme.Apply(map[string]string{"accent": "#112233"})
		if theme.Grading() != theme.GradesAll {
			t.Errorf("a block that states no status left %s in force after status: %s", theme.Grading(), mode)
		}
	}
}

// A value that is not one of the three is reported with the three, and every grade
// stays in its colour: the setting that fails open is the one that cannot hide a
// failure. The colours beside it apply, as one bad colour costs only its own line.
func TestABadStatusIsReportedWithTheModesAndCostsOnlyItself(t *testing.T) {
	reset(t)
	for _, value := range []string{"", "none", "Off", "alert", "true", " alerts"} {
		problems := theme.Apply(map[string]string{"status": value, "accent": "#112233"})
		if len(problems) != 1 || problems[0].Field != "status" {
			t.Fatalf("status: %q gave %v, want one problem naming status", value, problems)
		}
		if hint := problems[0].Hint; !strings.Contains(hint, "all, alerts, off") {
			t.Errorf("status: %q: the hint does not list what is accepted: %q", value, hint)
		}
		if theme.Grading() != theme.GradesAll {
			t.Errorf("status: %q left %s in force", value, theme.Grading())
		}
		if theme.Current()["accent"] != "#112233" {
			t.Errorf("status: %q cost the accent beside it", value)
		}
	}
}

// A near miss of the name is told the name, and the setting is among the keys the
// block takes, which a profile's mistake of it is reported against.
func TestAMistypedStatusKeyIsToldTheKeyAndTheListIncludesIt(t *testing.T) {
	reset(t)
	problems := theme.Apply(map[string]string{"staus": "off"})
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "did you mean `status`") {
		t.Fatalf("problems = %v, want the key it nearly is", problems)
	}
	if !strings.Contains(problems[0].Hint, "status") {
		t.Errorf("the list of keys the block takes leaves status out: %q", problems[0].Hint)
	}
}

// The setting is no colour: the palette's list, what it answers for its slots and
// what it holds live are colours alone, so everything that walks them as colours
// (the editor's boxes, the profile flag's completion, the swatches) is unaffected.
func TestStatusIsNoColourAndNoSlotOfThePalette(t *testing.T) {
	reset(t)
	theme.Apply(map[string]string{"status": "off"})
	if slices.Contains(theme.Fields(), "status") {
		t.Error("Fields() lists status")
	}
	if _, has := theme.Current()["status"]; has {
		t.Error("Current() holds status")
	}
	if _, has := theme.Effective(map[string]string{"status": "off"})["status"]; has {
		t.Error("Effective() holds status")
	}
}

// Whether applying would change anything is a question about the setting as well
// as the colours: the TUI asks it on every tick and repaints when it is yes, so a
// `status:` edited in the file reaches an open dashboard, and one that did not
// change is not a repaint.
func TestChangesNoticesAStatusChangeAndNothingElse(t *testing.T) {
	reset(t)
	theme.Apply(nil)
	if theme.Changes(nil) || theme.Changes(map[string]string{"status": "all"}) {
		t.Error("the palette in force is reported as a change")
	}
	if !theme.Changes(map[string]string{"status": "alerts"}) {
		t.Error("a new status is not a change")
	}
	theme.Apply(map[string]string{"status": "alerts"})
	if theme.Changes(map[string]string{"status": "alerts"}) {
		t.Error("the status in force is reported as a change")
	}
	if !theme.Changes(nil) {
		t.Error("taking the status off is not a change")
	}
	if !theme.Changes(map[string]string{"status": "alerts", "accent": "#112233"}) {
		t.Error("a colour beside the same status is not a change")
	}
	if !theme.Changes(map[string]string{"status": "no such mode"}) {
		t.Error("a status that cannot be used leaves the one in force standing, and that is a change from alerts")
	}
}

// The operator's own block states it and the palette folds it through; a layer an
// environment brings, or a file nobody named, is refused it with the line to open
// and where to set it, as the status colours are: an environment that could say
// `status: off` would make a failure read as plain text on the operator's screen.
func TestOnlyTheOperatorsOwnBlockCanStateStatus(t *testing.T) {
	own := theme.Layer{Source: "theme", Colors: map[string]string{"status": "alerts"}}
	environment := theme.Layer{Source: "profiles.prod.theme", Colors: map[string]string{"status": "off", "accent": "#6688AA"}, Guarded: true}
	unnamed := theme.Layer{Source: "theme", Colors: map[string]string{"status": "off"}, Guarded: true}

	overrides, problems := theme.Resolve(own, environment)
	if overrides["status"] != "alerts" || len(overrides) != 2 {
		t.Errorf("overrides = %v, want the operator's status and the environment's accent", overrides)
	}
	if len(problems) != 1 || problems[0].Key() != "profiles.prod.theme.status" {
		t.Fatalf("problems = %v, want the environment's status refused", problems)
	}
	if reason, hint := problems[0].Reason, problems[0].Hint; !strings.Contains(reason, "only your own config file") ||
		!strings.Contains(hint, "rta config set theme.status") {
		t.Errorf("the refusal does not say whose it is and how to set it: %q / %q", reason, hint)
	}

	overrides, problems = theme.Resolve(unnamed)
	if _, stated := overrides["status"]; stated {
		t.Errorf("a file nobody named set the status: %v", overrides)
	}
	if len(problems) != 1 || problems[0].Key() != "theme.status" {
		t.Errorf("problems = %v, want a file nobody named refused it", problems)
	}

	// And the refusal is of the entry, whatever it says: the same value the
	// operator may state is refused an environment, so there is no value that
	// slips through.
	for _, mode := range theme.GradeModes() {
		l := theme.Layer{Source: "profiles.x.theme", Colors: map[string]string{"status": mode}, Guarded: true}
		if o, ps := theme.Resolve(l); len(o) != 0 || len(ps) != 1 {
			t.Errorf("status: %s from an environment gave %v, %v", mode, o, ps)
		}
	}
}

func TestTheModesRoundTripAndNothingElseIsOne(t *testing.T) {
	for _, name := range theme.GradeModes() {
		mode, ok := theme.ParseGrades(name)
		if !ok || mode.String() != name {
			t.Errorf("%q parsed as %v, %v", name, mode, ok)
		}
	}
	if got := theme.GradeModes(); !slices.Equal(got, []string{"all", "alerts", "off"}) {
		t.Errorf("modes = %v", got)
	}
	for _, bad := range []string{"", "ALL", "On", "false", "alerts "} {
		if _, ok := theme.ParseGrades(bad); ok {
			t.Errorf("%q parsed as a mode", bad)
		}
	}
}
