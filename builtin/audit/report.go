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
}

// report collects findings as the checks run.
type report struct {
	findings []finding
}

func (r *report) add(g group, check, status, detail string, ref reference) {
	r.findings = append(r.findings, finding{group: g, check: check, status: status, detail: detail, ref: ref})
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

// table flattens the findings into the compact one-table view, headline
// first: the worst status found, with a tally.
func (r *report) table(summary bool) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Check"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Detail"},
		{Name: "Reference"},
	}}
	if summary {
		status, detail := r.worst()
		t.Rows = append(t.Rows, []string{"overall", status, detail, ""})
	}
	for _, f := range r.findings {
		t.Rows = append(t.Rows, []string{f.check, f.status, clip(f.detail), f.ref.String()})
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
			p.PutAs(g.id, g.title, sub.table(false))
		}
	}
	p.PutAs("references", "references", referenceTable(r.findings))
	return p.View()
}

// clip keeps a detail cell scannable: long values (a sprawling CSP or SPF
// record, say) are the presence of the thing, not its full text — the point
// is the grade, not the policy dump.
func clip(s string) string {
	const max = 96
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
