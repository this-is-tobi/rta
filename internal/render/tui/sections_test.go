package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Sections group tiles under a header that folds. These hold what the layout
// does with them; the keys that fold, the mouse and the writes are in
// sectionkeys_test.go.

// cellRows is the tiles on each row of the layout, in the order of the rows, with
// nil for a header: the picture most of the layout tests were written against,
// before a row could be one.
func cellRows(m Model) [][]int {
	rows := m.tileRows()
	out := make([][]int, len(rows))
	for i, r := range rows {
		out[i] = r.cells
	}
	return out
}

// shape spells the rows the way a person would draw them: `#name` for a header,
// `[1 2]` for a row of tiles.
func shape(m Model) string {
	var parts []string
	for _, r := range m.tileRows() {
		if r.isHeader() {
			parts = append(parts, "#"+r.header.name)
			continue
		}
		parts = append(parts, fmt.Sprint(r.cells))
	}
	return strings.Join(parts, " ")
}

// statusPlugin answers with one row whose status is the word it was given, which
// is what a header grades its tiles by.
func statusPlugin(name, status string) plugin.Plugin {
	return plugin.Plugin{Name: name, Summary: name, Capabilities: []plugin.Capability{{
		ID: name + ".status", Summary: name + " status", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Table{
				Columns: []view.Column{{Name: "Name"}, {Name: "Status", Kind: view.KindStatus}},
				Rows:    [][]string{{name, status}},
				Total:   1,
			}, nil
		},
	}}}
}

// factsPlugin answers with a pair, which grades nothing.
func factsPlugin(name string) plugin.Plugin {
	return plugin.Plugin{Name: name, Summary: name, Capabilities: []plugin.Capability{{
		ID: name + ".status", Summary: name + " status", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.KeyValue{Pairs: []view.Pair{{Key: "who", Value: name}}}, nil
		},
	}}}
}

// sectionRegistry has one plugin per state a tile can be in: fine, warning, bad,
// and one that says nothing graded.
func sectionRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		statusPlugin("alpha", "ok"), statusPlugin("beta", "warn"), statusPlugin("gamma", "error"),
		factsPlugin("delta"), statusPlugin("epsilon", "ok"), statusPlugin("zeta", "ok"),
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// grouped is a stated dashboard with two sections, one of them split by a tile
// that is in none, and one more tile after them.
func grouped() config.Dashboard {
	return config.Dashboard{Tiles: []config.Tile{
		{ID: "alpha.status", Section: "databases"},
		{ID: "delta.status"},
		{ID: "beta.status", Section: "databases"},
		{ID: "gamma.status", Section: "edge"},
		{ID: "epsilon.status"},
	}}
}

// sectioned draws a dashboard over the section registry with every answer in.
func sectioned(t *testing.T, dash config.Dashboard, w, h int) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	return filled(t, New(sectionRegistry(t), dash, nil), w, h)
}

// sizedModel gives a model the terminal's size and none of the answers.
func sizedModel(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

// saveActive switches an environment on, or off with "".
func saveActive(t *testing.T, name string) {
	t.Helper()
	if verr := profile.SaveSelection(profile.Selection{Active: name}); verr != nil {
		t.Fatal(verr)
	}
}

func capIDs(m Model) string {
	ids := make([]string, 0, len(m.tiles))
	for _, t := range m.tiles[1:] {
		ids = append(ids, strings.TrimSuffix(t.cap.ID, ".status"))
	}
	return strings.Join(ids, " ")
}

// A section is drawn together, at the place of its first tile; a tile in none
// keeps its own place, which is how a dashboard that names no section is the
// one it always was.
func TestTheTilesOfASectionAreDrawnTogetherAtTheFirstOnesPlace(t *testing.T) {
	m := sectioned(t, grouped(), 120, 40)
	if got := capIDs(m); got != "alpha beta delta gamma epsilon" {
		t.Errorf("tiles = %s, want the databases together where alpha was, delta where it was", got)
	}
	if got := shape(m); got != "#databases [1 2] [3] #edge [4] [5]" {
		t.Errorf("rows = %s", got)
	}
	runs := m.sectionRuns()
	if len(runs) != 2 || runs[0] != (sectionRun{"databases", 1, 3}) || runs[1] != (sectionRun{"edge", 4, 5}) {
		t.Errorf("runs = %+v", runs)
	}

	// The order says where a section goes: ordering its first tile moves it.
	dash := grouped()
	dash.Tiles = nil
	dash.Add = grouped().Tiles
	dash.Order = []string{"gamma.status"}
	m = sectioned(t, dash, 120, 40)
	if got := capIDs(m); got != "gamma alpha beta delta epsilon zeta" {
		t.Errorf("with the edge section ordered first: %s", got)
	}
}

// An entry that became several panels is a group by being one; one that stayed
// one panel is not, and one that names its own section keeps it.
func TestAnEntryThatExpandedIsASectionOfItsOwn(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{
		{ID: "db.status", Profile: "prod"},
		{ID: "db.status", Profile: "staging"},
	}})
	for _, tl := range m.tiles[1:] {
		want := ""
		if tl.expanded {
			want = "db · prod"
		}
		if tl.section != want {
			t.Errorf("%s is in %q, want %q", tl.key(), tl.section, want)
		}
	}
	if got := shape(m); got != "[1] #db · prod [2 3] [4]" {
		t.Errorf("rows = %s, want the two panels of prod under a header and the rest outside it", got)
	}

	named := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{
		{ID: "db.status", Profile: "prod", Section: "databases"},
	}})
	for _, tl := range named.tiles[1:] {
		if tl.expanded && tl.section != "databases" {
			t.Errorf("%s is in %q: an entry that names its section keeps it for every panel", tl.key(), tl.section)
		}
	}
}

// A tile that follows the switch expands into the environment that is on, and
// the group is named after that environment.
func TestAFollowingTilesGroupIsNamedAfterTheEnvironmentThatIsOn(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{})
	if got := shape(m); strings.Contains(got, "#") {
		t.Fatalf("with nothing switched on there is a header: %s", got)
	}
	saveActive(t, "prod")
	m.syncActive()
	if got := shape(m); !strings.Contains(got, "#db · prod") {
		t.Errorf("under prod, rows = %s, want its two connections under `db · prod`", got)
	}
	saveActive(t, "")
	m.syncActive()
	if got := shape(m); strings.Contains(got, "#") {
		t.Errorf("switched off, rows = %s: the group should be one panel again, and no group", got)
	}
}

// A header is a line of its own, and a folded section is that line and nothing
// else: its tiles are on no row.
func TestAFoldedSectionIsOneHeaderRow(t *testing.T) {
	dash := grouped()
	dash.Sections = map[string]config.DashboardSection{"databases": {Collapsed: true}}
	m := sectioned(t, dash, 120, 40)
	if got := shape(m); got != "#databases [3] #edge [4] [5]" {
		t.Fatalf("rows = %s", got)
	}
	heights := m.rowHeights()
	if heights[0] != 1 || len(heights) != 5 {
		t.Errorf("heights = %v, want the header one line", heights)
	}
	frame := plain(m.View().Content)
	for _, want := range []string{"▸ databases", "2 tiles"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the folded header does not say %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "alpha.status") || strings.Contains(frame, "beta.status") {
		t.Errorf("a folded section's tiles are drawn:\n%s", frame)
	}
	if !strings.Contains(frame, "▾ edge") || !strings.Contains(frame, "gamma.status") {
		t.Errorf("the other section is not drawn open:\n%s", frame)
	}
	for i := 1; i <= 2; i++ {
		if r, _ := m.rowAndCol(i); r != -1 {
			t.Errorf("tile %d of the folded section is on row %d", i, r)
		}
	}
}

// Folding hides detail and never a problem: the header says the worst thing the
// tiles inside it say, and that it has not heard from some of them.
func TestAFoldedHeaderSaysTheWorstOfItsTiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tiles []string
		want  string
		not   string
	}{
		{"a bad one beats a warning", []string{"alpha", "beta", "gamma"}, "● 1 bad", "warn"},
		{"a warning beats fine", []string{"alpha", "beta"}, "● 1 warn", "bad"},
		{"all fine is ok", []string{"alpha", "epsilon", "zeta"}, "ok", "●"},
		{"nothing graded says nothing", []string{"delta"}, "1 tile", "ok"},
		{"fine and nothing graded", []string{"alpha", "delta"}, "ok", "●"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tiles []config.Tile
			for _, id := range tc.tiles {
				tiles = append(tiles, config.Tile{ID: id + ".status", Section: "group"})
			}
			m := sectioned(t, config.Dashboard{
				Tiles:    tiles,
				Sections: map[string]config.DashboardSection{"group": {Collapsed: true}},
			}, 120, 40)
			header := strings.Split(plain(m.View().Content), "\n")[2]
			if !strings.Contains(header, tc.want) || strings.Contains(header, tc.not) {
				t.Errorf("the header = %q, want %q and not %q", header, tc.want, tc.not)
			}
			if !strings.Contains(header, "▸ group") {
				t.Errorf("the header = %q, want it folded", header)
			}
		})
	}
}

// A fold hides the panels and never the fact that one is red, at the width where
// there is least room to notice: the count goes first, then the name is cut with
// an ellipsis, and the worst of what the tiles say is on the line at every width.
func TestAHeaderKeepsTheWorstStatusAtEveryWidth(t *testing.T) {
	const name = "production databases of the orders team, eu"
	var tiles []config.Tile
	for _, id := range []string{"alpha", "beta", "gamma"} {
		tiles = append(tiles, config.Tile{ID: id + ".status", Section: name})
	}
	for _, tc := range []struct {
		width             int
		count, whole, cut bool
	}{
		{120, true, true, false},
		{80, true, true, false},
		{60, false, true, false},
		{40, false, false, true},
	} {
		for _, collapsed := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d columns, folded %v", tc.width, collapsed), func(t *testing.T) {
				m := sectioned(t, config.Dashboard{
					Tiles:    tiles,
					Sections: map[string]config.DashboardSection{name: {Collapsed: collapsed}},
				}, tc.width, 40)
				header := plain(m.renderSectionHeader(m.sectionRuns()[0], false))
				if got := lipgloss.Width(header); got > tc.width {
					t.Errorf("the header is %d cells on a %d-column terminal: %q", got, tc.width, header)
				}
				if !strings.Contains(header, "● 1 bad") {
					t.Errorf("the worst status is not on the header: %q", header)
				}
				if got := strings.Contains(header, "3 tiles"); got != tc.count {
					t.Errorf("the count is on the header: %v, want %v: %q", got, tc.count, header)
				}
				if got := strings.Contains(header, name); got != tc.whole {
					t.Errorf("the whole name is on the header: %v, want %v: %q", got, tc.whole, header)
				}
				if got := strings.Contains(header, "…"); got != tc.cut {
					t.Errorf("the name is cut with an ellipsis: %v, want %v: %q", got, tc.cut, header)
				}
				if !strings.Contains(header, "production") {
					t.Errorf("not even the start of the name is on the header: %q", header)
				}
			})
		}
	}
}

// A terminal too narrow for the status still gets one line, not a wrapped one.
func TestAHeaderIsOneLineOnATerminalTooNarrowForIt(t *testing.T) {
	tiles := []config.Tile{{ID: "alpha.status", Section: "databases"}, {ID: "gamma.status", Section: "databases"}}
	for _, width := range []int{12, 8, 3, 1} {
		m := sectioned(t, config.Dashboard{Tiles: tiles}, width, 40)
		header := m.renderSectionHeader(m.sectionRuns()[0], true)
		if w, h := lipgloss.Width(header), lipgloss.Height(header); w > width || h != 1 {
			t.Errorf("at %d columns the header is %d cells over %d lines: %q", width, w, h, plain(header))
		}
	}
}

// A section whose tiles have not answered is not fine, and is not called so.
func TestAHeaderSaysItIsStillWaitingForTilesToAnswer(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := New(sectionRegistry(t), config.Dashboard{
		Tiles:    []config.Tile{{ID: "alpha.status", Section: "g"}, {ID: "beta.status", Section: "g"}},
		Sections: map[string]config.DashboardSection{"g": {Collapsed: true}},
	}, nil)
	m = sizedModel(t, m, 120, 40)
	if header := strings.Split(plain(m.View().Content), "\n")[2]; !strings.Contains(header, "2 loading") || strings.Contains(header, "ok") {
		t.Errorf("before any answer the header = %q", header)
	}
	// One answers, with a warning: that is said now, and the other is still pending.
	next, _ := m.Update(tileCmd(2, m.tiles[2], statedConfig{}, "", nil, config.Connection{})())
	m = next.(Model)
	if header := strings.Split(plain(m.View().Content), "\n")[2]; !strings.Contains(header, "● 1 warn") || !strings.Contains(header, "1 loading") {
		t.Errorf("after the warning answered the header = %q", header)
	}
}

// An error is a tile that says it is wrong, except the ones a panel draws as a
// quiet sentence because there is nothing to say from here.
func TestAnErrorIsBadExceptWhereThePanelSaysItQuietly(t *testing.T) {
	g, answered := tileGrade(tile{err: view.Errorf("x.broken", "no")})
	if g != gradeBad || !answered {
		t.Errorf("an error graded %v, answered %v", g, answered)
	}
	g, answered = tileGrade(tile{err: view.Errorf("git.notarepo", "not here")})
	if g != gradeNone || !answered {
		t.Errorf("a quiet error graded %v, answered %v", g, answered)
	}
	if _, answered = tileGrade(tile{}); answered {
		t.Error("a tile with nothing on it is graded")
	}
}

func TestWhatGradesATile(t *testing.T) {
	for name, tc := range map[string]struct {
		v    view.View
		want grade
	}{
		"a status cell": {view.Table{
			Columns: []view.Column{{Name: "S", Kind: view.KindStatus}}, Rows: [][]string{{"ok"}, {"down"}},
		}, gradeBad},
		"a usage cell": {view.Table{
			Columns: []view.Column{{Name: "U", Kind: view.KindUsage}}, Rows: [][]string{{"12%"}, {"85%"}},
		}, gradeWarn},
		"a status column is the only one read": {view.Table{
			Columns: []view.Column{{Name: "Name"}}, Rows: [][]string{{"down"}},
		}, gradeNone},
		"a partial table": {view.Table{
			Columns: []view.Column{{Name: "Name"}}, Rows: [][]string{{"a"}},
			Warnings: []view.Error{{Code: "x.partial", Message: "one namespace was not listed"}},
		}, gradeWarn},
		"an advisory is not a gap": {view.Table{
			Columns: []view.Column{{Name: "Name"}}, Rows: [][]string{{"a"}},
			Warnings: []view.Error{{Code: "x.note", Message: "note", Advisory: true}},
		}, gradeNone},
		"calls waiting": {view.KeyValue{Pairs: []view.Pair{{Key: waitingKey, Value: "2 calls"}}}, gradeWarn},
		"none waiting":  {view.KeyValue{Pairs: []view.Pair{{Key: waitingKey, Value: "0"}}}, gradeNone},
		"inside a page": {view.Sections{Items: []view.Section{
			{Title: "a", View: view.Text{Body: "x"}},
			{Title: "b", View: view.Table{Columns: []view.Column{{Name: "S", Kind: view.KindStatus}}, Rows: [][]string{{"error"}}}},
		}}, gradeBad},
		"text": {view.Text{Body: "error"}, gradeNone},
	} {
		if got := viewGrade(tc.v); got != tc.want {
			t.Errorf("%s graded %v, want %v", name, got, tc.want)
		}
	}
}

// A section may state the width of its own tiles, and the dashboard's width is
// the rest's.
func TestASectionCanStateItsOwnWidth(t *testing.T) {
	var tiles []config.Tile
	for _, id := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		section := ""
		if id != "alpha" && id != "zeta" {
			section = "wide"
		}
		tiles = append(tiles, config.Tile{ID: id + ".status", Section: section})
	}
	m := sectioned(t, config.Dashboard{
		Width:    config.GridTracks,
		Tiles:    tiles,
		Sections: map[string]config.DashboardSection{"wide": {Width: 3}},
	}, 160, 60)
	if got := shape(m); got != "[1] #wide [2 3 4 5] [6]" {
		t.Fatalf("rows = %s, want four tiles across inside the section and one outside it", got)
	}
	// Four across on 160 cells; the one outside is the whole width.
	if a, b := m.tileWidth(2), m.tileWidth(1); a >= b || b != 160 {
		t.Errorf("widths: inside the section %d, outside %d", a, b)
	}
	// And a section that asks for more than the terminal holds is held to it: a
	// quarter of 80 cells is twenty, which no tile is drawn at.
	narrow := sectioned(t, config.Dashboard{
		Tiles:    tiles,
		Sections: map[string]config.DashboardSection{"wide": {Width: 3}},
	}, 80, 60)
	if got := narrow.span(2); got != 6 {
		t.Errorf("width: 3 on 80 cells is %d twelfths, want the 6 that fit", got)
	}
	if got := narrow.span(1); got != 6 {
		t.Errorf("a tile with nothing stated is %d twelfths on 80 cells, want 6", got)
	}
}

// Every frame of a sectioned dashboard is the shape the model says it is: a line
// for the header, the search bar, each row at its own height, the footer.
func TestASectionedFrameIsWholeAtEverySize(t *testing.T) {
	for _, size := range layoutSizes {
		for _, collapsed := range []bool{false, true} {
			dash := grouped()
			dash.Sections = map[string]config.DashboardSection{"databases": {Collapsed: collapsed}, "edge": {Width: 6}}
			m := sectioned(t, dash, size.w, size.h)
			name := fmt.Sprintf("%dx%d collapsed=%v", size.w, size.h, collapsed)
			content := m.View().Content
			assertFits(t, name, content, size.w)
			lines := strings.Split(plain(content), "\n")
			if len(lines) > size.h {
				t.Errorf("%s: %d lines on a %d-line terminal", name, len(lines), size.h)
			}
			rows := m.tileRows()
			first, last := m.rowWindow(len(rows))
			heights := m.drawnHeights(first, last)
			want := 1 + m.searchHeight() + lipgloss.Height(m.dashFooter())
			for _, h := range heights {
				want += h
			}
			if len(lines) != want {
				t.Fatalf("%s: frame is %d lines, the geometry says %d:\n%s", name, len(lines), want, strings.Join(lines, "\n"))
			}
			y := 1 + m.searchHeight()
			for r := first; r < last; r++ {
				row := lines[y]
				switch {
				case rows[r].isHeader():
					if heights[r-first] != 1 || !strings.Contains(row, rows[r].header.name) {
						t.Errorf("%s: row %d is a header drawn %d lines tall as %q", name, r, heights[r-first], row)
					}
				case strings.Count(row, "╭") != len(rows[r].cells):
					t.Errorf("%s: row %d opens %d panels, holds %d tiles: %q", name, r, strings.Count(row, "╭"), len(rows[r].cells), row)
				}
				y += heights[r-first]
			}
		}
	}
}

// A click lands where the paint did: on a tile, never on the one a header's line
// would have been counted as.
func TestAClickLandsOnTheTileUnderItAndNeverOnAHeader(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	heights := m.rowHeights()
	rows := m.tileRows()
	y := 1 + m.searchHeight()
	for r, row := range rows {
		for x := 0; x < m.width; x += 7 {
			got := m.tileAt(x, y)
			switch {
			case row.isHeader() && got != -1:
				t.Errorf("a click at (%d, %d) on the header of %s hit tile %d", x, y, row.header.name, got)
			case !row.isHeader() && got != -1 && !slices.Contains(row.cells, got):
				t.Errorf("a click at (%d, %d) on row %d %v hit tile %d", x, y, r, row.cells, got)
			}
		}
		if row.isHeader() {
			if gr, ok := m.gridRowAt(y); !ok || gr.header.name != row.header.name {
				t.Errorf("line %d is the header of %s, gridRowAt says %+v, %v", y, row.header.name, gr, ok)
			}
		} else if got := m.tileAt(1, y); got != row.cells[0] {
			t.Errorf("a click at the left of row %d hit tile %d, want %d", r, got, row.cells[0])
		}
		y += heights[r]
	}
}

// The arrows rest on a header as they do on a tile, and pass over what a folded
// section holds.
func TestTheArrowsPassThroughHeadersAndSkipAFoldedSectionsTiles(t *testing.T) {
	dash := grouped()
	dash.Sections = map[string]config.DashboardSection{"databases": {Collapsed: true}}
	m := sectioned(t, dash, 120, 60)
	if got := shape(m); got != "#databases [3] #edge [4] [5]" {
		t.Fatalf("rows = %s", got)
	}
	var visited []string
	at := func() string {
		if m.selected < 0 {
			return "#" + m.header
		}
		return fmt.Sprint(m.selected)
	}
	m.selected = 0
	for range 7 {
		m.moveSelection(1, 0)
		visited = append(visited, at())
	}
	// Right from the search bar is the first stop, which is a header; the folded
	// section's two tiles are not stops at all; and the end is an end.
	if got := strings.Join(visited, " "); got != "#databases 3 #edge 4 5 5 5" {
		t.Errorf("right, seven times: %s", got)
	}
	visited = nil
	for range 6 {
		m.moveSelection(-1, 0)
		visited = append(visited, at())
	}
	if got := strings.Join(visited, " "); got != "4 #edge 3 #databases #databases #databases" {
		t.Errorf("left, six times: %s", got)
	}
	// Up from the first stop is the search bar.
	m.moveSelection(0, -1)
	if m.selected != 0 || m.header != "" {
		t.Errorf("up from the first header: selected %d, header %q", m.selected, m.header)
	}
	// Down walks the rows: header, the row of tiles, the next header...
	visited = nil
	for range 5 {
		m.moveSelection(0, 1)
		visited = append(visited, at())
	}
	if got := strings.Join(visited, " "); got != "#databases 3 #edge 4 5" {
		t.Errorf("down, five times: %s", got)
	}
}

// A column is kept down a section and across its header: the header is a whole
// row, and passing through it is not choosing a column.
func TestPassingThroughAHeaderKeepsTheColumn(t *testing.T) {
	m := sectioned(t, config.Dashboard{Tiles: []config.Tile{
		{ID: "alpha.status", Section: "one"}, {ID: "beta.status", Section: "one"},
		{ID: "gamma.status", Section: "two"}, {ID: "delta.status", Section: "two"},
	}}, 120, 60)
	if got := shape(m); got != "#one [1 2] #two [3 4]" {
		t.Fatalf("rows = %s", got)
	}
	m.selected = 2
	m.goalCol = 1
	m.moveSelection(0, 1)
	if m.selected != -1 || m.header != "two" {
		t.Fatalf("down from beta: selected %d, header %q, want the header of two", m.selected, m.header)
	}
	m.moveSelection(0, 1)
	if m.selected != 4 {
		t.Errorf("down through the header from the second column landed on tile %d, want delta", m.selected)
	}
}

// The window follows the selection, and brings the header of its section when
// the tile is the first row under it.
func TestScrollingToATileBringsItsHeader(t *testing.T) {
	var tiles []config.Tile
	for i := range 6 {
		tiles = append(tiles, config.Tile{ID: "alpha.status", Profile: fmt.Sprintf("p%d", i), Section: fmt.Sprintf("s%d", i/2)})
	}
	m := sectioned(t, config.Dashboard{Tiles: tiles}, 40, 14)
	rows := m.tileRows()
	if len(rows) != 9 {
		t.Fatalf("rows = %s", shape(m))
	}
	// From the bottom of the screen, up to the first tile of the last section: the
	// window opens on the header above it, not on the tile.
	m.scroll = m.maxScroll()
	m.selected = 5
	m.clampScroll()
	row, _ := m.rowAndCol(5)
	if !rows[row-1].isHeader() || m.scroll != row-1 {
		t.Errorf("scroll = %d with the selection on row %d: the window does not open on its section's header", m.scroll, row)
	}
	if first, last := m.rowWindow(len(rows)); row < first || row >= last {
		t.Errorf("the selected row %d is outside the window [%d, %d)", row, first, last)
	}
	// And a selection on a header is as visible as one on a tile.
	m.selectStop(stop{header: "s0"})
	m.clampScroll()
	if m.scroll != 0 {
		t.Errorf("scroll = %d with the first header selected", m.scroll)
	}
}

// The scroll arithmetic counts headers as the rows they are: the last row can be
// reached, and the screen is filled when it is.
func TestTheWindowReachesTheLastRowOfASectionedDashboard(t *testing.T) {
	var tiles []config.Tile
	for i := range 8 {
		tiles = append(tiles, config.Tile{ID: "alpha.status", Profile: fmt.Sprintf("p%d", i), Section: fmt.Sprintf("s%d", i/2)})
	}
	m := sectioned(t, config.Dashboard{Tiles: tiles}, 40, 16)
	m = onSearchLine(m)
	last := len(m.tileRows()) - 1
	m.scroll = 1 << 10
	m.clampScroll()
	if m.scroll != m.maxScroll() || m.scroll >= last+1 {
		t.Fatalf("scroll = %d, max %d", m.scroll, m.maxScroll())
	}
	_, to := m.rowWindow(last + 1)
	if to != last+1 {
		t.Errorf("scrolled to the end, the window ends at row %d of %d", to, last+1)
	}
	heights := m.rowHeights()
	used := 0
	for _, h := range heights[m.scroll:] {
		used += h
	}
	if used > m.dashRowBudget() {
		t.Errorf("the rows from the last scroll position take %d lines, the budget is %d", used, m.dashRowBudget())
	}
	if m.scroll > 0 {
		if used+heights[m.scroll-1] <= m.dashRowBudget() {
			t.Errorf("the window could start a row earlier and still fit: the screen is not filled")
		}
	}
}

// The tiles were built again under the selection — the file was edited, or the
// environment switched — and what held it may not be drawn any more.
func TestTheSelectionIsKeptOnSomethingThatIsDrawn(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)

	m.selected = 1 // alpha, in databases
	m.dash.Sections = map[string]config.DashboardSection{"databases": {Collapsed: true}}
	m.keepSelection()
	if m.selected != -1 || m.header != "databases" {
		t.Errorf("a tile that is folded away: selected %d, header %q, want the header of its section", m.selected, m.header)
	}

	m.dash.Sections = nil
	m.keepSelection()
	if m.selected != -1 || m.header != "databases" {
		t.Errorf("a header stays selected while its section is there: selected %d, header %q", m.selected, m.header)
	}

	// The search bar swallows every letter, so the selection is not left there
	// for the keys that cycle: it goes to the first stop of what is drawn.
	m.header = "gone"
	m.keepSelection()
	if m.selected != -1 || m.header != "databases" {
		t.Errorf("a header whose section is gone: selected %d, header %q, want the first stop, the header of databases", m.selected, m.header)
	}
	m.selected, m.header = -1, "gone"
	m.dash.Tiles = []config.Tile{{ID: "alpha.status"}}
	m.rebuildTiles()
	if m.selected != 1 || m.header != "" {
		t.Errorf("a screen without sections: selected %d, header %q, want its first tile", m.selected, m.header)
	}
	empty := New(registry.New(), config.Dashboard{}, nil)
	empty.selected, empty.header = -1, "gone"
	empty.keepSelection()
	if empty.selected != 0 || empty.header != "" {
		t.Errorf("a screen with nothing on it: selected %d, header %q, want the search bar", empty.selected, empty.header)
	}

	m = sectioned(t, grouped(), 120, 60)
	m.selected = 99
	m.keepSelection()
	if m.selected != len(m.tiles)-1 {
		t.Errorf("a tile past the end: selected %d", m.selected)
	}
}

// V is a cycling key, so pressing it twice shows the third screen — also from a
// header whose section the next screen does not have. The selection used to fall
// to the search bar there, which takes every letter typed, so the second V was
// typed into the query and the screen did not move.
func TestVTwiceFromASectionHeaderAdvancesTwoScreens(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	cfg := config.Config{Dashboard: grouped()}
	cfg.SetBlock("one", config.Dashboard{Tiles: []config.Tile{{ID: "epsilon.status"}, {ID: "zeta.status"}}})
	cfg.SetBlock("two", config.Dashboard{Tiles: []config.Tile{{ID: "zeta.status", Section: "other"}}})
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	m := filled(t, New(sectionRegistry(t), cfg.Dashboard, nil), 120, 40)
	m.selectStop(stop{header: "databases"})

	press := func(want string) {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg{Code: 'V', Text: "V"})
		m = next.(Model)
		if m.view != want {
			t.Fatalf("V drew view %q, want %q (selected %d, header %q, flash %q)", m.view, want, m.selected, m.header, m.flash)
		}
		if m.selected == 0 {
			t.Fatalf("on view %q the selection is on the search bar, where the next V is typed", want)
		}
	}
	press("one")
	press("two")
	press("")
}

// A section that has stopped being folded in the file follows the file, and one
// folded in the session stays folded for as long as the file says what it said.
func TestASessionsFoldFollowsTheFileWhenTheFileChangesItsMind(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	if m.collapsed("databases") {
		t.Fatal("a section nothing folds starts folded")
	}
	m.setCollapsed("databases", true)
	if !m.collapsed("databases") {
		t.Fatal("the fold did not take")
	}
	if m.collapsed("edge") {
		t.Error("folding one section folded another")
	}
	// The file now starts it folded: the person asked for what they already have.
	m.dash.Sections = map[string]config.DashboardSection{"databases": {Collapsed: true}}
	if !m.collapsed("databases") {
		t.Error("the section is open though the file starts it folded")
	}
	// And the file changing its mind again is the file's: the session's old fold,
	// made against an open section, is not what the person meant now.
	m.dash.Sections = nil
	m.setCollapsed("databases", false)
	m.dash.Sections = map[string]config.DashboardSection{"databases": {Collapsed: true}}
	if !m.collapsed("databases") {
		t.Error("an unfold made against an open section outlived the file starting it folded")
	}
	// A screen of its own is a section of its own.
	m.view = "ops"
	if !m.collapsed("databases") {
		t.Error("the other screen's section has no start of its own")
	}
	m.setCollapsed("databases", false)
	m.view = ""
	if !m.collapsed("databases") {
		t.Error("unfolding on one screen unfolded the same name on another")
	}
}
