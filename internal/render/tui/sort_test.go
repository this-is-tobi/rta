package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// firstColumn is the first cell of each row, in order.
func firstColumn(t view.Table) []string {
	out := make([]string, len(t.Rows))
	for i, row := range t.Rows {
		out[i] = row[0]
	}
	return out
}

// storeListing is a listing as a plugin fills one: a name, and a size valued in
// bytes, the last row's not measured.
func storeListing() view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}}}
	t.Add(view.Plain("small"), view.Bytes(int64(512)))
	t.Add(view.Plain("unmeasured"), view.Plain("-"))
	t.Add(view.Plain("huge"), view.Bytes(int64(10<<30)))
	t.Add(view.Plain("mid"), view.Bytes(int64(9<<10)))
	return t
}

// A size sorts by its bytes and not by its text, either way, with the cell
// nobody measured last both ways; the values move with their rows.
func TestASizeSortsByItsBytesAndTheUnmeasuredGoLast(t *testing.T) {
	for _, tc := range []struct {
		desc bool
		want []string
	}{
		{true, []string{"huge", "mid", "small", "unmeasured"}},
		{false, []string{"small", "mid", "huge", "unmeasured"}},
	} {
		got, perm := sortTable(storeListing(), 1, tc.desc)
		if !slices.Equal(firstColumn(got), tc.want) {
			t.Errorf("desc=%v: %v, want %v", tc.desc, firstColumn(got), tc.want)
		}
		for i, p := range perm {
			if got.Rows[i][0] != storeListing().Rows[p][0] {
				t.Errorf("desc=%v: perm[%d] = %d names %q, the row shown is %q", tc.desc, i, p, storeListing().Rows[p][0], got.Rows[i][0])
			}
		}
		if got.Values[0] == nil || got.Values[0][1] != storeListing().Values[perm[0]][1] {
			t.Errorf("desc=%v: the first row's value %q is not its own", tc.desc, got.Values[0])
		}
	}
}

// Names sort the way a person reads them, numbers by the number they spell
// and letters without regard to case; a status by how bad it is, worst first;
// a time by its moment whatever zone it is written in. Rows that tie keep the
// order they came in.
func TestEachKindSortsByWhatItsCellsAre(t *testing.T) {
	names := view.Table{Columns: []view.Column{{Name: "Pod"}}, Rows: [][]string{{"pod-10"}, {"pod-2"}, {"Pod-1"}, {"pod-2"}}}
	if got := firstColumn(first(sortTable(names, 0, false))); !slices.Equal(got, []string{"Pod-1", "pod-2", "pod-2", "pod-10"}) {
		t.Errorf("names: %v", got)
	}

	statuses := view.Table{Columns: []view.Column{{Name: "State", Kind: view.KindStatus}},
		Rows: [][]string{{"ok"}, {"done"}, {"Running"}, {"warn — lagging"}, {"fail — down"}, {"-"}}}
	if got := firstColumn(first(sortTable(statuses, 0, false))); !slices.Equal(got, []string{"fail — down", "warn — lagging", "Running", "ok", "done", "-"}) {
		t.Errorf("statuses worst first: %v", got)
	}

	times := view.Table{Columns: []view.Column{{Name: "At", Kind: view.KindTimestamp}},
		Rows: [][]string{{"2026-03-04T06:00:00+01:00"}, {"2026-03-04T05:30:00Z"}, {"never"}, {"2026-03-04T04:00:00Z"}}}
	if got := firstColumn(first(sortTable(times, 0, true))); !slices.Equal(got, []string{"2026-03-04T05:30:00Z", "2026-03-04T06:00:00+01:00", "2026-03-04T04:00:00Z", "never"}) {
		t.Errorf("times newest first: %v", got)
	}
}

func first(t view.Table, _ []int) view.Table { return t }

// The picker offers each column both ways, the way a person wants it first
// leading, and never a masked column: sorting by it would say how the values
// nobody may see compare.
func TestTheOrdersOfferedAreEveryColumnButAMaskedOne(t *testing.T) {
	tbl := storeListing()
	tbl.Columns = append(tbl.Columns, view.Column{Name: "Token"})
	tbl.Redacted = []string{"Token"}
	var labels []string
	for _, o := range orderOptions(tbl) {
		labels = append(labels, o.label)
	}
	want := []string{"as returned", "Name — A to Z", "Name — Z to A", "Size — largest first", "Size — smallest first"}
	if !slices.Equal(labels, want) {
		t.Errorf("orders offered: %q, want %q", labels, want)
	}
	if _, _, ok := ordered(tbl, tableOrder{column: "Token"}); ok {
		t.Error("a masked column was sorted by")
	}
}

func sortCap() plugin.Capability {
	return plugin.Capability{
		ID: "store.list", Summary: "list what is stored", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return storeListing(), nil },
	}
}

// S opens the orders over a result, the order chosen is the table's, its line
// says so, and it stays across a refresh of the same capability; "as returned"
// puts the rows back, and another capability's result starts as returned.
func TestSSortsAResultAndTheOrderOutlivesARefresh(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	shown, _ := sized.(Model).Update(resultMsg{cap: sortCap(), view: storeListing()})
	if !strings.Contains(plain(shown.(Model).footerFor(modeResult)), "S sort") {
		t.Errorf("the bar does not offer the sort: %q", plain(shown.(Model).footerFor(modeResult)))
	}
	opened, _ := shown.(Model).Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	om := opened.(Model)
	if om.mode != modePick || om.pick.kind != pickSort || om.pick.value != "" {
		t.Fatalf("S did not open the sort picker on the order the result is in: mode %v, picker %+v", om.mode, om.pick)
	}

	om.pick.value = tableOrder{column: "Size", desc: true}.pickValue()
	next, _ := om.confirmPick()
	sorted := next.(Model)
	tbl, _, _ := rowTable(sorted.result.raw)
	if sorted.mode != modeResult || !slices.Equal(firstColumn(tbl), []string{"huge", "mid", "small", "unmeasured"}) {
		t.Fatalf("after choosing Size largest first: mode %v, rows %v", sorted.mode, firstColumn(tbl))
	}
	if shownTbl, _, _ := rowTable(sorted.result.view); !slices.Equal(firstColumn(shownTbl), firstColumn(tbl)) {
		t.Errorf("the screen's copy is in another order: %v", firstColumn(shownTbl))
	}
	if meta := plain(sorted.resultMeta()); !strings.Contains(meta, "sorted by Size, largest first") {
		t.Errorf("the line does not say how the table is sorted: %q", meta)
	}

	refreshed, _ := sorted.Update(resultMsg{cap: sortCap(), view: storeListing()})
	if tbl, _, _ := rowTable(refreshed.(Model).result.raw); !slices.Equal(firstColumn(tbl), []string{"huge", "mid", "small", "unmeasured"}) {
		t.Errorf("a refresh came back as returned: %v", firstColumn(tbl))
	}

	back := refreshed.(Model).sortResult(tableOrder{})
	if tbl, _, _ := rowTable(back.result.raw); !slices.Equal(firstColumn(tbl), firstColumn(storeListing())) {
		t.Errorf("as returned: %v, want %v", firstColumn(tbl), firstColumn(storeListing()))
	}

	other := sortCap()
	other.ID = "store.other"
	elsewhere, _ := sorted.Update(resultMsg{cap: other, view: storeListing()})
	if order := elsewhere.(Model).result.order; order != (tableOrder{}) {
		t.Errorf("another capability's result came in order %+v", order)
	}
}

// The cursor stays on the row it was on through every order, as returned
// included: the row an action acts on is the one the cursor shows, and a sort
// that left the cursor where it was would aim the next action at whatever row
// moved under it.
func TestTheCursorStaysOnItsRowWhenTheTableIsSorted(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	m.current = sortCap()
	m.result = resultMsg{cap: sortCap(), view: storeListing()}.cleaned()
	m.row = 0
	for _, o := range []tableOrder{{column: "Size", desc: true}, {column: "Name"}, {}} {
		m = m.sortResult(o)
		tbl, _, _ := rowTable(m.result.raw)
		if got := tbl.Rows[m.row][0]; got != "small" {
			t.Errorf("in order %+v the cursor is on %q, want the row it was on", o, got)
		}
	}
}

// cursorRow is the first cell of the row the cursor is on.
func cursorRow(m Model) string {
	tbl, _, _ := rowTable(m.result.view)
	return tbl.Rows[m.row][0]
}

// A refresh that sorts a new row above the cursor leaves the cursor on the row
// it was on, which the next key acts on: kept by index, each call newly parked
// on a sorted queue moved it to another call.
func TestARefreshKeepsTheCursorOnItsRowWhereTheSortMovedIt(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	m.current = sortCap()
	m.result = resultMsg{cap: sortCap(), view: storeListing()}.cleaned()
	m = m.sortResult(tableOrder{column: "Size", desc: true})
	m.row = 1
	if got := cursorRow(m); got != "mid" {
		t.Fatalf("the cursor starts on %q, want mid", got)
	}
	grown := storeListing()
	grown.Add(view.Plain("giant"), view.Bytes(int64(20<<30)))
	next, _ := m.Update(resultMsg{cap: sortCap(), view: grown})
	if got := cursorRow(next.(Model)); got != "mid" {
		t.Errorf("after a refresh that sorted giant above it the cursor is on %q, want mid", got)
	}
	gone := view.Table{Columns: storeListing().Columns}
	gone.Add(view.Plain("huge"), view.Bytes(int64(10<<30)))
	gone.Add(view.Plain("small"), view.Bytes(int64(512)))
	shrunk, _ := next.(Model).Update(resultMsg{cap: sortCap(), view: gone})
	if got := cursorRow(shrunk.(Model)); got != "small" {
		t.Errorf("with its row gone the cursor is on %q, want the row that took its place", got)
	}
}

// storeWithShow is a listing whose rows open a page of their own, so the
// listing is held on the trail while the page is on screen.
func storeWithShow(t *testing.T) (*registry.Registry, plugin.Capability, plugin.Capability) {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{Name: "store", Summary: "a store", Capabilities: []plugin.Capability{
		{ID: "store.list", Summary: "list what is stored", Safety: plugin.Read, Idempotent: true,
			Actions: []plugin.Action{{Key: "o", Label: "open", Target: "store.show", Source: plugin.ActionRow}},
			Run:     func(context.Context, plugin.Request) (view.View, error) { return storeListing(), nil }},
		{ID: "store.show", Summary: "show one", Safety: plugin.Read, Idempotent: true,
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Positional: true, Required: true, Help: "n"}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.KeyValue{Pairs: []view.Pair{{Key: "size", Value: "9.0 KiB"}}}, nil
			}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := reg.Capability("store.list")
	show, _ := reg.Capability("store.show")
	return reg, list, show
}

// Back from a row's page, the listing is in the order it was left in and the
// cursor on the row that was opened. It came back as returned with the cursor
// at the same index, on another row, and a done or a deny then acted on that.
func TestBackFromARowsPageTheListIsAsItWasLeft(t *testing.T) {
	reg, list, show := storeWithShow(t)
	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	shown, _ := sized.(Model).Update(resultMsg{cap: list, view: storeListing()})
	sorted := shown.(Model).sortResult(tableOrder{column: "Size", desc: true})
	sorted.row = 1
	if got := cursorRow(sorted); got != "mid" {
		t.Fatalf("the cursor starts on %q, want mid", got)
	}
	opened, _ := sorted.Update(resultMsg{cap: show, view: view.KeyValue{Pairs: []view.Pair{{Key: "size", Value: "9.0 KiB"}}}})
	back, _ := opened.(Model).Update(resultMsg{cap: list, view: storeListing()})
	bm := back.(Model)
	if bm.result.order != (tableOrder{column: "Size", desc: true}) {
		t.Errorf("back from the page the list is in order %+v, want Size largest first", bm.result.order)
	}
	if got := cursorRow(bm); got != "mid" {
		t.Errorf("back from the page the cursor is on %q, want mid", got)
	}
}

// logListing is a log as one reads: oldest first, the newest last.
func logListing() view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Call"}, {Name: "At", Kind: view.KindTimestamp}}, Tail: true}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i := range 10 {
		when := at.Add(time.Duration(i) * time.Minute)
		t.Add(view.Plain(fmt.Sprintf("call-%02d", i)), view.Time(when.Format("15:04"), when))
	}
	return t
}

// A log opens on its newest row and goes back to it on a refresh, in its own
// order. Sorted newest first, its last row is the oldest, and a refresh left
// the cursor there and the pane at the bottom.
func TestARefreshOfASortedLogKeepsItsRow(t *testing.T) {
	logCap := sortCap()
	logCap.ID = "store.log"
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	opened, _ := sized.(Model).Update(resultMsg{cap: logCap, view: logListing()})
	if got := cursorRow(opened.(Model)); got != "call-09" {
		t.Fatalf("a log opened on %q, want its newest row", got)
	}
	newest := opened.(Model).sortResult(tableOrder{column: "At", desc: true})
	if got := cursorRow(newest); got != "call-09" {
		t.Fatalf("sorted newest first the cursor is on %q, want call-09", got)
	}
	refreshed, _ := newest.Update(resultMsg{cap: logCap, view: logListing()})
	if rm := refreshed.(Model); cursorRow(rm) != "call-09" || rm.row != 0 {
		t.Errorf("a refresh of the sorted log put the cursor on %q (row %d), want call-09 on top", cursorRow(rm), rm.row)
	}
	own := refreshed.(Model).sortResult(tableOrder{})
	own.row = 3
	again, _ := own.Update(resultMsg{cap: logCap, view: logListing()})
	if got := cursorRow(again.(Model)); got != "call-09" {
		t.Errorf("in its own order a refresh put the cursor on %q, want the newest row", got)
	}
}

// Statuses of one grade keep the order they came in, either way, and what is
// done with goes last either way: broken by text, OVERDUE · Oct 20 came before
// OVERDUE · Sep 30, and "best first" opened on the done to-dos.
func TestAStatusSortKeepsTiesInOrderAndPutsWhatIsDoneLast(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Due", Kind: view.KindStatus}}, Rows: [][]string{
		{"OVERDUE · Sep 30"}, {"done"}, {"ok · Nov 30"}, {"OVERDUE · Oct 20"}, {"WARN soon · Nov 2"}, {"ok · Jan 5 2027"},
	}}
	for desc, want := range map[bool][]string{
		false: {"OVERDUE · Sep 30", "OVERDUE · Oct 20", "WARN soon · Nov 2", "ok · Nov 30", "ok · Jan 5 2027", "done"},
		true:  {"ok · Nov 30", "ok · Jan 5 2027", "WARN soon · Nov 2", "OVERDUE · Sep 30", "OVERDUE · Oct 20", "done"},
	} {
		got, _ := sortTable(tbl, 0, desc)
		if !slices.Equal(firstColumn(got), want) {
			t.Errorf("desc=%v: %v, want %v", desc, firstColumn(got), want)
		}
	}
}

// A column of a kind that sorts by value is not offered when no row has one: a
// table filled through Rows alone holds its sizes as text, and a sort by one
// moved nothing while the line said the table was sorted.
func TestAColumnWithNothingToSortByIsNotOffered(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Size", Kind: view.KindBytes}, {Name: "State", Kind: view.KindStatus}},
		Rows: [][]string{{"alpha", "1.5 GiB", "ok"}, {"beta", "300 MiB", "warn"}}}
	var labels []string
	for _, o := range orderOptions(tbl) {
		labels = append(labels, o.label)
	}
	if slices.ContainsFunc(labels, func(l string) bool { return strings.HasPrefix(l, "Size") }) ||
		!slices.ContainsFunc(labels, func(l string) bool { return strings.HasPrefix(l, "State") }) {
		t.Errorf("offered %q, want Name and State and not Size", labels)
	}
}

// A list stopped at its limit with nothing to count it is a page of a longer
// one when a warning says it goes on, as git log at its limit does: sorted
// oldest first it read as the repository's first commits. An advisory warning
// sits beside a whole list and says nothing is missing.
func TestASortedListCutAtItsLimitSaysItIsAPage(t *testing.T) {
	cut := view.Table{Columns: []view.Column{{Name: "Commit"}}, Rows: [][]string{{"a"}, {"b"}},
		Warnings: []view.Error{{Code: "git.log.older", Message: "older commits not shown"}}}
	if said := orderSaid(cut, tableOrder{column: "Commit"}); !strings.HasSuffix(said, ", on this page") {
		t.Errorf("a list cut at its limit says %q", said)
	}
	whole := cut
	whole.Warnings = []view.Error{{Code: "x.note", Message: "a note about every row", Advisory: true}}
	if said := orderSaid(whole, tableOrder{column: "Commit"}); strings.Contains(said, "on this page") {
		t.Errorf("a whole list with an advisory warning says %q", said)
	}
}

// twoTables is a page as fs.usage --detail answers one: a summary, then two
// tables of sizes.
func twoTables() view.Sections {
	return view.Sections{Items: []view.Section{
		{ID: "summary", Title: "summary", View: view.KeyValue{Pairs: []view.Pair{{Key: "total", Value: "10.0 GiB"}}}},
		{ID: "entries", Title: "biggest entries", View: storeListing()},
		{ID: "largest-files", Title: "largest files anywhere beneath", View: storeListing()},
	}}
}

// A page with several tables sorts any of them: the picker names each column
// under its table's title, and the order chosen sorts that table alone, says
// which on the line, is the one the heading is marked for, and survives a
// refresh. With the key reaching a page's one table alone, neither of
// fs.usage's could be sorted.
func TestAPageWithTwoTablesSortsTheOneChosen(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 40})
	shown, _ := sized.(Model).Update(resultMsg{cap: sortCap(), view: twoTables()})
	sm := shown.(Model)
	if !sm.sortable() {
		t.Fatal("a page with two tables cannot be sorted")
	}
	var labels []string
	for _, o := range orderOptions(sm.result.raw) {
		labels = append(labels, o.label)
	}
	for _, want := range []string{"biggest entries › Size — largest first", "largest files anywhere beneath › Size — largest first"} {
		if !slices.Contains(labels, want) {
			t.Errorf("offered %q, want %q among them", labels, want)
		}
	}

	order := tableOrder{section: "largest-files", column: "Size", desc: true}
	sorted := sm.sortResult(order)
	files := func(m Model) []string { return firstColumn(m.result.raw.(view.Sections).Items[2].View.(view.Table)) }
	if got := files(sorted); !slices.Equal(got, []string{"huge", "mid", "small", "unmeasured"}) {
		t.Errorf("the largest files are %v, want them largest first", got)
	}
	if got := firstColumn(sorted.result.raw.(view.Sections).Items[1].View.(view.Table)); !slices.Equal(got, firstColumn(storeListing())) {
		t.Errorf("the other table moved: %v", got)
	}
	if meta := plain(sorted.resultMeta()); !strings.Contains(meta, "sorted by largest files anywhere beneath › Size, largest first") {
		t.Errorf("the line does not say which table is sorted: %q", meta)
	}
	if got := sorted.resultOptions().Sorted; got != (cli.SortMark{Section: "largest-files", Column: "Size", Desc: true}) {
		t.Errorf("the heading is marked for %+v", got)
	}
	refreshed, _ := sorted.Update(resultMsg{cap: sortCap(), view: twoTables()})
	if got := files(refreshed.(Model)); !slices.Equal(got, []string{"huge", "mid", "small", "unmeasured"}) {
		t.Errorf("after a refresh the largest files are %v", got)
	}
}
