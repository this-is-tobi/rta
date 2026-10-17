package audit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/eolapi"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What audit.kube.eol did not grade, and why.
//
// **Silence about an image read as "all good".** Fifty-seven of seventy-one
// images on a real cluster had no row of their own: the ones endoflife.date does
// not track were one Info line of names joined into a cell and cut at ninety-six
// characters, so nobody could read which; the ones it does track but could not
// be read (a floating tag, a bare digest, a tag that names no cycle) were Info
// rows the overall line ignores. The overall said "no issues in what was
// checked" over an audit that graded a fifth of what runs.
//
// So an image that is not graded is a check that could not run, with a reason.
// The compact table counts them in one row, so the overall line says how many
// and the graded ones are not buried under fifty-seven rows; the detail page
// lists each in a table of its own, so no name is clipped and the list is data
// on every format.

// ungradedReason is why an image has no verdict: a code a script selects on,
// and the phrase the compact row's tally uses for the images that share it.
type ungradedReason struct{ code, tally string }

var (
	reasonNoProduct = ungradedReason{"no-product", "not on endoflife.date"}
	// Suggest only. A name that starts like a tracked product is often a piece
	// of it (cert-manager-controller), and as often another project's work
	// (prometheus-config-reloader, bank-vaults' vault-operator) whose version
	// number says nothing about the product's. A confident wrong verdict is
	// worse than a row that says it was not graded, so this is never graded.
	reasonSuggested  = ungradedReason{"suggested", "named like a tracked product"}
	reasonFloating   = ungradedReason{"floating", "floating or digest-only"}
	reasonDigestOnly = ungradedReason{"digest-only", "floating or digest-only"}
	reasonNoCycle    = ungradedReason{"no-cycle", "naming no release cycle"}
	reasonNoData     = ungradedReason{"no-data", "with no release data"}
	// reasonUnanswered is a product whose page endoflife.date did not serve: a
	// 503 from its host, a rate limit that outlasted the retry, a connection
	// that dropped. Said apart from no-data, which is the site's answer.
	reasonUnanswered = ungradedReason{"unanswered", "unanswered by endoflife.date"}
	// reasonNotLookedUp is an image whose product was past maxEOLProducts.
	reasonNotLookedUp = ungradedReason{"not-looked-up", "past the lookup limit"}

	// ungradedOrder is the order the table lists them in and the tally says them.
	ungradedOrder = []ungradedReason{reasonNoProduct, reasonSuggested, reasonFloating, reasonDigestOnly,
		reasonNoCycle, reasonNoData, reasonUnanswered, reasonNotLookedUp}
)

// notGraded is one distinct image reference that has no verdict.
type notGraded struct {
	use    *imageUse
	reason ungradedReason
	// next is what happened, and what would change it, as one sentence.
	next string
}

// reportNotGraded adds the one compact row that counts what was not graded and
// returns the detail page's table of each, or nothing when everything was.
//
// total is every distinct image reference that runs, so the row reads "57 of 71"
// and the numbers reconcile with the graded rows above it. The row is a check
// that could not run, counted n times: the overall line says "57 checks could
// not run" and not "1".
func reportNotGraded(r *findings.Report, ungraded []notGraded, total int, clusterWide bool) []view.Section {
	if len(ungraded) == 0 {
		return nil
	}
	sort.SliceStable(ungraded, func(i, j int) bool { return reasonRank(ungraded[i].reason) < reasonRank(ungraded[j].reason) })
	r.AddUncheckedN(grpKubeEOLImages,
		fmt.Sprintf("images not graded (%d of %d)", len(ungraded), total),
		tallyUngraded(ungraded)+" — the detail page lists each", refUnmaintained, len(ungraded))

	t := view.Table{Columns: []view.Column{
		{Name: "Image"}, {Name: "Where"}, {Name: "Reason"}, {Name: "Next Step"},
	}}
	for _, n := range ungraded {
		where := findings.Plural(n.use.pods, "pod")
		if clusterWide {
			where += " in " + findings.Plural(len(n.use.namespaces), "namespace")
		}
		t.Rows = append(t.Rows, []string{plugin.ListedName(n.use.shown()), where, n.reason.code, n.next})
	}
	t.Total = len(t.Rows)
	return []view.Section{{ID: "not-graded", Title: "not graded", View: t}}
}

func reasonRank(r ungradedReason) int {
	for i, o := range ungradedOrder {
		if o == r {
			return i
		}
	}
	return len(ungradedOrder)
}

// tallyUngraded counts the images by reason, in the table's order: "43 not on
// endoflife.date, 8 named like a tracked product, 4 floating or digest-only".
func tallyUngraded(ungraded []notGraded) string {
	var phrases []string
	counts := map[string]int{}
	for _, n := range ungraded {
		if counts[n.reason.tally] == 0 {
			phrases = append(phrases, n.reason.tally)
		}
		counts[n.reason.tally]++
	}
	parts := make([]string, len(phrases))
	for i, p := range phrases {
		parts[i] = strconv.Itoa(counts[p]) + " " + p
	}
	return strings.Join(parts, ", ")
}

// shown is the reference as the table's Image column spells it: the name, the
// tag if it has one, and a marker where the bytes are pinned by digest.
func (u *imageUse) shown() string {
	s := u.name
	if u.tag != "" {
		s += ":" + u.tag
	}
	if u.digest {
		s += "@digest"
	}
	return s
}

// untracked says why an image whose name is not a catalogue product has no
// verdict. A name that starts like a product is pointed at it; anything else
// is simply not on the site.
func (g *grader) untracked(u *imageUse) (ungradedReason, string) {
	if product := g.similarTo(u.name); product != "" {
		return reasonSuggested, "named like " + product + ", which endoflife.date tracks, but not graded as it: " +
			"a piece of a product is often versioned apart from it"
	}
	return reasonNoProduct, "endoflife.date does not list it, so there is nothing to grade it against"
}

// similarTo is the catalogue product whose name or alias the image's name
// starts with, followed by a hyphen — the longest such, so cert-manager-webhook
// is cert-manager and not a three-letter alias that happens to begin it. At
// least three characters, so "go-" and "pg-" are not a guess at anything.
func (g *grader) similarTo(name string) string {
	var best string
	for key := range g.index {
		if len(key) >= 3 && strings.HasPrefix(name, key+"-") &&
			(len(key) > len(best) || (len(key) == len(best) && key < best)) {
			best = key
		}
	}
	return g.index[best]
}

// noCycle says why a tag of a tracked product matches none of its cycles, in
// the words that tell the reader what to do: a bare major that several cycles
// sit under needs one pinned, a version past the newest cycle is probably a
// release the site has not added yet, and anything else just names no cycle.
func noCycle(p *eolapi.Product, version string) string {
	shown := plugin.ListedName(version)
	if !strings.Contains(version, ".") {
		var under []string
		for _, rel := range p.Releases {
			if strings.HasPrefix(rel.Name, version+".") {
				under = append(under, rel.Name)
			}
		}
		if len(under) > 1 {
			return fmt.Sprintf("%s names a major with several cycles under it (%s): pin one", shown, strings.Join(under, ", "))
		}
	}
	if newest, ok := newestCycle(p.Releases); ok && newerThan(version, newest) {
		return fmt.Sprintf("%s is newer than the newest cycle endoflife.date lists (%s); it may not have added the release yet",
			shown, newest)
	}
	return fmt.Sprintf("%s names no release cycle of %s that endoflife.date knows (%s)", shown, p.Name, cycleNames(p.Releases))
}

// newestCycle is the highest cycle the site lists whose name is a plain dotted
// number. A cycle called "lts" or "rolling" is not one a version can be
// newer than.
func newestCycle(releases []eolapi.Release) (string, bool) {
	var best string
	var bestParts []int
	for _, rel := range releases {
		parts, ok := dotted(rel.Name)
		if ok && (bestParts == nil || compareDotted(parts, bestParts) > 0) {
			best, bestParts = rel.Name, parts
		}
	}
	return best, bestParts != nil
}

// newerThan reports that version is a plain dotted number above cycle on the
// parts they share: 2.2.0 is newer than 2.1, and 2.1.5 is not, being in it.
func newerThan(version, cycle string) bool {
	v, vok := dotted(version)
	c, cok := dotted(cycle)
	return vok && cok && compareDotted(v, c) > 0
}

func dotted(s string) ([]int, bool) {
	if s == "" {
		return nil, false
	}
	var parts []int
	for _, field := range strings.Split(s, ".") {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
}

// compareDotted compares on the components both have; one that is the other's
// prefix compares equal, which is what "is in that cycle" means.
func compareDotted(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		switch {
		case a[i] > b[i]:
			return 1
		case a[i] < b[i]:
			return -1
		}
	}
	return 0
}
