package gen

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A length or a count outside what gen holds names that input
// (view.Error.Input), so a form opens again on its box and an agent knows
// which argument to send again; and on every capability that holds it, since
// gen.password and gen.uuid share one count. Excluding every character class
// is four switches together and no one of them, and names none.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	for name, tc := range map[string]struct {
		id     string
		values map[string]any
		code   string
		input  string
	}{
		"a password of no length":        {"gen.password", map[string]any{"length": 0}, "gen.password.tooshort", "length"},
		"a password over the limit":      {"gen.password", map[string]any{"length": maxPasswordLength + 1}, "gen.password.toolong", "length"},
		"a token of no length":           {"gen.token", map[string]any{"length": -3}, "gen.token.tooshort", "length"},
		"a token over the limit":         {"gen.token", map[string]any{"length": maxTokenBytes + 1}, "gen.token.toolong", "length"},
		"no passwords":                   {"gen.password", map[string]any{"length": 20, "count": 0}, "gen.count.toofew", "count"},
		"more passwords than the limit":  {"gen.password", map[string]any{"length": 20, "count": maxCount + 1}, "gen.count.toomany", "count"},
		"no UUIDs":                       {"gen.uuid", map[string]any{"count": -1}, "gen.count.toofew", "count"},
		"more UUIDs than the limit":      {"gen.uuid", map[string]any{"count": maxCount + 1}, "gen.count.toomany", "count"},
		"every character class excluded": {"gen.password", map[string]any{"length": 20, "no-lower": true, "no-upper": true, "no-digits": true}, "gen.password.noalphabet", ""},
	} {
		t.Run(name, func(t *testing.T) {
			var ran bool
			for _, c := range Plugin().Capabilities {
				if c.ID != tc.id {
					continue
				}
				ran = true
				_, err := c.Run(context.Background(), req(tc.values))
				if err == nil {
					t.Fatalf("%s ran; want %s", tc.id, tc.code)
				}
				verr := view.AsError(err, "test")
				if verr.Code != tc.code || verr.Input != tc.input {
					t.Fatalf("got %s naming %q (%s); want %s naming %q", verr.Code, verr.Input, verr.Message, tc.code, tc.input)
				}
				if f, ok := c.ErrorInput(verr); tc.input != "" && (!ok || f.Name != tc.input) {
					t.Errorf("%s names %q, which %s does not declare", verr.Code, verr.Input, c.ID)
				}
			}
			if !ran {
				t.Fatalf("no capability %s", tc.id)
			}
		})
	}
}
