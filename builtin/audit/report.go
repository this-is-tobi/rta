package audit

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The shape every audit in this plugin shares: run checks, collect findings,
// and arrange the same findings two ways — a compact table for the tile and
// the pipe, a sectioned page for the reader working through them.
//
// It lives here rather than in the capability that first needed it because
// the second capability proved it was general. The alternative — each audit
// growing its own notion of a row, a grade and a section — is how the compact
// and detailed views of the same run start disagreeing with each other.

// Status vocabulary shared with theme.ClassifyStatus: "ok" greens, "warn"
// ambers, "fail"/"expired" reds, "info" mutes. Findings speak it so the
// grade reads at a glance.
const (
	stOK   = "ok"
	stWarn = "warn"
	stFail = "fail"
	stInfo = "info"
)

// finding is one graded check. Findings are values rather than pre-rendered
// table rows so the same set can be counted, grouped into a detail page's
// sections and flattened into the compact table without three separate
// notions of what a row contains — the compact view used to build []string
// rows directly, which meant the summary had to read a status back out of
// row[1] and any column added ahead of it silently broke the tally.
// group is one area of a report: a stable id that a script or an agent
// addresses the section by, and a heading a person reads. It was a single
// prose string doing both jobs, so rewording a heading silently renamed the
// key everything else addressed it by — the exact tension view.Section
// exists to resolve, met here by the audit that most wants stable output.
type group struct{ id, title string }

type finding struct {
	group  group
	check  string
	status string
	detail string
	ref    reference
	// link is the one URL that answers this finding, and it is a field rather
	// than the tail of detail because detail is clipped and a clipped URL is
	// not a URL. Every vulnerable-dependency row read "… — https…" — on a
	// hundred-column terminal as much as a forty-column one, since the cut
	// happens here and not in the renderer. The advisory page is the single
	// most useful thing a hit can carry, and it was the one part guaranteed to
	// be thrown away.
	link string
}

// report collects findings as the checks run.
type report struct {
	findings []finding
}

func (r *report) add(g group, check, status, detail string, ref reference) {
	r.findings = append(r.findings, finding{group: g, check: check, status: status, detail: detail, ref: ref})
}

// addLinked is add for a finding that can be followed somewhere.
func (r *report) addLinked(g group, check, status, detail string, ref reference, link string) {
	r.findings = append(r.findings,
		finding{group: g, check: check, status: status, detail: detail, ref: ref, link: link})
}

// worst returns the report's overall grade and a tally to explain it.
func (r *report) worst() (string, string) {
	var warn, fail int
	for _, f := range r.findings {
		switch f.status {
		case stWarn:
			warn++
		case stFail:
			fail++
		}
	}
	switch {
	case fail > 0 && warn > 0:
		return stFail, fmt.Sprintf("%d failing, %s", fail, plural(warn, "warning"))
	case fail > 0:
		return stFail, fmt.Sprintf("%d failing", fail)
	case warn > 0:
		return stWarn, plural(warn, "warning")
	}
	return stOK, "no issues found"
}

// The column carrying a finding's link appears only when this report has one
// to put in it. An always-present column would be empty on every row of every
// mail and web audit — and a column of nothing is width taken from the prose
// that needed it, on exactly the narrow terminals this is meant to help.
func (r *report) linked() bool {
	for _, f := range r.findings {
		if f.link != "" {
			return true
		}
	}
	return false
}

// table is the compact one-table view: one line per finding, headline first.
func (r *report) table(summary bool) view.Table { return r.build(summary, compactDetail) }

// section is one group of a detail page, which is a different promise from the
// compact table and needs a different bound.
//
// A summary row is a line: it says which check and how it went, and a sprawling
// CSP truncated at ninety-six characters is the right amount of a policy dump
// to put on it. A detail page is what somebody opens *because* the line was not
// enough, and it was clipping to exactly the same ninety-six — so the page that
// exists to say more said precisely as much, and the advisory list a hit is
// actually made of ended in an ellipsis on every screen.
func (r *report) section() view.Table { return r.build(false, pageDetail) }

func (r *report) build(summary bool, maxDetail int) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Check"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Detail"},
		{Name: "Reference"},
	}}
	links := r.linked()
	if links {
		t.Columns = append(t.Columns, view.Column{Name: "Link"})
	}
	// Exactly as many cells as there are columns, always: a renderer that
	// masks by column name cannot mask a cell that has no column.
	row := func(cells ...string) []string {
		out := make([]string, len(t.Columns))
		copy(out, cells)
		return out
	}
	if summary {
		status, detail := r.worst()
		t.Rows = append(t.Rows, row("overall", status, detail))
	}
	for _, f := range r.findings {
		t.Rows = append(t.Rows, row(f.check, f.status, clipTo(f.detail, maxDetail), f.ref.String(), f.link))
	}
	t.Total = len(t.Rows)
	return t
}

// only narrows a report to one group, so a section renders through exactly
// the same table builder the compact view uses.
func (r *report) only(g group) *report {
	out := &report{}
	for _, f := range r.findings {
		if f.group == g {
			out.findings = append(out.findings, f)
		}
	}
	return out
}

// grade is the two lines every detail page opens with, so that a reader who
// stops after the summary has still read the answer.
func (r *report) grade() []view.Pair {
	status, tally := r.worst()
	return []view.Pair{
		{Key: "grade", Value: status + " — " + tally},
		{Key: "checks", Value: strconv.Itoa(len(r.findings))},
	}
}

// detailPage is the full-page report: the same findings, grouped into the
// areas a hardening pass actually works through one at a time, plus the
// controls they cite. Nothing is recomputed — a detail page is an
// arrangement of what the compact view already found, which is what keeps
// the two from ever disagreeing.
//
// order names the groups and fixes their sequence. It is per-audit because
// the areas differ, but the assembly does not.
func detailPage(ctx context.Context, req plugin.Request, r *report, order []group, summary view.View) view.View {
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", summary)
	for _, g := range order {
		// A group with nothing to say (no cookies were set, no CORS headers
		// came back) gets no heading: an empty section reads as a check that
		// failed to run rather than one that had no subject.
		if sub := r.only(g); len(sub.findings) > 0 {
			p.PutAs(g.id, g.title, sub.section())
		}
	}
	p.PutAs("references", "references", referenceTable(r.findings))
	return p.View()
}

// How much of a detail is worth showing, per surface.
//
// compactDetail is a line: the compact table is one row per finding and the
// point of it is the grade, so a sprawling CSP or SPF record is present rather
// than quoted. pageDetail is a paragraph: a detail page is what somebody opens
// because the line was not enough, and the things that actually run past a
// line there — an advisory list, a chain of packages, a policy — are worth two
// or three wrapped lines and are still not worth a screen.
const (
	compactDetail = 96
	pageDetail    = 240
)

// clip keeps a detail cell scannable at compact length.
func clip(s string) string { return clipTo(s, compactDetail) }

// clipTo is clip at a stated bound. The renderer wraps and never truncates, so
// this is the only thing between a 2 KB Permissions-Policy header and a cell
// that fills the screen with it — which is a producer's judgement about what is
// worth reading, not a layout decision, and belongs here rather than there.
func clipTo(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace/newlines
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// plural counts a noun. English is not worth modelling, but "2 advisorys" in
// a security report reads as carelessness about everything else in it, and
// the -y rule is the one that comes up.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if len(noun) > 1 && strings.HasSuffix(noun, "y") &&
		!strings.ContainsRune("aeiou", rune(noun[len(noun)-2])) {
		return fmt.Sprintf("%d %sies", n, noun[:len(noun)-1])
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
