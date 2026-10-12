package plugin

import "testing"

// A grant is checked against the records a call names, before the call is
// resolved. A one-entry fence that filled in the input a grant names as its
// scope would hand the handler a record the gate never saw, which is why a
// profile never fills one (ProfileFillable) and why the fence's default does
// not either: the call has to name the value, and the gate sees it.
func TestAFenceDoesNotFillTheInputAGrantNamesAsItsScope(t *testing.T) {
	c := Capability{
		ID: "demo.pods", Scope: "namespace",
		Inputs: []Field{
			{Name: "namespace", Type: String, Config: "namespace"},
			{Name: "cluster", Type: String, Config: "cluster"},
		},
	}
	fence := Fence{"namespace": {"staging"}, "cluster": {"shop"}}

	got := Resolve(c, Inputs{Fence: fence})
	if got["namespace"] != nil {
		t.Errorf("the scope input was filled with %v by a fence the grant gate never saw", got["namespace"])
	}
	if got["cluster"] != "shop" {
		t.Errorf("cluster = %v, want the one value its fence allows", got["cluster"])
	}
	if CheckFence(c, Inputs{Fence: fence}) == nil {
		t.Error("a call that named no value for a fenced scope was not refused")
	}
	if err := CheckFence(c, Inputs{Fence: fence, Caller: map[string]any{"namespace": "staging"}}); err != nil {
		t.Errorf("naming the allowed value was refused: %v", err)
	}
}
