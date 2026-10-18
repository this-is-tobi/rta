package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A run form is the call's own questions and the connection it is aimed at, and
// the connection is most of the boxes: a database declares a dozen settings to
// the one or two the call is about. The settings an operator states in their
// config come after the call's own inputs, each block under the name of its
// group (formgroups_test.go), and the cursor starts on the first box that has to
// be answered rather than on whatever was declared first.

// connectionPlugin declares its connection first, the way a plugin tends to: a
// mount and an address the operator's config states, in front of the path that
// is the call.
func connectionPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	mount := plugin.Field{Name: "mount", Type: plugin.String, Default: "secret", Config: "mount", Local: true}
	address := plugin.Field{Name: "address", Type: plugin.String, Default: "127.0.0.1:8200", Config: "address", Local: true}
	return plugin.Plugin{
		Name: "conn", Summary: "a service with a connection",
		Capabilities: []plugin.Capability{
			{
				ID: "conn.get", Summary: "get", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{
					mount,
					{Name: "path", Type: plugin.String, Required: true},
					{Name: "version", Type: plugin.Int},
					address,
					// The credential the environment supplies: Local, and no config states it,
					// but it is the connection as much as the address is.
					{Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true},
					// Config-backed and not Local: the call's own input with a default from the
					// config, which is not where the connection is.
					{Name: "limit", Type: plugin.Int, Default: 50, Config: "limit"},
				},
			},
			{
				ID: "conn.login", Summary: "login", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{mount, {Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true}},
			},
			// A store's passphrase is Local and the environment can fill it, and the config
			// states nothing of a connection here: there is none to set it apart as.
			{
				ID: "conn.unlock", Summary: "unlock", Safety: plugin.Write, Run: run,
				Inputs: []plugin.Field{
					{Name: "key", Type: plugin.String, Required: true},
					{Name: "passphrase", Type: plugin.Secret, Local: true, EnvFallback: true},
				},
			},
			{ID: "conn.status", Summary: "status", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{mount, address}},
			{
				ID: "conn.put", Summary: "put", Safety: plugin.Write, Run: run,
				Inputs: []plugin.Field{
					{Name: "note", Type: plugin.String},
					{Name: "name", Type: plugin.String, Required: true},
					address,
				},
			},
			{
				ID: "conn.named", Summary: "named", Safety: plugin.Write, Run: run,
				Inputs: []plugin.Field{
					{Name: "note", Type: plugin.String},
					{Name: "name", Type: plugin.String, Required: true},
				},
			},
		},
	}
}

// forwardedPlugin has an address a forward can fill, which puts the environment
// picker above the form of every capability it declares.
func forwardedPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	return plugin.Plugin{
		Name: "fwd", Summary: "a service a forward reaches",
		Capabilities: []plugin.Capability{{
			ID: "fwd.get", Summary: "get", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true, Endpoint: plugin.EndpointHost},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true, Endpoint: plugin.EndpointPort},
				{Name: "note", Type: plugin.String},
				{Name: "key", Type: plugin.String, Required: true},
			},
		}},
	}
}

// tap is one message into the model with the commands it asks for left unrun.
// What a keystroke asks of a text box is its cursor's blink, which a test that ran
// it would wait half a second for, once for every key.
func tap(m Model, msg tea.Msg) Model {
	out, _ := m.Update(msg)
	return out.(Model)
}

// strike types text a character at a time, as the terminal delivers it.
func strike(m Model, text string) Model {
	for _, r := range text {
		m = tap(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func askedNames(cf *capForm) []string {
	var out []string
	for _, f := range cf.fields {
		out = append(out, f.Name)
	}
	return out
}

// openedOn is a capability's run form with the cursor where the form itself
// put it, which is a command the form asks the event loop for.
func openedOn(t *testing.T, m Model, id string) Model { return openedWith(t, m, id, nil) }

// openedWith is openedOn for a form that opens on what a previous run was given.
func openedWith(t *testing.T, m Model, id string, prev map[string]any) Model {
	t.Helper()
	c := capNamed(t, m, id)
	model, cmd := m.startForm(c, prev)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	nm.current = c
	return feedBack(t, nm, cmd)
}

func focusedName(t *testing.T, m Model) string {
	t.Helper()
	focused := m.form.form.GetFocusedField()
	for name, in := range m.form.inputs {
		if focused == huh.Field(in) {
			return name
		}
	}
	if len(m.form.widgets) > 0 && focused == m.form.widgets[0] {
		return "(first box)"
	}
	return "(a box that is not a text box)"
}

func TestTheConnectionIsAskedAfterTheCallsOwnInputsUnderItsBlocks(t *testing.T) {
	m := openedOn(t, adHocModel(t, connectionPlugin()), "conn.get")

	want := []string{"path", "version", "limit", "mount", "address", "token"}
	if got := askedNames(m.form); !slices.Equal(got, want) {
		t.Fatalf("the form asks %v, want %v: the call's own inputs, then the connection, each as declared", got, want)
	}

	rows := strings.Split(plain(m.formView()), "\n")
	heading := rowOf(rows, func(row string) bool { return strings.Contains(row, "── target") })
	limit := rowOf(rows, func(row string) bool { return boxTitle(row) == "limit" })
	mount := rowOf(rows, func(row string) bool { return boxTitle(row) == "mount" })
	if limit < 0 || limit >= heading || heading >= mount {
		t.Fatalf("rows: limit %d, heading %d, mount %d, want the heading between them:\n%s",
			limit, heading, mount, strings.Join(rows, "\n"))
	}
	for _, block := range []string{"target", "network", "credentials"} {
		if n := strings.Count(plain(m.formView()), "── "+block+" "); n != 1 {
			t.Errorf("the %s heading is drawn %d times", block, n)
		}
	}
}

// rowOf is the index of the first row match accepts, or -1.
func rowOf(rows []string, match func(string) bool) int {
	for i, row := range rows {
		if match(row) {
			return i
		}
	}
	return -1
}

// boxTitle is what a row holds between the panel's frame and the focus bar, which
// is a box's name on the row that draws it.
func boxTitle(row string) string { return strings.TrimSpace(strings.Trim(row, "│┃ ")) }

// The head counts boxes. A heading is not one, and where the cursor is among the
// boxes is counted in the order they are asked in.
func TestTheHeadCountsTheBoxesAndNotTheHeadingInTheNewOrder(t *testing.T) {
	m := openedOn(t, adHocModel(t, connectionPlugin()), "conn.get")
	if head := headOf(m); !strings.Contains(head, "1 of 6") {
		t.Errorf("head = %q, want 1 of 6 on path", head)
	}
	// Enter on a required box that holds nothing is refused, so path is answered.
	m = strike(m, "p")
	for range 3 {
		m = pressEnter(t, m)
	}
	if got := focusedName(t, m); got != "mount" {
		t.Fatalf("three enters from path land on %q, want mount", got)
	}
	if head := headOf(m); !strings.Contains(head, "4 of 6") {
		t.Errorf("head = %q, want 4 of 6 on mount: the heading sits between and is not a box", head)
	}
}

// A form of nothing but connection has no call to set it apart from, and is still
// headed: the name of a block says what its boxes are, which the title does not.
func TestAFormOfNothingButConnectionIsHeadedByItsBlocks(t *testing.T) {
	for _, tc := range []struct {
		id           string
		asks, headed []string
	}{
		{"conn.status", []string{"mount", "address"}, []string{"target", "network"}},
		{"conn.login", []string{"mount", "token"}, []string{"target", "credentials"}},
	} {
		m := openedOn(t, adHocModel(t, connectionPlugin()), tc.id)
		if got := askedNames(m.form); !slices.Equal(got, tc.asks) {
			t.Errorf("%s asks %v, want %v as declared", tc.id, got, tc.asks)
		}
		if got := focusedName(t, m); got != tc.asks[0] {
			t.Errorf("%s opens on %q, want its first box %q", tc.id, got, tc.asks[0])
		}
		view := plain(m.formView())
		at := -1
		for _, block := range tc.headed {
			next := strings.Index(view, "── "+block+" ")
			if next <= at {
				t.Errorf("%s: the %s heading is at %d after one at %d, want the blocks in the order they are declared:\n%s",
					tc.id, block, next, at, view)
			}
			at = next
		}
	}
}

// Without a setting the config states there is no connection to set apart, and a
// credential is the call's own: a store's passphrase is not the connection of
// anything.
func TestACredentialAloneIsNotAConnection(t *testing.T) {
	m := openedOn(t, adHocModel(t, connectionPlugin()), "conn.unlock")
	if got, want := askedNames(m.form), []string{"key", "passphrase"}; !slices.Equal(got, want) {
		t.Errorf("the form asks %v, want %v as declared", got, want)
	}
	if got := headings(m.formView()); len(got) != 0 {
		t.Errorf("a store's passphrase is headed %v as a connection:\n%s", got, plain(m.formView()))
	}
}

func TestTheCursorStartsOnTheFirstRequiredBoxThatHoldsNothing(t *testing.T) {
	m := adHocModel(t, connectionPlugin(), forwardedPlugin())

	cases := map[string]string{
		// Required and empty, behind an optional box.
		"conn.put": "name",
		// Nothing required: the first box.
		"conn.status": "mount",
		// Behind the environment picker, which has nothing to choose but the base
		// configuration and the entry that states a connection.
		"fwd.get": "key",
	}
	for id, want := range cases {
		if got := focusedName(t, openedOn(t, m, id)); got != want {
			t.Errorf("%s opens on %q, want %q", id, got, want)
		}
	}

	// Required and already answered, as `e` on a result opens it: nothing has to
	// be answered, so it opens on its first box as it always did.
	if got := focusedName(t, openedWith(t, m, "conn.named", map[string]any{"name": "x"})); got != "note" {
		t.Errorf("a form whose required box is answered opens on %q, want its first box", got)
	}
}

// With an environment configured the picker is the question, and it keeps the
// cursor: it changes what every box below it means.
func TestARealPickerKeepsTheCursorOverARequiredBox(t *testing.T) {
	m := adHocModelWith(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"fwd": conn(map[string]any{"host": "staging.internal"})}},
	}}, forwardedPlugin())
	m = openedOn(t, m, "fwd.get")
	if m.form.form.GetFocusedField() != m.form.widgets[0] {
		t.Errorf("the cursor left the environment picker for %q", focusedName(t, m))
	}
}
