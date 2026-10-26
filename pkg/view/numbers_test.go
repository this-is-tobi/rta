package view

import (
	"strings"
	"testing"
)

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

// A number's name goes where the rest of the page goes, so it is mapped with
// the rest. One the mapping would change is dropped rather than renamed: it
// was never a name (a name is lower case with underscores, which no cleaning
// touches), two of them could clean to one, and Redacted, which is mapped,
// would otherwise be matched against the name as sent and miss it.
func TestANumberWhoseNameTheMappingWouldChangeIsDropped(t *testing.T) {
	strip := func(s string) string { return strings.ReplaceAll(s, "\u200b", "") }
	kv := KeyValue{
		Pairs:    []Pair{{Key: "keys", Value: "16 keys"}, {Key: "tok\u200ben", Value: "s3cret"}},
		Redacted: []string{"tok\u200ben"},
		Numbers:  map[string]float64{"keys": 16, "tok\u200ben": 6},
	}
	got := Redact(MapStrings(kv, strip)).(KeyValue)
	if len(got.Numbers) != 1 || got.Numbers["keys"] != 16 {
		t.Errorf("numbers = %v, want only keys", got.Numbers)
	}
	if kv.Numbers["tok\u200ben"] != 6 {
		t.Error("MapStrings edited the view it was given")
	}

	clean := KeyValue{Pairs: []Pair{{Key: "keys", Value: "16 keys"}}, Numbers: map[string]float64{"keys": 16}}
	if got := MapStrings(clean, strip).(KeyValue); got.Numbers["keys"] != 16 || len(got.Numbers) != 1 {
		t.Errorf("a well-named number did not survive the mapping: %v", got.Numbers)
	}
}
