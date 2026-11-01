package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func renderSorted(t *testing.T, v view.View, sorted SortMark, ascii bool, width int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true, ASCII: ascii, Width: width, Sorted: sorted}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func twoSizes() view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}}}
	t.Add(view.Plain("a"), view.Bytes(int64(10)))
	t.Add(view.Plain("b"), view.Bytes(int64(20)))
	return t
}

// The column a table is sorted by carries the order's arrow on its heading, up
// for ascending and down for descending, in either character set, and no
// other column does.
func TestTheSortedColumnCarriesItsArrow(t *testing.T) {
	for _, c := range []struct {
		desc, ascii bool
		want        string
	}{{true, false, "SIZE ↓"}, {false, false, "SIZE ↑"}, {true, true, "SIZE v"}, {false, true, "SIZE ^"}} {
		got := renderSorted(t, twoSizes(), SortMark{Column: "Size", Desc: c.desc}, c.ascii, 0)
		if !strings.Contains(got, c.want) || strings.Contains(got, "NAME ↓") || strings.Contains(got, "NAME ↑") {
			t.Errorf("desc=%v ascii=%v:\n%s\nwant %q on Size's heading alone", c.desc, c.ascii, got, c.want)
		}
	}
	if got := renderSorted(t, twoSizes(), SortMark{}, false, 0); strings.ContainsAny(got, "↑↓") {
		t.Errorf("a table in its own order carries an arrow:\n%s", got)
	}
}

// On a page the arrow goes to the one table the mark names, by its section's
// key; a mark for a table that is the whole view marks none of a page's.
func TestOnAPageTheArrowGoesToTheTableItNames(t *testing.T) {
	page := view.Sections{Items: []view.Section{
		{ID: "entries", Title: "biggest entries", View: twoSizes()},
		{ID: "files", Title: "largest files", View: twoSizes()},
	}}
	got := renderSorted(t, page, SortMark{Section: "files", Column: "Size", Desc: true}, false, 0)
	if strings.Count(got, "SIZE ↓") != 1 || strings.Index(got, "SIZE ↓") < strings.Index(got, "LARGEST FILES") {
		t.Errorf("the arrow is not on the largest files table alone:\n%s", got)
	}
	if got := renderSorted(t, page, SortMark{Column: "Size", Desc: true}, false, 0); strings.Contains(got, "↓") {
		t.Errorf("a mark for the whole view marked a page's table:\n%s", got)
	}
}

// A narrow screen gives up optional columns, and never the one the rows are
// sorted by: given up, the rows were in an order nobody could see.
func TestTheSortedColumnIsNeverGivenUp(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Path"}, {Name: "Size", Kind: view.KindBytes, Priority: 1}}}
	tbl.Add(view.Plain("a"), view.Plain(strings.Repeat("p", 40)), view.Bytes(int64(10)))
	if plain := renderSorted(t, tbl, SortMark{}, false, 40); strings.Contains(plain, "SIZE") {
		t.Fatalf("at 40 columns the optional Size column was kept, so this proves nothing:\n%s", plain)
	}
	if got := renderSorted(t, tbl, SortMark{Column: "Size", Desc: true}, false, 40); !strings.Contains(got, "SIZE ↓") {
		t.Errorf("the sorted column was given up at 40 columns:\n%s", got)
	}
}
