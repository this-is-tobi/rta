package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// `rta profile set --theme` states a profile's palette the way `--set` and
// `--only` state their blocks: whole, from flags, refusing what the file would
// have carried as a line nothing paints. `--untheme` takes it off as
// `--unfence` takes the fences.

func themeSession(t *testing.T) func(args ...string) (string, string, error) {
	t.Helper()
	restoreTheme(t)
	return session(t, setRegistry(t))
}

func profileOf(t *testing.T, name string) config.Profile {
	t.Helper()
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Profiles[name]
}

func TestProfileSetStatesTheWholeThemeFromFlags(t *testing.T) {
	run := themeSession(t)
	out, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal",
		"--color", "#dd3333", "--theme", "primary=#3366AA", "--theme", "muted=#00aa66")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	p := profileOf(t, "staging")
	if len(p.Theme) != 2 || p.Theme["primary"] != "#3366AA" || p.Theme["muted"] != "#00aa66" {
		t.Errorf("theme = %v", p.Theme)
	}
	if p.Color != "#dd3333" {
		t.Errorf("color = %q: stating a theme moved the colour", p.Color)
	}
	// And the card the command prints says it, where it says everything else.
	if !strings.Contains(out, "theme") || !strings.Contains(out, "muted=#00aa66 primary=#3366AA") {
		t.Errorf("the receipt does not show the palette:\n%s", out)
	}

	// Restated whole: a slot left out is removed, and said.
	out, errOut, err = run("profile", "set", "staging", "--theme", "primary=#445566")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := profileOf(t, "staging").Theme; len(got) != 1 || got["primary"] != "#445566" {
		t.Errorf("theme = %v, want the one slot the flags state", got)
	}
	if !strings.Contains(out, "dropped") || !strings.Contains(out, "theme.muted") {
		t.Errorf("what the restatement dropped is not said:\n%s", out)
	}
	if got := profileOf(t, "staging").Plugins["db"].Set["host"]; got != "db.internal" {
		t.Errorf("host = %v: a profile-level flag disturbed the plugin's block", got)
	}
}

func TestProfileSetThemeIsIdempotent(t *testing.T) {
	run := themeSession(t)
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal",
		"--theme", "primary=#3366AA"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	before := configOf(t)
	out, errOut, err := run("profile", "set", "staging", "--theme", "primary=#3366AA")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, "already reads this way") || configOf(t) != before {
		t.Errorf("a run that asked for what is there was not a no-op:\n%s", out)
	}
}

func TestProfileSetUnthemeRemovesTheBlockAndNothingElse(t *testing.T) {
	run := themeSession(t)
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal",
		"--color", "#dd3333", "--note", "shared", "--theme", "primary=#3366AA", "--theme", "muted=#00aa66"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	out, errOut, err := run("profile", "set", "staging", "--untheme")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	p := profileOf(t, "staging")
	if len(p.Theme) != 0 || p.Color != "#dd3333" || p.Note != "shared" || p.Plugins["db"].Set["host"] != "db.internal" {
		t.Errorf("profile = %+v, want only the theme gone", p)
	}
	if !strings.Contains(out, "removed") || !strings.Contains(out, "theme.muted, theme.primary") {
		t.Errorf("what was removed is not said:\n%s", out)
	}
	if strings.Contains(configOf(t), "theme") {
		t.Errorf("an empty theme: block was left in the file:\n%s", configOf(t))
	}
	// Nothing to take off is no write and no refusal.
	out, _, err = run("profile", "set", "staging", "--untheme")
	if err != nil || !strings.Contains(out, "already reads this way") {
		t.Errorf("--untheme on a profile with none: %q %v", out, err)
	}
}

func TestProfileSetThemeRefusesWhatTheFileWouldCarryAsALineNothingPaints(t *testing.T) {
	run := themeSession(t)
	for _, c := range []struct {
		name string
		args []string
		code string
		hint string
	}{
		{"a slot the palette does not have", []string{"--theme", "primry=#112233"}, "core.profile.theme.slot", "did you mean `primary`"},
		{"a slot in another case", []string{"--theme", "LABEL=#C792EA"}, "core.profile.theme.slot", "did you mean `label`"},
		{"a status colour", []string{"--theme", "good=#00aa66"}, "core.profile.theme.status", "rta config set theme.good"},
		{"the other status colours", []string{"--theme", "ink=#00aa66"}, "core.profile.theme.status", "your own config file"},
		{"the setting about drawing status", []string{"--theme", "status=off"}, "core.profile.theme.status", "rta config set theme.status"},
		{"a colour that is not one", []string{"--theme", "primary=red"}, "core.profile.theme.color", "#rrggbb"},
		{"a value that is only three digits", []string{"--theme", "primary=#123"}, "core.profile.theme.color", "#rrggbb"},
		{"no colour at all", []string{"--theme", "primary="}, "core.profile.theme.empty", "expanded"},
		{"no pair", []string{"--theme", "primary"}, "core.profile.theme.shape", "primary=" + theme.Builtin("primary")},
		{"a colour that is not one, for a slot with a built-in of its own", []string{"--theme", "label=red"}, "core.profile.theme.color", "--theme label='" + theme.Builtin("label") + "'"},
		{"a slot twice", []string{"--theme", "primary=#3366AA", "--theme", "primary=#445566"}, "core.profile.theme.repeated", "once"},
		{"a theme and its removal", []string{"--theme", "primary=#3366AA", "--untheme"}, "core.profile.theme.both", "one or the other"},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal"}, c.args...)
			_, errOut, err := run(args...)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if codeOf(t, errOut) != c.code || !strings.Contains(errOut, c.hint) {
				t.Errorf("stderr = %q, want %s and %q", errOut, c.code, c.hint)
			}
			if _, statErr := os.Stat(config.Path()); statErr == nil {
				t.Errorf("a refused run wrote %s", config.Path())
			}
		})
	}
}

// The list a refusal gives is what the flag takes, which is not the palette's ten:
// offering the status colours in the answer to a typo is offering the next refusal.
func TestTheSlotRefusalListsTheSlotsAProfileCanState(t *testing.T) {
	run := themeSession(t)
	_, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal", "--theme", "colour=#C792EA")
	if err == nil {
		t.Fatal("it was accepted")
	}
	if !strings.Contains(errOut, "one of: accent, faint, label, muted, primary") {
		t.Errorf("the refusal does not list the five slots a profile can state:\n%s", errOut)
	}
	for _, status := range []string{"good", "warn", "bad", "inverse", "ink"} {
		if strings.Contains(errOut, status) {
			t.Errorf("the refusal offers %s, which a profile cannot state:\n%s", status, errOut)
		}
	}
}

func TestProfileSetThemeDryRunWritesNothingAndSaysWhat(t *testing.T) {
	run := themeSession(t)
	out, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal",
		"--theme", "primary=#3366AA", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if _, statErr := os.Stat(config.Path()); statErr == nil {
		t.Fatalf("--dry-run wrote %s", config.Path())
	}
	if !strings.Contains(out, "would create") || !strings.Contains(out, "primary=#3366AA") {
		t.Errorf("the preview does not say what the theme would be:\n%s", out)
	}
}

// A profile stated in a drop-in is rewritten in the drop-in, theme and all.
func TestProfileSetThemeWritesWhereTheProfileLives(t *testing.T) {
	run := themeSession(t)
	dir := filepath.Dir(config.Path())
	if err := os.MkdirAll(filepath.Join(dir, "config.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropIn := filepath.Join(dir, "config.d", "10-staging.yaml")
	if err := os.WriteFile(dropIn, []byte("profiles:\n  staging:\n    plugins:\n      db:\n        set:\n          host: db.internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := run("profile", "set", "staging", "--theme", "label=#C792EA"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got, _ := os.ReadFile(dropIn); !strings.Contains(string(got), `label: "#C792EA"`) || !strings.Contains(string(got), "db.internal") {
		t.Errorf("the drop-in:\n%s", got)
	}
	if got, _ := os.ReadFile(config.Path()); string(got) != "output: json\n" {
		t.Errorf("the config file changed:\n%s", got)
	}
}

func TestTheThemeFlagCompletesTheSlotsWithTheirBuiltIns(t *testing.T) {
	reg := setRegistry(t)
	root := NewRoot(reg, "test")
	set, _, err := root.Find([]string{"profile", "set"})
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := set.GetFlagCompletionFunc("theme")
	if !ok {
		t.Fatal("--theme has no completion")
	}
	got, directive := fn(set, nil, "")
	if directive&cobra.ShellCompDirectiveNoSpace == 0 {
		t.Error("the slot is offered with a space after it, and the colour is the next thing typed")
	}
	slots := names(got)
	for _, want := range []string{"primary=", "accent=", "label=", "muted=", "faint="} {
		if !slices.Contains(slots, want) {
			t.Errorf("%s is not offered: %v", want, slots)
		}
	}
	// A profile cannot state the status colours, so a completion does not offer them.
	for _, refused := range []string{"good=", "warn=", "bad=", "inverse=", "ink="} {
		if slices.Contains(slots, refused) {
			t.Errorf("%s is offered for a block that cannot state it: %v", refused, slots)
		}
	}
	if len(slots) != 5 {
		t.Errorf("%d slots offered, want the five a profile may state", len(slots))
	}
	if after, _ := fn(set, nil, "primary="); len(after) != 0 {
		t.Errorf("a colour is offered past the =: %v", after)
	}
}

// The `profile show` page: the row for the palette, and the warnings for what
// the palette could not use.
const themedShow = `theme:
  good: "#00aa66"
profiles:
  prod:
    color: "#dd3333"
    theme:
      primary: red
      shiny: "#123456"
      bad: "#00AA66"
      label: "#C792EA"
    plugins:
      db:
        set:
          host: db.prod
  plain:
    plugins:
      db:
        set:
          host: db.plain
`

func TestProfileShowSaysWhatThePaletteIsAndWhatItCouldNotUse(t *testing.T) {
	restoreTheme(t)
	out, _, err := runWith(t, setRegistry(t), themedShow, "profile", "show", "prod")
	if err != nil {
		t.Fatal(err)
	}
	flat := squash(out)
	for _, want := range []string{
		// Only what stands is the palette the profile has: label. The three the
		// palette cannot use are the warnings, and not also a line saying they paint.
		"theme label=#C792EA — the palette while this is switched on, over yours",
		"warning theme.primary: \"red\" is not a color",
		"warning theme.shiny: not a color this rta understands",
		"theme.bad: is a status colour",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("profile show does not say %q:\n%s", want, out)
		}
	}
	for _, notWanted := range []string{"bad=#00AA66", "primary=red", "shiny=#123456"} {
		if strings.Contains(flat, notWanted) {
			t.Errorf("profile show names %q as the palette, which the palette left out:\n%s", notWanted, out)
		}
	}
	// A profile that says nothing about colour has no such row.
	out, _, _ = runWith(t, setRegistry(t), themedShow, "profile", "show", "plain")
	if strings.Contains(out, "theme") {
		t.Errorf("a profile with no palette shows one:\n%s", out)
	}
}

// One entry that cannot be used is a warning on the profile, never a reason to
// refuse it: it is listed as usable, switched to, and run through.
func TestAProfileThemeEntryThatCannotBeUsedNeverMakesTheProfileInvalid(t *testing.T) {
	restoreTheme(t)
	reg := setRegistry(t)
	out, _, err := runWith(t, reg, themedShow, "profile", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	rows := jsonRows(t, out)
	var prod []string
	for _, row := range rows {
		if row[0] == "prod" {
			prod = row
		}
	}
	if prod == nil {
		t.Fatalf("prod is not listed: %v", rows)
	}
	if prod[2] != "warn" {
		t.Errorf("status = %q, want warn: usable, and not as written", prod[2])
	}
	if prod[4] != "1 colour" {
		t.Errorf("theme = %q, want the count of what stands: of the four it states, one is applied", prod[4])
	}
	for _, row := range rows {
		if row[0] == "plain" && row[4] != "" {
			t.Errorf("a profile with no palette lists %q", row[4])
		}
	}
	if _, errOut, err := runWith(t, reg, themedShow, "use", "prod"); err != nil {
		t.Errorf("a profile with a theme entry that cannot be used could not be switched to: %v %s", err, errOut)
	}
}

func TestDoctorSaysWhichProfileThemeEntryCannotBeUsed(t *testing.T) {
	restoreTheme(t)
	out, _, _ := runWith(t, setRegistry(t), themedShow, "doctor")
	flat := squash(out)
	for _, want := range []string{"theme.primary", "theme.shiny", "theme.bad"} {
		if !strings.Contains(flat, want) {
			t.Errorf("doctor does not say %q:\n%s", want, out)
		}
	}
}

// `rta config check` names the line, as it names the operator's own block's.
func TestConfigCheckNamesAProfileThemeEntryByItsLine(t *testing.T) {
	restoreTheme(t)
	out, _, _ := runWith(t, setRegistry(t), themedShow, "config", "check", "-o", "json")
	rows := jsonRows(t, out)
	keys := map[string]string{}
	for _, row := range rows {
		keys[row[1]] = row[0]
	}
	for _, want := range []string{"profiles.prod.theme.primary", "profiles.prod.theme.shiny", "profiles.prod.theme.bad"} {
		line, ok := keys[want]
		if !ok {
			t.Errorf("config check does not name %s: %v", want, keys)
		} else if line == "" {
			t.Errorf("%s is named without its line", want)
		}
	}
	// And the colour that is exactly a status colour is named as `color`.
	cfg := "profiles:\n  prod:\n    color: \"#FF4357\"\n    plugins:\n      db:\n        set:\n          host: p\n"
	out, _, _ = runWith(t, setRegistry(t), cfg, "config", "check", "-o", "json")
	rows = jsonRows(t, out)
	if len(rows) != 1 || rows[0][1] != "profiles.prod.color" || !strings.Contains(rows[0][2], "same colour as bad") {
		t.Errorf("rows = %v, want the colour named as the failure colour", rows)
	}
}

// A block whose every entry is left out applies nothing, and the first glance —
// the count in the list, the line on the page — must not say it applies
// something above warnings that say it does not.
func TestAProfileWhoseThemeIsAllLeftOutIsListedAndShownWithoutAPalette(t *testing.T) {
	restoreTheme(t)
	cfg := "profiles:\n  calm:\n    theme:\n      good: \"#229F6C\"\n      bad: \"#229F6C\"\n      inverse: \"#FF4357\"\n" +
		"    plugins:\n      db:\n        set:\n          host: c\n"
	out, _, err := runWith(t, setRegistry(t), cfg, "profile", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range jsonRows(t, out) {
		if row[0] == "calm" && (row[2] != "warn" || row[4] != "") {
			t.Errorf("calm is listed as %v: want a warning, and no colours counted", row)
		}
	}
	out, _, _ = runWith(t, setRegistry(t), cfg, "profile", "show", "calm")
	flat := squash(out)
	if strings.Contains(flat, "the palette while this is switched on") {
		t.Errorf("profile show says the profile has a palette:\n%s", out)
	}
	for _, want := range []string{"warning theme.good: is a status colour", "warning theme.bad: is a status colour", "warning theme.inverse: is a status colour"} {
		if !strings.Contains(flat, want) {
			t.Errorf("profile show does not warn %q:\n%s", want, out)
		}
	}
}

// The same near-miss `profile set --theme` answers is answered in the file's
// own reports, which is where the mistake is likeliest and read without an
// editor to ask.
func TestAMiscasedSlotInTheFileIsMetWithTheOneItNearlyIs(t *testing.T) {
	restoreTheme(t)
	cfg := "theme:\n  Good: \"#00aa66\"\nprofiles:\n  unknown:\n    theme:\n      Label: \"#C792EA\"\n" +
		"    plugins:\n      db:\n        set:\n          host: c\n"
	reg := setRegistry(t)
	out, _, _ := runWith(t, reg, cfg, "config", "check", "-o", "json")
	said := map[string]string{}
	for _, row := range jsonRows(t, out) {
		said[row[1]] = row[2]
	}
	for key, want := range map[string]string{"theme.Good": "did you mean `good`?", "profiles.unknown.theme.Label": "did you mean `label`?"} {
		if !strings.Contains(said[key], want) {
			t.Errorf("config check says %q of %s, want it to say %q", said[key], key, want)
		}
	}
	for _, args := range [][]string{{"doctor"}, {"profile", "show", "unknown"}, {"profile", "list"}} {
		out, _, _ := runWith(t, reg, cfg, args...)
		if !strings.Contains(squash(out), "did you mean `label`?") {
			t.Errorf("%v does not say what Label nearly is:\n%s", args, out)
		}
	}
}

// An unquoted # starts a YAML comment, so the colour is no value at all — and
// the report used to tell the person they had written an empty string.
func TestAnUnquotedColourIsToldAsAHashThatStartedAComment(t *testing.T) {
	restoreTheme(t)
	cfg := "theme:\n  muted: #8B8B96\nprofiles:\n  nullval:\n    theme:\n      label: #C792EA\n" +
		"    plugins:\n      db:\n        set:\n          host: c\n"
	reg := setRegistry(t)
	out, _, _ := runWith(t, reg, cfg, "config", "check", "-o", "json")
	said := map[string]string{}
	for _, row := range jsonRows(t, out) {
		said[row[1]] = row[2]
	}
	for key, slot := range map[string]string{"theme.muted": "muted", "profiles.nullval.theme.label": "label"} {
		got := said[key]
		if strings.Contains(got, `""`) || !strings.Contains(got, "YAML comment") || !strings.Contains(got, slot+`: "`+theme.Builtin(slot)+`"`) {
			t.Errorf("config check says %q of %s, want a value-less colour with the quoted form shown", got, key)
		}
	}
	for _, args := range [][]string{{"doctor"}, {"profile", "show", "nullval"}, {"profile", "list"}} {
		out, _, _ := runWith(t, reg, cfg, args...)
		flat := squash(out)
		if strings.Contains(flat, `"" is not a color`) || !strings.Contains(flat, "YAML comment") {
			t.Errorf("%v does not tell an unquoted # from an empty string:\n%s", args, out)
		}
	}
}
