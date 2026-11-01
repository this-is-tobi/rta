package cli

import (
	"strings"
	"testing"
	"time"

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

	if got, want := render(t, tbl, CSV), "Mount,Size (bytes),Status\n/,994662584320,WARN >80%\n/boot,?,unknown\n"; got != want {
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

// csv names the unit each column's values are written in, since it carries no
// kinds: a size in bytes, a length of time in seconds, a percentage in
// percent. Not where the name says it already, not for a time, which says its
// zone itself, and not for a column that writes no value.
func TestCSVNamesTheUnitItsValuesAreIn(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{
		{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}, {Name: "Age", Kind: view.KindDuration},
		{Name: "Use%", Kind: view.KindPercent}, {Name: "CPU", Kind: view.KindUsage},
		{Name: "At", Kind: view.KindTimestamp}, {Name: "Raw", Kind: view.KindBytes},
	}}
	tbl.Add(view.Plain("a"), view.Bytes(int64(1024)), view.Duration("1m", time.Minute),
		view.Number("47.6%", 47.6), view.Number("12%", 12),
		view.Time("today", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)), view.Plain("1.5 GiB"))
	got, _, _ := strings.Cut(render(t, tbl, CSV), "\n")
	if want := "Name,Size (bytes),Age (seconds),Use%,CPU (%),At,Raw"; got != want {
		t.Errorf("header %q, want %q", got, want)
	}
}

// A value taken from the text is the one csv writes, as json does: a time a
// plugin wrote with its offset and gave no value is valued in UTC, and csv
// wrote the text as written while json had it in UTC.
func TestCSVWritesTheValueATextHolds(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "At", Kind: view.KindTimestamp}}, Rows: [][]string{{"2026-10-31T14:02:00+01:00"}}}
	if got, want := render(t, tbl, CSV), "At\n2026-10-31T13:02:00Z\n"; got != want {
		t.Errorf("csv:\n%s\nwant:\n%s", got, want)
	}
}
