package mcp

import (
	"strconv"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A table cut to fit a tool result is cut with its values, from the end it
// keeps: every value left must stand beside the row it was written for.
func TestACutTableKeepsEachValueBesideItsRow(t *testing.T) {
	for _, tail := range []bool{false, true} {
		tbl := view.Table{Columns: []view.Column{{Name: "N", Kind: view.KindNumber}}, Tail: tail}
		for i := range 400 {
			tbl.Add(view.Count(i))
		}
		cut, ok := shrinkTable(tbl, 2000, func(int) string { return "" })
		if !ok {
			t.Fatalf("tail=%v: nothing fit", tail)
		}
		out := cut.(view.Table)
		if len(out.Rows) == len(tbl.Rows) || len(out.Values) != len(out.Rows) {
			t.Fatalf("tail=%v: %d rows and %d values kept of %d", tail, len(out.Rows), len(out.Values), len(tbl.Rows))
		}
		for i, row := range out.Rows {
			if out.Values[i][0] != row[0] {
				t.Fatalf("tail=%v: row %d reads %q beside the value %q", tail, i, row[0], out.Values[i][0])
			}
		}
		if tail && out.Rows[len(out.Rows)-1][0] != strconv.Itoa(399) {
			t.Errorf("a tail table kept %q last, want its newest row", out.Rows[len(out.Rows)-1][0])
		}
	}
}
