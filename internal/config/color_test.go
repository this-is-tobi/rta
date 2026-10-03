package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/theme"
)

// **The colour grammar is written down three times, and it has to mean one
// thing.** `theme.HexColor` enforces it for `theme:` overrides, `schemaColor`
// states it to an editor through `rta config schema`, and `colorPattern`
// decides whether a profile's badge gets painted.
//
// This package deliberately does not import the render tree — schema.go's own
// comment says why, and a leaf that knew about colours would be exactly the
// layering it declines — so the copies are held to one answer from here, in a
// test, which may import whatever it needs to compare them.
func TestTheColourGrammarIsTheSameEverywhereItIsWrittenDown(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"#FF6B7A", true},
		{"#ff6b7a", true},
		{"#FFF", false},
		{"FF6B7A", false},
		{"red", false},
		{"", false},
		{"#GGGGGG", false},
		{"#FF6B7A ", false},
	} {
		if got := colorPattern.MatchString(c.in); got != c.ok {
			t.Errorf("colorPattern %q = %v, want %v", c.in, got, c.ok)
		}
		if got := theme.HexColor.MatchString(c.in); got != c.ok {
			t.Errorf("theme.HexColor %q = %v, want %v — the two have drifted", c.in, got, c.ok)
		}
	}
}

// The palette's slot names are written down twice as well — the theme package
// owns them, and the schema tells an editor which ones a theme: block takes —
// and the schema once described them as the names a `rta theme` command lists,
// which is no command. It names them itself now, and this holds the two lists
// to the same names.
func TestTheSchemaNamesTheSlotsThePaletteHas(t *testing.T) {
	want := theme.Fields()
	got := append([]string(nil), themeKeys...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the schema's theme keys are %v, the palette's are %v", got, want)
	}
	block := Schema()["properties"].(map[string]any)["theme"].(map[string]any)
	description := block["description"].(string)
	for _, name := range want {
		if !strings.Contains(description, name) {
			t.Errorf("the theme block's description does not name %q: %s", name, description)
		}
	}
	if strings.Contains(description, "rta theme") {
		t.Errorf("the theme block's description sends the reader to a command that does not exist: %s", description)
	}
}

// And a profile is judged by exactly that grammar, so the file, the editor's
// completion and the badge agree about which lines are a mistake.
func TestAProfileColourIsJudgedByThatGrammar(t *testing.T) {
	if (Profile{}).BadColor() {
		t.Error("a profile with no colour was called bad; not writing one is the ordinary case")
	}
	if (Profile{Color: "#FF6B7A"}).BadColor() {
		t.Error("a colour was rejected")
	}
	if !(Profile{Color: "red"}).BadColor() {
		t.Error("a word was accepted as a colour")
	}
}
