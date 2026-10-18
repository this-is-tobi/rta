package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A run form is a column of boxes, and a database's connection alone is a dozen of
// them. Each box belongs to a block a person thinks in — where the service is, how
// it is verified, who is asking, what is picked inside it — and the form draws the
// blocks under their names, one row each.

// service declares what a database plugin does: a connection of eleven settings
// in the order a driver lists them, which is not the order a person reads them,
// and a call of its own in front of them in some capabilities.
func service() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	local := func(name string, typ plugin.FieldType) plugin.Field {
		return plugin.Field{Name: name, Type: typ, Local: true, Config: name}
	}
	connection := []plugin.Field{
		local("host", plugin.String),
		local("port", plugin.Int),
		local("user", plugin.String),
		local("database", plugin.String),
		local("sslmode", plugin.String),
		{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
		local("sslrootcert", plugin.String),
		local("tls-server-name", plugin.String),
	}
	return plugin.Plugin{
		Name: "svc", Summary: "a service",
		Capabilities: []plugin.Capability{
			{ID: "svc.overview", Summary: "overview", Safety: plugin.Read, Run: run, Inputs: connection},
			{
				ID: "svc.table.list", Summary: "list tables", Safety: plugin.Read, Run: run,
				Inputs: append([]plugin.Field{
					{Name: "schema", Type: plugin.String},
					{Name: "limit", Type: plugin.Int, Default: 50, Config: "limit"},
				}, connection...),
			},
			{
				ID: "svc.object.list", Summary: "list objects", Safety: plugin.Read, Run: run,
				Inputs: append([]plugin.Field{
					{Name: "bucket", Type: plugin.String, Required: true, Group: "scope"},
					{Name: "prefix", Type: plugin.String, Group: "scope"},
					{Name: "after", Type: plugin.String},
					{Name: "region", Type: plugin.String, Local: true, Config: "region", Group: "scope"},
				}, connection[:3]...),
			},
			{
				ID: "svc.replica.get", Summary: "read a replica", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{
					{Name: "key", Type: plugin.String, Required: true},
					{Name: "host", Type: plugin.String, Local: true, Config: "host", Group: "replica"},
					{Name: "port", Type: plugin.Int, Local: true, Config: "port"},
					{Name: "user", Type: plugin.String, Local: true, Config: "user"},
				},
			},
		},
	}
}

// headings are the names of the blocks the view draws, top to bottom.
func headings(view string) []string {
	var out []string
	for _, row := range strings.Split(plain(view), "\n") {
		if rest, ok := strings.CutPrefix(boxTitle(row), "── "); ok {
			name, _, _ := strings.Cut(rest, " ─")
			out = append(out, name)
		}
	}
	return out
}

func TestAFormOfNothingButConnectionIsAskedInBlocksByName(t *testing.T) {
	m := openedOn(t, adHocModel(t, service()), "svc.overview")

	// A block is its inputs together, in the order the blocks first appear.
	want := []string{"host", "port", "user", "password", "database", "sslmode", "sslrootcert", "tls-server-name"}
	if got := askedNames(m.form); !slices.Equal(got, want) {
		t.Fatalf("the form asks %v, want %v", got, want)
	}
	if got, want := headings(m.formView()), []string{"network", "credentials", "target", "tls"}; !slices.Equal(got, want) {
		t.Errorf("the form is headed %v, want %v", got, want)
	}
	if head := headOf(m); !strings.Contains(head, "1 of 8") {
		t.Errorf("head = %q, want 1 of 8: a heading is not a box", head)
	}
}

// The call's own inputs come first, then a block for each group a call input
// names, then the connection.
func TestTheCallsOwnInputsThenItsGroupsThenTheConnection(t *testing.T) {
	m := openedOn(t, adHocModel(t, service()), "svc.object.list")

	want := []string{"after", "bucket", "prefix", "region", "host", "port", "user"}
	if got := askedNames(m.form); !slices.Equal(got, want) {
		t.Fatalf("the form asks %v, want %v: what is in no block, then the blocks as they first appear", got, want)
	}
	// region is declared into `scope` beside the call's own bucket and prefix, and is a
	// connection input: one block by name, wherever its members came from.
	if got, want := headings(m.formView()), []string{"scope", "network", "credentials"}; !slices.Equal(got, want) {
		t.Errorf("the form is headed %v, want %v", got, want)
	}
}

// An input of the call's that is in no group is under no heading, and the first
// heading comes after it.
func TestACallsInputInNoGroupIsUnderNoHeading(t *testing.T) {
	m := openedOn(t, adHocModel(t, service()), "svc.table.list")

	if got, want := askedNames(m.form)[:2], []string{"schema", "limit"}; !slices.Equal(got, want) {
		t.Fatalf("the form opens with %v, want %v", got, want)
	}
	rows := strings.Split(plain(m.formView()), "\n")
	limit := rowOf(rows, func(row string) bool { return boxTitle(row) == "limit" })
	first := rowOf(rows, func(row string) bool { return strings.HasPrefix(boxTitle(row), "── network") })
	if limit < 0 || first < limit {
		t.Errorf("limit is on row %d and the first heading on %d:\n%s", limit, first, strings.Join(rows, "\n"))
	}
	if got := headings(m.formView()); len(got) == 0 || got[0] != "network" {
		t.Errorf("the form is headed %v, want the first heading to be network, after the call's own inputs", got)
	}
}

// What a plugin declares wins over the name, so a host that is a replica's is
// headed as the author said, and a block of one box is headed like any other.
func TestADeclaredGroupHeadsItsBlockAndAOneBoxBlockIsHeaded(t *testing.T) {
	m := openedOn(t, adHocModel(t, service()), "svc.replica.get")

	if got, want := askedNames(m.form), []string{"key", "host", "port", "user"}; !slices.Equal(got, want) {
		t.Fatalf("the form asks %v, want %v", got, want)
	}
	if got, want := headings(m.formView()), []string{"replica", "network", "credentials"}; !slices.Equal(got, want) {
		t.Errorf("the form is headed %v, want %v: host declared replica, port fell back to network, user to credentials", got, want)
	}
}

// A heading is one row, directly above the first box of its block, so what it
// costs a form that has to scroll in eighty columns is the row and not a gap.
func TestAHeadingIsOneRowAboveItsFirstBox(t *testing.T) {
	m := openedOn(t, adHocModel(t, service()), "svc.table.list")

	rows := strings.Split(plain(m.formView()), "\n")
	for _, block := range []struct{ name, first string }{{"network", "host"}, {"credentials", "user"}} {
		at := rowOf(rows, func(row string) bool { return strings.Contains(row, "── "+block.name+" ") })
		if at < 1 || at+1 >= len(rows) {
			t.Fatalf("no %s heading in:\n%s", block.name, strings.Join(rows, "\n"))
		}
		if got := boxTitle(rows[at+1]); got != block.first {
			t.Errorf("the row under the %s heading is %q, want the box %q", block.name, got, block.first)
		}
		if got := boxTitle(rows[at-1]); got != "" {
			t.Errorf("the row over the %s heading is %q, want the blank row that ends the box above", block.name, got)
		}
	}
}

// huh scrolls a form so that the box the cursor is on is the first thing drawn, so
// a heading that was a widget of its own between the boxes was the row just
// scrolled off whenever the cursor entered a block, and the form of a database's
// connection opened without the name of its first block. The cursor on the first
// box of a block shows the block's name, however short the terminal.
func TestEnteringABlockShowsItsName(t *testing.T) {
	m := adHocModel(t, service())
	m.width, m.height = 80, 14
	m = openedOn(t, m, "svc.overview")

	if got := headings(m.formView()); len(got) == 0 || got[0] != "network" {
		t.Fatalf("the form opens on %v, want the first block's name at the top:\n%s", got, plain(m.formView()))
	}
	for range 2 {
		m = pressEnter(t, m)
	}
	if got := focusedName(t, m); got != "user" {
		t.Fatalf("two enters land on %q, want user", got)
	}
	if got := headings(m.formView()); len(got) == 0 || got[0] != "credentials" {
		t.Errorf("on the first box of a block the form is headed %v, want credentials at the top:\n%s",
			got, plain(m.formView()))
	}
}

// A heading is a row of the box that opens its block, so a box a condition hides
// takes its heading with it, and the box that is shown brings it back.
func TestABoxAConditionHidesTakesItsHeadingWithIt(t *testing.T) {
	c := positionCap(plugin.Write, "kind", "extra", "last")
	build := func() Model {
		cf := newCapForm(c, c.Inputs, nil, true, nil,
			hideUnless("extra", func(cf *capForm) bool { return *cf.bindings["kind"] == "x" }),
			withSection("extra", "more"))
		m := formModel(t, cf)
		m.current = c
		m.fitForm()
		return m
	}

	hidden := pressEnter(t, press(t, build(), "y"))
	if got := headings(hidden.formView()); len(got) != 0 {
		t.Errorf("with the box hidden the form is headed %v:\n%s", got, plain(hidden.formView()))
	}
	if got := focusedNameOf(hidden); got != "last" {
		t.Fatalf("with the box hidden enter lands on %q, want last", got)
	}

	shown := pressEnter(t, press(t, build(), "x"))
	if got := headings(shown.formView()); !slices.Equal(got, []string{"more"}) {
		t.Errorf("with the box shown the form is headed %v, want [more]:\n%s", got, plain(shown.formView()))
	}
}

func focusedNameOf(m Model) string {
	focused := m.form.form.GetFocusedField()
	for name, in := range m.form.inputs {
		if focused == huh.Field(in) {
			return name
		}
	}
	return ""
}

// At eighty columns by twenty-four rows a form opens on its first box and shows the
// first heading under it: the headings do not push the call's own boxes off.
func TestTheFirstScreenAtEightyByTwentyFourShowsTheFirstBoxAndTheFirstHeading(t *testing.T) {
	m := adHocModel(t, service())
	m.width, m.height = 80, 24
	m = openedOn(t, m, "svc.table.list")

	view := plain(m.formView())
	if rows := strings.Count(view, "\n") + 1; rows > 24 {
		t.Fatalf("the form is %d rows tall in a terminal of 24:\n%s", rows, view)
	}
	for _, want := range []string{"schema", "limit", "── network"} {
		if !strings.Contains(view, want) {
			t.Errorf("the first screen lacks %q:\n%s", want, view)
		}
	}
}
