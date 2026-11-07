package tui

import (
	"context"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// moreModel is a model on the first two of twelve rows a process list counted,
// from a capability whose limit goes to five, as sys.ps declares one; asked
// records the limit of every run.
func moreModel(t *testing.T) (Model, *[]int) {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	asked := &[]int{}
	c := plugin.Capability{ID: "demo.ps", Summary: "processes", Safety: plugin.Read, Idempotent: true,
		Inputs: []plugin.Field{{Name: "limit", Type: plugin.Int, Default: 2, Min: 1, Max: 5, Help: "rows"}},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			*asked = append(*asked, req.Int("limit"))
			return processes(req.Int("limit")), nil
		}}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	sized, _ := New(reg, config.Dashboard{}, nil).Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m := sized.(Model)
	m.current, m.lastValues = c, map[string]any{"limit": 2}
	shown, _ := m.Update(resultMsg{cap: c, view: processes(2)})
	return shown.(Model), asked
}

// processes is the first n of twelve rows.
func processes(n int) view.Table {
	t := view.Table{Columns: []view.Column{{Name: "PID"}}, Total: 12}
	for i := 1; i <= n; i++ {
		t.Rows = append(t.Rows, []string{strconv.Itoa(i)})
	}
	return t
}

// "2 of 12 rows" with an empty pane below was answered only by e, the limit
// box and a number typed. M runs it again with twice the rows, up to what the
// limit allows, and is not offered once the table is whole or the limit at its
// most.
func TestMShowsMoreRowsOfACutTable(t *testing.T) {
	m, asked := moreModel(t)
	if bar := plain(m.footerFor(m.mode)); !strings.Contains(bar, "M more rows") {
		t.Fatalf("a cut table's bar does not offer more rows:\n%s", bar)
	}
	m = pump(t, m, keyMsg("M"))
	m = pump(t, m, keyMsg("M"))
	if got := *asked; len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Errorf("M ran with limits %v, want 4 then the limit's most, 5", got)
	}
	if _, offered := m.moreRows(); offered {
		t.Error("more rows is offered with the limit at its most")
	}
	if bar := plain(m.footerFor(m.mode)); strings.Contains(bar, "more rows") {
		t.Errorf("the bar offers more rows it cannot give:\n%s", bar)
	}
}
