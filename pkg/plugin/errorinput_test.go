package plugin

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// An error names the input it is about by the name the capability declares it
// under, and a surface acts on no other: a plugin's error crosses the wire
// holding whatever its handler wrote.
func TestAnErrorNamesOnlyAnInputTheCapabilityDeclares(t *testing.T) {
	c := Capability{ID: "weather.station.show", Inputs: []Field{{Name: "city", Type: String}}}
	if f, ok := c.ErrorInput(view.Errorf("weather.nosuchcity", "no station").WithInput("city")); !ok || f.Name != "city" {
		t.Errorf("a declared input = %q, %v", f.Name, ok)
	}
	for _, verr := range []*view.Error{
		nil,
		view.Errorf("weather.down", "the service did not answer"),
		view.Errorf("weather.nosuchcity", "no station").WithInput("town"),
	} {
		if f, ok := c.ErrorInput(verr); ok {
			t.Errorf("%+v names %q, which nothing declares", verr, f.Name)
		}
	}
}

// A warning's input is held to the capability as an error's is, at every depth
// of a page: kept for a declared input the caller allows, and cleared for a
// Local one an agent is not told and a name nothing declares.
func TestAWarningsInputIsHeldToTheDeclaration(t *testing.T) {
	c := Capability{ID: "demo.item.list", Inputs: []Field{
		{Name: "name", Type: String},
		{Name: "passphrase", Type: Secret, Local: true},
	}}
	warned := func() view.Table {
		return view.Table{Warnings: []view.Error{
			{Code: "demo.a", Input: "name"}, {Code: "demo.b", Input: "passphrase"}, {Code: "demo.c", Input: "ghost"},
		}}
	}
	page := view.Sections{Warnings: warned().Warnings, Items: []view.Section{{Title: "rows", View: warned()}}}
	held := c.HeldWarnings(page, func(f Field) bool { return !f.Local }).(view.Sections)
	for _, ws := range [][]view.Error{held.Warnings, held.Items[0].View.(view.Table).Warnings} {
		if ws[0].Input != "name" || ws[1].Input != "" || ws[2].Input != "" {
			t.Errorf("held warnings = %+v, want only the declared input that is not Local", ws)
		}
	}
	if page.Warnings[1].Input != "passphrase" {
		t.Error("holding the warnings changed the page it was given")
	}
}

// A section a sibling could not produce is a warning of the page, and the input
// the sibling's refusal named is one of the sibling's call: the page takes none.
func TestASiblingsRefusalOnAPageNamesNoInput(t *testing.T) {
	p := NewPage(context.Background(), NewRequest(nil, false, false))
	p.AddAs("rows", "rows", Capability{ID: "demo.rows", Safety: Read, Run: func(context.Context, Request) (view.View, error) {
		return nil, view.Errorf("demo.nosuch", "no such row").WithInput("id")
	}}, nil)
	page := p.View()
	if len(page.Warnings) != 1 || page.Warnings[0].Input != "" {
		t.Errorf("warnings = %+v, want the sibling's refusal naming no input", page.Warnings)
	}
}

// The host's own refusal of a value names the input it refused, whatever is
// wrong with it, so a form reopens on that box and an agent knows the argument;
// and so does its refusal of a required input left out, on every surface.
func TestTheHostsRefusalsNameTheInputTheyRefuse(t *testing.T) {
	c := Capability{ID: "demo.item.list", Inputs: []Field{
		{Name: "limit", Type: Int, Min: 1, Max: 10},
		{Name: "mode", Type: String, Options: []string{"fast", "slow"}},
		{Name: "every", Type: Duration},
		{Name: "name", Type: String, Required: true},
	}}
	for input, given := range map[string]any{"limit": 99, "mode": "medium", "every": "30"} {
		req := NewRequest(map[string]any{"name": "x", input: given}, false, false)
		if verr := CheckInputs(c, req); verr == nil || verr.Input != input {
			t.Errorf("%s = %v: refused as %+v, want the refusal to name %s", input, given, verr, input)
		}
	}
	for _, s := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceMCP} {
		verr := CheckRequired(c, NewRequest(nil, false, false).WithSurface(s))
		if verr == nil || verr.Code != "core.input.missing" || verr.Input != "name" {
			t.Errorf("%s: a required input left out = %+v, want core.input.missing naming name", s, verr)
		}
	}
}
