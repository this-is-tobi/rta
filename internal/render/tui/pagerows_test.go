package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A roster with a role in force is still one table, so its rows are walked and
// x and n are offered on them the same as when no role was ever issued.
func TestTheRowActionsWorkOnARosterLedByARole(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Chdir(t.TempDir())
	reg := realRegistry(t)
	now := time.Now()
	for _, g := range []grant.Grant{
		{Target: "kv.get", Scope: "db", Agent: "claude", Role: "dev"},
		{Target: "kv.get", Scope: "api", Agent: "claude"},
	} {
		g.Issued, g.Expires, g.TTL = now, now.Add(15*time.Minute), "15m"
		if verr := grant.Issue(g, true); verr != nil {
			t.Fatal(verr)
		}
	}
	list, _ := reg.Capability("grant.list")
	v, err := list.Run(context.Background(), plugin.NewRequest(nil, false, true).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("grant.list with a role in force answered %s, want one table", view.TypeOf(v))
	}
	want := -1
	for i, row := range tbl.Rows {
		if record, _ := cellNamed(tbl, row, "Record"); record == "api" {
			want = i
		}
	}
	if want < 0 {
		t.Fatalf("no row of the roster names the grant on api: %+v", tbl)
	}

	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	withRes, _ := sized.(Model).Update(resultMsg{cap: list, view: v})
	rm := withRes.(Model)
	if !rm.interactive() {
		t.Fatal("the roster's page is not a list to walk")
	}
	offered := map[string]bool{}
	for _, it := range rm.resultFooterItems() {
		offered[it.label] = true
	}
	if !offered["renew"] || !offered["revoke"] {
		t.Errorf("the roster's page offers %v, want renew and revoke", offered)
	}
	if meta := rm.resultMeta(); !strings.Contains(meta, "row 1/2") {
		t.Errorf("the meta line says %q, want the row under the cursor counted", meta)
	}

	for range want {
		next, _ := rm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		rm = next.(Model)
	}
	acted, _ := rm.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	am := acted.(Model)
	if am.form == nil || am.current.ID != "grant.revoke" {
		t.Fatalf("x on the page: current=%s form=%v, want grant.revoke's form", am.current.ID, am.form != nil)
	}
	if got := am.form.values(); got["target"] != "kv.get" || got["scope"] != "api" || got["agent"] != "claude" {
		t.Errorf("x on the grant on api revokes %#v, want that grant", got)
	}
}

// Only a page with one table and no other is walked: the row under the
// cursor is one row of one table, and the highlight a render draws reaches
// every table on the page.
func TestAPageWithMoreThanOneTableIsNotWalked(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "id"}}, Rows: [][]string{{"1"}}}
	note := view.Text{Body: "said around it"}
	for _, c := range []struct {
		name string
		page view.View
		want int
	}{
		{"a table", tbl, -1},
		{"a table between texts", view.Sections{Items: []view.Section{{View: note}, {View: tbl}, {View: note}}}, 1},
		{"two tables", view.Sections{Items: []view.Section{{View: tbl}, {View: tbl}}}, -2},
		{"a table and one nested", view.Sections{Items: []view.Section{{View: tbl},
			{View: view.Sections{Items: []view.Section{{View: tbl}}}}}}, -2},
		{"only text", view.Sections{Items: []view.Section{{View: note}}}, -2},
	} {
		_, at, ok := rowTable(c.page)
		if got := map[bool]int{true: at, false: -2}[ok]; got != c.want {
			t.Errorf("%s: at %d (ok %v), want %d", c.name, at, ok, c.want)
		}
	}
}

// The row under the cursor stays on screen below whatever the page draws
// above its table. The pane scrolled by the row's place in a bare table, so
// on a page with a tall section above the table, walking down the rows
// scrolled the pane short of them and the selected row went off its foot.
func TestTheSelectedRowOfAPageStaysInView(t *testing.T) {
	rows := make([][]string, 12)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("row-%02d", i)}
	}
	lead := strings.Repeat("a line said above the table\n", 24)
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "demo.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Actions: []plugin.Action{{Key: "x", Label: "remove", Target: "demo.rm", Source: plugin.ActionRow}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Sections{Items: []view.Section{
						{ID: "lead", Title: "Lead", View: view.Text{Body: lead}},
						{ID: "rows", Title: "Rows", View: view.Table{Columns: []view.Column{{Name: "name"}}, Rows: rows}},
					}}, nil
				}},
			{ID: "demo.rm", Summary: "remove", Safety: plugin.Write, Idempotent: true,
				Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Positional: true, Required: true, Help: "n"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "removed"}, nil
				}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Capability("demo.list")
	v, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	withRes, _ := sized.(Model).Update(resultMsg{cap: c, view: v})
	rm := withRes.(Model)
	// The page opens at its top, as every result does, and what it says
	// there leads the rows; from the first key on, the cursor's row is
	// kept in view, down to the last and back up to the first.
	walk := []string{}
	for range len(rows) - 1 {
		walk = append(walk, "down")
	}
	for range len(rows) - 1 {
		walk = append(walk, "up")
	}
	for _, key := range walk {
		code := tea.KeyDown
		if key == "up" {
			code = tea.KeyUp
		}
		next, _ := rm.Update(tea.KeyPressMsg{Code: code})
		rm = next.(Model)
		name := rows[rm.row][0]
		at := -1
		for n, line := range strings.Split(rm.viewport.GetContent(), "\n") {
			if strings.Contains(line, name) {
				at = n
			}
		}
		top := rm.viewport.YOffset()
		if at < top || at >= top+rm.viewport.Height() {
			t.Fatalf("%s: %s is drawn on line %d, and the pane shows lines %d to %d",
				key, name, at, top, top+rm.viewport.Height()-1)
		}
	}
}
