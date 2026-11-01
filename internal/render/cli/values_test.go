package cli

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// csv is read by programs alone, so a cell with a value is written as the
// value: a spreadsheet sums 994662584320 and cannot sum "926.4 GiB". A
// status keeps its text, which says more than the word it is valued as.
// Pretty output keeps the cell; yaml carries both, the value in its type.
func TestCSVWritesACellsValueAndPrettyItsText(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Mount"}, {Name: "Size", Kind: view.KindBytes}, {Name: "Status", Kind: view.KindStatus}}}
	tbl.Add(view.Plain("/"), view.Bytes(int64(994662584320)), view.Status("WARN >80%", "warn"))
	tbl.Add(view.Plain("/boot"), view.Plain("?"), view.Plain("unknown"))

	if got, want := render(t, tbl, CSV), "Mount,Size,Status\n/,994662584320,WARN >80%\n/boot,?,unknown\n"; got != want {
		t.Errorf("csv:\n%s\nwant:\n%s", got, want)
	}
	if got := render(t, tbl, Pretty); !strings.Contains(got, "926.4 GiB") || strings.Contains(got, "994662584320") {
		t.Errorf("pretty output does not draw the cell, or draws the value:\n%s", got)
	}
	got := render(t, tbl, YAML)
	if !strings.Contains(got, "- 994662584320") || !strings.Contains(got, "- warn") {
		t.Errorf("yaml does not carry the values:\n%s", got)
	}

	// A masked column's value is masked with it, in csv as everywhere.
	tbl.Redacted = []string{"Size"}
	if got := render(t, tbl, CSV); strings.Contains(got, "994662584320") || !strings.Contains(got, view.Mask) {
		t.Errorf("a redacted size reached csv:\n%s", got)
	}
}
