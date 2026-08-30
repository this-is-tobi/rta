package audit

import (
	"math"
	"strings"
)

// CVSS v3 base scores, computed here because an advisory that carries only a
// vector carries no word.
//
// **Why compute rather than only report what a record states.** OSV records
// come from many databases and they do not agree on what they publish. A
// GitHub advisory states `database_specific.severity: HIGH` *and* a vector; a
// RustSec advisory states the vector alone; a Go or PyPI record states
// neither and leaves the severity to its aliases. Reading only the stated
// word means every Rust finding is graded "unknown" — and a report where the
// severity column is blank for a whole ecosystem is one nobody can triage
// with, which is the entire reason for fetching details at all.
//
// The formula is FIRST's, from the CVSS v3.1 specification §7.1, and it is
// arithmetic rather than judgement: the same vector always produces the same
// score, and the test checks that against scores NVD publishes for real
// advisories. v4 vectors are not scored — that formula is a large lookup
// table rather than an equation, and an advisory carrying only a v4 vector
// falls back to no word rather than to a wrong one.

// cvssRating maps a base score onto the qualitative word every consumer of
// this data actually uses. The bands are the specification's own (§5).
func cvssRating(score float64) string {
	switch {
	case score >= 9.0:
		return "critical"
	case score >= 7.0:
		return "high"
	case score >= 4.0:
		return "medium"
	case score > 0:
		return "low"
	}
	return ""
}

// cvss3Metrics are the base metrics, keyed by their vector abbreviations.
//
// Privileges Required is the one metric whose weight depends on another —
// Scope changes what "low" and "high" cost an attacker — so it is two tables
// and the lookup below picks between them.
var (
	cvssAV = map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}
	cvssAC = map[string]float64{"L": 0.77, "H": 0.44}
	cvssUI = map[string]float64{"N": 0.85, "R": 0.62}
	cvssIA = map[string]float64{"H": 0.56, "L": 0.22, "N": 0}

	cvssPRUnchanged = map[string]float64{"N": 0.85, "L": 0.62, "H": 0.27}
	cvssPRChanged   = map[string]float64{"N": 0.85, "L": 0.68, "H": 0.50}
)

// cvss3Score computes the base score of a CVSS v3.x vector, and reports
// whether the vector was one it could read at all.
//
// A vector missing any base metric is not scored rather than scored with a
// default: every base metric is mandatory in a v3 vector, so one that is
// absent means this is not the string it looks like, and a number invented
// for it would be a severity nobody published.
func cvss3Score(vector string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(vector), "/")
	if len(parts) == 0 || (parts[0] != "CVSS:3.0" && parts[0] != "CVSS:3.1") {
		return 0, false
	}
	m := make(map[string]string, len(parts))
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, ":")
		if ok {
			m[k] = v
		}
	}

	changed := m["S"] == "C"
	pr := cvssPRUnchanged
	if changed {
		pr = cvssPRChanged
	}
	av, okAV := cvssAV[m["AV"]]
	ac, okAC := cvssAC[m["AC"]]
	prv, okPR := pr[m["PR"]]
	ui, okUI := cvssUI[m["UI"]]
	c, okC := cvssIA[m["C"]]
	i, okI := cvssIA[m["I"]]
	a, okA := cvssIA[m["A"]]
	if !okAV || !okAC || !okPR || !okUI || !okC || !okI || !okA || (m["S"] != "U" && m["S"] != "C") {
		return 0, false
	}

	iss := 1 - (1-c)*(1-i)*(1-a)
	impact := 6.42 * iss
	if changed {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	if impact <= 0 {
		return 0, true
	}
	exploitability := 8.22 * av * ac * prv * ui
	total := impact + exploitability
	if changed {
		total *= 1.08
	}
	return cvssRoundUp(math.Min(total, 10)), true
}

// cvssRoundUp is the specification's own Roundup (§7.1 Appendix A): the
// smallest number, to one decimal place, that is not less than the input.
//
// Not math.Ceil(x*10)/10, which is the obvious spelling and is wrong on the
// values binary floating point cannot hold exactly — 8.6 arrives as
// 8.599999999999999 often enough that the naive version rounds a 8.6 up to
// 8.7. The integer form below is what the specification publishes for
// exactly this reason.
func cvssRoundUp(x float64) float64 {
	i := int(math.Round(x * 100000))
	if i%10000 == 0 {
		return float64(i) / 100000
	}
	return (math.Floor(float64(i)/10000) + 1) / 10
}
