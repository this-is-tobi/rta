package tui

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A dashboard of twelve tiles, four of them lists of five hundred pods, took
// tens of milliseconds to paint and more to answer an arrow key: every tile was
// drawn in full several times a paint, every row of every list each time to show
// nine of them, and the grid was packed again for every tile's width. A tile
// draws no row past what its box shows, and a paint works its layout out once.

// everyLine asks for a tile's body with no bound, as a test that reads all of
// it does.
const everyLine = math.MaxInt

// podList is a table of n pods, too wide for a tile to draw as a grid.
func podList(n int) view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Namespace"}, {Name: "Status", Kind: view.KindStatus},
		{Name: "Restarts", Kind: view.KindNumber}, {Name: "Node"}, {Name: "Age", Kind: view.KindDuration}}, Total: n}
	for i := range n {
		t.Rows = append(t.Rows, []string{fmt.Sprintf("pod-%04d-abcdef", i), fmt.Sprintf("namespace-%d", i%7),
			"Running", fmt.Sprint(i % 3), fmt.Sprintf("node-%d", i%5), fmt.Sprintf("%dd", i%40)})
	}
	return t
}

// largeDashboard is a stated dashboard of twelve tiles, every fourth a list of
// five hundred pods, all of them answered.
func largeDashboard(tb testing.TB) Model {
	tb.Helper()
	dir := tb.TempDir()
	tb.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	tb.Setenv("RTA_DATA_DIR", dir)
	pods := podList(500)
	small := view.KeyValue{Pairs: []view.Pair{{Key: "cpu", Value: "12%"}, {Key: "mem", Value: "40%"}, {Key: "load", Value: "0.4"}}}
	var caps []plugin.Capability
	var tiles []config.Tile
	for i := range 12 {
		v := view.View(small)
		if i%3 == 0 {
			v = pods
		}
		id := fmt.Sprintf("perf.t%02d", i)
		caps = append(caps, plugin.Capability{ID: id, Summary: "tile " + fmt.Sprint(i), Safety: plugin.Read, Idempotent: true,
			Run: func(context.Context, plugin.Request) (view.View, error) { return v, nil }})
		tiles = append(tiles, config.Tile{ID: id})
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "perf", Summary: "perf", Capabilities: caps}); err != nil {
		tb.Fatal(err)
	}
	sized, _ := New(reg, config.Dashboard{Tiles: tiles}, nil).Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	m := sized.(Model)
	for i, t := range m.tiles {
		if t.search {
			continue
		}
		v, _ := t.cap.Run(context.Background(), plugin.Request{})
		next, _ := m.Update(tileMsg{key: t.key(), idx: i, v: v})
		m = next.(Model)
	}
	m.selected = 1
	return m
}

// What a box shows of a long list is what it showed when the whole list was
// drawn: the columns are measured over every row, so a row past the cut is no
// less a reason for a column to be as wide as it is.
func TestALongListDrawsTheSameTileWhenOnlyWhatItShowsIsDrawn(t *testing.T) {
	pods := podList(500)
	pods.Rows[400][0] = "a-pod-whose-name-is-much-longer-than-any-other-pod"
	all, ok := glanceTable(pods, 60, everyLine)
	if !ok {
		t.Fatal("the fixture is not glanced")
	}
	cut, _ := glanceTable(pods, 60, 9)
	if len(cut) != 10 || !slices.Equal(cut, all[:10]) {
		t.Errorf("bounded at 9 the glance is %d lines, and its lines are those of the whole one %v", len(cut), slices.Equal(cut, all[:min(len(cut), len(all))]))
	}
	// What renderTile reads of them: more than the box shows, so that it says
	// there is more, and the first of them as the whole list draws them.
	tl := tile{cap: plugin.Capability{ID: "kube.pod.list", Summary: "pods"}, view: pods}
	full := tileLines(tl, 64, false, everyLine)
	for _, height := range []int{tileClipHeight, tileHeight, 30} {
		lines := tileLines(tl, 64, false, height)
		if len(lines) <= height-2 || !slices.Equal(lines, full[:len(lines)]) {
			t.Errorf("at height %d the tile has %d lines, the whole list's first %v", height, len(lines), slices.Equal(lines, full[:min(len(lines), len(full))]))
		}
	}
	if got := tileRowHeight(tl, 64); got != tileHeight {
		t.Errorf("a long list asks for a row of %d, want %d", got, tileHeight)
	}
}

// The layout a paint or a scroll works out is theirs alone: a model an answer
// or a key leaves behind holds none, or the next paint would draw the tiles
// where the last one put them.
func TestNoKeyOrAnswerLeavesALayoutBehind(t *testing.T) {
	m := largeDashboard(t)
	_ = m.View()
	if m.layout != nil {
		t.Fatal("a paint left its layout on the model")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if next.(Model).layout != nil {
		t.Error("an arrow key left its layout on the model")
	}
	answered, _ := next.(Model).Update(tileMsg{key: m.tiles[1].key(), idx: 1, v: podList(3)})
	if answered.(Model).layout != nil {
		t.Error("an answer left its layout on the model")
	}
}

// A tile that answers again with a shorter list gets a shorter row on the next
// paint: nothing of the paint before is kept.
func TestARowFollowsItsTileFromOnePaintToTheNext(t *testing.T) {
	m := largeDashboard(t)
	before := m.withLayout().rowHeights()
	for _, i := range m.tileRows()[0].cells {
		next, _ := m.Update(tileMsg{key: m.tiles[i].key(), idx: i, v: view.KeyValue{Pairs: []view.Pair{{Key: "pods", Value: "0"}}}})
		m = next.(Model)
	}
	after := m.withLayout().rowHeights()
	if after[0] >= before[0] {
		t.Errorf("the first row is %d lines before and %d after its lists emptied", before[0], after[0])
	}
}

func BenchmarkALargeDashboardPaint(b *testing.B) {
	m := largeDashboard(b)
	for b.Loop() {
		_ = m.View()
	}
}

func BenchmarkALargeDashboardArrowKey(b *testing.B) {
	m := largeDashboard(b)
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	up := tea.KeyPressMsg{Code: tea.KeyUp}
	for b.Loop() {
		next, _ := m.Update(down)
		_ = next.(Model).View()
		next, _ = next.(Model).Update(up)
		_ = next.(Model).View()
	}
}
