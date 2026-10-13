package tui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The screen is painted in the colours of the environment in force, and it
// follows the environment: a switch in the TUI, `rta use` from another terminal,
// and an edit of the profile all move it, and switching off puts it back. The
// palette is package state, so every test here restores it.

func themedProfiles() config.Config {
	cfg := twoProfileConfig()
	cfg.Theme = map[string]string{"accent": "#222222"}
	staging := cfg.Profiles["staging"]
	staging.Color = "#dd3333"
	staging.Theme = map[string]string{"faint": "#00aa66"}
	cfg.Profiles["staging"] = staging
	prod := cfg.Profiles["prod"]
	prod.Theme = map[string]string{"primary": "#445566"}
	cfg.Profiles["prod"] = prod
	return cfg
}

// switchOff ends the switch and lands what the update loop does about it.
func switchOff(t *testing.T, m *Model) {
	t.Helper()
	if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
		t.Fatal(verr)
	}
	applySwitch(t, m)
	if m.active != "" {
		t.Fatalf("active = %q after switching off", m.active)
	}
}

func builtin(slot string) string { return theme.Effective(nil)[slot] }

// hexOf is a colour as "#RRGGBB", to compare a style's with a palette's.
func hexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)
}

func TestASessionOpensInTheColoursOfTheEnvironmentThatIsOn(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	m = New(m.reg, config.Dashboard{}, nil)
	if m.active != "staging" {
		t.Fatalf("active = %q, want the switched-on environment", m.active)
	}

	if !m.syncPalette(readStamps()) {
		t.Fatal("nothing was painted for the environment the session opens in")
	}
	live := theme.Current()
	if live["primary"] != "#dd3333" || live["faint"] != "#00aa66" || live["accent"] != "#222222" {
		t.Errorf("palette = %v, want staging's over the operator's", live)
	}
	if got := hexOf(m.spinner.Style.GetForeground()); got != "#DD3333" {
		t.Errorf("the spinner is %s, want the environment's primary: it was built before the palette moved", got)
	}
	// Idempotent: the tick asks again every few seconds and nothing is repainted.
	if m.syncPalette(readStamps()) {
		t.Error("an unchanged environment was painted again")
	}
}

func TestSwitchingEnvironmentRepaintsTheScreenAndSwitchingOffPutsItBack(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	m.syncPalette(readStamps())
	if got := theme.Current()["primary"]; got != builtin("primary") {
		t.Fatalf("primary = %s with nothing on, want the built-in", got)
	}

	// A result on screen holds text rendered when it landed.
	c := plugin.Capability{ID: "demo.hello"}
	m.current, m.mode = c, modeResult
	shown := view.KeyValue{Pairs: []view.Pair{{Key: "used", Value: "4.2 GB"}}}
	m.result = resultMsg{cap: c, view: shown, raw: shown}
	m = m.fitToWindow()
	m.renderResult()
	if got := fgBefore(t, m.viewport.View(), "used"); got != "38;2;217;119;87" {
		t.Fatalf("a key on the result is %s before the switch, want the built-in primary", got)
	}

	switchOn(t, &m, "staging")
	if got := theme.Current()["primary"]; got != "#dd3333" {
		t.Errorf("primary = %s on staging, want its colour", got)
	}
	if got := hexOf(m.spinner.Style.GetForeground()); got != "#DD3333" {
		t.Errorf("the spinner is %s on staging", got)
	}
	// The text rendered before the switch was drawn again, not left in the
	// colours of where it came from.
	if got := fgBefore(t, m.viewport.View(), "used"); got != "38;2;221;51;51" {
		t.Errorf("a key on the result is %s after the switch, want staging's primary", got)
	}
	// And what is drawn each time, the footer's keys, follows by itself.
	if got := fgBefore(t, m.View().Content, "esc"); got != "38;2;221;51;51" {
		t.Errorf("a key in the footer is %s on staging", got)
	}

	switchOff(t, &m)
	live := theme.Current()
	if live["primary"] != builtin("primary") || live["faint"] != builtin("faint") {
		t.Errorf("primary = %s, faint = %s after switching off, want the built-ins", live["primary"], live["faint"])
	}
	if live["accent"] != "#222222" {
		t.Errorf("accent = %s, want the operator's own block still", live["accent"])
	}
	if got := hexOf(m.spinner.Style.GetForeground()); got != strings.ToUpper(builtin("primary")) {
		t.Errorf("the spinner is %s after switching off", got)
	}
	if got := fgBefore(t, m.viewport.View(), "used"); got != "38;2;217;119;87" {
		t.Errorf("a key on the result is %s after switching off, want the built-in primary", got)
	}
}

// The `u` key is the switch, and it reaches the palette like any other.
func TestUOnTheProfilesPaneSwitchesTheColoursWithTheEnvironment(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	m.mode = modeProfiles
	for i, row := range m.profiles {
		if row.name == "prod" {
			m.profileSel = i
		}
	}
	m = press(t, m, "u")
	if m.active != "prod" {
		t.Fatalf("active = %q after u on prod", m.active)
	}
	if got := theme.Current()["primary"]; got != "#445566" {
		t.Errorf("primary = %s on prod, want its own theme's", got)
	}
	// And onto an environment that states nothing about colour: not prod's
	// left behind.
	for i, row := range m.profiles {
		if row.name == "staging" {
			m.profileSel = i
		}
	}
	m = press(t, m, "u")
	live := theme.Current()
	if live["primary"] != "#dd3333" || live["faint"] != "#00aa66" {
		t.Errorf("primary = %s, faint = %s on staging, want its own", live["primary"], live["faint"])
	}
}

// `rta use` in another terminal changes the selection and nothing in this
// process, so it arrives with the refresh tick.
func TestAnRtaUseInAnotherTerminalReachesTheColoursOnTheNextTick(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	m.syncPalette(readStamps())

	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	next, _ := m.Update(tickMsg{gen: m.tickGen})
	m = next.(Model)
	if m.active != "prod" {
		t.Fatalf("active = %q after the tick", m.active)
	}
	if got := theme.Current()["primary"]; got != "#445566" {
		t.Errorf("primary = %s after the tick, want prod's", got)
	}

	// The deadline lapsing, or --off from the other terminal, is the same tick.
	if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
		t.Fatal(verr)
	}
	next, _ = m.Update(tickMsg{gen: m.tickGen})
	m = next.(Model)
	if got := theme.Current()["primary"]; got != builtin("primary") {
		t.Errorf("primary = %s after the switch went off, want the built-in", got)
	}
}

// A profile edited while it is the one in force recolours the screen without a
// switch — from the profile form, and from a hand on the file.
func TestEditingTheProfileInForceRepaintsTheScreen(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	if got := theme.Current()["primary"]; got != "#dd3333" {
		t.Fatalf("primary = %s on staging", got)
	}

	// The profile form: its colour is the primary.
	next, _ := m.startProfileForm("staging")
	nm := next.(Model)
	*nm.form.bindings[profileColorField] = "#3366cc"
	saved, _ := nm.saveProfileForm()
	m = saved.(Model)
	if got := theme.Current()["primary"]; got != "#3366cc" {
		t.Errorf("primary = %s after saving the profile form, want the colour it was given", got)
	}

	// A hand on the file: its own theme, which the next tick reads.
	if err := config.Mutate(func(c config.Config) (config.Config, bool) {
		p := c.Profiles["staging"]
		p.Theme = map[string]string{"primary": "#abcdef", "faint": "#00aa66"}
		c.Profiles["staging"] = p
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	// Back on the dashboard, where the tick runs.
	m.mode = modeDashboard
	after, _ := m.Update(tickMsg{gen: m.tickGen})
	m = after.(Model)
	if got := theme.Current()["primary"]; got != "#abcdef" {
		t.Errorf("primary = %s after the file changed, want the profile's own theme's", got)
	}
}

// A profile's theme survives the saves that rewrite the profile — the editor
// keeps what it does not collect, as it keeps the plugins.
func TestSavingTheProfileFormKeepsItsTheme(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	next, _ := m.startProfileForm("staging")
	nm := next.(Model)
	*nm.form.bindings[profileNoteField] = "edited"
	_, _ = nm.saveProfileForm()
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Theme["faint"]; got != "#00aa66" {
		t.Errorf("staging's theme = %v after the form saved, want it kept", onDisk.Profiles["staging"].Theme)
	}
	if got := onDisk.Profiles["staging"].Color; got != "#dd3333" {
		t.Errorf("staging's color = %q after the form saved", got)
	}
}

// ...and the saves of a connection inside it, which write the `set:` block.
func TestSavingAConnectionKeepsTheProfilesTheme(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	m.profileOpen = "staging"
	row, _ := m.openProfile()
	key := row.conns[0].key
	next, _ := m.startConnForm(key)
	nm := next.(Model)
	if nm.form == nil {
		t.Fatal("the connection form did not open")
	}
	_, _ = nm.saveConnForm()
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Theme["faint"]; got != "#00aa66" {
		t.Errorf("staging's theme = %v after its connection saved, want it kept", onDisk.Profiles["staging"].Theme)
	}
}

// A config nobody named is honoured for the operator's own block and for
// nothing an environment says.
func TestAnEnvironmentFromAFileNobodyNamedPaintsNothingInTheTUI(t *testing.T) {
	resetTheme(t)
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if err := os.WriteFile(filepath.Join(dir, ".rta.yaml"), []byte(`theme:
  accent: "#6688AA"
profiles:
  staging:
    color: "#dd3333"
    theme:
      faint: "#00aa66"
    plugins:
      db:
        set:
          host: s.internal
`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	m.active = "staging"
	m.syncPalette(readStamps())
	live := theme.Current()
	if live["primary"] != builtin("primary") || live["faint"] != builtin("faint") {
		t.Errorf("primary = %s, faint = %s: a repository's profile painted the screen", live["primary"], live["faint"])
	}
	if live["accent"] != "#6688AA" {
		t.Errorf("accent = %s, want the block that file has always been able to state", live["accent"])
	}
}

// A file that does not parse leaves the screen as it is: a typo is no reason
// for the colours to change under somebody.
func TestAnUnreadableConfigLeavesTheColoursAlone(t *testing.T) {
	resetTheme(t)
	m := profileModel(t, themedProfiles())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	if err := os.WriteFile(config.Path(), []byte("profiles: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m.syncPalette(readStamps()) {
		t.Error("an unreadable config repainted the screen")
	}
	if got := theme.Current()["primary"]; got != "#dd3333" {
		t.Errorf("primary = %s after the file broke, want what was drawn", got)
	}
}
