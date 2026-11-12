package plugindist

import (
	"cmp"
	"sort"
	"strings"
)

// OutdatedRow is one installed plugin whose index no longer agrees with what
// rta.lock recorded at install or upgrade time.
type OutdatedRow struct {
	Name             string
	Index            string
	InstalledVersion string
	// AvailableVersion is what the index currently claims, set only when
	// Problem is not — a version comparison needs a version to compare against.
	AvailableVersion string
	// Older says the claim is below the installed version by its first three
	// numbers (compareClaims): an upgrade would move the plugin back.
	Older bool
	// Problem is set instead of AvailableVersion when the index no longer has
	// an answer at all: detached, or the manifest is gone from it. Either way
	// there is nothing to compare, which is itself worth a row rather than a
	// silent skip — Manifests' own "one malformed file must not cost the
	// operator the rest" rule, applied per plugin instead of per file.
	Problem string
}

// Outdated compares every installed plugin's recorded version against what
// its index currently claims. Cheap like Search — nothing is fetched or
// executed, so an empty Problem and a version match are only ever a hint that
// nothing has changed, never proof: the version string is the index's claim,
// and `rta plugin upgrade <name>` is what actually re-verifies against the
// bytes. A publisher respinning the same version number under different bytes
// is invisible here for exactly the reason Upgrade's own doc comment names —
// "the same publisher signing a worse plugin verifies perfectly" — a cheap
// check one layer up from a signature has the same blind spot.
func Outdated() []OutdatedRow {
	var rows []OutdatedRow
	for _, e := range ReadLock() {
		listed, verr := Resolve(e.Index + "/" + e.Name)
		if verr != nil {
			rows = append(rows, OutdatedRow{Name: e.Name, Index: e.Index,
				InstalledVersion: e.Version, Problem: verr.Message})
			continue
		}
		if listed.Manifest.Version != e.Version {
			order, ordered := compareClaims(listed.Manifest.Version, e.Version)
			rows = append(rows, OutdatedRow{Name: e.Name, Index: e.Index,
				InstalledVersion: e.Version, AvailableVersion: listed.Manifest.Version,
				Older: ordered && order < 0})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// compareClaims orders two version claims by the three numbers each begins
// with, MAJOR.MINOR.PATCH, a "v" before them allowed.
//
// **A suffix after the numbers is shown and never ordered.** git describe's
// `-3-gabc1234` sorts after its release and a pre-release's `-rc.1` before
// it, and no one reading of a suffix gets both right: semver's ranks the
// first below its release and compares `9-g…` against `10-g…` as text, which
// would hold back every forward dev build rta's own scaffold teaches authors
// to stamp. So two claims with the same three numbers and a suffix on either
// side are ones rta cannot order, and ok is false for them, as for anything
// that does not begin with three numbers ("dev", "nightly-2026-11-01", "1.2").
// The price is a release moving to its own pre-release, which is taken as
// any unordered move is.
//
// **What it is for, and what it is not.** It notices an index that moves a
// plugin back while telling the truth about its versions — a maintainer
// reverting a release, an index pointed back at a genuine older one — so that
// moving back becomes a decision somebody makes. A version is the index's
// claim (lock.go): one that lies is not stopped by this, nothing resolves
// through it, and nothing is trusted because of it. It fails toward asking.
//
// Both claims are an index's text, so the numbers are compared as runs of
// digits, by length and then by text, never parsed into an int that a long
// run would wrap.
func compareClaims(a, b string) (order int, ok bool) {
	an, asuffix, aok := claimNumbers(a)
	bn, bsuffix, bok := claimNumbers(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range an {
		if c := compareDigits(an[i], bn[i]); c != 0 {
			return c, true
		}
	}
	if asuffix != "" || bsuffix != "" {
		return 0, false
	}
	return 0, true
}

// claimNumbers splits a claim into the three numbers it begins with and what
// follows them: "v1.4.0-rc.1" is 1, 4, 0 and "-rc.1". ok is false for a claim
// that does not begin with three numbers.
func claimNumbers(claim string) (nums [3]string, suffix string, ok bool) {
	s := strings.TrimPrefix(claim, "v")
	for i := range nums {
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		if end == 0 {
			return nums, "", false
		}
		nums[i], s = s[:end], s[end:]
		if i < len(nums)-1 {
			if !strings.HasPrefix(s, ".") {
				return nums, "", false
			}
			s = s[1:]
		}
	}
	return nums, s, true
}

// compareDigits orders two runs of digits by the number they spell.
func compareDigits(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	return strings.Compare(a, b)
}
