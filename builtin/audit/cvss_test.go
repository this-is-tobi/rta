package audit

import "testing"

// Against scores somebody else published.
//
// A severity this code computes is one nobody reviewed, so the check that
// matters is not "does the arithmetic run" but "does it agree with NVD".
// Every vector below is from a real advisory, with the base score NVD states
// for it — including the two shapes the formula treats specially: Scope
// Changed, which uses a different impact equation and a 1.08 multiplier, and
// an all-None impact, which scores zero however reachable it is.
func TestCVSSAgreesWithPublishedScores(t *testing.T) {
	for _, tc := range []struct {
		vector string
		score  float64
		rating string
		what   string
	}{
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H", 7.5, "high", "CVE-2022-41723, golang.org/x/net"},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "critical", "the classic unauthenticated RCE"},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N", 6.1, "medium", "reflected XSS, the canonical Scope:Changed"},
		{"CVSS:3.1/AV:L/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N", 1.8, "low", "a local, hard, privileged read"},
		{"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N", 0, "", "no impact is no score, however reachable"},
		{"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:N/A:H", 5.9, "medium", "CVE-2023-44487-shaped"},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H", 10, "critical", "the ceiling, which the min() clamps"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			got, ok := cvss3Score(tc.vector)
			if !ok {
				t.Fatalf("%s was not readable", tc.vector)
			}
			if got != tc.score {
				t.Errorf("score = %v, want %v (%s)", got, tc.score, tc.what)
			}
			if r := cvssRating(got); r != tc.rating {
				t.Errorf("rating = %q, want %q", r, tc.rating)
			}
		})
	}
}

// A vector that is not one is not scored, because a number invented for it
// would be a severity nobody published. Every base metric is mandatory in
// v3, so an absent one means this is not the string it looks like.
func TestAnUnreadableVectorIsNotScored(t *testing.T) {
	for _, vector := range []string{
		"",
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N",     // no A
		"CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H", // AV is not a value
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:X/C:N/I:N/A:H", // S is not a value
		"CVSS:2.0/AV:N/AC:L/Au:N/C:P/I:P/A:P",          // a v2 vector
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N",
		"AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H", // no prefix
		"nonsense",
	} {
		if score, ok := cvss3Score(vector); ok {
			t.Errorf("cvss3Score(%q) = %v, want unreadable", vector, score)
		}
	}
}

// Roundup is the specification's, not math.Ceil(x*10)/10.
//
// The naive spelling is wrong on the values binary floating point cannot
// hold: 8.6 arrives as 8.599999999999999 often enough that it rounds up to
// 8.7, which is a severity band boundary away from being a different word.
func TestRoundUpIsTheSpecificationsAndNotTheObviousOne(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{4.0, 4.0},
		{4.02, 4.1},
		{8.6, 8.6},
		{0.0, 0.0},
		{9.999, 10.0},
	} {
		if got := cvssRoundUp(tc.in); got != tc.want {
			t.Errorf("cvssRoundUp(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
