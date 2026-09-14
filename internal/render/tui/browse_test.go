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
