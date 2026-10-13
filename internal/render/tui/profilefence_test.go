package tui

import (
	"strings"
	"testing"

	huh "charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
)

// A fence is a boundary an operator wrote on purpose, and the connection form
// edits `set:` and not `only:` — so the two ways it could hurt one are by
// forgetting it and by writing a default it refuses on every call.

func fencedStaging() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {
			Set:  map[string]any{"host": "staging.internal"},
			Only: map[string]any{"schema": []any{"app", "app-jobs"}},
		}}},
	}}
}

// Editing a host rebuilds `set:` from the boxes, and the fence is not in a
// box: it must come through the save as it went in, or editing the host would
// silently take the wall down.
func TestEditingAConnectionKeepsItsFence(t *testing.T) {
	noHistory(t)
	m := profileModel(t, fencedStaging())
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	*nm.form.bindings[profileSetPrefix+"host"] = "moved.internal"
	_, _ = nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	got := onDisk.Profiles["staging"].Plugins["db"]
	if got.Set["host"] != "moved.internal" {
		t.Errorf("host = %v, want the edited value", got.Set["host"])
	}
	list, _ := got.Only["schema"].([]any)
	if len(list) != 2 || list[0] != "app" || list[1] != "app-jobs" {
		t.Errorf("the fence did not survive the save: %#v", got.Only)
	}
}

// A default the fence refuses on every call is refused where the person is
// looking at the box: said beside it as the box is left, with the values the
// list holds first, because the footer that carries it is cut at the edge of a
// terminal and the list is the answer. The form edits `set:` and not `only:`, so
// the way to change the list comes after, and nothing is written.
func TestAValueTypedOutsideTheFenceIsRefusedAtItsBox(t *testing.T) {
	noHistory(t)
	m := profileModel(t, fencedStaging())
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	nm = focusBox(t, nm, profileSetPrefix+"schema")
	nm = typeKeys(t, nm, "prod")

	// The save key, which submits what is in the boxes the way enter on each would.
	next, _ = nm.fastSubmitForm()
	nm = next.(Model)

	if nm.form == nil || nm.mode != modeForm {
		t.Fatalf("a value outside the fence closed the form: mode %v", nm.mode)
	}
	box := nm.form.form.GetFocusedField()
	if box != huh.Field(nm.form.inputs[profileSetPrefix+"schema"]) {
		t.Fatalf("the cursor is not on the box that holds the value outside the fence")
	}
	if err := box.Error(); err == nil || err.Error() != "must be one of app, app-jobs" {
		t.Errorf("the box says %v, want what the list holds", err)
	}
	// In the footer, and in the part of it that survives a narrow terminal.
	nm.width = 60
	footer := ansi.Strip(nm.footerFor(modeForm))
	if !strings.Contains(footer, "must be one of app, app-jobs") {
		t.Errorf("the footer at 60 columns = %q, want the allowed values", footer)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, written := onDisk.Profiles["staging"].Plugins["db"].Set["schema"]; written {
		t.Error("a refused save wrote the value anyway")
	}
}

// A box that only displays a default the file never stated is not a statement:
// nothing is written for it, so a default the fence would refuse is not refused
// — the plugin's own `localhost` under a list of the hosts the connection
// reaches is what every untouched form shows.
func TestADisplayedDefaultOutsideTheFenceIsNotRefused(t *testing.T) {
	noHistory(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {
			Only: map[string]any{"host": []any{"staging.internal"}},
		}}},
	}})
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	if got := *nm.form.bindings[profileSetPrefix+"host"]; got != "localhost" {
		t.Fatalf("the host box shows %q, want the plugin's own default", got)
	}

	next, _ = nm.fastSubmitForm()
	nm = next.(Model)
	if nm.form != nil {
		t.Fatalf("an untouched form was refused: %v", nm.form.form.Errors())
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if set := onDisk.Profiles["staging"].Plugins["db"].Set; len(set) != 0 {
		t.Errorf("an untouched save wrote %v", set)
	}
}

// The save is the last line of defence for the boxes that hold no typed text — a
// picker and a toggle — and a value that reached it from somewhere else: it
// refuses the same way, with the values first, and puts the cursor on the box,
// where the form it rebuilds would otherwise open on the first of ninety lines
// with the box it is about out of sight.
func TestTheConnectionFormRefusesADefaultOutsideTheFence(t *testing.T) {
	noHistory(t)
	m := profileModel(t, fencedStaging())
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	*nm.form.bindings[profileSetPrefix+"schema"] = "prod"
	after, _ := nm.saveConnForm()
	nm = after.(Model)

	if !strings.HasPrefix(nm.flash, "set.schema must be one of app, app-jobs") ||
		!strings.Contains(nm.flash, "rta profile set staging --plugin db --only schema=") {
		t.Errorf("flash = %q, want the refusal with the values first, and the command that changes the list", nm.flash)
	}
	if strings.Contains(nm.flash, "prod") {
		t.Errorf("the refusal repeats the value: %q", nm.flash)
	}
	if nm.form == nil || nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs[profileSetPrefix+"schema"]) {
		t.Error("the rebuilt form does not put the cursor on the box that was refused")
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, written := onDisk.Profiles["staging"].Plugins["db"].Set["schema"]; written {
		t.Error("a refused save wrote the default anyway")
	}

	// A value inside the list is an ordinary edit.
	next, _ = m.startConnForm("db")
	nm = next.(Model)
	*nm.form.bindings[profileSetPrefix+"schema"] = "app-jobs"
	_, _ = nm.saveConnForm()
	onDisk, err = config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Plugins["db"].Set["schema"]; got != "app-jobs" {
		t.Errorf("schema = %v, a value the list holds was refused", got)
	}
}

// A fence is about the plugin it was written for. An entry re-aimed at another
// one would carry keys the new plugin reads nothing from, which Lookup refuses
// whole — so it is dropped, and the flash says so.
func TestRepointingAnEntryDropsTheFenceOfThePluginItLeft(t *testing.T) {
	noHistory(t)
	m := profileModel(t, fencedStaging())
	if err := m.reg.Register(otherDBPlugin()); err != nil {
		t.Fatal(err)
	}
	m.plugins = pluginRows(m.reg, config.Dashboard{}, nil)
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profilePluginField] = "db2"
	after, _, rebuilt := nm.reseedOnConnPluginChange()
	if !rebuilt {
		t.Fatal("no rebuild")
	}
	nm = after.(Model)
	nm.form.form = startedForm(nm.form)
	after, _ = nm.saveConnForm()
	nm = after.(Model)

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Plugins["db2"]; len(got.Only) != 0 {
		t.Errorf("the entry kept a fence written for another plugin: %#v", got.Only)
	}
	if !strings.Contains(nm.flash, "only.schema") {
		t.Errorf("flash = %q, want it to say the fence was dropped", nm.flash)
	}
}

// The pane is the one place in the TUI that says a connection is fenced, since
// the form beside it does not edit the list.
func TestTheProfilePaneSaysAConnectionIsFenced(t *testing.T) {
	noHistory(t)
	m := profileModel(t, fencedStaging())
	row := m.profiles[0]
	if len(row.conns) != 1 {
		t.Fatalf("the fixture's profile holds %d connections", len(row.conns))
	}
	got := connSummary(row.conns[0])
	if !strings.Contains(got, "host=staging.internal") || !strings.Contains(got, "only schema=app|app-jobs") {
		t.Errorf("summary = %q, want the default and the fence", got)
	}
	// A connection that is fenced and sets nothing is not "sets nothing".
	only := connRow{key: "db", conn: config.Connection{Only: map[string]any{"schema": []any{"app"}}}}
	if got := connSummary(only); strings.Contains(got, "sets nothing") || !strings.Contains(got, "only schema=app") {
		t.Errorf("summary = %q", got)
	}
}
