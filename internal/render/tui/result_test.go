package tui

import (
	"strings"
	"testing"

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
