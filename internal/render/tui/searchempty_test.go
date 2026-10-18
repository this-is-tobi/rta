package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The first `/` of a newcomer opened a box holding a prompt and a border that
// says what the keys do, and nothing about what to type: the box matches nothing
// for an empty query on purpose (rankItems), and the line that answered every
// other state had no words for this one. While the query is empty the box says
// three things to try and one shape of answer.
func TestAnEmptySearchBoxSaysWhatToTypeAtEveryWidthItIsDrawnAt(t *testing.T) {
	for _, w := range []int{132, 100, 80, 60, 44} {
		m, _ := realModel(t, w, 30)
		m.searchEditing = true
		box := ansi.Strip(m.renderSearchTile(w, true))
		lines := strings.Split(box, "\n")
		if !strings.Contains(box, "try: password · dns · ") || !strings.Contains(box, "sys.cpu") {
			t.Errorf("%d wide: the empty box says nothing about what to type:\n%s", w, box)
		}
		if len(lines) != m.searchHeight() {
			t.Errorf("%d wide: the box is %d lines, the layout under it measures %d", w, len(lines), m.searchHeight())
		}
		for _, line := range lines {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("%d wide: a line of the box is %d cells:\n%s", w, got, line)
			}
		}
	}
	m, _ := realModel(t, 100, 30)
	m.searchEditing = true
	if box := ansi.Strip(m.renderSearchTile(100, true)); !strings.Contains(box, "or an ID such as sys.cpu") {
		t.Errorf("the whole line does not show where there is room for it:\n%s", box)
	}
}

// Once there is a query the line has done its work: the matches take its place,
// and a query that matches nothing says so, not what to try.
func TestTheHintToTypeGivesWayToTheMatchesAndToNoMatches(t *testing.T) {
	m, _ := realModel(t, 100, 30)
	m.searchEditing = true
	for query, want := range map[string]string{"sys": "sys.", "zzzq": "no matches"} {
		m.query = query
		box := ansi.Strip(m.renderSearchTile(100, true))
		if strings.Contains(box, "try:") {
			t.Errorf("%q: the box still suggests what to type:\n%s", query, box)
		}
		if !strings.Contains(box, want) {
			t.Errorf("%q: the box lacks %q:\n%s", query, want, box)
		}
	}
}
