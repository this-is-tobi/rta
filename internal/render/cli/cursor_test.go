package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// view.Table.Page was in the contract, crossed the wire through pkg/sdk/wire,
// and was read by no surface — so a listing bounded at its limit came back
// looking exactly like a complete one.
//
// Same defect the csv branch already records for Total, one field along: a
// source of a thousand objects answering with two hundred is not wrong,
// answering with two hundred and saying nothing is. The continuation value is
// shown rather than merely flagged, because it is the argument somebody types
// next.

func bounded() view.Table {
	return view.Table{
		Columns: []view.Column{{Name: "key"}},
		Rows:    [][]string{{"obj-0001"}, {"obj-0002"}},
		Total:   2,
		Page:    &view.Cursor{Next: "obj-0002"},
	}
}

func TestPrettySaysAListingContinues(t *testing.T) {
	var out bytes.Buffer
	if err := Render(&out, bounded(), Options{Format: Pretty, NoColor: true, Width: 80}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "more rows after obj-0002") {
		t.Errorf("a bounded listing rendered as a complete one:\n%s", out.String())
	}
}

func TestCSVReportsAContinuationOnTheNotesChannel(t *testing.T) {
	var out, notes bytes.Buffer
	if err := Render(&out, bounded(), Options{Format: CSV, Notes: &notes}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notes.String(), "more rows after obj-0002") {
		t.Errorf("notes = %q, want the continuation reported", notes.String())
	}
	// The body stays pure csv, the same rule the row-count note follows.
	if strings.Contains(out.String(), "#") {
		t.Errorf("the note leaked into the csv body:\n%s", out.String())
	}
}

// A complete answer says nothing about continuing: a cursor on a finished
// listing sends somebody looking for data that is not there.
func TestACompleteListingSaysNothingAboutContinuing(t *testing.T) {
	whole := bounded()
	whole.Page = nil
	var out bytes.Buffer
	if err := Render(&out, whole, Options{Format: Pretty, NoColor: true, Width: 80}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "more rows") {
		t.Errorf("a complete listing claims there is more:\n%s", out.String())
	}
}

// Both facts fit on the one footer line: how much of the whole this is, and
// where to pick it up.
func TestTheRowCountAndTheContinuationShareTheFooter(t *testing.T) {
	page := bounded()
	page.Total = 744
	var out bytes.Buffer
	if err := Render(&out, page, Options{Format: Pretty, NoColor: true, Width: 80}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "2 of 744 rows") || !strings.Contains(body, "more rows after obj-0002") {
		t.Errorf("the footer dropped one of the two facts:\n%s", body)
	}
}
