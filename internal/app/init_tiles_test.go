package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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
	for _, o := range tileOptions(reg, nil, 0) {
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

// An option wider than the form was wrapped by huh, and the wrapped part is a
// few letters of a sentence on a line of its own, indented under nothing:
// "pkg", "tha", "t" — three more options, to read it. Every label is now cut
// to the width the form has, the ID first and whole, so nothing wraps.
func TestInitCutsEveryTileLabelToTheWidthOfTheForm(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	const width = 70
	cut := 0
	for _, o := range tileOptions(reg, nil, width) {
		if got := ansi.StringWidth(o.Key); got > width {
			t.Errorf("%s is offered as %q, %d wide on a %d-wide form", o.Value, o.Key, got, width)
		}
		if !strings.HasPrefix(o.Key, o.Value+" — ") {
			t.Errorf("%s: the ID is not whole at the front of %q", o.Value, o.Key)
		}
		if strings.HasSuffix(o.Key, "…") {
			cut++
		}
	}
	if cut == 0 {
		t.Error("no label was long enough to be cut; this checks nothing")
	}
	// And without a width, nothing is cut at all.
	for _, o := range tileOptions(reg, nil, 0) {
		c, _ := reg.Capability(o.Value)
		if want := c.ID + " — " + c.Summary; o.Key != want {
			t.Errorf("%s was cut with no width to cut it to: %q", o.Value, o.Key)
		}
	}
}
