package plugin

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A section is a call the host never saw, so the page lays the defaults its
// capability declares, as Resolve does before a handler the host runs. A
// target list whose `health` defaults to `any` read "" from a page, matched
// nothing, and answered an empty table under a summary that counted three
// targets. What the page and the section state still wins over a default.
func TestAPageSectionRunsWithTheDefaultsItsCapabilityDeclares(t *testing.T) {
	var got Request
	targets := Capability{
		ID: "demo.target.list", Summary: "targets", Safety: Read,
		Inputs: []Field{
			{Name: "health", Type: String, Default: "any", Options: []string{"any", "up", "down"}},
			{Name: "limit", Type: Int, Default: 20},
		},
		Run: func(_ context.Context, req Request) (view.View, error) {
			got = req
			return view.Text{Body: "targets"}, nil
		},
	}
	base := NewRequest(map[string]any{"limit": 5}, false, false)

	NewPage(context.Background(), base).AddAs("targets", "Targets", targets, nil)
	if h := got.String("health"); h != "any" {
		t.Errorf("the section read health %q, want the declared default any", h)
	}
	if n := got.Int("limit"); n != 5 {
		t.Errorf("the section read limit %d, want the page's own 5 over the default", n)
	}

	if _, err := NewPage(context.Background(), base).Run(targets, map[string]any{"health": "down"}); err != nil {
		t.Fatal(err)
	}
	if h := got.String("health"); h != "down" {
		t.Errorf("Run read health %q, want the value it was given over the default", h)
	}
}
