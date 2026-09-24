package tui

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A form box says what the host would refuse, as it is typed: a number out of
// its range is named in the footer rather than discovered after the run.
func TestAFormBoxHoldsANumberToItsRange(t *testing.T) {
	port := plugin.Field{Name: "port", Type: plugin.Int, Min: 1, Max: 65535}
	ratio := plugin.Field{Name: "ratio", Type: plugin.Float, Min: 0.0, Max: 1.0}
	limit := plugin.Field{Name: "limit", Type: plugin.Int, Min: 1}
	for _, tc := range []struct {
		f    plugin.Field
		in   string
		want string
	}{
		{port, "70000", "must be from 1 to 65535"},
		{port, "0", "must be from 1 to 65535"},
		{port, "443", ""},
		{port, "four", "must be an integer"},
		{ratio, "1.5", "must be from 0 to 1"},
		{ratio, "0.25", ""},
		{limit, "0", "must be at least 1"},
		{limit, "", ""}, // not given: the default fills it
	} {
		err := validatorFor(tc.f)(tc.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s %q: %q, want %q", tc.f.Name, tc.in, got, tc.want)
		}
	}
}
