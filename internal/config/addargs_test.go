package config

import "testing"

// AddArgs is the way back a receipt or the TUI's footer spells: the whole
// entry, so it can be pasted, with a list-shaped input as one --set per
// element and the inputs in a stable order.
func TestAddArgsSpellsTheWholeEntry(t *testing.T) {
	cases := []struct {
		tile Tile
		want string
	}{
		{Tile{ID: "sys.overview"}, "sys.overview"},
		{Tile{ID: "kube.overview", Profile: "prod/edge"}, "kube.overview --profile prod/edge"},
		{Tile{ID: "sys.overview", Span: 2, With: map[string]any{"cores": true}}, "sys.overview --span 2 --set cores=true"},
		{Tile{ID: "cert.expiry", With: map[string]any{"port": 8443, "host": "a.example"}},
			"cert.expiry --set host=a.example --set port=8443"},
		{Tile{ID: "eol.check", With: map[string]any{"product": []any{"nodejs", "go"}}},
			"eol.check --set product=nodejs --set product=go"},
	}
	for _, c := range cases {
		if got := c.tile.AddArgs(); got != c.want {
			t.Errorf("%+v: AddArgs() = %q, want %q", c.tile, got, c.want)
		}
	}
}
