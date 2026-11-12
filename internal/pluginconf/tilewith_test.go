package pluginconf

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A tile's `with:` naming an input its capability does not take is refused,
// naming the input the plugin renamed it to, one a slip away, or where the
// inputs are listed; one naming only inputs it takes runs.
func TestATileNamingAnInputItsCapabilityDoesNotTakeIsRefused(t *testing.T) {
	c := plugin.Capability{ID: "pg.status", Inputs: []plugin.Field{
		{Name: "tls", Config: "tls"}, {Name: "host", Config: "host"}, {Name: "limit", Config: "status.limit"},
	}}
	renamed := map[string]string{"sslmode": "tls"}
	for _, tc := range []struct {
		with map[string]any
		hint string
	}{
		{map[string]any{"sslmode": "verify-full"}, `pg takes "tls" in its place`},
		{map[string]any{"hots": "db"}, `did you mean "host"?`},
		{map[string]any{"zzzzzzzz": 1}, "`rta explain pg.status` lists the inputs it takes"},
	} {
		verr := TileWith(c, renamed, tc.with)
		if verr == nil || verr.Code != "core.dashboard.input.unknown" || verr.Hint != tc.hint {
			t.Errorf("%v: got %+v, want hint %q", tc.with, verr, tc.hint)
		}
	}
	if verr := TileWith(c, renamed, map[string]any{"tls": "verify-full", "limit": 5}); verr != nil {
		t.Errorf("a tile naming inputs it takes was refused: %v", verr)
	}
	if verr := TileWith(c, renamed, map[string]any{"sslmode": "x"}); !strings.Contains(verr.Message, `"sslmode"`) {
		t.Errorf("message does not name the input: %q", verr.Message)
	}
}
