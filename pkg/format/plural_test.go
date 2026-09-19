package format

import "testing"

// The two shapes are separate functions because they were one name.
//
// Six packages had grown a local `plural(n int, one, many string) string`,
// and they were not all the same function: four returned the word alone and
// two returned the count with it. Same name, same signature, two answers —
// so a call written from memory, or a line moved between packages, produced
// "3 3 days" or a bare "days" with nothing to catch it. Naming them apart is
// most of the fix; these pin that they stay apart.
func TestPluralIsTheWordAndCountIsTheWordWithItsNumber(t *testing.T) {
	for _, c := range []struct {
		n                 int
		one, many         string
		plural, withCount string
	}{
		{0, "day", "days", "days", "0 days"},
		{1, "day", "days", "day", "1 day"},
		{2, "day", "days", "days", "2 days"},
		{-1, "day", "days", "days", "-1 days"},
		{1, "is", "are", "is", "1 is"},
		{7, "is", "are", "are", "7 are"},
	} {
		if got := Plural(c.n, c.one, c.many); got != c.plural {
			t.Errorf("Plural(%d, %q, %q) = %q, want %q", c.n, c.one, c.many, got, c.plural)
		}
		if got := Count(c.n, c.one, c.many); got != c.withCount {
			t.Errorf("Count(%d, %q, %q) = %q, want %q", c.n, c.one, c.many, got, c.withCount)
		}
	}
}

// Nothing but exactly one is singular. Zero reads as a plural in English
// ("0 files"), and a negative count is a bug upstream that must still render
// rather than crash — both were already the behaviour of every copy this
// replaces, and both are easy to "fix" into something worse.
func TestOnlyOneIsSingular(t *testing.T) {
	for _, n := range []int{-2, -1, 0, 2, 3, 100} {
		if got := Plural(n, "one", "many"); got != "many" {
			t.Errorf("Plural(%d) = %q, want the plural form", n, got)
		}
	}
	if got := Plural(1, "one", "many"); got != "one" {
		t.Errorf("Plural(1) = %q, want the singular form", got)
	}
}

// The derived pair, for the callers that have one noun rather than two
// forms. The -y rule (advisory → advisories) is the only irregularity worth
// modelling, and the consonant before the y is the whole of it: "key" and
// "day" keep theirs. Getting that backwards trades one wrong plural for
// another in a codebase that counts keys.
//
// Three packages had this rule written out, one of them — the TUI's — with
// the rule missing, so the same noun read "entries" from `kv` and "entrys"
// from a form box. It is not hypothetical: "1 capabilities" sat on the
// plugin inventory for every plugin declaring exactly one, `debug` among
// them, until the TUI grew its own copy of the rule — and "1 warnings"
// reads as a bug in the tool rather than a typo in a string.
func TestPluralOfDerivesTheFormAndCountOfPrintsItWithTheNumber(t *testing.T) {
	for _, c := range []struct {
		noun, many string
	}{
		{"warning", "warnings"},
		{"capability", "capabilities"},
		{"entry", "entries"},
		{"advisory", "advisories"},
		{"key", "keys"}, // vowel before the y
		{"day", "days"}, // vowel before the y
		{"y", "ys"},     // too short for the rule to index behind
	} {
		if got := PluralOf(2, c.noun); got != c.many {
			t.Errorf("PluralOf(2, %q) = %q, want %q", c.noun, got, c.many)
		}
		if got := PluralOf(1, c.noun); got != c.noun {
			t.Errorf("PluralOf(1, %q) = %q, want it unchanged", c.noun, got)
		}
		if got := PluralOf(0, c.noun); got != c.many {
			t.Errorf("PluralOf(0, %q) = %q, want the plural form", c.noun, got)
		}
		if got, want := CountOf(2, c.noun), "2 "+c.many; got != want {
			t.Errorf("CountOf(2, %q) = %q, want %q", c.noun, got, want)
		}
		if got, want := CountOf(1, c.noun), "1 "+c.noun; got != want {
			t.Errorf("CountOf(1, %q) = %q, want %q", c.noun, got, want)
		}
	}
}
