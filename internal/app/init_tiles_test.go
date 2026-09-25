package app

import "testing"

// `rta init` offers as tiles what the dashboard would run, and a Piped input
// is one a tile has no way to fill: the TUI reads no pipe, and the wizard
// writes no with:. It offered codec.jwt, codec.jwk and debug.ansi all the
// same, and each tile it wrote answered "nothing to read" on every refresh —
// what `rta dashboard add` and + in the TUI already refuse.
func TestInitOffersNoTileAPipedInputLeavesEmpty(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	offered := map[string]bool{}
	for _, o := range tileOptions(reg, nil) {
		offered[o.Value] = true
	}
	for _, id := range []string{"codec.jwt", "codec.jwk", "debug.ansi"} {
		if offered[id] {
			t.Errorf("%s was offered as a tile", id)
		}
	}
	if !offered["sys.cpu"] {
		t.Error("sys.cpu, which needs nothing, was not offered")
	}
}
