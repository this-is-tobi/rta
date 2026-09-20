package eol

import (
	"strings"
	"testing"
)

func TestParseSelectorReadsAnExactNameAsItself(t *testing.T) {
	for _, s := range []string{"15", "bookworm", "24.04"} {
		sel, verr := parseSelector(s)
		if verr != nil {
			t.Fatalf("parseSelector(%q): %v", s, verr)
		}
		if sel.ranged || sel.exact != s {
			t.Errorf("parseSelector(%q) = %+v, want an exact selector", s, sel)
		}
	}
}

func TestParseSelectorReadsBothOpenAndClosedRanges(t *testing.T) {
	cases := []struct {
		in     string
		lo, hi []int
	}{
		{"13..16", []int{13}, []int{16}},
		{"15..", []int{15}, nil},
		{"..16", nil, []int{16}},
		{"24.04..24.10", []int{24, 4}, []int{24, 10}},
		{" 13..16 ", []int{13}, []int{16}},
	}
	for _, c := range cases {
		sel, verr := parseSelector(c.in)
		if verr != nil {
			t.Fatalf("parseSelector(%q): %v", c.in, verr)
		}
		if !sel.ranged || compareNumeric(sel.lo, c.lo) != 0 || compareNumeric(sel.hi, c.hi) != 0 ||
			(sel.lo == nil) != (c.lo == nil) || (sel.hi == nil) != (c.hi == nil) {
			t.Errorf("parseSelector(%q) = %+v, want lo=%v hi=%v", c.in, sel, c.lo, c.hi)
		}
	}
}

func TestParseSelectorRefusesWhatItCannotOrder(t *testing.T) {
	cases := []struct{ in, wantInMessage string }{
		{"..", "selects nothing"},
		{"bookworm..16", "not a number a range can start from"},
		{"13..trixie", "not a number a range can end at"},
		{"16..13", "runs backwards"},
		{"1..2..3", "not a number a range can end at"},
	}
	for _, c := range cases {
		_, verr := parseSelector(c.in)
		if verr == nil {
			t.Fatalf("parseSelector(%q) accepted", c.in)
		}
		if verr.Code != "eol.cycle.selector" || !strings.Contains(verr.Message, c.wantInMessage) {
			t.Errorf("parseSelector(%q) = %s %q, want eol.cycle.selector saying %q", c.in, verr.Code, verr.Message, c.wantInMessage)
		}
	}
}

func numbered(names ...string) []release {
	out := make([]release, len(names))
	for i, n := range names {
		out[i] = release{Name: n}
	}
	return out
}

func names(rs []release) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return strings.Join(out, ",")
}

func TestPickRangeIsInclusiveKeepsOrderAndSkipsCodenames(t *testing.T) {
	releases := numbered("18", "17", "16", "15", "14", "13", "12", "bookworm")
	cases := []struct{ sel, want string }{
		{"13..16", "16,15,14,13"},
		{"17..", "18,17"},
		{"..13", "13,12"},
		{"16..16", "16"},
		{"20..", ""},
		{"bookworm", "bookworm"},
		{"BOOKWORM", "bookworm"},
		{"99", ""},
	}
	for _, c := range cases {
		sel, verr := parseSelector(c.sel)
		if verr != nil {
			t.Fatalf("parseSelector(%q): %v", c.sel, verr)
		}
		if got := names(sel.pick(releases)); got != c.want {
			t.Errorf("pick(%q) = %q, want %q", c.sel, got, c.want)
		}
	}
}

func TestPickOrdersDottedCyclesNumerically(t *testing.T) {
	releases := numbered("10", "9.6", "9.5", "24.10", "24.04")
	sel, verr := parseSelector("9.6..24.04")
	if verr != nil {
		t.Fatal(verr)
	}
	if got := names(sel.pick(releases)); got != "10,9.6,24.04" {
		t.Errorf("pick = %q, want 9.6 and 10 and 24.04 — a dotted number is not a decimal", got)
	}
}

func TestCompareNumericTreatsAMissingComponentAsZero(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"15", "15.0", 0},
		{"15", "15.4", -1},
		{"15.4", "15", 1},
		{"9.6", "10", -1},
		{"24.04", "24.10", -1},
	}
	for _, c := range cases {
		a, _ := numericCycle(c.a)
		b, _ := numericCycle(c.b)
		if got := compareNumeric(a, b); got != c.want {
			t.Errorf("compareNumeric(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNumericCycleRefusesAnythingButDigitsAndDots(t *testing.T) {
	for _, s := range []string{"bookworm", "15a", "", ".5", "5.", "v15"} {
		if _, ok := numericCycle(s); ok {
			t.Errorf("numericCycle(%q) accepted", s)
		}
	}
}
