package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// At 80 columns the catalogue gave the summary seventeen cells, and the column
// that says what a capability does read "Namespaces with …": the permission
// column is sized by the longest value of the whole catalogue, and the ID column
// by its longest ID, so the one column that is cut is the one a person reads to
// choose. The row under the cursor says it in full on a line of its own above the
// bar, with what an agent needs for it said out as a sentence.

// catalogueAt is the catalogue, on its first page, in a terminal of the size.
func catalogueAt(t *testing.T, width, height int) (Model, *registry.Registry) {
	t.Helper()
	m, reg := realModel(t, width, height)
	m = press(t, m, "b")
	if m.mode != modeBrowse {
		t.Fatalf("b opened %v, want the catalogue", m.mode)
	}
	return m, reg
}

// selecting puts the cursor on the capability with the ID, wherever the list has
// it, and returns the model that draws that.
func selecting(t *testing.T, m Model, id string) Model {
	t.Helper()
	for i, it := range m.list.Items() {
		if c, ok := it.(capItem); ok && c.c.ID == id {
			m.list.Select(i)
			return m
		}
	}
	t.Fatalf("the catalogue has no %s", id)
	return m
}

// lineAboveTheBar is the last line before the hint bar of the catalogue's frame.
func lineAboveTheBar(m Model) string {
	frame := strings.Split(strings.TrimRight(plain(m.browseView()), "\n"), "\n")
	return frame[len(frame)-lipgloss.Height(m.browseFooter())-1]
}

func TestTheCatalogueSaysTheSelectedRowInFullAboveTheBar(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}} {
		width, height := size[0], size[1]
		m, reg := catalogueAt(t, width, height)
		for _, id := range []string{"audit.kube.netpol", "net.dns", "sys.cpu"} {
			c := mustCap(t, reg, id)
			line := lineAboveTheBar(selecting(t, m, id))
			if got := lipgloss.Width(line); got > width {
				t.Errorf("%dx%d: the line for %s is %d cells wide: %q", width, height, id, got, line)
			}
			// The summary in full where it fits, and cut with the ellipsis where it
			// does not, which is never before the width.
			want := strings.Repeat(" ", marker) + c.Summary
			if lipgloss.Width(want) > width {
				want = lineCut(want, width)
			}
			if !strings.HasPrefix(line, strings.TrimSuffix(want, "…")) {
				t.Errorf("%dx%d: the line for %s is %q, want it to open on %q", width, height, id, line, want)
			}
		}
	}
}

// lineCut is s cut to width cells with the ellipsis, as the line is.
func lineCut(s string, width int) string {
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)+"…") > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// The sentence is the docs' own, said for the row: what an agent can do with it.
func TestThePermissionSentenceSaysWhatAnAgentCanDoWithTheRow(t *testing.T) {
	for _, tc := range []struct {
		c    plugin.Capability
		want string
	}{
		{plugin.Capability{ID: "a.b", Safety: plugin.Read}, "read · an agent can call it at once"},
		{plugin.Capability{ID: "a.b", Safety: plugin.Write, NeedsGrant: true}, "write · an agent needs a grant you issue first"},
		{plugin.Capability{ID: "a.b", Safety: plugin.Destructive}, "destructive · an agent needs a grant you issue first"},
		{plugin.Capability{ID: "a.b", Safety: plugin.Write, HumanOnly: true}, "write · never for an agent — yours alone"},
	} {
		if got := permissionSentence(tc.c); got != tc.want {
			t.Errorf("permissionSentence(%s %s) = %q, want %q", tc.c.ID, tc.c.Safety, got, tc.want)
		}
	}
}

// Both halves are on the line when there is room for them, in the order the
// columns have, summary and then permission.
func TestTheLineAboveTheBarCarriesThePermissionSentenceToo(t *testing.T) {
	m, _ := catalogueAt(t, 160, 40)
	for id, want := range map[string]string{
		"sys.cpu":  "read · an agent can call it at once",
		"kv.rm":    "an agent needs a grant you issue first",
		"lock.add": "never for an agent — yours alone",
	} {
		if line := lineAboveTheBar(selecting(t, m, id)); !strings.Contains(line, want) {
			t.Errorf("the line for %s is %q, want the sentence %q", id, line, want)
		}
	}
}

// A sentence cut in two says less than the column beside it: the permission is
// on the line whole, in the sentence or in the column's own words, or it is not
// on it, and the summary has the line either way.
func TestThePermissionOnTheLineIsNeverCutInTwo(t *testing.T) {
	for _, width := range []int{44, 60, 80, 100, 120, 160} {
		m, reg := catalogueAt(t, width, 40)
		said := map[string]int{}
		for _, id := range []string{"audit.kube.netpol", "net.dns", "sys.cpu", "kv.rm", "lock.add", "gen.uuid"} {
			c := mustCap(t, reg, id)
			line := strings.TrimRight(lineAboveTheBar(selecting(t, m, id)), " ")
			summary, tail, joined := strings.Cut(line, "  ·  ")
			if !strings.HasPrefix(c.Summary, strings.TrimSuffix(strings.TrimPrefix(summary, strings.Repeat(" ", marker)), "…")) {
				t.Errorf("width %d: the line for %s opens %q, not on its summary %q", width, id, summary, c.Summary)
			}
			switch {
			case !joined:
				said["none"]++
			case tail == permissionSentence(c):
				said["sentence"]++
			case tail == permissionText(c):
				said["column's words"]++
			default:
				t.Errorf("width %d: the line for %s ends %q, neither the sentence %q nor the column's words %q",
					width, id, tail, permissionSentence(c), permissionText(c))
			}
		}
		// The three are all reached: wide enough for the sentence, for the words,
		// and for neither.
		if width == 160 && said["sentence"] == 0 || width == 80 && said["none"] == 0 {
			t.Errorf("width %d: the lines said %v", width, said)
		}
	}
}

// The row is reserved, not conjured: the frame is the terminal's height with it,
// the list is one row shorter than it was, and a cursor on nothing — a filter
// that found no match — leaves the row blank and the frame where it was.
func TestTheLineTakesOneRowFromTheListAndTheFrameKeepsItsHeight(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {120, 40}} {
		width, height := size[0], size[1]
		m, _ := catalogueAt(t, width, height)
		if got := lipgloss.Height(m.browseView()); got != height {
			t.Errorf("%dx%d: the catalogue is %d lines tall", width, height, got)
		}
		if want := height - 1 - lipgloss.Height(m.browseFooter()) - 1; m.list.Height() != want {
			t.Errorf("%dx%d: the list is %d rows, want %d: the window less the header, the bar and the line", width, height, m.list.Height(), want)
		}
		nothing := filterFor(t, m, "zzzznope")
		if got := lipgloss.Height(nothing.browseView()); got != height {
			t.Errorf("%dx%d: with no match the catalogue is %d lines tall", width, height, got)
		}
		if line := strings.TrimSpace(lineAboveTheBar(nothing)); line != "" {
			t.Errorf("%dx%d: the line above the bar says %q over a cursor on nothing", width, height, line)
		}
	}
}

// Where the terminal is short the list keeps its rows and the line goes: the row
// is what a person reads to choose, but the rows are what there is to choose
// from, and at 44x14 there are eleven of them.
func TestTheLineGivesWayBeforeTheListDoes(t *testing.T) {
	m, _ := catalogueAt(t, 44, 14)
	if got := lipgloss.Height(m.browseView()); got != 14 {
		t.Errorf("44x14: the catalogue is %d lines tall", got)
	}
	if want := 14 - 1 - lipgloss.Height(m.browseFooter()); m.list.Height() != want {
		t.Errorf("44x14: the list is %d rows, want all %d the window leaves it", m.list.Height(), want)
	}
	if line := lineAboveTheBar(m); strings.Contains(line, "an agent") {
		t.Errorf("44x14: the line is drawn although the list has no room to spare: %q", line)
	}
	// And the same terminal, taller by the rows the line needs, draws it.
	taller, _ := catalogueAt(t, 44, 14+minCatalogueRows)
	if line := lineAboveTheBar(taller); strings.TrimSpace(line) == "" {
		t.Errorf("44x%d: there is room for the line and it is blank", 14+minCatalogueRows)
	}
}

// The window and the list agree at every size: the list is sized after every
// message from the same arithmetic the frame is drawn with, or the frame is a
// line taller than the terminal and the renderer cuts the bar off.
func TestTheCatalogueFitsTheTerminalAtEverySizeWithTheLine(t *testing.T) {
	for _, height := range []int{8, 12, 14, 16, 20, 26} {
		for _, width := range []int{44, 100} {
			m, _ := catalogueAt(t, width, height)
			if got := lipgloss.Height(plain(m.View().Content)); got > height {
				t.Errorf("%dx%d: the catalogue is %d lines tall", width, height, got)
			}
			if rows, _ := m.catalogueRows(m.browseFooter()); m.list.Height() != rows {
				t.Errorf("%dx%d: the list is %d rows and the frame budgets %d", width, height, m.list.Height(), rows)
			}
		}
	}
}
