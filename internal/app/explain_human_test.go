package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A capability for the person at the terminal is never a tool, and its
// card said so on one row while the next row told an agent's operator to
// issue a grant for it — a grant `rta grant allow` refuses as needless.
// Asserted over the whole catalogue, because the contradiction was on
// every human-only card at once.
func TestAHumanOnlyCardNamesNoGrant(t *testing.T) {
	reg, _ := NewRegistry()
	humanOnly := 0
	for _, c := range reg.Capabilities() {
		if !c.HumanOnly {
			continue
		}
		humanOnly++
		card, ok := cardView(reg, c).(view.KeyValue)
		if !ok {
			t.Fatalf("%s: card is %s, want key-value pairs", c.ID, view.TypeOf(cardView(reg, c)))
		}
		if got := pairValue(card, "grant required (mcp)"); got != "" {
			t.Errorf("%s: grant required = %q on a capability that is never a tool", c.ID, got)
		}
		if got := pairValue(card, "profiles"); strings.Contains(got, "grant allow") {
			t.Errorf("%s: profiles = %q, prices a grant over MCP", c.ID, got)
		}
		if got := pairValue(card, "mcp-tool"); !strings.Contains(got, "never an agent") {
			t.Errorf("%s: mcp-tool = %q, want the reason there is none", c.ID, got)
		}
	}
	if humanOnly == 0 {
		t.Fatal("no human-only capability in the catalogue to check")
	}
}
