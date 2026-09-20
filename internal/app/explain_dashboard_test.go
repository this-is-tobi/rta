package app

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/render/tui"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The card says what the landing screen does with a capability: whether it
// runs unasked, why not when it does not, and at what pace once somebody
// names it. Three declaration facts on three lines added up to that, and
// none of them was on the card by that name.
func TestExplainCardSaysWhatTheDashboardDoesWithIt(t *testing.T) {
	reg, _ := NewRegistry()
	card := func(id string) string {
		c, ok := reg.Capability(id)
		if !ok {
			t.Fatalf("no capability %s", id)
		}
		kv, ok := cardView(reg, c).(view.KeyValue)
		if !ok {
			t.Fatalf("%s: card is not key-value pairs", id)
		}
		return pairValue(kv, "dashboard")
	}

	// Declared pace, declined automatic run.
	if got := card("eol.watch"); !strings.Contains(got, "declines to run unasked") || !strings.Contains(got, "every 2h") {
		t.Errorf("eol.watch: %q, want the refusal and the two-hour pace", got)
	}
	// Declined, at the host's own pace.
	if got := card("audit.kube.eol"); !strings.Contains(got, "declines to run unasked") || !strings.Contains(got, "every few seconds") {
		t.Errorf("audit.kube.eol: %q", got)
	}
	// Needs an input nothing defaults.
	if got := card("cert.expiry"); !strings.Contains(got, "needs to be told what to look at") {
		t.Errorf("cert.expiry: %q", got)
	}
	// Never: a tile runs with no confirmation.
	if got := card("note.rm"); !strings.Contains(got, "never a tile") {
		t.Errorf("note.rm: %q", got)
	}
	// The one the automatic dashboard actually picks for its plugin.
	for _, p := range reg.Plugins() {
		if p.Name != "sys" {
			continue
		}
		id, ok := tui.TileFor(reg, p)
		if !ok {
			t.Fatal("sys has no automatic tile")
		}
		if got := card(id); !strings.Contains(got, "automatic tile") {
			t.Errorf("%s: %q, want it named as sys's automatic tile", id, got)
		}
		// Another previewable sys capability is a tile only when named.
		for _, c := range p.Capabilities {
			if c.ID != id && tui.Unasked(c) == "" {
				if got := card(c.ID); !strings.Contains(got, "when named") || !strings.Contains(got, id) {
					t.Errorf("%s: %q, want it offered when named and the automatic pick cited", c.ID, got)
				}
				break
			}
		}
	}
}

func TestPaceReadsAsAPersonWritesIt(t *testing.T) {
	cases := map[string]string{"2h": "2h", "90m": "90m", "1h30m": "90m", "45s": "45s"}
	for in, want := range cases {
		d, err := time.ParseDuration(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := pace(d); got != want {
			t.Errorf("pace(%s) = %q, want %q", in, got, want)
		}
	}
}
