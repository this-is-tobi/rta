package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The line under a result counts what came back, and a one-row table is as
// ordinary a result as there is: it read "1 of 1 rows".
func TestTheResultLineCountsInTheRightNumber(t *testing.T) {
	c := plugin.Capability{ID: "demo.list", Safety: plugin.Read}
	for rows, want := range map[int]string{1: "1 of 1 row", 3: "3 of 3 rows"} {
		table := view.Table{Columns: []view.Column{{Name: "Name"}}}
		for range rows {
			table.Rows = append(table.Rows, []string{"x"})
		}
		m := Model{current: c, result: resultMsg{cap: c, view: table}}
		got := plain(m.resultMeta())
		if !strings.Contains(got, want) || (rows == 1 && strings.Contains(got, "rows")) {
			t.Errorf("%d rows: meta = %q, want %q", rows, got, want)
		}
	}
}

// An empty listing's pane says what would fill it, and the line above it
// does not count the nothing it replaces: "0 of 0 rows" over "nothing is
// locked" said the same thing twice, the second time as a figure.
//
// A table with no sentence keeps its count, since there the headings are
// drawn and the count is what says nothing came back.
func TestAnEmptyListingIsASentenceNotACount(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = sized.(Model)
	c := plugin.Capability{ID: "demo.list", Safety: plugin.Read}
	table := view.Table{Columns: []view.Column{{Name: "Name"}}, Empty: "nothing is locked"}
	m.mode, m.current = modeResult, c
	m.result = resultMsg{cap: c, view: table, raw: table}
	m.renderResult()
	pane := plain(m.viewport.View())
	if !strings.Contains(pane, "nothing is locked") || strings.Contains(pane, "NAME") {
		t.Errorf("pane = %q, want the sentence in place of the headings", pane)
	}
	if meta := plain(m.resultMeta()); strings.Contains(meta, "row") {
		t.Errorf("meta = %q, want no count above the sentence", meta)
	}
	table.Empty = ""
	m.result = resultMsg{cap: c, view: table, raw: table}
	if meta := plain(m.resultMeta()); !strings.Contains(meta, "0 of 0 rows") {
		t.Errorf("meta = %q, want the count over an empty grid", meta)
	}
}

// A page with no sections says what would fill it in the pane, and the line
// above it lists no titles: an empty list of them left a separator pointing
// at nothing.
func TestAnEmptyPageIsASentenceNotAnEmptyContents(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = sized.(Model)
	c := plugin.Capability{ID: "demo.fix", Safety: plugin.Read}
	page := view.Sections{Empty: "nothing to paste"}
	m.mode, m.current = modeResult, c
	m.result = resultMsg{cap: c, view: page, raw: page}
	m.renderResult()
	if pane := plain(m.viewport.View()); !strings.Contains(pane, "nothing to paste") {
		t.Errorf("pane = %q, want the sentence", pane)
	}
	if meta := strings.TrimSpace(plain(m.resultMeta())); strings.HasSuffix(meta, "·") {
		t.Errorf("meta = %q, want no separator before an empty list of titles", meta)
	}
}
