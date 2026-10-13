package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// The theme editor opens on whose palette it is — the top-level block, or one
// profile's — defaulting to the environment in force, and saves into the place
// picked with the guarantees it always had: an untouched save writes nothing,
// only what changed is written, and one entry that cannot be used costs its own
// line.

func names(targets []themeTarget) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.name
	}
	return out
}

// swatchOf is the colour the preview draws a field's swatch in, as the
// "38;2;r;g;b" a terminal receives, or "×" when it draws a cross instead.
func swatchOf(t *testing.T, preview, field string) string {
	t.Helper()
	cross := regexp.MustCompile(`××××\x1b\[m ` + regexp.QuoteMeta(field) + `(\s|$)`)
	if cross.MatchString(preview) {
		return "×"
	}
	m := regexp.MustCompile(`\x1b\[([0-9;]*)m████\x1b\[m ` + regexp.QuoteMeta(field) + `(\s|$)`).FindStringSubmatch(preview)
	if m == nil {
		t.Fatalf("no swatch for %s in %q", field, preview)
	}
	return regexp.MustCompile(`38;2;\d+;\d+;\d+`).FindString(m[1])
}

// openTheme is `t`: what the key does once it is the key of a screen and not of
// the search bar the first tile is.
func openTheme(t *testing.T, m Model) Model {
	t.Helper()
	m.mode = modeDashboard
	next, _ := m.startThemeForm()
	return next.(Model)
}

func TestTheThemeEditorOpensOnAPickerOfGlobalAndEveryProfile(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m = openTheme(t, m)
	if m.mode != modeTheme || m.themePick == nil || m.themeForm != nil {
		t.Fatalf("mode = %v, picker = %v, form = %v: the editor did not open on a choice", m.mode, m.themePick != nil, m.themeForm != nil)
	}
	got := names(m.themePick.targets)
	if len(got) != 3 || got[0] != "" || got[1] != "prod" || got[2] != "staging" {
		t.Errorf("the picker offers %q, want global and each profile", got)
	}
	if m.themePick.value != "" {
		t.Errorf("the picker defaults to %q with nothing switched on, want global", m.themePick.value)
	}
	view := plain(m.themeView())
	for _, want := range []string{"global", "prod", "staging", "whose palette"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "2 colours of its own") && !strings.Contains(view, "1 colour of its own") {
		t.Errorf("the picker does not say which profiles have a palette of their own:\n%s", view)
	}
}

// What the picker says an environment has of its own is what is applied: a
// block whose entries are left out is not a palette, and "2 colours of its own"
// above a warning that neither is used says the opposite.
func TestThePickerCountsTheColoursThatStandAndNotTheOnesLeftOut(t *testing.T) {
	resetTheme(t)
	cfg := themedProfiles()
	staging := cfg.Profiles["staging"]
	staging.Theme = map[string]string{"faint": "#00aa66", "good": "#00aa66", "bad": "red"}
	cfg.Profiles["staging"] = staging
	prod := cfg.Profiles["prod"]
	prod.Theme = map[string]string{"good": "#00aa66"}
	cfg.Profiles["prod"] = prod
	m := openTheme(t, profileModel(t, cfg))
	view := plain(m.themeView())
	if !strings.Contains(view, "staging — 1 colour of its own") {
		t.Errorf("the picker does not say staging has the one colour that stands:\n%s", view)
	}
	if regexp.MustCompile(`prod — \d+ colour`).MatchString(view) {
		t.Errorf("the picker says prod has a colour of its own when every entry is left out:\n%s", view)
	}
}

func TestThePickerDefaultsToTheEnvironmentInForce(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "prod")
	m = openTheme(t, m)
	if m.themePick == nil || m.themePick.value != "prod" {
		t.Fatalf("the picker is on %v, want the environment in force", m.themePick)
	}
	if !strings.Contains(plain(m.themeView()), "prod — switched on") {
		t.Errorf("the picker does not say which one is on:\n%s", plain(m.themeView()))
	}
}

// A picker with one entry is a question with one answer.
func TestWithNoProfileTheEditorOpensOnTheTopLevelBlockAsItAlwaysDid(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, config.Config{Theme: map[string]string{"accent": "#222222"}})
	m = openTheme(t, m)
	if m.themePick != nil || m.themeForm == nil || m.themeForm.target.name != "" {
		t.Fatalf("picker = %v, form = %v: want the editor on the top-level block", m.themePick != nil, m.themeForm != nil)
	}
	if got := *m.themeForm.bindings["accent"]; got != "#222222" {
		t.Errorf("accent = %q, want the stored colour", got)
	}
}

func TestPickingAProfileOpensItsOwnBlockAndNoneOfTheColoursBeneathIt(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m = openTheme(t, m)
	m = press(t, m, "down") // global -> prod
	m = press(t, m, "down") // prod -> staging
	// enter and ctrl+s are the same answer on a form of one field; the second is
	// driven directly, since a form advances through commands a test does not run.
	next, _ := m.fastSubmitThemePick()
	m = next.(Model)
	if m.themeForm == nil || m.themePick != nil || m.themeForm.target.name != "staging" {
		t.Fatalf("form = %v, picker = %v: the choice did not open staging's editor", m.themeForm, m.themePick)
	}
	// Seeded from what staging states, and not from the palette beneath it: the
	// operator's accent is not staging's, and a box seeded with it would write
	// it into the profile.
	if got := *m.themeForm.bindings["faint"]; got != "#00aa66" {
		t.Errorf("faint = %q, want staging's own", got)
	}
	if got := *m.themeForm.bindings["accent"]; got != "" {
		t.Errorf("accent = %q: the operator's colour was copied into the profile's boxes", got)
	}
	if got := *m.themeForm.bindings["primary"]; got != "" {
		t.Errorf("primary = %q: the profile's colour was copied into its theme's boxes", got)
	}
	// And the panel says whose it is.
	if !strings.Contains(plain(m.themeView()), "theme · staging") {
		t.Errorf("the editor does not say it is on staging:\n%s", plain(m.themeView()))
	}
}

func TestEscOnThePickerLeavesWithoutOpeningOrWritingAnything(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	before, _ := os.ReadFile(config.Path())
	m = openTheme(t, m)
	m = press(t, m, "esc")
	if m.mode != modeDashboard || m.themePick != nil || m.themeForm != nil {
		t.Errorf("mode = %v, picker = %v, form = %v after esc", m.mode, m.themePick != nil, m.themeForm != nil)
	}
	if after, _ := os.ReadFile(config.Path()); string(after) != string(before) {
		t.Error("the config changed")
	}
}

// What the preview shows is the palette the place would produce: blank is what
// is beneath, and a colour that cannot be used is a cross.
func TestThePreviewIsTheMergedPaletteThePlaceWouldProduce(t *testing.T) {
	resetTheme(t)
	cfg, err := loadThemed(t)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := themeTargetFor(cfg, "staging")
	if !ok {
		t.Fatal("staging is not a target")
	}
	tf := newThemeForm(target)
	preview := tf.preview(200)

	// Blank: the operator's own accent, staging's colour as the primary, and the
	// built-in for what nobody states.
	for field, want := range map[string]string{
		"accent":  "38;2;34;34;34",
		"primary": "38;2;221;51;51",
		"faint":   "38;2;0;170;102", // staging's own, stored
		"warn":    "38;2;191;126;0", // the built-in
	} {
		if got := swatchOf(t, preview, field); got != want {
			t.Errorf("%s swatch = %s, want %s", field, got, want)
		}
	}

	*tf.bindings["label"] = "#C792EA"
	*tf.bindings["muted"] = "not a colour"
	*tf.bindings["primary"] = "#ff4357" // the built-in failure colour: the selection would read as a failure
	*tf.bindings["accent"] = "#101010"  // too dim to be seen on a dark terminal
	preview = tf.preview(200)
	if got := swatchOf(t, preview, "label"); got != "38;2;199;146;234" {
		t.Errorf("a typed colour is %s, want itself", got)
	}
	for field, why := range map[string]string{
		"muted": "a colour that is not one", "primary": "a primary that is a status", "accent": "a colour nobody could see",
	} {
		if got := swatchOf(t, preview, field); got != "×" {
			t.Errorf("%s is %s, want a cross: the palette would leave it out", why, got)
		}
	}
	// The strip is the palette this place makes and every slot of it, the status
	// colours too, which are the operator's and shown as they will be drawn.
	if got := swatchOf(t, preview, "good"); got != "38;2;34;159;108" {
		t.Errorf("good = %s, want the built-in beneath it", got)
	}
}

// loadThemed is the themed fixture as the file states it.
func loadThemed(t *testing.T) (config.Config, error) {
	t.Helper()
	profileModel(t, themedProfiles())
	return config.LoadFile()
}

func fileText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func editorOn(t *testing.T, m Model, name string) Model {
	t.Helper()
	next, _ := m.openThemeEditor(name)
	nm := next.(Model)
	if nm.themeForm == nil {
		t.Fatalf("no editor opened on %q: %s", name, nm.flash)
	}
	return nm
}

func TestSavingWritesIntoThePlacePickedAndNowhereElse(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	before := fileText(t)
	m = editorOn(t, m, "staging")
	*m.themeForm.bindings["primary"] = "#123456"
	next, _ := m.saveTheme()
	nm := next.(Model)

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	staging := onDisk.Profiles["staging"]
	if staging.Theme["primary"] != "#123456" || staging.Theme["faint"] != "#00aa66" {
		t.Errorf("staging's theme = %v, want what it had and the new primary", staging.Theme)
	}
	if staging.Color != "#dd3333" || staging.Note != "shared" || len(staging.Plugins) != 1 {
		t.Errorf("the rest of the profile moved: %+v", staging)
	}
	if got := onDisk.Theme; len(got) != 1 || got["accent"] != "#222222" {
		t.Errorf("the top-level block = %v, want it as it was", got)
	}
	if onDisk.Profiles["prod"].Theme["primary"] != "#445566" {
		t.Errorf("another profile's theme moved: %v", onDisk.Profiles["prod"].Theme)
	}
	if !strings.Contains(nm.flash, "saved 2 theme overrides for staging") {
		t.Errorf("flash = %q, want it to say whose", nm.flash)
	}
	// Only that one line of the file is different.
	if after := fileText(t); !strings.Contains(after, "primary: \"#123456\"") || strings.Count(after, "\n") != strings.Count(before, "\n")+1 {
		t.Errorf("the file changed by more than the one entry:\n%s", after)
	}
}

func TestSavingAnUntouchedFormWritesNothing(t *testing.T) {
	for _, name := range []string{"", "staging"} {
		t.Run("place "+name, func(t *testing.T) {
			resetTheme(t)
			m := profileModel(t, themedProfiles())
			before := fileText(t)
			m = editorOn(t, m, name)
			if _, _ = m.saveTheme(); fileText(t) != before {
				t.Errorf("an untouched save rewrote the file:\n%s", fileText(t))
			}
		})
	}
}

// A hand-edited file keeps its comments and its order through the save of the
// block beside them.
func TestSavingAProfilesThemeKeepsWhatWasTypedAroundIt(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	text := "# mine\ntheme:\n  accent: \"#222222\" # the amber\nprofiles:\n  staging:\n    # the red one\n    color: \"#dd3333\"\n" +
		"    theme:\n      good: \"#00aa66\"\n    plugins:\n      db:\n        set:\n          host: staging.internal\n"
	if err := os.WriteFile(config.Path(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	m = editorOn(t, m, "staging")
	*m.themeForm.bindings["label"] = "#C792EA"
	_, _ = m.saveTheme()
	got := fileText(t)
	for _, want := range []string{"# mine", "# the amber", "# the red one", `label: "#C792EA"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the save lost %q:\n%s", want, got)
		}
	}
}

func TestClearingEveryBoxRemovesTheProfilesThemeAndNothingElse(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m = editorOn(t, m, "staging")
	*m.themeForm.bindings["faint"] = ""
	next, _ := m.saveTheme()
	nm := next.(Model)
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if staging := onDisk.Profiles["staging"]; len(staging.Theme) != 0 || staging.Color != "#dd3333" {
		t.Errorf("staging = %+v, want its theme gone and its colour kept", staging)
	}
	if !strings.Contains(nm.flash, "removed staging's theme") {
		t.Errorf("flash = %q", nm.flash)
	}
	if strings.Contains(fileText(t), "faint") {
		t.Errorf("an empty theme: block was left in the file:\n%s", fileText(t))
	}
}

// A colour that cannot be used costs its own line, and the rest of the block
// still applies. The editor refuses most of these at the field, so this is the
// file that changed under it: the entry is saved as written and the notice says
// how many were left out and where to read which.
func TestOneEntryThatCannotBeUsedCostsOnlyItsOwnLine(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	m = editorOn(t, m, "staging")
	*m.themeForm.bindings["primary"] = "not-a-colour"
	*m.themeForm.bindings["label"] = "#C792EA"
	next, _ := m.saveTheme()
	nm := next.(Model)
	if !strings.Contains(nm.flash, "1 field not applied") {
		t.Errorf("flash = %q, want it to say one entry was left out", nm.flash)
	}
	live := theme.Current()
	if live["label"] != "#C792EA" || live["faint"] != "#00aa66" {
		t.Errorf("label = %s, faint = %s: the entries that were fine did not apply", live["label"], live["faint"])
	}
	if live["primary"] != "#dd3333" {
		t.Errorf("primary = %s, want the profile's colour beneath the entry that was not a colour", live["primary"])
	}
}

// **A notice that says what to do must not be cut before it does.** The footer
// holds a notice to one line, so the one that told a person what to change —
// "left out, so the colour beneath it stands — pick one…" — was clipped at the
// words before the instruction, at a hundred columns and at sixty. What is said
// after a save is that something was left out and the command that prints which
// and why in full, short enough to end the line at the narrowest terminal.
func TestTheNoticeAfterASaveThatLeftSomethingOutNamesTheCommandAtAnyWidth(t *testing.T) {
	for _, width := range []int{100, 80, 60} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			resetTheme(t)
			m := profileModel(t, themedProfiles())
			m.width, m.height = width, 30
			m = editorOn(t, m, "staging")
			*m.themeForm.bindings["primary"] = "#ff4357" // the failure colour: left out
			next, _ := m.saveTheme()
			nm := next.(Model)
			if bar := plain(nm.footerFor(modeDashboard)); !strings.Contains(bar, "`audit.doctor`") ||
				!strings.Contains(bar, "not applied") {
				t.Errorf("at %d columns the footer reads %q, and not the notice and the command that prints which", width, bar)
			}
		})
	}
}

// The status colours are the operator's to set in their own named config file,
// and a block that cannot state them is not offered the boxes for them: a box
// that saves a line the palette always leaves out is the wrong thing to offer.
func TestAGuardedBlocksEditorDoesNotOfferTheStatusColours(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	status := []string{"good", "warn", "bad", "inverse", "ink"}
	text := []string{"primary", "accent", "label", "muted", "faint"}

	staging := editorOn(t, m, "staging").themeForm
	for _, slot := range status {
		if _, offered := staging.bindings[slot]; offered {
			t.Errorf("a profile's editor offers a box for %s", slot)
		}
		if _, offered := staging.inputs[slot]; offered {
			t.Errorf("a profile's editor draws a field for %s", slot)
		}
	}
	for _, slot := range text {
		if _, offered := staging.bindings[slot]; !offered {
			t.Errorf("a profile's editor has no box for %s", slot)
		}
	}
	// What is drawn is still the whole palette, since the preview is the screen
	// this place makes and the statuses are on it.
	for _, slot := range append(status, text...) {
		_ = swatchOf(t, staging.preview(200), slot)
	}

	// The operator's own block, in the file they named, is offered every colour.
	global := editorOn(t, m, "").themeForm
	for _, slot := range append(status, text...) {
		if _, offered := global.bindings[slot]; !offered {
			t.Errorf("the operator's own editor has no box for %s", slot)
		}
	}
}

// The same rule for the top-level block of a file nobody named, which is held
// to it (theme.Resolve) and so is not offered what it cannot state either.
func TestTheTopLevelEditorOfAFileNobodyNamedDoesNotOfferTheStatusColours(t *testing.T) {
	resetTheme(t)
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if err := os.WriteFile(filepath.Join(dir, ".rta.yaml"), []byte("theme:\n  accent: \"#6688AA\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	tf := editorOn(t, m, "").themeForm
	for _, slot := range []string{"good", "warn", "bad", "inverse", "ink"} {
		if _, offered := tf.bindings[slot]; offered {
			t.Errorf("a file nobody named is offered a box for %s", slot)
		}
	}
	if _, offered := tf.bindings["accent"]; !offered {
		t.Error("a file nobody named is not offered the colours it can state")
	}
}

// **What the editor already knows is told at the field.** The preview drew a
// cross for a colour the palette would leave out and nothing said why, and the
// save went through with a line it knew was dead. Each box takes the reason the
// palette gives (theme.Resolve), so the colour is refused where it is typed.
func TestAColourThatWouldBeLeftOutIsRefusedAtTheFieldWithItsReason(t *testing.T) {
	resetTheme(t)
	cfg := twoProfileConfig()
	cfg.Theme = map[string]string{"good": "#0000ff"}
	m := profileModel(t, cfg)
	tf := editorOn(t, m, "staging").themeForm

	for _, tc := range []struct {
		field, typed string
		want         []string
	}{
		{"primary", "#0000ff", []string{"would be left out", "same colour as good"}}, // the operator's own ok
		{"primary", "#FF4357", []string{"would be left out", "same colour as bad"}},  // the built-in failure colour
		{"muted", "#101010", []string{"would be left out", "1.10:1", "black"}},
		{"label", "#F5F5F5", []string{"would be left out", "1.09:1", "white"}},
		{"accent", "red", []string{"must be #rrggbb"}},
	} {
		err := tf.validators[tc.field](tc.typed)
		if err == nil {
			t.Errorf("%s = %s was accepted", tc.field, tc.typed)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s = %s: %q does not say %q", tc.field, tc.typed, err, want)
			}
		}
	}
	for _, tc := range []struct{ field, typed string }{
		{"primary", "#3366AA"}, {"primary", ""}, {"primary", "  "}, {"muted", "#8B8B96"}, {"faint", "#4A4A56"}, {"label", "#C792EA"},
	} {
		if err := tf.validators[tc.field](tc.typed); err != nil {
			t.Errorf("%s = %q was refused: %v", tc.field, tc.typed, err)
		}
	}
}

// And on the way out: the submit that would save a colour the field refuses
// does not save, and the footer carries the reason.
func TestTheEditorWillNotSaveAColourItsFieldRefuses(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m.width, m.height = 100, 40
	before := fileText(t)
	m = editorOn(t, m, "staging")
	// Typed through the box, as a person does: huh copies the bound value into
	// its own input once and validates that copy.
	m.themeForm.form = typeInto(startedHuhForm(m.themeForm.form), "#ff4357")
	next, _ := m.fastSubmitThemeForm()
	nm := next.(Model)
	if nm.mode != modeTheme || nm.themeForm == nil {
		t.Fatalf("mode = %v, form = %v: a colour the field refuses was saved", nm.mode, nm.themeForm != nil)
	}
	if fileText(t) != before {
		t.Errorf("the file changed:\n%s", fileText(t))
	}
	if bar := plain(nm.footerFor(modeTheme)); !strings.Contains(bar, "would be left out") || !strings.Contains(bar, "same colour as bad") {
		t.Errorf("the footer does not say why: %s", bar)
	}
}

// The editor writes the boxes it offers. A status entry already in a profile's
// block, written by hand where it can only be left out, has no box to keep it
// in, and the notice says it went and where it belongs.
func TestSavingAProfilesThemeDropsAStatusEntryItHasNoBoxFor(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	if err := config.Mutate(func(c config.Config) (config.Config, bool) {
		p := c.Profiles["staging"]
		p.Theme = map[string]string{"faint": "#00aa66", "good": "#229F6C", "bad": "#229F6C"}
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
		t.Errorf("staging's theme = %v, want the two colours the editor holds", got)
	}
	if !strings.Contains(nm.flash, "dropped bad, good") || !strings.Contains(nm.flash, "your own config file") {
		t.Errorf("flash = %q, want it to say the status entries went and whose they are", nm.flash)
	}
}

// What is saved for an environment that is not on is not painted, and the flash
// says when it will be.
func TestSavingAProfileThatIsNotInForceLeavesTheScreenAlone(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m.syncPalette(readStamps())
	before := theme.Current()
	m = editorOn(t, m, "prod")
	*m.themeForm.bindings["accent"] = "#33AA66"
	next, _ := m.saveTheme()
	nm := next.(Model)
	if got := theme.Current(); got["accent"] != before["accent"] || got["primary"] != before["primary"] {
		t.Errorf("the screen moved for a profile that is not on: %v", got)
	}
	if !strings.Contains(nm.flash, "drawn while it is switched on") {
		t.Errorf("flash = %q", nm.flash)
	}
}

func TestSavingTheProfileInForceRepaintsAtOnce(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	m = editorOn(t, m, "staging")
	*m.themeForm.bindings["primary"] = "#123456"
	next, _ := m.saveTheme()
	nm := next.(Model)
	if got := theme.Current()["primary"]; got != "#123456" {
		t.Errorf("primary = %s, want the colour just saved", got)
	}
	if got := hexOf(nm.spinner.Style.GetForeground()); got != "#123456" {
		t.Errorf("the spinner is %s, want it to follow", got)
	}
}

// Saving the top-level block while an environment is in force draws that
// environment's palette over it, and not the block alone.
func TestSavingTheTopLevelBlockKeepsTheEnvironmentInForceOnTop(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	m = editorOn(t, m, "")
	*m.themeForm.bindings["muted"] = "#808080"
	next, _ := m.saveTheme()
	_ = next
	live := theme.Current()
	if live["muted"] != "#808080" || live["primary"] != "#dd3333" || live["faint"] != "#00aa66" {
		t.Errorf("muted = %s, primary = %s, faint = %s: the environment's colours were not kept over the block saved",
			live["muted"], live["primary"], live["faint"])
	}
}

func TestAProfileThatWentBeforeTheSaveIsNotRecreatedByIt(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m = editorOn(t, m, "prod")
	*m.themeForm.bindings["accent"] = "#33AA66"
	if err := config.Mutate(func(c config.Config) (config.Config, bool) {
		delete(c.Profiles, "prod")
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	next, _ := m.saveTheme()
	nm := next.(Model)
	if !strings.Contains(nm.flash, "no profile prod") {
		t.Errorf("flash = %q, want a refusal", nm.flash)
	}
	if strings.Contains(fileText(t), "prod") {
		t.Errorf("a save recreated the profile that went:\n%s", fileText(t))
	}
}

// A profile stated in a drop-in is edited in the drop-in.
func TestAProfilesThemeInADropInIsSavedThere(t *testing.T) {
	resetTheme(t)
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "config.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropIn := filepath.Join(dir, "config.d", "10-prod.yaml")
	if err := os.WriteFile(dropIn, []byte("profiles:\n  prod:\n    theme:\n      good: \"#00aa66\"\n    plugins:\n      db:\n        set:\n          host: p.internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	m = editorOn(t, m, "prod")
	*m.themeForm.bindings["label"] = "#C792EA"
	_, _ = m.saveTheme()

	if got, _ := os.ReadFile(dropIn); !strings.Contains(string(got), `label: "#C792EA"`) || !strings.Contains(string(got), "p.internal") {
		t.Errorf("the drop-in was not written, or lost what it had:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "config.yaml")); string(got) != "output: json\n" {
		t.Errorf("the config file changed:\n%s", got)
	}
}

// The footer teaches the keys the picker answers to, like every picker.
func TestThePickersFooterNamesItsKeys(t *testing.T) {
	resetTheme(t)
	m := openTheme(t, profileModel(t, themedProfiles()))
	bar := plain(m.footerFor(modeTheme))
	for _, want := range []string{"edit", "cancel"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the picker's footer does not say %q: %s", want, bar)
		}
	}
}
