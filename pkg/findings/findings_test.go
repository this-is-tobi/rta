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
	if page.Items[1].Title != grpVulnerable.Title {
		t.Errorf("section title = %q, want the group's title", page.Items[1].Title)
	}
	// The page's bound is wider than the row's: this is the same finding at
	// the length the reader opened the page for.
	if detail := page.Items[1].View.(view.Table).Rows[0][2]; detail != "GHSA-x" {
		t.Errorf("section detail = %q", detail)
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
