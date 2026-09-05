package cli

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/view"
)

// usageTable is one row per band, so a single render exercises all three.
func usageTable(kind view.ColumnKind) view.Table {
	return view.Table{
		Columns: []view.Column{{Name: "claim"}, {Name: "used %", Kind: kind}},
		Rows: [][]string{
			{"data-pg-0", "31%"},
			{"data-pg-1", "84%"},
			{"data-pg-2", "96%"},
		},
	}
}

func renderColoured(t *testing.T, v view.View, width int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Width: width}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// The colour is on the number itself: there is no room for a status column
// beside six percentages in kube.metrics.pod, which is the case that made a
// gradeable percentage worth a kind of its own.
func TestAFullVolumeIsColouredAndAnEmptyOneIsNot(t *testing.T) {
	out := renderColoured(t, usageTable(view.KindUsage), 100)
	// Against the palette's own styles rather than against UsageStyle, which
	// is what the renderer just called: comparing a cell to the classifier
	// that produced it asserts only that the renderer called *something*, and
	// a band that regressed would move both sides together.
	for _, tc := range []struct {
		cell  string
		style lipgloss.Style
	}{
		{"31%", theme.GoodText},
		{"84%", theme.WarnText},
		{"96%", theme.BadText},
	} {
		if want := tc.style.Render(tc.cell); !strings.Contains(out, want) {
			t.Errorf("%s is not rendered in the band it belongs to", tc.cell)
		}
	}
	// The three bands are three different things, which is the assertion that
	// would still pass if every one of them resolved to Plain.
	if theme.GoodText.Render("x") == theme.BadText.Render("x") {
		t.Fatal("comfortable and full render identically")
	}
}

// A percentage with no bad end must not be graded, and the kind is the only
// thing separating the two — the cells are the same strings.
func TestAPlainPercentageIsLeftAlone(t *testing.T) {
	plain := renderColoured(t, usageTable(view.KindPercent), 100)
	graded := renderColoured(t, usageTable(view.KindUsage), 100)
	if plain == graded {
		t.Fatal("KindPercent and KindUsage render identically")
	}
	if strings.Contains(plain, theme.BadText.Render("96%")) {
		t.Error("a KindPercent cell was graded")
	}
}

// A narrow terminal drops the grid for records, and that layout is where a
// full disk is easiest to scroll past — so it is the one that can least
// afford to lose the grading, not the one it is acceptable to lose it in.
func TestTheRecordLayoutKeepsTheGrading(t *testing.T) {
	wide := usageTable(view.KindUsage)
	wide.Columns = append(wide.Columns, view.Column{Name: "storage class"})
	for i := range wide.Rows {
		wide.Rows[i] = append(wide.Rows[i], "longhorn-retain-replicated")
	}
	out := renderColoured(t, wide, 30)
	if strings.Contains(out, "╭") {
		t.Fatalf("still a grid at 30 cells:\n%s", out)
	}
	if !strings.Contains(out, theme.BadText.Render("96%")) {
		t.Errorf("the full volume lost its colour in the record layout:\n%s", out)
	}
}

// --no-color and a pipe are the same request, and the number has to stay
// legible under both: the grading is an addition to the cell, never a
// substitution for its text.
func TestWithoutColourTheNumberIsStillThere(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, usageTable(view.KindUsage), Options{NoColor: true, Width: 100}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, cell := range []string{"31%", "84%", "96%"} {
		if !strings.Contains(out, cell) {
			t.Errorf("%s is missing from the uncoloured render", cell)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("NoColor still emitted an escape sequence")
	}
}

// A graded column is still a number, so it right-aligns like one. It is the
// only kind that is both, and the switch that added the grading is the same
// switch that decides alignment.
func TestAGradedColumnStillRightAligns(t *testing.T) {
	out := renderColoured(t, usageTable(view.KindUsage), 100)
	var ends []int
	for _, line := range strings.Split(out, "\n") {
		plain := ansi.Strip(line)
		// The heading is "USED %" and carries one too, which is the sign
		// itself rather than a value under it.
		i := strings.LastIndex(plain, "%")
		if i <= 0 || plain[i-1] < '0' || plain[i-1] > '9' {
			continue
		}
		ends = append(ends, lipgloss.Width(plain[:i]))
	}
	if len(ends) != 3 {
		t.Fatalf("found %d percentage cells, want 3:\n%s", len(ends), out)
	}
	for _, e := range ends[1:] {
		if e != ends[0] {
			t.Fatalf("percentages do not line up: %v", ends)
		}
	}
}
