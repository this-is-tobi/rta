package audit

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A finding's link has to reach the table whole. It used to be the tail of
// the detail string, so clip() cut it in half on every screen — the report
// said "… — https…" and the advisory page it named was unreachable.
func TestALinkSurvivesClipping(t *testing.T) {
	r := &report{}
	long := strings.Repeat("lodash 4.17.20 is named in a great many advisories ", 4)
	r.addLinked(grpVulnerable, "lodash", stFail, long, refVulnerableDep,
		"https://osv.dev/vulnerability/GHSA-35jh-r3h4-6jhm")

	tbl := r.table(false)
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

// The column is earned, not always present: an audit with nothing to link to
// must not spend width on an empty column, which is exactly the width the
// prose beside it needed on a narrow terminal.
func TestNoLinkColumnWithoutALink(t *testing.T) {
	r := &report{}
	r.add(grpInventory, "advisories", stOK, "none of the checked dependencies is named", refVulnerableDep)
	for _, c := range r.table(true).Columns {
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
		build   func(*report)
		summary bool
	}{
		{"linked", func(r *report) {
			r.addLinked(grpVulnerable, "lodash", stFail, "d", refVulnerableDep, "https://osv.dev/x")
			r.add(grpInventory, "dependencies", stInfo, "5 declared", refVulnerableDep)
		}, true},
		{"unlinked", func(r *report) {
			r.add(grpInventory, "dependencies", stInfo, "5 declared", refVulnerableDep)
		}, true},
		{"unlinked, no summary", func(r *report) {
			r.add(grpInventory, "dependencies", stInfo, "5 declared", refVulnerableDep)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &report{}
			tc.build(r)
			tbl := r.table(tc.summary)
			for i, row := range tbl.Rows {
				if len(row) != len(tbl.Columns) {
					t.Errorf("row %d has %d cells for %d columns: %v", i, len(row), len(tbl.Columns), row)
				}
			}
		})
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
