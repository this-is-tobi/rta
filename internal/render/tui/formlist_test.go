package tui

import (
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A list box reads and writes the grammar the CLI's list flag takes: an
// element holding a comma, a double quote or space at an end is held in
// double quotes, a quote inside written twice. The box split at every comma
// and trimmed each piece, so no text typed into it gave such an element, and
// a default holding one was written into the box as the elements it was not.

// awkward holds one of each element the old box could not.
var awkward = []string{"a,b", `say "hi"`, " padded ", "plain"}

// A list seeded into a box, a record's own through Prefill or a declared
// default, reads back as itself, and so does text typed in the same grammar.
func TestAListBoxHoldsAnElementWithACommaAQuoteOrEdgeSpaces(t *testing.T) {
	c := plugin.Capability{ID: "note.edit", Summary: "edit", Safety: plugin.Write, Inputs: []plugin.Field{
		{Name: "tags", Type: plugin.StringSlice, Help: "t"},
		{Name: "keys", Type: plugin.SecretSlice, Help: "k"},
	}}
	cf := newCapForm(c, c.Inputs, map[string]any{"tags": awkward, "keys": awkward}, true, nil)
	want := `"a,b", "say ""hi""", " padded ", plain`
	for _, name := range []string{"tags", "keys"} {
		if box := *cf.bindings[name]; box != want {
			t.Errorf("the %s box holds %q, want %q", name, box, want)
		}
	}
	got := cf.values()
	for _, name := range []string{"tags", "keys"} {
		if back, _ := got[name].([]string); !slices.Equal(back, awkward) {
			t.Errorf("an untouched %s box came back as %q, want %q", name, got[name], awkward)
		}
	}

	*cf.bindings["tags"] = `x, "y, z" ,  " w", "q""uote"`
	if back, _ := cf.values()["tags"].([]string); !slices.Equal(back, []string{"x", "y, z", " w", `q"uote`}) {
		t.Errorf("typed tags came back as %q", back)
	}

	declared := plugin.Field{Name: "tags", Type: plugin.StringSlice, Default: awkward}
	if got := typedValue(declared, defaultString(declared)); !slices.Equal(got.([]string), awkward) {
		t.Errorf("a declared default reads back from its box as %q, want %q", got, awkward)
	}
}

// Two lists are one answer only when they hold the same elements: the one
// element "a, b" and the elements a and b were compared as the same text.
func TestTwoListsCompareByTheirElements(t *testing.T) {
	if seedString([]string{"a, b"}) == seedString([]string{"a", "b"}) {
		t.Error(`the element "a, b" compared as the elements a and b`)
	}
	typed, _ := typedValue(plugin.Field{Type: plugin.StringSlice}, "a,  b").([]string)
	if seedString([]string{"a", "b"}) != seedString(typed) {
		t.Error("a list typed with space after its comma compared as another")
	}
	if seedString([]string{"a", "b"}) == seedString([]string{"a", " b"}) {
		t.Error(`the element " b" compared as the element b`)
	}
}

// Text the box cannot read as a list is refused in the footer, as it is
// typed, in words that do not repeat a masked value.
func TestAListBoxRefusesAQuoteLeftOpen(t *testing.T) {
	for _, typ := range []plugin.FieldType{plugin.StringSlice, plugin.SecretSlice} {
		f := plugin.Field{Name: "tags", Type: typ}
		for text, refused := range map[string]bool{
			`a, "b`: true, `"a"b`: true, `a, "b,c"`: false, `5" screen, x`: false, "": false,
		} {
			err := validatorFor(f)(text)
			if (err != nil) != refused {
				t.Errorf("%v box %q: %v, refused should be %v", typ, text, err, refused)
			}
		}
	}
}

// A suggestion goes into a list box as an element it reads back as itself:
// quoted when it holds a comma, and whenever the item being typed opened
// with a quote; a comma inside quotes does not end the item being typed.
func TestAListSuggestionIsAnElementTheBoxReadsBack(t *testing.T) {
	declared := []string{"a,b", "alpha"}
	for typed, want := range map[string][]string{
		"x, a":      {`x, "a,b"`, "x, alpha"},
		`x, "a`:     {`x, "a,b"`, `x, "alpha"`},
		`"a,b", al`: {`"a,b", alpha`},
	} {
		if got := extending(typed, declared); !slices.Equal(got, want) {
			t.Errorf("extending(%q) = %q, want %q", typed, got, want)
		}
	}
}
