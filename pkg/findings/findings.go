// Package findings is the shape of a graded check: a report that collects
// findings as checks run and arranges the same findings two ways — a compact
// table for a dashboard tile and the pipe, a sectioned page for the reader
// working through them — with every finding citing the control it grades
// against.
//
// It is part of the SDK rather than a corner of the audit built-in because
// the second audit proved it general and a plugin's audit should not have to
// carry a private copy. A Keycloak realm, a GitHub repository or a Vault
// mount graded by a plugin renders exactly as `rta audit web` does: the same
// columns, the same grade line, the same references section — so a reader
// learns one report and an operator's scripts read one shape. The
// alternative, each audit growing its own notion of a row, a grade and a
// section, is how the compact and detailed views of the same run start
// disagreeing with each other.
//
// Nothing here crosses the wire on its own. A Report renders to the view
// types every capability already returns (view.Table, view.Pair, a
// plugin.Page), which is what makes it a package a plugin imports rather
// than a protocol change the host has to learn.
package findings

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Status vocabulary shared with the renderers' status classifier: "ok"
// greens, "warn" ambers, "fail" reds, "info" mutes. Findings speak it so the
// grade reads at a glance, and Worst counts only Warn and Fail — Info is a
// fact worth a row, never a mark against the subject.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
	Info = "info"
)

// Group is one area of a report: a stable ID that a script or an agent
// addresses the section by, and a Title a person reads. It was a single
// prose string doing both jobs, so rewording a heading silently renamed the
// key everything else addressed it by — the exact tension view.Section
// exists to resolve, met here by the audit that most wants stable output.
type Group struct{ ID, Title string }

// Finding is one graded check. Findings are values rather than pre-rendered
// table rows so the same set can be counted, grouped into a detail page's
// sections and flattened into the compact table without three separate
// notions of what a row contains — the compact view used to build []string
// rows directly, which meant the summary had to read a status back out of
// row[1] and any column added ahead of it silently broke the tally.
type Finding struct {
	Group  Group
	Check  string
	Status string
	Detail string
	Ref    Reference
	// Link is the one URL that answers this finding, and it is a field rather
	// than the tail of Detail because Detail is clipped and a clipped URL is
	// not a URL. Every vulnerable-dependency row read "… — https…" — on a
	// hundred-column terminal as much as a forty-column one, since the cut
	// happens here and not in the renderer. The advisory page is the single
	// most useful thing a hit can carry, and it was the one part guaranteed
	// to be thrown away.
	Link string
}

// Report collects findings as the checks run. The zero value is ready to use.
type Report struct {
	Findings []Finding
}

// Add records one graded check.
func (r *Report) Add(g Group, check, status, detail string, ref Reference) {
	r.Findings = append(r.Findings, Finding{Group: g, Check: check, Status: status, Detail: detail, Ref: ref})
}

// AddLinked is Add for a finding that can be followed somewhere.
func (r *Report) AddLinked(g Group, check, status, detail string, ref Reference, link string) {
	r.Findings = append(r.Findings,
		Finding{Group: g, Check: check, Status: status, Detail: detail, Ref: ref, Link: link})
}

// Worst returns the report's overall grade and a tally to explain it.
func (r *Report) Worst() (string, string) {
	var warn, fail int
	for _, f := range r.Findings {
		switch f.Status {
		case Warn:
			warn++
		case Fail:
			fail++
		}
	}
	switch {
	case fail > 0 && warn > 0:
		return Fail, fmt.Sprintf("%d failing, %s", fail, Plural(warn, "warning"))
	case fail > 0:
		return Fail, fmt.Sprintf("%d failing", fail)
	case warn > 0:
		return Warn, Plural(warn, "warning")
	}
	return OK, "no issues found"
}

// The column carrying a finding's link appears only when this report has one
// to put in it. An always-present column would be empty on every row of every
// mail and web audit — and a column of nothing is width taken from the prose
// that needed it, on exactly the narrow terminals this is meant to help.
func (r *Report) linked() bool {
	for _, f := range r.Findings {
		if f.Link != "" {
			return true
		}
	}
	return false
}

// Table is the compact one-table view: one line per finding, and with
// summary the overall grade as the first row.
func (r *Report) Table(summary bool) view.Table { return r.build(summary, compactDetail) }

// section is one group of a detail page, which is a different promise from
// the compact table and needs a different bound.
//
// A summary row is a line: it says which check and how it went, and a
// sprawling CSP truncated at ninety-six characters is the right amount of a
// policy dump to put on it. A detail page is what somebody opens *because*
// the line was not enough, and it was clipping to exactly the same
// ninety-six — so the page that exists to say more said precisely as much,
// and the advisory list a hit is actually made of ended in an ellipsis on
// every screen.
func (r *Report) section() view.Table { return r.build(false, pageDetail) }

func (r *Report) build(summary bool, maxDetail int) view.Table {
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
		status, detail := r.Worst()
		t.Rows = append(t.Rows, row("overall", status, detail))
	}
	for _, f := range r.Findings {
		t.Rows = append(t.Rows, row(f.Check, f.Status, clipTo(f.Detail, maxDetail), f.Ref.String(), f.Link))
	}
	t.Total = len(t.Rows)
	return t
}

// only narrows a report to one group, so a section renders through exactly
// the same table builder the compact view uses.
func (r *Report) only(g Group) *Report {
	out := &Report{}
	for _, f := range r.Findings {
		if f.Group == g {
			out.Findings = append(out.Findings, f)
		}
	}
	return out
}

// Grade is the two lines every detail page opens with, so that a reader who
// stops after the summary has still read the answer.
func (r *Report) Grade() []view.Pair {
	status, tally := r.Worst()
	return []view.Pair{
		{Key: "grade", Value: status + " — " + tally},
		{Key: "checks", Value: strconv.Itoa(len(r.Findings))},
	}
}

// Page is the full-page report: the same findings, grouped into the areas a
// hardening pass actually works through one at a time, plus the controls
// they cite. Nothing is recomputed — a detail page is an arrangement of what
// the compact view already found, which is what keeps the two from ever
// disagreeing.
//
// order names the groups and fixes their sequence. It is per-audit because
// the areas differ, but the assembly does not. summary is the page's opening
// section, usually a view.KeyValue built around Grade.
func (r *Report) Page(ctx context.Context, req plugin.Request, order []Group, summary view.View) view.View {
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", summary)
	for _, g := range order {
		// A group with nothing to say (no cookies were set, no CORS headers
		// came back) gets no heading: an empty section reads as a check that
		// failed to run rather than one that had no subject.
		if sub := r.only(g); len(sub.Findings) > 0 {
			p.PutAs(g.ID, g.Title, sub.section())
		}
	}
	p.PutAs("references", "references", r.References())
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

// Clip keeps a piece of prose scannable at compact length. A producer uses it
// on the part of a detail that could run away — a DNS record, an error
// message — so the words around it survive on the compact row, where the
// whole detail is clipped again at the same bound.
func Clip(s string) string { return clipTo(s, compactDetail) }

// clipTo is Clip at a stated bound. The renderer wraps and never truncates,
// so this is the only thing between a 2 KB Permissions-Policy header and a
// cell that fills the screen with it — which is a producer's judgement about
// what is worth reading, not a layout decision, and belongs here rather than
// there.
func clipTo(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ") // collapse whitespace/newlines
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// Plural counts a noun. English is not worth modelling, but a botched plural
// in a security report reads as carelessness about everything else in it, and
// the -y rule (advisory, advisories) is the one that comes up.
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if len(noun) > 1 && strings.HasSuffix(noun, "y") &&
		!strings.ContainsRune("aeiou", rune(noun[len(noun)-2])) {
		return fmt.Sprintf("%d %sies", n, noun[:len(noun)-1])
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
