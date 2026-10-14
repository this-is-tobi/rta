package view

import "testing"

// A number named for a masked key is the value in another form, so masking the
// key masks it, and the numbers of the keys that are shown stay.
func TestRedactDropsTheNumberOfAMaskedKey(t *testing.T) {
	kv := KeyValue{
		Pairs:    []Pair{{Key: "keys", Value: "16 keys"}, {Key: "token", Value: "s3cret"}},
		Redacted: []string{"token"},
		Numbers:  map[string]float64{"keys": 16, "token": 6},
	}
	got := Redact(kv).(KeyValue)
	if _, there := got.Numbers["token"]; there {
		t.Errorf("the number of a masked key survived: %v", got.Numbers)
	}
	if got.Numbers["keys"] != 16 {
		t.Errorf("the number of a shown key was lost: %v", got.Numbers)
	}
	if kv.Numbers["token"] != 6 {
		t.Error("Redact edited the view it was given")
	}
}
