package findings

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

var (
	grpVulnerable = Group{ID: "vulnerabilities", Title: "known vulnerabilities"}
	grpInventory  = Group{ID: "inventory", Title: "inventory"}

	refVulnerableDep = Reference{OWASP: OWASPSupplyChain, CWE: "CWE-1395", Title: "Dependency on Vulnerable Third-Party Component"}
)

// The grade is the worst finding, and the tally says how many of each; a
// report with nothing wrong, or nothing at all, is ok — an empty report is
// "nothing found", never "nothing ran", which the producer says in its own
// words when that is what happened.
func TestWorstIsTheWorstFindingWithATally(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		status   string
		tally    string
	}{
		{"empty", nil, OK, "no issues found"},
		{"info only", []string{Info, Info}, OK, "no issues found"},
		{"one warning", []string{OK, Warn}, Warn, "1 warning"},
		{"warnings", []string{Warn, Warn}, Warn, "2 warnings"},
		{"failing", []string{Fail, OK}, Fail, "1 failing"},
		{"both", []string{Fail, Warn, Warn, Fail}, Fail, "2 failing, 2 warnings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Report{}
			for i, s := range tc.statuses {
				r.Add(grpInventory, "check"+string(rune('a'+i)), s, "d", refVulnerableDep)
			}
			status, tally := r.Worst()
			if status != tc.status || tally != tc.tally {
				t.Errorf("Worst() = %q, %q; want %q, %q", status, tally, tc.status, tc.tally)
			}
		})
	}
}

// A check that could not run grades nothing about the subject, and it is not
// nothing either: "no issues found" over a report whose DMARC lookup timed
// out is a claim about a record nobody read. The tally says what did not run,
// whatever the grade.
func TestAnUncheckedFindingIsCountedAndNeverGraded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statuses  []string
		unchecked int
		status    string
		tally     string
	}{
		{"alone", nil, 1, OK, "no issues in what was checked, but 1 check could not run"},
		{"beside ok", []string{OK, Info}, 3, OK, "no issues in what was checked, but 3 checks could not run"},
		{"beside a warning", []string{Warn}, 1, Warn, "1 warning, 1 check could not run"},
		{"beside both", []string{Fail, Warn, Warn}, 2, Fail, "1 failing, 2 warnings, 2 checks could not run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Report{}
			for i, s := range tc.statuses {
				r.Add(grpInventory, "check"+string(rune('a'+i)), s, "d", refVulnerableDep)
			}
			for i := range tc.unchecked {
				r.AddUnchecked(grpInventory, "lookup"+string(rune('a'+i)), "lookup failed", refVulnerableDep)
			}
			status, tally := r.Worst()
			if status != tc.status || tally != tc.tally {
				t.Errorf("Worst() = %q, %q; want %q, %q", status, tally, tc.status, tc.tally)
			}
			for _, f := range r.Findings {
				if f.Unchecked && f.Status != Info {
					t.Errorf("an unchecked finding graded %q, want %q", f.Status, Info)
				}
			}
		})
	}
}

// A finding's link has to reach the table whole. It used to be the tail of
// the detail string, so Clip cut it in half on every screen — the report
// said "… — https…" and the advisory page it named was unreachable.
func TestALinkSurvivesClipping(t *testing.T) {
	r := &Report{}
	long := strings.Repeat("lodash 4.17.20 is named in a great many advisories ", 4)
	r.AddLinked(grpVulnerable, "lodash", Fail, long, refVulnerableDep,
		"https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm")

	tbl := r.Table(false)
	link := columnIndex(t, tbl, "Link")
	if len(tbl.Rows) != 1 {
		t.Fatalf("want one row, got %d", len(tbl.Rows))
	}
	if got := tbl.Rows[0][link]; got != "https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm" {
		t.Errorf("link cell = %q", got)
	}
	if detail := tbl.Rows[0][columnIndex(t, tbl, "Detail")]; !strings.HasSuffix(detail, "…") {
		t.Fatalf("this detail is meant to be long enough to clip, so the test proves something: %q", detail)
	}
}

// The compact bound is a layout decision for a row that is one line of a
// terminal. A model has no line to fit, and was handed the next step an audit
// recommends cut off mid-word.
func TestAModelIsHandedEachDetailWhole(t *testing.T) {
	long := strings.Repeat("run osv-scanner against this project for reachability. ", 6)
	r := &Report{}
	r.Add(grpInventory, "next step", Info, long, refVulnerableDep)

	detail := func(tbl view.Table) string { return tbl.Rows[0][columnIndex(t, tbl, "Detail")] }
	if got := detail(r.TableFor(plugin.SurfaceMCP, false)); got != strings.TrimSpace(long) {
		t.Errorf("over MCP the detail was cut: %q", got)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		if got := detail(r.TableFor(sf, false)); !strings.HasSuffix(got, "…") {
			t.Errorf("%v: a long detail was not clipped to a line: %q", sf, got)
		}
	}
}

// A detail page's bound is a layout decision as much as the compact row's is,
// and a model has no screen either way. The page clipped at 240 on every
// surface, so the untracked-images row of audit.kube.eol and a long wildcard
// list reached an agent cut off in the one place the table was meant to be
// whole.
func TestAModelIsHandedEachPageSectionDetailWhole(t *testing.T) {
	long := strings.Repeat("a long list that names every verb a role was given. ", 14)
	whole := strings.Join(strings.Fields(long), " ")
	r := &Report{}
	r.Add(grpInventory, "next step", Info, long, refVulnerableDep)

	detail := func(sf plugin.Surface) string {
		page, ok := r.Page(context.Background(), plugin.Request{}.WithSurface(sf), []Group{grpInventory},
			view.KeyValue{Pairs: r.Grade()}).(view.Sections)
		if !ok {
			t.Fatal("Page did not return sections")
		}
		tbl := page.Items[1].View.(view.Table)
		return tbl.Rows[0][columnIndex(t, tbl, "Detail")]
	}
	if got := detail(plugin.SurfaceMCP); got != whole {
		t.Errorf("over MCP the page's detail was cut: %d of %d characters", len([]rune(got)), len([]rune(whole)))
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		got := detail(sf)
		if !strings.HasSuffix(got, "…") || len([]rune(got)) != pageDetail {
			t.Errorf("%v: a long detail on a page was not clipped to a paragraph: %d characters", sf, len([]rune(got)))
		}
	}
}

// **The clip is a screen's, and a report says whether its reader has one.**
// `-o json` is the one exact format and `audit web -o md >> report` is a
// document: both ended their rows in an ellipsis, because the cut was made
// here for every reader but a model's. Whole is the producer's word that the
// reader is a program, and it sends every detail as written in the compact
// table and on the page alike.
func TestAReportMarkedWholeSendsEveryDetailUnclipped(t *testing.T) {
	long := strings.Repeat("a long list that names every verb a role was given. ", 14)
	whole := strings.Join(strings.Fields(long), " ")
	r := &Report{Whole: true}
	r.Add(grpInventory, "next step", Info, long, refVulnerableDep)

	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		tbl := r.TableFor(sf, false)
		if got := tbl.Rows[0][columnIndex(t, tbl, "Detail")]; got != whole {
			t.Errorf("%v: the compact table cut a whole report's detail to %d of %d characters", sf, len([]rune(got)), len([]rune(whole)))
		}
		page, ok := r.Page(context.Background(), plugin.Request{}.WithSurface(sf), []Group{grpInventory},
			view.KeyValue{Pairs: r.Grade()}).(view.Sections)
		if !ok {
			t.Fatal("Page did not return sections")
		}
		pt := page.Items[1].View.(view.Table)
		if got := pt.Rows[0][columnIndex(t, pt, "Detail")]; got != whole {
			t.Errorf("%v: the page cut a whole report's detail to %d of %d characters", sf, len([]rune(got)), len([]rune(whole)))
		}
	}

	r.Whole = false
	if got := r.Table(false).Rows[0][2]; !strings.HasSuffix(got, "…") || len([]rune(got)) != compactDetail {
		t.Errorf("a report not marked whole is no longer clipped for a screen: %d characters", len([]rune(got)))
	}
}

// The column is earned, not always present: an audit with nothing to link to
// must not spend width on an empty column, which is exactly the width the
// prose beside it needed on a narrow terminal.
func TestNoLinkColumnWithoutALink(t *testing.T) {
	r := &Report{}
	r.Add(grpInventory, "advisories", OK, "none of the checked dependencies is named", refVulnerableDep)
	for _, c := range r.Table(true).Columns {
		if c.Name == "Link" {
			t.Fatal("a Link column appeared with no finding to link")
		}
	}
}

// Every row carries exactly one cell per column, whichever shape the table
// took. A row longer than its headers puts a value in a column nothing can
// name — and view.Redact masks by column name.
func TestEveryRowMatchesTheColumns(t *testing.T) {
	for _, tc := range []struct {
		name    string
		build   func(*Report)
		summary bool
	}{
		{"linked", func(r *Report) {
			r.AddLinked(grpVulnerable, "lodash", Fail, "d", refVulnerableDep, "https://osv.dev/x")
			r.Add(grpInventory, "dependencies", Info, "5 declared", refVulnerableDep)
		}, true},
		{"unlinked", func(r *Report) {
			r.Add(grpInventory, "dependencies", Info, "5 declared", refVulnerableDep)
		}, true},
		{"unlinked, no summary", func(r *Report) {
			r.Add(grpInventory, "dependencies", Info, "5 declared", refVulnerableDep)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &Report{}
			tc.build(r)
			tbl := r.Table(tc.summary)
			for i, row := range tbl.Rows {
				if len(row) != len(tbl.Columns) {
					t.Errorf("row %d has %d cells for %d columns: %v", i, len(row), len(tbl.Columns), row)
				}
			}
		})
	}
}

// A detail page is the compact findings rearranged, nothing more: one
// section per group that has something to say, in the order the audit
// fixed, opened by the summary it was handed and closed by the references.
// A group with no finding gets no heading — an empty section reads as a
// check that failed to run.
func TestPageIsTheFindingsGroupedInOrder(t *testing.T) {
	r := &Report{}
	r.Add(grpInventory, "dependencies", Info, "5 declared", refVulnerableDep)
	r.AddLinked(grpVulnerable, "lodash", Fail, "GHSA-x", refVulnerableDep, "https://osv.dev/x")
	empty := Group{ID: "provenance", Title: "provenance"}

	page, ok := r.Page(context.Background(), plugin.Request{}, []Group{grpVulnerable, empty, grpInventory},
		view.KeyValue{Pairs: r.Grade()}).(view.Sections)
	if !ok {
		t.Fatal("Page did not return sections")
	}
	var ids []string
	for _, s := range page.Items {
		ids = append(ids, s.ID)
	}
	if got, want := strings.Join(ids, ","), "summary,vulnerabilities,inventory,references"; got != want {
		t.Errorf("sections = %s, want %s", got, want)
	}
	// The summary says the grade and leaves the findings to their sections: a
	// finding listed in both is a page that says everything twice.
	summary, ok := page.Items[0].View.(view.KeyValue)
	if !ok || len(summary.Pairs) == 0 || summary.Pairs[0].Key != "grade" {
		t.Errorf("summary = %#v, want a KeyValue opening with the grade", page.Items[0].View)
	}
	if page.Items[1].Title != grpVulnerable.Title {
		t.Errorf("section title = %q, want the group's title", page.Items[1].Title)
	}
	// The page's bound is wider than the row's: this is the same finding at
	// the length the reader opened the page for.
	if detail := page.Items[1].View.(view.Table).Rows[0][2]; detail != "GHSA-x" {
		t.Errorf("section detail = %q", detail)
	}
}

// A finding can stand for several checks that could not run. The eol audit
// could not grade fifty-seven images and said so in one row; the overall line
// counted that row as one check, so a cluster where four images in five were
// never graded read "1 check could not run".
func TestWorstSumsCountedUncheckedFindings(t *testing.T) {
	r := &Report{}
	r.Add(grpInventory, "graded", OK, "d", refVulnerableDep)
	r.AddUncheckedN(grpInventory, "images not graded", "57 of 71", refVulnerableDep, 57)
	r.AddUnchecked(grpInventory, "kubelets", "not read", refVulnerableDep)

	status, tally := r.Worst()
	if status != OK || tally != "no issues in what was checked, but 58 checks could not run" {
		t.Errorf("Worst() = %q, %q", status, tally)
	}
	var checks string
	for _, p := range r.Grade() {
		if p.Key == "checks" {
			checks = p.Value
		}
	}
	if checks != "59" {
		t.Errorf("checks = %q, want 59: the graded one and the 58 that could not run", checks)
	}
	// A count below one is one check, not none: a row that exists stands for
	// something.
	z := &Report{}
	z.AddUncheckedN(grpInventory, "x", "d", refVulnerableDep, 0)
	if _, tally := z.Worst(); tally != "no issues in what was checked, but 1 check could not run" {
		t.Errorf("tally for a count of 0 = %q", tally)
	}
}

// A page can carry a table that is not a group of findings: what could not be
// graded and why is a table of its own, one row per thing, and a joined cell
// in a finding is how it came to be clipped past reading. The extra sections
// sit after the groups and before the references.
func TestPageAppendsExtraSections(t *testing.T) {
	r := &Report{}
	r.Add(grpInventory, "dependencies", Info, "5 declared", refVulnerableDep)
	extra := view.Section{ID: "not-graded", Title: "not graded", View: view.Table{
		Columns: []view.Column{{Name: "Image"}}, Rows: [][]string{{"redis:7"}}, Total: 1}}

	page, ok := r.Page(context.Background(), plugin.Request{}, []Group{grpInventory},
		view.KeyValue{Pairs: r.Grade()}, extra).(view.Sections)
	if !ok {
		t.Fatal("Page did not return sections")
	}
	var ids []string
	for _, s := range page.Items {
		ids = append(ids, s.ID)
	}
	if got, want := strings.Join(ids, ","), "summary,inventory,not-graded,references"; got != want {
		t.Errorf("sections = %s, want %s", got, want)
	}
	if page.Items[2].Title != "not graded" {
		t.Errorf("extra title = %q", page.Items[2].Title)
	}
	// Source compatible: a call with none is the page it always was.
	plain := r.Page(context.Background(), plugin.Request{}, []Group{grpInventory},
		view.KeyValue{Pairs: r.Grade()}).(view.Sections)
	if len(plain.Items) != 3 {
		t.Errorf("a page without extras has %d sections, want 3", len(plain.Items))
	}
}

func TestPlural(t *testing.T) {
	for _, tc := range []struct {
		n    int
		noun string
		want string
	}{
		{1, "warning", "1 warning"},
		{2, "warning", "2 warnings"},
		{2, "advisory", "2 advisories"},
		{3, "key", "3 keys"},
		{0, "file", "0 files"},
	} {
		if got := Plural(tc.n, tc.noun); got != tc.want {
			t.Errorf("Plural(%d, %q) = %q, want %q", tc.n, tc.noun, got, tc.want)
		}
	}
}

func columnIndex(t *testing.T, tbl view.Table, name string) int {
	t.Helper()
	for i, c := range tbl.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("no %q column in %v", name, tbl.Columns)
	return -1
}
