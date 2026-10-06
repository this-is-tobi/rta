package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func revealCatalog(reveals bool) func() []plugin.Capability {
	return func() []plugin.Capability {
		return []plugin.Capability{{
			ID: "vault.item.get", Summary: "a value", Safety: plugin.Write, NeedsGrant: true,
			Scope: "key", Reveals: reveals,
		}}
	}
}

func showPairs(t *testing.T, catalog func() []plugin.Capability, id string) []view.Pair {
	t.Helper()
	c := capability(t, "agent.show")
	req := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: map[string]any{"id": id}}), false, false).
		WithSurface(plugin.SurfaceCLI)
	v, err := runShow(catalog)(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v.(view.Sections).Items[0].View.(view.KeyValue).Pairs
}

func pairNamed(pairs []view.Pair, key string) (string, bool) {
	for _, p := range pairs {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// The page an operator decides on says that this answer is the value, taken
// from the catalogue on this machine: the request is a file the agent's server
// wrote, and one that said it revealed nothing would have left the page quiet
// about the one thing it is for.
func TestTheConsentPageSaysWhenTheCallRevealsAValue(t *testing.T) {
	isolate(t)
	r := park(t, "vault.item.get", "prod/db")
	got, ok := pairNamed(showPairs(t, revealCatalog(true), r.ID), "reveals")
	if !ok || !strings.Contains(got, "stored value itself") {
		t.Errorf("the page of a reveal has no reveals line: %q %v", got, ok)
	}
	if _, ok := pairNamed(showPairs(t, revealCatalog(false), r.ID), "reveals"); ok {
		t.Error("the page of a capability that reveals nothing says it reveals")
	}
	if _, ok := pairNamed(showPairs(t, func() []plugin.Capability { return nil }, r.ID), "reveals"); ok {
		t.Error("a capability this machine does not know says it reveals")
	}
}

// The card a terminal asks its question under says it too, in its own row.
func TestTheAnswerCardSaysWhenTheCallRevealsAValue(t *testing.T) {
	isolate(t)
	r := park(t, "vault.item.get", "prod/db")
	if card := allowCard(r, true); !strings.Contains(card, "reveals") || !strings.Contains(card, "stored value itself") {
		t.Errorf("the card of a reveal does not say so:\n%s", card)
	}
	if card := allowCard(r, false); strings.Contains(card, "reveals") {
		t.Errorf("the card of a plain call says it reveals:\n%s", card)
	}
}
