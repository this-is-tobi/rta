package format

import (
	"math"
	"testing"
)

// A quantity is read as the number it is, whatever suffix spells it: binary
// in powers of 1024, decimal in powers of 1000 on either side of one, and an
// exponent in place of either. What is not in Kubernetes' grammar is not a
// quantity, rather than a number read from part of it.
func TestParseQuantityReadsEveryForm(t *testing.T) {
	for in, want := range map[string]float64{
		"8Gi": 8 << 30, "512Mi": 512 << 20, "1.5Ki": 1536, "1Ei": 1 << 60,
		"10G": 1e10, "7000M": 7e9, "2E": 2e18, "4k": 4000,
		"500m": 0.5, "148912532n": 0.148912532, "250u": 0.00025,
		"1e3": 1000, "1E3": 1000, "1e-3": 0.001, "1000000000": 1e9,
		".5": 0.5, "5.": 5, " 3Gi ": 3 << 30, "0": 0,
	} {
		got, ok := ParseQuantity(in)
		if !ok || math.Abs(got-want) > 1e-9*math.Max(1, want) {
			t.Errorf("ParseQuantity(%q) = %v, %v, want %v", in, got, ok, want)
		}
	}
	// Exactly, where the suffix is below one: what metrics-server reports is
	// compared as the number it is.
	if got, _ := ParseQuantity("148912532n"); got != 0.148912532 {
		t.Errorf("148912532n = %v, want 0.148912532 exactly", got)
	}
	for _, bad := range []string{"", "Gi", "-1Gi", "+1Gi", "1GiB", "1 Gi", "abc", "0x10", "Inf", "NaN", "1_000", "5e", "1Kb"} {
		if got, ok := ParseQuantity(bad); ok {
			t.Errorf("ParseQuantity(%q) = %v, want no quantity", bad, got)
		}
	}
}

// What the parse is for: a 10G claim that a provisioner rounded up to 10Gi
// holds more than it asked for, which compared as text read as a volume short
// of its claim.
func TestATenGClaimOnATenGiVolumeIsNotShort(t *testing.T) {
	asked, _ := ParseQuantity("10G")
	held, _ := ParseQuantity("10Gi")
	if held < asked {
		t.Errorf("10Gi (%v) read as less than 10G (%v)", held, asked)
	}
}
