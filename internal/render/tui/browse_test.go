package tui

import "testing"

// The permission column survives an eighty-column terminal. It used to be
// the first column dropped when the summary fell under a full sentence, and
// at 80 columns — the width the catalogue is most often opened at — that
// removed the one column the pane exists for: which capabilities an agent
// may call without a grant, and which it may never call.
func TestThePermissionColumnSurvivesEightyColumns(t *testing.T) {
	m, _ := realModel(t, 80, 24)
	_, perm, summary := m.cols.columns(78)
	if perm == 0 {
		t.Fatalf("the permission column is dropped at 80 columns (summary would be %d)", summary)
	}
	if summary < minSummary {
		t.Errorf("summary = %d cells with the permission column kept, below the %d floor", summary, minSummary)
	}
	if _, perm, _ := m.cols.columns(40); perm != 0 {
		t.Error("at 40 columns the permission column still has to give way to the id")
	}
}

// Typing a filter puts the cursor on the best match, so filter-then-enter
// runs the first row shown. The catalogue opens with its cursor on index 1,
// under the first section header, and bubbles leaves the cursor where it is
// while the matches change under it — headers drop out of a filtered list,
// so index 1 became the second match and enter ran a different capability
// than the one the filter ranked first.
func TestFilteringPutsTheCursorOnTheFirstMatch(t *testing.T) {
	m := filterFor(t, browsing(t), "demo")
	caps := visibleCaps(m)
	if len(caps) < 2 {
		t.Fatalf("the fixture needs two matches to tell the first from the second, got %v", caps)
	}
	sel, ok := m.list.SelectedItem().(capItem)
	if !ok || sel.c.ID != caps[0] {
		t.Errorf("the cursor is on %v after filtering, want the first match %s", m.list.SelectedItem(), caps[0])
	}
}
