package profile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// Which colours an environment contributes to the screen, and when it
// contributes none.

const themedConfig = `
theme:
  accent: "#222222"
profiles:
  prod:
    color: "#dd3333"
    theme:
      primary: "#3366AA"
    plugins:
      pg:
        set:
          host: p.internal
  plain:
    plugins:
      pg:
        set:
          host: s.internal
`

func sources(layers []theme.Layer) []string {
	var out []string
	for _, l := range layers {
		out = append(out, l.Source)
	}
	return out
}

func TestWithNoEnvironmentTheOperatorsOwnBlockIsTheWholePalette(t *testing.T) {
	cfg := load(t, themedConfig)
	layers := ThemeLayers(cfg, "")
	if got := sources(layers); !reflect.DeepEqual(got, []string{"theme"}) {
		t.Errorf("layers = %v, want the operator's block alone", got)
	}
	if layers[0].Colors["accent"] != "#222222" || layers[0].Guarded {
		t.Errorf("the operator's block is %+v, want its colours, and not held to an environment's rule", layers[0])
	}
}

func TestAnEnvironmentBringsItsColourAsThePrimaryAndThenItsOwnTheme(t *testing.T) {
	cfg := load(t, themedConfig)
	layers := ThemeLayers(cfg, "prod")
	if got := sources(layers); !reflect.DeepEqual(got, []string{"theme", "profiles.prod", "profiles.prod.theme"}) {
		t.Fatalf("layers = %v, want the operator's block, the colour, then the environment's own", got)
	}
	if layers[1].Colors["primary"] != "#dd3333" || layers[1].As != "color" || !layers[1].Guarded {
		t.Errorf("the colour layer is %+v, want it the guarded primary, written as color", layers[1])
	}
	if layers[2].Colors["primary"] != "#3366AA" || !layers[2].Guarded {
		t.Errorf("the environment's own layer is %+v", layers[2])
	}
	overrides, problems := theme.Resolve(layers...)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	// Its own theme states a primary, which wins over the colour; the operator's
	// accent is still there beneath both.
	if overrides["primary"] != "#3366AA" || overrides["accent"] != "#222222" {
		t.Errorf("overrides = %v", overrides)
	}
}

func TestAnEnvironmentWithOnlyAColourPaintsTheColourAsThePrimary(t *testing.T) {
	cfg := load(t, "profiles:\n  prod:\n    color: \"#dd3333\"\n    plugins:\n      pg:\n        set: {host: p}\n")
	overrides, problems := theme.Resolve(ThemeLayers(cfg, "prod")...)
	if len(problems) != 0 || overrides["primary"] != "#dd3333" {
		t.Errorf("overrides = %v, problems = %v: the colour is not the primary", overrides, problems)
	}
}

// An instance is one of the places a profile reaches, not an environment with
// a screen of its own.
func TestAnInstanceReferenceIsItsEnvironmentsColours(t *testing.T) {
	cfg := load(t, themedConfig)
	if got, want := sources(ThemeLayers(cfg, "prod/analytics")), sources(ThemeLayers(cfg, "prod")); !reflect.DeepEqual(got, want) {
		t.Errorf("staging/analytics paints from %v, its environment from %v", got, want)
	}
}

func TestAnEnvironmentThatStatesNothingAboutColourContributesNothing(t *testing.T) {
	cfg := load(t, themedConfig)
	for _, env := range []string{"plain", "nowhere"} {
		if got := sources(ThemeLayers(cfg, env)); !reflect.DeepEqual(got, []string{"theme"}) {
			t.Errorf("%s: layers = %v, want the operator's block alone", env, got)
		}
	}
}

// A colour that is not a colour marks nothing, rather than painting the
// primary in a colour nobody chose.
func TestAColourThatIsNotAColourIsNotThePrimary(t *testing.T) {
	cfg := load(t, "profiles:\n  prod:\n    color: red\n    plugins:\n      pg:\n        set: {host: p}\n")
	if got := sources(ThemeLayers(cfg, "prod")); !reflect.DeepEqual(got, []string{"theme"}) {
		t.Errorf("layers = %v, want the operator's block alone", got)
	}
}

// A repository's own config does not get to repaint an environment's screen.
// A profile from a file nobody named is not honoured at all, so nothing it
// says about colour is either — while the operator's block, which that file
// has always been able to state, still is.
func TestAProfileFromAFileNobodyNamedPaintsNothing(t *testing.T) {
	cfg := config.Config{
		Theme: map[string]string{"accent": "#6688AA"},
		Profiles: map[string]config.Profile{"prod": {
			Color: "#dd3333", Theme: map[string]string{"primary": "#3366AA"},
		}},
	}
	if cfg.Profiles["prod"].Trusted() {
		t.Fatal("a profile nothing vouched for reads as trusted")
	}
	layers := ThemeLayers(cfg, "prod")
	if got := sources(layers); !reflect.DeepEqual(got, []string{"theme"}) {
		t.Fatalf("layers = %v, want the operator's block alone", got)
	}
	overrides, _ := theme.Resolve(layers...)
	if !reflect.DeepEqual(overrides, map[string]string{"accent": "#6688AA"}) {
		t.Errorf("overrides = %v: the untrusted environment's colours reached the palette", overrides)
	}
	if problems := ThemeProblems(cfg, "prod"); len(problems) != 0 {
		t.Errorf("an environment that paints nothing is reported as if it did: %v", problems)
	}
}

// The block a file nobody named states is honoured, and always was; what it is
// not is a way to state the status colours, which the operator's own block may
// and a cloned repository's may not.
func TestAFileNobodyNamedCannotStateTheStatusColours(t *testing.T) {
	block := map[string]string{"good": "#112233", "bad": "#112233", "inverse": "#FF4358", "accent": "#445566"}

	named := load(t, "theme:\n  good: \"#112233\"\n  bad: \"#112233\"\n  inverse: \"#FF4358\"\n  accent: \"#445566\"\n")
	overrides, problems := theme.Resolve(ThemeLayers(named, "")...)
	if len(problems) != 0 || overrides["good"] != "#112233" || overrides["bad"] != "#112233" || overrides["inverse"] != "#FF4358" {
		t.Errorf("the operator's own block was held back: %v %v", overrides, problems)
	}

	unnamed := config.Config{Theme: block}
	overrides, problems = theme.Resolve(ThemeLayers(unnamed, "")...)
	for _, slot := range []string{"good", "bad", "inverse"} {
		if _, there := overrides[slot]; there {
			t.Errorf("a file nobody named stated %s: %v", slot, overrides)
		}
	}
	if overrides["accent"] != "#445566" {
		t.Errorf("the rest of its block was not honoured: %v", overrides)
	}
	if len(problems) != 3 || problems[0].Source != "theme" {
		t.Errorf("problems = %v, want the three lines of its block named", problems)
	}
}

func TestThemeProblemsNameWhatTheEnvironmentStatesThatCannotBeUsed(t *testing.T) {
	cfg := load(t, `
theme:
  good: "#00aa66"
profiles:
  prod:
    color: "#FF4357"
    theme:
      bad: "#00AA66"
      primary: red
      shiny: "#123456"
      accent: "#445566"
    plugins:
      pg:
        set: {host: p}
`)
	got := map[string]string{}
	for _, p := range ThemeProblems(cfg, "prod") {
		got[p.Key()] = p.Reason
	}
	for key, want := range map[string]string{
		"profiles.prod.color":         "bad",           // the colour is exactly the failure colour
		"profiles.prod.theme.bad":     "status colour", // and a status is the operator's to set
		"profiles.prod.theme.shiny":   "not a color this rta understands",
		"profiles.prod.theme.primary": `"red"`,
	} {
		if !strings.Contains(got[key], want) {
			t.Errorf("%s: %q, want it to say %q", key, got[key], want)
		}
	}
	if len(got) != 4 {
		t.Errorf("problems = %v, want exactly the four lines", got)
	}
	if _, listed := got["profiles.prod.theme.accent"]; listed {
		t.Error("an entry that is fine was reported")
	}
}

// A block whose entries are all left out applies nothing, and the first glance —
// a count, a line saying what the palette is — must not say the opposite of the
// warnings under it. What stands is what is counted and named.
func TestThemeStandingIsWhatThePaletteKeepsOfTheBlock(t *testing.T) {
	cfg := load(t, `
profiles:
  prod:
    color: "#dd3333"
    theme:
      good: "#229F6C"
      bad: "#FF4357"
      primary: red
      shiny: "#123456"
      label: "#C792EA"
      muted: "#101010"
      accent: "#445566"
    plugins:
      pg:
        set: {host: p}
  calm:
    theme:
      good: "#229F6C"
      inverse: "#FF4357"
    plugins:
      pg:
        set: {host: c}
  plain:
    plugins:
      pg:
        set: {host: s}
`)
	if got, want := ThemeStanding(cfg, "prod"), (map[string]string{"label": "#C792EA", "accent": "#445566"}); !reflect.DeepEqual(got, want) {
		t.Errorf("prod stands as %v, want %v: the status colours, the colour that is none, the slot nothing has and the one too dim are not applied", got, want)
	}
	if got := ThemeStanding(cfg, "calm"); len(got) != 0 {
		t.Errorf("calm stands as %v, want nothing: every entry was left out", got)
	}
	for _, name := range []string{"plain", "nowhere"} {
		if got := ThemeStanding(cfg, name); len(got) != 0 {
			t.Errorf("%s stands as %v, want nothing", name, got)
		}
	}
}

// A profile from a file nobody named is not honoured, so none of its colours stand.
func TestNothingStandsOfAProfileFromAFileNobodyNamed(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{"prod": {Theme: map[string]string{"label": "#C792EA"}}}}
	if got := ThemeStanding(cfg, "prod"); len(got) != 0 {
		t.Errorf("stands as %v: a repository's profile painted something", got)
	}
}

func TestAnOperatorsOwnBadEntryIsNotTheEnvironmentsProblem(t *testing.T) {
	cfg := load(t, "theme:\n  nope: \"#112233\"\nprofiles:\n  prod:\n    color: \"#dd3333\"\n    plugins:\n      pg:\n        set: {host: p}\n")
	if problems := ThemeProblems(cfg, "prod"); len(problems) != 0 {
		t.Errorf("the profile was blamed for the operator's block: %v", problems)
	}
}
