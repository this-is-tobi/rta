package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
)

// The dashboard that states no section and holds no entry that expanded draws
// exactly as it did before sections existed.
//
// Sections change what the grid is made of — a header row, a column count of
// their own, a fold — and every one of those is a place where a layout that
// meant to leave an ordinary dashboard alone could move a row, a gap or a
// footer hint by a cell. The frame tests beside this one hold geometry (every
// row is as tall as the model says, every panel as wide), and so cannot say
// that the picture is the same picture. This does: the frames below were taken
// from the dashboard as it was drawn before the sections change, at every size
// the layout tests use, over the shapes a dashboard has — the automatic set,
// a stated list with a span and an answer that overflows, a fixed column count,
// the search box open, a flash in the footer, the selection on a middle tile
// and on the last, which scrolls.
//
// It is a golden, with the cost a golden has: a deliberate change to the header,
// the footer, a panel's border or what a tile says makes it fail, and the
// failure is the diff to read and then accept with
//
//	go test ./internal/render/tui -run TestAnUnsectionedDashboardIsDrawnAsItAlwaysWas -update-frames
//
// A failure that is not a change somebody meant is the test doing its job. The
// flag is not -update, which teatest registers for its own golden files.
var updateFrames = flag.Bool("update-frames", false, "rewrite the golden dashboard frames")

const dashboardFramesFile = "testdata/dashboard-frames.txt"

func TestAnUnsectionedDashboardIsDrawnAsItAlwaysWas(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	var out strings.Builder
	for _, size := range layoutSizes {
		for _, shape := range goldenShapes(t) {
			m := filled(t, New(shape.reg, shape.dash, nil), size.w, size.h)
			for _, state := range goldenStates() {
				fmt.Fprintf(&out, "=== %s %dx%d %s ===\n%s\n", shape.name, size.w, size.h, state.name,
					plain(state.apply(m).View().Content))
			}
		}
	}
	got := out.String()
	if *updateFrames {
		if err := os.MkdirAll(filepath.Dir(dashboardFramesFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dashboardFramesFile, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(dashboardFramesFile)
	if err != nil {
		t.Fatalf("no golden frames: %v — run the test with -update-frames to take them", err)
	}
	if got == string(want) {
		return
	}
	gotFrames, wantFrames := strings.Split(got, "=== "), strings.Split(string(want), "=== ")
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("%d frames drawn, %d in the golden file", len(gotFrames)-1, len(wantFrames)-1)
	}
	for i := range gotFrames {
		if gotFrames[i] != wantFrames[i] {
			name, _, _ := strings.Cut(gotFrames[i], "\n")
			t.Fatalf("the frame %q is not the one it always was:\n--- drawn\n%s\n--- golden\n%s",
				name, gotFrames[i], wantFrames[i])
		}
	}
}

type goldenShape struct {
	name string
	reg  *registry.Registry
	dash config.Dashboard
}

// goldenShapes are the dashboards the frames are taken of: none states a
// section and none holds a tile that expands, which is the whole condition.
func goldenShapes(t *testing.T) []goldenShape {
	t.Helper()
	return []goldenShape{
		{"automatic", answeringRegistry(t, "alpha", "beta", "gamma", "delta", "epsilon"), config.Dashboard{}},
		{"stated", heightRegistry(t), dashFixture()},
		{"spanned", heightRegistry(t), config.Dashboard{Width: 6, Tiles: []config.Tile{
			{ID: "short.info", Width: 12}, {ID: "mid.info"}, {ID: "short.info", Profile: "prod"},
			{ID: "tall.info"}, {ID: "mid.info", Profile: "staging"}, {ID: "short.info"}, {ID: "tall.info", Width: 12},
		}}},
		{"hidden-ordered-added", heightRegistry(t), config.Dashboard{
			Hidden: []string{"mid.info"}, Order: []string{"tall.info", "short.info"},
			Add: []config.Tile{{ID: "mid.info", Profile: "prod"}, {ID: "tall.info", Profile: "staging", Width: 12}},
		}},
	}
}

type goldenState struct {
	name  string
	apply func(Model) Model
}

// goldenStates are what is done to a drawn dashboard before it is photographed.
func goldenStates() []goldenState {
	selecting := func(i int) func(Model) Model {
		return func(m Model) Model {
			m.selected = min(i, len(m.tiles)-1)
			m.clampScroll()
			return m
		}
	}
	return []goldenState{
		{"initial", func(m Model) Model { return m }},
		{"third-tile-and-a-flash", func(m Model) Model {
			m = selecting(3)(m)
			m.flash = "saved the new order"
			return m
		}},
		{"last-tile", selecting(1 << 20)},
		{"search-open", func(m Model) Model {
			m.searchEditing, m.query = true, "a"
			return m
		}},
	}
}
