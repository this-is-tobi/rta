package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/boxlist"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
		if got := boxlist.Extend(typed, declared); !slices.Equal(got, want) {
			t.Errorf("Extend(%q) = %q, want %q", typed, got, want)
		}
	}
}

// listCap is a capability with one list box that suggests the candidates it is
// given, the shape of the seventeen core capabilities that take several of
// something.
func listCap(candidates ...string) plugin.Capability {
	return plugin.Capability{
		ID: "demo.tag", Summary: "tag", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "tag", Type: plugin.StringSlice,
			Suggest: func(context.Context, plugin.Request) []string { return candidates }}},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
}

func listForm(t *testing.T, candidates ...string) (Model, *capForm) {
	t.Helper()
	c := listCap(candidates...)
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	return formModel(t, cf), cf
}

// browseTo presses an arrow in the form and reads the box back.
func browseTo(t *testing.T, m Model, cf *capForm, field string, key tea.KeyPressMsg, want string) Model {
	t.Helper()
	m = pressKey(t, m, key)
	if got := *cf.bindings[field]; got != want {
		t.Fatalf("after %v the %s box holds %q, want %q", key, field, got, want)
	}
	return m
}

// The arrows walk a list box's suggestions as they walk a single value's. The
// offer was recomputed from the box after each arrow, so once a candidate was
// placed the list was only the candidates that begin with it, and the walk
// stood on the first one for good.
func TestArrowsBrowseAListBoxOneCandidateAtATime(t *testing.T) {
	noHistory(t)
	m, cf := listForm(t, "a", "b", "c")

	for _, want := range []string{"a", "b", "c", "a"} {
		m = browseTo(t, m, cf, "tag", downKey, want)
	}
	for _, want := range []string{"c", "b"} {
		m = browseTo(t, m, cf, "tag", upKey, want)
	}
}

// A list is walked one item at a time from where an item begins, with what the
// box already holds left out: the box after "a, " is offered b and c, since
// naming a twice is a mistake the walk should not invite.
func TestArrowsBrowseTheNextItemOfAListFromWhereItBegins(t *testing.T) {
	noHistory(t)
	m, cf := listForm(t, "a", "b", "c")
	m.form.form = typeInto(m.form.form, "a, ")

	for _, want := range []string{"a, b", "a, c", "a, b"} {
		m = browseTo(t, m, cf, "tag", downKey, want)
	}
	m = browseTo(t, m, cf, "tag", upKey, "a, c")

	m.form.form = typeInto(m.form.form, ", ")
	m = browseTo(t, m, cf, "tag", downKey, "a, c, b")
}

// Typing is the box's own again: an arrow over text somebody wrote, or over a
// candidate they then went on to edit, is the widget's.
func TestTypingOverABrowsedListItemHandsTheArrowsBack(t *testing.T) {
	noHistory(t)
	m, cf := listForm(t, "a", "b", "c")

	m.form.form = typeInto(m.form.form, "x")
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["tag"]; got != "x" {
		t.Fatalf("an arrow over typed text replaced it with %q", got)
	}

	m.form.form = typeInto(m.form.form, ", ")
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["tag"]; got != "x, a" {
		t.Fatalf("an arrow at the start of the next item holds %q, want x, a", got)
	}
	m.form.form = typeInto(m.form.form, "z")
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["tag"]; got != "x, az" {
		t.Errorf("an arrow over an edited candidate replaced it with %q", got)
	}
}

// A path box is walked by the widget from what is typed, and rta places a
// candidate only into an empty one, as before.
func TestAPathBoxStillBrowsesFromAnEmptyBoxOnly(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.key", Summary: "key", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "identity", Type: plugin.Path,
			Suggest: func(context.Context, plugin.Request) []string { return []string{"/keys/one", "/keys/two"} }}},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)

	m = pressKey(t, m, downKey)
	if got := *cf.bindings["identity"]; got != "/keys/one" {
		t.Fatalf("down on an empty path box holds %q, want /keys/one", got)
	}
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["identity"]; got != "/keys/two" {
		t.Fatalf("second down holds %q, want /keys/two", got)
	}

	m.form.form = typeInto(m.form.form, "x")
	_ = pressKey(t, m, downKey)
	if got := *cf.bindings["identity"]; got != "/keys/twox" {
		t.Errorf("down over edited text holds %q — rta replaced what was typed", got)
	}
}

// The core capabilities that take several of something, through the registry
// they ship in. note.rm's ids are numbers a person cannot guess, and one rm
// takes several, so the walk is the way to name them.
func TestArrowsBrowseTheNoteIdsOfNoteRm(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	add, _ := reg.Capability("note.add")
	for _, title := range []string{"one", "two", "three", "four", "five"} {
		if _, err := add.Run(context.Background(), plugin.NewRequest(map[string]any{"title": title}, false, false)); err != nil {
			t.Fatalf("seeding a note: %v", err)
		}
	}
	rm, ok := reg.Capability("note.rm")
	if !ok {
		t.Fatal("note.rm is not registered")
	}
	cf := newCapForm(rm, rm.Inputs, nil, true, nil)
	m := formModel(t, cf)

	for _, want := range []string{"1", "2", "3", "4", "5", "1"} {
		m = browseTo(t, m, cf, "id", downKey, want)
	}
	m.form.form = typeInto(m.form.form, ", ")
	for _, want := range []string{"1, 2", "1, 3"} {
		m = browseTo(t, m, cf, "id", downKey, want)
	}
}

// net.hosts.rm over a hosts file of its own: the names in it, one at a time.
func TestArrowsBrowseTheHostnamesOfNetHostsRm(t *testing.T) {
	noHistory(t)
	file := filepath.Join(t.TempDir(), "hosts")
	hosts := "127.0.0.1 localhost\n10.0.0.1 alpha.local beta.local\n10.0.0.2 gamma.local\n"
	if err := os.WriteFile(file, []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	rm, ok := reg.Capability("net.hosts.rm")
	if !ok {
		t.Fatal("net.hosts.rm is not registered")
	}
	cf := newCapForm(rm, rm.Inputs, map[string]any{"file": file}, true, nil)
	m := formModel(t, cf)

	for _, want := range []string{"localhost", "alpha.local", "beta.local", "gamma.local", "localhost"} {
		m = browseTo(t, m, cf, "hostname", downKey, want)
	}
}
