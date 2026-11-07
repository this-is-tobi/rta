package plugin

import "testing"

// A request nobody stamped a confirmer on cannot ask, and says no; one that was
// stamped asks through it, and keeps it across the inputs a page hands down.
func TestARequestAsksOnlyThroughTheConfirmerItWasGiven(t *testing.T) {
	plain := NewRequest(nil, false, false)
	if plain.CanConfirm() || plain.Confirm("card") {
		t.Error("a request with no confirmer can ask")
	}
	var shown []string
	stamped := plain.WithConfirmer(func(card string) bool {
		shown = append(shown, card)
		return true
	})
	if !stamped.CanConfirm() || !stamped.With(map[string]any{"key": "x"}).Confirm("card") {
		t.Error("a stamped request did not ask, or lost its confirmer to With")
	}
	if len(shown) != 1 || shown[0] != "card" {
		t.Errorf("shown %q, want the card once", shown)
	}
	if plain.CanConfirm() {
		t.Error("stamping a copy gave the original a confirmer")
	}
}
