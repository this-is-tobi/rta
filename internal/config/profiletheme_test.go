package config

import (
	"reflect"
	"strings"
	"testing"
)

// A profile states its own palette beside its colour, and these hold what makes
// that a part of the profile rather than a second block next to it: it is read
// with the entry, it is written back where the entry lives, and it carries no
// more trust than the entry does.

const themedProfile = `profiles:
  prod:
    color: "#dd3333"
    theme:
      primary: "#112233"
      good: "#00aa66"
    plugins:
      db:
        set:
          host: p.example
`

func TestAProfileThemeIsReadWithTheProfileAndIsNotAnUnknownKey(t *testing.T) {
	layout(t, map[string]string{"config.yaml": themedProfile})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	prod := cfg.Profiles["prod"]
	want := map[string]string{"primary": "#112233", "good": "#00aa66"}
	if !reflect.DeepEqual(prod.Theme, want) {
		t.Errorf("theme = %v, want %v", prod.Theme, want)
	}
	// The loader names every key of a profile that no field claims, and a
	// theme: that was not in profileKeys would be named as one — with the
	// profile refused for it, since Lookup refuses a profile with a key nothing
	// reads.
	if unknown := prod.UnknownKeys(); len(unknown) != 0 {
		t.Errorf("a profile's theme: is reported as a key nothing reads: %v", unknown)
	}
}

// A theme: in a drop-in is the profile's, and a profile is stated in one file —
// the rule every other key of the entry already follows.
func TestAProfileThemeLivesInTheFileThatStatesItsProfile(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/10-prod.yaml": themedProfile,
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["prod"].Theme["primary"]; got != "#112233" {
		t.Fatalf("the drop-in's theme was not read with its profile: %v", cfg.Profiles["prod"].Theme)
	}

	// A write that changes the profile goes back to the drop-in, theme and all,
	// and copies nothing of it into the file.
	if err := Mutate(func(c Config) (Config, bool) {
		p := c.Profiles["prod"]
		p.Note = "the shop"
		c.Profiles["prod"] = p
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	dropIn := read(t, dir+"/config.d/10-prod.yaml")
	for _, want := range []string{"note: the shop", `primary: "#112233"`, `good: "#00aa66"`} {
		if !strings.Contains(dropIn, want) {
			t.Errorf("the drop-in lost %q:\n%s", want, dropIn)
		}
	}
	if main := read(t, dir+"/config.yaml"); strings.Contains(main, "theme") || strings.Contains(main, "#112233") {
		t.Errorf("a profile's theme was copied into the config file:\n%s", main)
	}
}

// Stating the profile's theme in a second file is stating the profile twice,
// which is refused naming both files rather than merged by an order nobody
// wrote down: the same answer the rest of the entry gets.
func TestAProfileThemeInAnotherFileThanItsProfileIsRefused(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml":           themedProfile,
		"config.d/10-more.yaml": "profiles:\n  prod:\n    theme:\n      bad: \"#ff0000\"\n",
	})
	_, err := LoadFile()
	if err == nil {
		t.Fatal("a profile stated in two files was read")
	}
	for _, want := range []string{"profile prod", "config.yaml", "10-more.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// A profile in a file nobody named is not honoured at all, so what it says
// about colour is not either: the stamp the loader puts on it is what every
// reader of a profile asks, and the theme is read through that.
func TestAProfileThemeFromAFileNobodyNamedCarriesNoTrust(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	writeFiles(t, dir, map[string]string{".rta.yaml": themedProfile})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	prod, ok := cfg.Profiles["prod"]
	if !ok || prod.Theme["primary"] != "#112233" {
		t.Fatalf("the fallback's profile was not read: %v", cfg.Profiles)
	}
	if prod.Trusted() {
		t.Error("a profile from the working-directory fallback reads as trusted, and its theme with it")
	}
}

// What a person typed around the theme survives a rewrite of the profile: the
// comment above it, the entries in the order they were written, and the colour
// beside it.
func TestAProfileThemeSurvivesARewriteOfItsProfile(t *testing.T) {
	text := `profiles:
  prod:
    # the red one, for the shop
    color: "#dd3333"
    theme:
      # a quieter selection
      primary: "#112233"
    plugins:
      db:
        set:
          host: p.example
`
	dir := layout(t, map[string]string{"config.yaml": text})
	if err := Mutate(func(c Config) (Config, bool) {
		p := c.Profiles["prod"]
		p.TTL = "1h"
		c.Profiles["prod"] = p
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir+"/config.yaml")
	for _, want := range []string{"# the red one, for the shop", "# a quieter selection", `primary: "#112233"`, "ttl: 1h"} {
		if !strings.Contains(got, want) {
			t.Errorf("rewriting the profile lost %q:\n%s", want, got)
		}
	}
}

// And a write that changes nothing writes nothing: the guarantee every writer
// already makes, which a field the typed tree could not carry would break.
func TestARewriteThatChangesNothingLeavesAThemedFileAlone(t *testing.T) {
	dir := layout(t, map[string]string{"config.yaml": themedProfile})
	if err := Mutate(func(c Config) (Config, bool) { return c, true }); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir+"/config.yaml"); got != themedProfile {
		t.Errorf("an unchanged write rewrote the file:\n%s", got)
	}
}

// The schema describes a profile's theme: the way it describes the top-level
// one — the same names, the same grammar — so an editor completes and checks
// the two alike.
func TestTheSchemaDescribesAProfileThemeLikeTheTopLevelOne(t *testing.T) {
	s := Schema()
	global := s["properties"].(map[string]any)["theme"].(map[string]any)
	profile := s["$defs"].(map[string]any)["profile"].(map[string]any)["properties"].(map[string]any)["theme"].(map[string]any)
	for _, key := range []string{"type", "additionalProperties"} {
		if !reflect.DeepEqual(global[key], profile[key]) {
			t.Errorf("a profile's theme %s is %v, the top level's is %v", key, profile[key], global[key])
		}
	}
	if d, _ := profile["description"].(string); !strings.Contains(d, "environment") {
		t.Errorf("a profile's theme description does not say whose it is: %q", d)
	}
	color := s["$defs"].(map[string]any)["profile"].(map[string]any)["properties"].(map[string]any)["color"].(map[string]any)
	if d, _ := color["description"].(string); strings.Contains(d, "and nothing else") {
		t.Errorf("a profile's colour is still described as painting only its name: %q", d)
	}
}

// The status colours are the operator's to set in their own named config file,
// and what the schema tells an editor says so: the top-level block names all
// ten slots and a profile's names the five an environment can state, so a
// `good:` under a profile is marked as the mistake it is while it is typed.
func TestTheSchemaDoesNotOfferAProfileTheStatusColours(t *testing.T) {
	s := Schema()
	global := s["properties"].(map[string]any)["theme"].(map[string]any)
	profileProps := s["$defs"].(map[string]any)["profile"].(map[string]any)["properties"].(map[string]any)
	profile := profileProps["theme"].(map[string]any)
	names := func(block map[string]any) []string {
		return block["propertyNames"].(map[string]any)["enum"].([]string)
	}
	if got := names(global); len(got) != 10 {
		t.Errorf("the top-level theme names %v, want the ten slots", got)
	}
	if got, want := names(profile), []string{"primary", "accent", "muted", "faint", "label"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a profile's theme names %v, want the five text colours %v", got, want)
	}
	for place, block := range map[string]map[string]any{"the top level's": global, "a profile's": profile} {
		d, _ := block["description"].(string)
		for _, want := range []string{"status colours", "good, warn, bad, inverse, ink", "your own config file"} {
			if !strings.Contains(d, want) {
				t.Errorf("%s theme description does not say %q: %q", place, want, d)
			}
		}
		if !strings.Contains(d, "1.5:1") {
			t.Errorf("%s theme description does not say what a text colour keeps: %q", place, d)
		}
	}
	color := profileProps["color"].(map[string]any)["description"].(string)
	for _, want := range []string{"badge keeps it", "1.5:1"} {
		if !strings.Contains(color, want) {
			t.Errorf("a profile's colour description does not say %q: %q", want, color)
		}
	}
}
