package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// layoutRegistry returns capabilities shaped like the real built-ins,
// including deliberately wide content to stress the layout.
func layoutRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "lay", Summary: "layout fixtures",
		Capabilities: []plugin.Capability{
			{
				ID: "lay.chart", Summary: "per-core bars", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					c := view.Chart{Kind: view.ChartBar, Unit: "%", Max: 100}
					for i := 0; i < 4; i++ {
						c.Series = append(c.Series, view.Series{
							Name: "core" + strings.Repeat("0", i+1), Points: []float64{25 * float64(i+1)},
						})
					}
					return c, nil
				},
			},
			{
				ID: "lay.kv", Summary: "key values", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.KeyValue{Pairs: []view.Pair{
						{Key: "os", Value: "darwin 26.5.2 (arm64) with an unnecessarily long platform description"},
						{Key: "uptime", Value: "32d 16h"},
					}}, nil
				},
			},
			{
				ID: "lay.table", Summary: "wide table", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Table{
						Columns: []view.Column{{Name: "ID", Kind: view.KindNumber}, {Name: "Status", Kind: view.KindStatus}, {Name: "Task"}},
						Rows: [][]string{
							{"1", "open", "a task with a really long description that will definitely exceed narrow tile widths"},
							{"2", "done", "short"},
						},
						Total: 2,
					}, nil
				},
			},
			{
				ID: "lay.needy", Summary: "needs input", Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "target", Type: plugin.String, Positional: true, Required: true, Help: "host to probe"},
					{Name: "count", Type: plugin.Int, Default: 4, Help: "number of probes"},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// sized builds a model at a given terminal size with all tiles resolved.
func sized(t *testing.T, w, h int) Model {
	t.Helper()
	m := New(layoutRegistry(t), config.Dashboard{}, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	model := next.(Model)
	for i, ti := range model.tiles {
		msg := tileCmd(i, ti, nil, "", nil, config.Connection{})()
		next, _ = model.Update(msg)
		model = next.(Model)
	}
	return model
}

func frame(t *testing.T, m Model) string {
	t.Helper()
	return m.View().Content
}

// assertFits fails if any visible line is wider than the terminal.
func assertFits(t *testing.T, name, content string, width int) {
	t.Helper()
	for i, line := range strings.Split(content, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("%s: line %d is %d cells wide, terminal is %d:\n%q", name, i+1, got, width, line)
		}
	}
}

var layoutSizes = []struct{ w, h int }{
	{60, 20},  // narrow laptop split
	{80, 24},  // classic
	{100, 30}, // comfortable
	{132, 45}, // wide
}

func TestFramesFitTerminalWidth(t *testing.T) {
	for _, size := range layoutSizes {
		m := sized(t, size.w, size.h)

		// Dashboard.
		assertFits(t, sizeName("dashboard", size.w), frame(t, m), size.w)

		// Browse.
		m.mode = modeBrowse
		assertFits(t, sizeName("browse", size.w), frame(t, m), size.w)

		// Result pane with wide content.
		c, _ := layoutRegistry(t).Capability("lay.table")
		msg := runCmd(context.Background(), 0, c, nil, false, nil, "", nil, config.Connection{})()
		next, _ := m.Update(msg)
		rm := next.(Model)
		assertFits(t, sizeName("result", size.w), frame(t, rm), size.w)

		// Form.
		needy, _ := layoutRegistry(t).Capability("lay.needy")
		fnext, _ := m.open(needy)
		fm := fnext.(Model)
		assertFits(t, sizeName("form", size.w), frame(t, fm), size.w)

		// Running.
		fm.mode = modeRunning
		assertFits(t, sizeName("running", size.w), frame(t, fm), size.w)
	}
}

func sizeName(mode string, w int) string {
	return fmt.Sprintf("%s@%d", mode, w)
}

// TestDumpFrames prints every mode at the classic size for visual review:
//
//	go test ./internal/render/tui -run TestDumpFrames -v
func TestDumpFrames(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("verbose only")
	}
	m := sized(t, 100, 30)
	t.Logf("dashboard 100x30:\n%s", frame(t, m))
	m.mode = modeBrowse
	t.Logf("browse 100x30:\n%s", frame(t, m))
	c, _ := layoutRegistry(t).Capability("lay.table")
	next, _ := m.Update(runCmd(context.Background(), 0, c, nil, false, nil, "", nil, config.Connection{})())
	t.Logf("result 100x30:\n%s", frame(t, next.(Model)))
	needy, _ := layoutRegistry(t).Capability("lay.needy")
	fnext, _ := m.open(needy)
	t.Logf("form 100x30:\n%s", frame(t, fnext.(Model)))
}
