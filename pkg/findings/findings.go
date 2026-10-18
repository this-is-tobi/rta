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
	"github.com/this-is-tobi/rta/pkg/format"
	"math"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Status vocabulary shared with the renderers' status classifier: "ok"
// greens, "warn" ambers, "fail" reds, "info" mutes. Findings speak it so the
// grade reads at a glance, and Worst grades on only Warn and Fail — Info is a
// fact worth a row, never a mark against the subject, and a check that could
// not run (AddUnchecked) is Info the tally names.
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
	// Unchecked marks a check that could not run — a lookup that failed or
	// timed out, a file that could not be parsed. It is Info, since it says
	// nothing about the subject, and Worst counts it all the same: a report
	// whose DMARC lookup timed out read "no issues found", a claim about a
	// record nobody read. Set by AddUnchecked.
	Unchecked bool
	// Count is how many checks this finding stands for, where one row says
	// that fifty-seven images were not graded: zero is one, the row of any
	// ordinary finding. Worst and Grade count checks, not rows, so that the
	// overall line does not call fifty-seven checks that could not run one.
	// Set by AddUncheckedN.
	Count int
}

// checks is how many checks the finding stands for.
func (f Finding) checks() int {
	if f.Count < 1 {
		return 1
	}
	return f.Count
}

// Report collects findings as the checks run. The zero value is ready to use.
type Report struct {
	Findings []Finding
	// Whole says the reader is a program or a document and not a screen, so
	// every detail is sent as written. A detail is cut at the length a person
	// can scan (compactDetail, pageDetail), and that is a layout decision:
	// `-o json` is the one exact format, and `audit web -o md >> report` is a
	// document, and both ended their rows in an ellipsis. The producer knows
	// whom it answers, so it says so; the zero value keeps the cut, which is
	// what a plugin that never sets it has always had.
	Whole bool
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

// AddUnchecked records a check that could not run, and why: graded Info and
// counted by Worst, so the overall line says what the report did not read.
func (r *Report) AddUnchecked(g Group, check, detail string, ref Reference) {
	r.Findings = append(r.Findings,
		Finding{Group: g, Check: check, Status: Info, Detail: detail, Ref: ref, Unchecked: true})
}

// AddUncheckedN is AddUnchecked for one row that stands for n checks that could
// not run — the images an audit could not grade, which a row per image would
// bury the graded ones under. The overall line counts n.
func (r *Report) AddUncheckedN(g Group, check, detail string, ref Reference, n int) {
	r.Findings = append(r.Findings,
		Finding{Group: g, Check: check, Status: Info, Detail: detail, Ref: ref, Unchecked: true, Count: n})
}

// Worst returns the report's overall grade and a tally to explain it.
//
// A check that could not run never moves the grade — it is not a mark
// against the subject — and always reaches the tally, so an ok is "no issues
// in what was checked" rather than "no issues found" when something was not.
func (r *Report) Worst() (string, string) {
	var warn, fail, unchecked int
	for _, f := range r.Findings {
		switch {
		case f.Unchecked:
			unchecked += f.checks()
		case f.Status == Warn:
			warn += f.checks()
		case f.Status == Fail:
			fail += f.checks()
		}
	}
	var parts []string
	status := OK
	if fail > 0 {
		status = Fail
		parts = append(parts, fmt.Sprintf("%d failing", fail))
	}
	if warn > 0 {
		if status == OK {
			status = Warn
		}
		parts = append(parts, Plural(warn, "warning"))
	}
	notRun := Plural(unchecked, "check") + " could not run"
	switch {
	case len(parts) == 0 && unchecked > 0:
		return OK, "no issues in what was checked, but " + notRun
	case len(parts) == 0:
		return OK, "no issues found"
	case unchecked > 0:
		parts = append(parts, notRun)
	}
	return status, strings.Join(parts, ", ")
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

// TableFor is Table for the surface that asked. Over MCP a detail is sent
// whole: the bound is a layout decision, a row that is one screen line, and a
// model has no screen. It cost the part that says what to do: audit_deps
// ended its one next step on "Run osv-scanner, trivy or grype ag…", and the
// only way to the rest was the detail page, many times the size, whose rows
// are clipped too. What could run away is the producer's to bound (Clip), and
// the bridge bounds what is sent in all.
func (r *Report) TableFor(sf plugin.Surface, summary bool) view.Table {
	if sf == plugin.SurfaceMCP {
		return r.build(summary, math.MaxInt)
	}
	return r.Table(summary)
}

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
//
// Per surface for the reason TableFor is: the bound is a paragraph on a screen
// and nothing at all for a model, which has none.
func (r *Report) section(sf plugin.Surface) view.Table {
	if sf == plugin.SurfaceMCP {
		return r.build(false, math.MaxInt)
	}
	return r.build(false, pageDetail)
}

func (r *Report) build(summary bool, maxDetail int) view.Table {
	if r.Whole {
		maxDetail = math.MaxInt
	}
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
	out := &Report{Whole: r.Whole}
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
	var checks int
	for _, f := range r.Findings {
		checks += f.checks()
	}
	return []view.Pair{
		{Key: "grade", Value: status + " — " + tally},
		{Key: "checks", Value: strconv.Itoa(checks)},
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
// section: a view.KeyValue built around Grade, with whatever the page is about
// ahead of it (the domain, the cluster, the realm).
//
// **It is a KeyValue and not a view.View on purpose.** The findings are the
// group sections below it; the compact table a tile draws holds the same
// findings, so handing it over as the summary put every finding on the page
// twice, and five audits did exactly that — each reported as "it runs the check
// twice" when the check ran once and the page said everything twice. A type
// that cannot hold a table makes that mistake a compile error instead of
// something a reader notices on a screen.
//
// extra are sections that are not groups of findings, placed after the groups
// and before the references: a table of its own where each row is a thing and
// not a check, such as the images an audit could not grade and why. A finding's
// detail is one clipped cell, and a list of fifty-seven names in one cell is
// how that list came to be unreadable; as a table it is whole on every format.
func (r *Report) Page(ctx context.Context, req plugin.Request, order []Group, summary view.KeyValue, extra ...view.Section) view.View {
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", summary)
	for _, g := range order {
		// A group with nothing to say (no cookies were set, no CORS headers
		// came back) gets no heading: an empty section reads as a check that
		// failed to run rather than one that had no subject.
		if sub := r.only(g); len(sub.Findings) > 0 {
			p.PutAs(g.ID, g.Title, sub.section(req.Surface()))
		}
	}
	for _, s := range extra {
		p.PutAs(s.ID, s.Title, s.View)
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

// Plural counts a noun: Plural(2, "advisory") is "2 advisories".
//
// format.CountOf is the implementation and the place to add to. This stays
// because it is published — a plugin's report is written against it — and
// because a findings producer should not have to reach for a second package
// to count what it found. The rule itself lived here and in three other
// packages, one of which had it wrong.
func Plural(n int, noun string) string { return format.CountOf(n, noun) }
