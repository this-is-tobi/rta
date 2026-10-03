package app

import (
	"context"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A list input given on the command line reaches the capability as the list
// the person meant. `--header 'If-None-Match: "33a64df5"'` was refused with `parse
// error on line 1, column 16: bare " in non-quoted-field`: pflag reads the
// argument as a line of CSV, and a quote in the middle of a field is not one.
func TestAListFlagTakesAnArgumentThatIsNotCSVWhole(t *testing.T) {
	var got []string
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "lst", Summary: "has a list",
		Capabilities: []plugin.Capability{{
			ID: "lst.show", Summary: "show the list", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "item", Type: plugin.StringSlice, Help: "items"}},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				got = req.StringSlice("item")
				return view.Text{Body: "ok"}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"--item", `If-None-Match: "33a64df5"`}, []string{`If-None-Match: "33a64df5"`}},
		{[]string{"--item", `Link: <https://a>; rel="next"`}, []string{`Link: <https://a>; rel="next"`}},
		{[]string{"--item", `a"b`}, []string{`a"b`}},
		// What always worked.
		{[]string{"--item", "a,b"}, []string{"a", "b"}},
		{[]string{"--item", "a", "--item", "b,c"}, []string{"a", "b", "c"}},
		{[]string{"--item", `"x, y",z`}, []string{"x, y", "z"}},
		{[]string{"--item", ""}, []string{}},
	} {
		got = nil
		if _, _, err := run(t, reg, append([]string{"lst", "show"}, c.args...)...); err != nil {
			t.Errorf("%q: %v", c.args, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: the capability got %q, want %q", c.args, got, c.want)
		}
	}
}
