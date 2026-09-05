package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Tab on the fields that complete from this machine rather than from a
// service — which is nearly every field in the app.
//
// The three fetching paths already answered tab with one rule; these did not,
// because they answered it with huh's keymap instead of with the rule, and
// huh cannot express it: Input.Update tests Next before AcceptSuggestion and
// returns early when the value does not validate, so tab bound to both was a
// dead key on exactly the boxes that had something to offer.

// **The report.** The profile editor's plugin field is required, starts empty
// on a new entry, and its own help says "press tab" — and tab did nothing at
// all there: huh refused the key on the failed required-empty validation
// before the widget could look at its suggestions.
//
// It cannot complete on that press either, since bubbles matches only against
// non-empty text. So the answer is the names themselves, the same answer an
// empty coordinate gets from the cluster.
func TestTabOnAnEmptyRequiredBoxSaysWhatIsOnOffer(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}})
	m.profileOpen = "staging"
	// A new entry inherits whatever the plugins pane has under the cursor, so
	// the empty box this is about is the one where nothing is selected.
	m.pluginSel = len(m.plugins)
	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)

	if got := *nm.form.bindings[profilePluginField]; got != "" {
		t.Fatalf("a new entry starts with %q, not the empty box this is about", got)
	}
	focused := nm.form.form.GetFocusedField()
	nm = pressTab(t, nm)
	if !strings.Contains(nm.flash, "db") {
		t.Errorf("tab on the empty plugin picker said %q — it must name what is installed", nm.flash)
	}
	if nm.form.form.GetFocusedField() != focused {
		t.Error("tab left the field it had just answered for")
	}
}

// And the press after it moves on, because a key that only ever listed would
// be the dead key this whole rule exists to remove. The list has been said;
// there is nothing else tab can do here.
func TestTheTabAfterTheListingMovesOn(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}})
	m.profileOpen = "staging"
	m.pluginSel = len(m.plugins)
	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)

	focused := nm.form.form.GetFocusedField()
	nm = pressTab(t, nm) // lists
	nm = pressTab(t, nm) // nothing left to say
	if nm.form.form.GetFocusedField() == focused {
		t.Error("the second tab on an empty box stayed — the box is the same and so is the list")
	}
}

// **"It should have completed the digest."** A profile names a third-party
// plugin pinned to the artifact it was approved as, and nobody types a digest
// — typing one wrong is the exact failure the pin exists to prevent — so the
// field that carries it is where the answer has to be offered.
//
// The box holds the bare name, the offer is `name@digest`, and that strictly
// extends it: one press takes it, and the cursor stays so the digest that was
// just accepted is on screen beside the field it belongs to.
func TestTabCompletesAPluginNameToItsPinnedArtifact(t *testing.T) {
	noHistory(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{Name: "weather", Summary: "weather summary",
		Capabilities: []plugin.Capability{
			{ID: "weather.now", Summary: "now", Safety: plugin.Read, Run: run},
		}}, registry.Origin{Path: "/usr/local/bin/rta-plugin-weather", Digest: pinnedDigest}); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{}},
	}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 100, 40
	m.profileOpen = "staging"
	m.pluginSel = len(m.plugins) // no seed: the box starts empty

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)

	pinned := ""
	for _, row := range nm.plugins {
		if row.plugin.Name == "weather" {
			pinned = row.pinnedName()
		}
	}
	if !strings.Contains(pinned, "@") {
		t.Fatalf("the fixture's plugin is not pinned to an artifact (%q)", pinned)
	}

	nm.form.form = typeInto(nm.form.form, "weather")
	nm = pressTab(t, nm)
	if got := *nm.form.bindings[profilePluginField]; got != pinned {
		t.Errorf("after tab the field holds %q, want %q — the digest was not completed", got, pinned)
	}
	// By which field, not by which widget: naming a plugin rebuilds the form
	// on that plugin's own config boxes (reseedOnConnPluginChange), so the
	// widget the cursor is in is a new object holding the same question.
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs[profilePluginField]) {
		t.Error("the accept moved off the field, so the digest it just took is not on screen")
	}
}

// A digest that is a plausible length, so pinnedName's Short() has something
// to shorten and the completion under test is the real shape.
const pinnedDigest = "sha256:9f2b1c4e7a3d5086bb1e2f4c6d8a0b2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f"

// A path is walked a segment at a time, which is the whole reason the accept
// keeps the cursor.
//
// This is what one press doing both jobs cost: tab completed `~/dev` and threw
// the cursor onto the next field, so the only way to reach a nested path was
// to type the rest of it. Every shell has behaved otherwise since before any
// of this existed.
func TestTabWalksAPathWithoutLeavingTheField(t *testing.T) {
	noHistory(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "alpha", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := plugin.Capability{
		ID: "demo.read", Summary: "read", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "file", Type: plugin.Path},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)
	m.form.form = typeInto(m.form.form, filepath.Join(dir, "al"))

	alpha := filepath.Join(dir, "alpha") + string(filepath.Separator)
	focused := m.form.form.GetFocusedField()
	m = pressTab(t, m)
	if got := *cf.bindings["file"]; got != alpha {
		t.Fatalf("the first tab left %q, want %q", got, alpha)
	}
	if m.form.form.GetFocusedField() != focused {
		t.Fatal("completing a path segment cost the focus — the rest of the path is now untypable by tab")
	}
	// And the next segment needs no typing at all: the accepted directory
	// ends in a separator, so what is inside it extends the box.
	m = pressTab(t, m)
	beta := filepath.Join(dir, "alpha", "beta") + string(filepath.Separator)
	if got := *cf.bindings["file"]; got != beta {
		t.Errorf("the second tab left %q, want %q — the walk stops one level in", got, beta)
	}
	if m.form.form.GetFocusedField() != focused {
		t.Error("the second accept moved off the field")
	}
	// And the end of the walk is the end of the field: beta is empty, so
	// nothing extends the box and tab is navigation again. This half is the
	// one huh cannot do on its own — its accept simply does nothing when
	// there is nothing to accept — so a path box without it is a box the
	// cursor cannot tab out of.
	m = pressTab(t, m)
	if m.form.form.GetFocusedField() == focused {
		t.Error("tab at the end of a path stayed — the cursor cannot leave the box")
	}
}

// A comma list is the third field completed a piece at a time, and it needs
// the same two halves as a path: the accept stays so the next item can be
// taken, and the press after the last one leaves.
func TestTabWalksACommaListAndThenLeavesIt(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.tag", Summary: "tag", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "tags", Type: plugin.StringSlice,
				Suggest: func(context.Context, plugin.Request) []string { return []string{"alpha", "beta"} }},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)
	focused := m.form.form.GetFocusedField()

	m.form.form = typeInto(m.form.form, "al")
	m = pressTab(t, m)
	if got := *cf.bindings["tags"]; got != "alpha" {
		t.Fatalf("tags = %q, want the first item taken", got)
	}
	if m.form.form.GetFocusedField() != focused {
		t.Fatal("the accept moved off the field — a second item is now untypable by tab")
	}

	m.form.form = typeInto(m.form.form, ",be") // typeInto appends to the box
	m = pressTab(t, m)
	if got := *cf.bindings["tags"]; got != "alpha,beta" {
		t.Fatalf("tags = %q, want the second item taken beside the first", got)
	}

	m = pressTab(t, m)
	if m.form.form.GetFocusedField() == focused {
		t.Error("tab at the end of a comma list stayed — the cursor cannot leave the box")
	}
}

// The theme editor is the other form in this package, and it has to answer
// tab identically — the rule lives in tabOn, not in a keymap the two happen
// to share.
//
// It is also where the listing earns its keep: every box starts empty because
// an empty box means "no override", so until the palette was said out loud
// the only way to find out what "tab completes" offered was to guess a prefix.
func TestTabInTheThemeEditorFollowsTheSameRule(t *testing.T) {
	noHistory(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.mode = modeTheme
	m.themeForm = newThemeForm(nil)
	m.themeForm.form = startedHuhForm(m.themeForm.form)

	focused := m.themeForm.form.GetFocusedField()
	m = pressTab(t, m)
	if !strings.HasPrefix(m.flash, "#") {
		t.Errorf("tab on an empty palette box said %q — it must name the colours on offer", m.flash)
	}
	if m.themeForm.form.GetFocusedField() != focused {
		t.Fatal("tab left the box it had just answered for")
	}
	m = pressTab(t, m)
	if m.themeForm.form.GetFocusedField() == focused {
		t.Error("tab is a dead key in the theme editor — ten boxes and no way to walk them")
	}
}

// Accepting in the theme editor keeps the cursor too, so the colour that was
// taken is on screen beside the field it was taken for.
func TestTheThemeEditorAcceptsInPlace(t *testing.T) {
	noHistory(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.mode = modeTheme
	m.themeForm = newThemeForm(nil)
	m.themeForm.form = startedHuhForm(m.themeForm.form)

	first := m.themeForm.offers[themeFieldOrder[0]][0]
	focused := m.themeForm.form.GetFocusedField()
	m.themeForm.form = typeInto(m.themeForm.form, first[:4])
	m = pressTab(t, m)
	if got := *m.themeForm.bindings[themeFieldOrder[0]]; got != first {
		t.Errorf("%s = %q, want the palette entry accepted", themeFieldOrder[0], got)
	}
	if m.themeForm.form.GetFocusedField() != focused {
		t.Error("the accept moved off the box")
	}
}

// Tab advances without validating, and the form still refuses to submit on an
// empty required box.
//
// Both halves matter and they pull in opposite directions. Tab is navigation:
// every form anybody has used lets you tab past a box you have not filled in,
// and blocking it is how the reported bug felt from the inside. But huh's
// last field has nowhere to advance to, so an unchecked NextField there is a
// submit — which is the one place "tab just navigates" would have written a
// half-filled profile to disk. It does not, because huh blurs the field it is
// leaving and a blur validates.
func TestTabPastARequiredBoxNeverSubmitsIt(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.two", Summary: "two", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "first", Type: plugin.String},
			{Name: "last", Type: plugin.String, Required: true},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)

	m = pressTab(t, m) // off the first box, which nothing requires
	m = pressTab(t, m) // and off the last, which is required and empty
	if m.form == nil {
		t.Fatal("the form closed — tab submitted a required box nobody filled in")
	}
	if state := m.form.form.State; state == huh.StateCompleted {
		t.Errorf("form state = %v, want it still open", state)
	}
}

// shift+tab is the way back, and nothing here took it.
//
// It is the obvious place to move completion to when tab looks overloaded, and
// it is the wrong one: "previous field" is as standard as "next field", huh
// binds it, every form anybody has used binds it — and it is the only way back
// through a form, so spending it would leave tab as the sole direction of
// travel. Tab did not need a second key; it needed to stop being two keymap
// entries that fired in the wrong order.
func TestShiftTabIsStillTheWayBack(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)

	first := m.form.form.GetFocusedField()
	m = pressTab(t, m) // an untouched box: says what is on offer
	m = pressTab(t, m) // and now there is nothing left to say
	if m.form.form.GetFocusedField() == first {
		t.Fatal("tab did not advance, so this proves nothing about coming back")
	}

	back, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = back.(Model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			back, _ = m.Update(msg)
			m = back.(Model)
		}
	}
	if m.form.form.GetFocusedField() != first {
		t.Error("shift+tab did not go back — the form has one direction of travel")
	}
}

// listing is one line, so a field offering a directory says how many rather
// than saying all of them.
func TestListingBoundsWhatItSaysOutLoud(t *testing.T) {
	short := []string{"alpha", "beta"}
	if got := listing(short); got != "alpha, beta" {
		t.Errorf("listing(%v) = %q", short, got)
	}
	long := make([]string, listedAtMost+3)
	for i := range long {
		long[i] = string(rune('a' + i))
	}
	got := listing(long)
	if !strings.HasSuffix(got, "and 3 more") {
		t.Errorf("listing of %d = %q, want the count of what it did not name", len(long), got)
	}
	if strings.Contains(got, long[listedAtMost]) {
		t.Errorf("listing named past the bound: %q", got)
	}
	// The bound's exact value is a taste call and deliberately not pinned
	// here — what is pinned is that there is one, because the list this line
	// most often carries is a whole directory.
	flood := make([]string, 500)
	for i := range flood {
		flood[i] = "a-fairly-long-suggestion-name"
	}
	if n := len(listing(flood)); n > 400 {
		t.Errorf("listing of %d entries is %d characters — the flash is one line", len(flood), n)
	}
}

// A tab that is not in a text box is still huh's — a picker, a confirm and a
// multi-select advance on their own bindings, and this package must not have
// quietly taken the key off them.
func TestTabStillAdvancesAPicker(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.pick", Summary: "pick", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "mode", Type: plugin.String, Options: []string{"one", "two"}, Required: true},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)

	focused := m.form.form.GetFocusedField()
	m = pressTab(t, m)
	if m.form.form.GetFocusedField() == focused {
		t.Error("tab did not leave the picker")
	}
	m.form.form = typeInto(m.form.form, "x")
	if got := *cf.bindings["note"]; got != "x" {
		t.Errorf("note = %q — the cursor did not land on the box below the picker", got)
	}
}

// The theme editor answers the arrows by the same rule (browseOn), over its
// palette: every box starts empty, so without this the palette could be
// listed by tab and never walked.
func TestArrowsBrowseThePaletteInTheThemeEditor(t *testing.T) {
	noHistory(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.mode = modeTheme
	m.themeForm = newThemeForm(nil)
	m.themeForm.form = startedHuhForm(m.themeForm.form)
	name, ok := m.themeForm.focusedInput()
	if !ok {
		t.Fatal("no focused box")
	}
	offered := m.themeForm.offers[name]
	m = pressKey(t, m, downKey)
	if got := *m.themeForm.bindings[name]; got != offered[0] {
		t.Fatalf("down on an empty palette box = %q, want %q", got, offered[0])
	}
	m = pressKey(t, m, downKey)
	if got := *m.themeForm.bindings[name]; got != offered[1] {
		t.Fatalf("second down = %q, want %q", got, offered[1])
	}
	m = pressKey(t, m, upKey)
	if got := *m.themeForm.bindings[name]; got != offered[0] {
		t.Fatalf("up = %q, want %q", got, offered[0])
	}
	if n, _ := m.themeForm.focusedInput(); n != name {
		t.Error("browsing left the box")
	}
}
