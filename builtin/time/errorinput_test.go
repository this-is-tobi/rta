package time

import (
	"context"
	"testing"
	stdtime "time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// What `when` or `zone` was given that is no instant or no zone names that
// input (view.Error.Input), so a form opens again on its box and an agent
// knows which argument to send again. when's refusals come out of resolve,
// which reads a string and cannot know whose it is, so they are held here,
// through the capability, and not through resolve.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	type refusal struct {
		values      map[string]any
		code, input string
	}
	cases := map[string]refusal{
		"a length of time":        {map[string]any{"when": "2h"}, "time.at.unsigned", "when"},
		"a day out of range":      {map[string]any{"when": "2026-02-30"}, "time.at.unreadable", "when"},
		"not an instant":          {map[string]any{"when": "yesterday"}, "time.at.unreadable", "when"},
		"a zone that cannot load": {map[string]any{"when": "now", "zone": "Mars/Olympus"}, "time.at.zone", "zone"},
	}
	// A reading of the clock the zone skipped is refused only where the
	// clocks went forward over it, and only with a tz database to say so.
	if paris, err := stdtime.LoadLocation("Europe/Paris"); err == nil {
		t.Cleanup(func(prev *stdtime.Location) func() { return func() { stdtime.Local = prev } }(stdtime.Local))
		stdtime.Local = paris
		cases["a time the clock skipped"] = refusal{map[string]any{"when": "2026-03-29 02:30"}, "time.at.skipped", "when"}
	}
	c := Plugin().Capabilities[0]
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := c.Run(context.Background(), req(tc.values))
			if err == nil {
				t.Fatalf("%v ran; want %s", tc.values, tc.code)
			}
			verr := view.AsError(err, "test")
			if verr.Code != tc.code || verr.Input != tc.input {
				t.Fatalf("got %s naming %q (%s); want %s naming %q", verr.Code, verr.Input, verr.Message, tc.code, tc.input)
			}
			if f, ok := c.ErrorInput(verr); !ok || f.Name != tc.input {
				t.Errorf("%s names %q, which %s does not declare", verr.Code, verr.Input, c.ID)
			}
		})
	}
}
