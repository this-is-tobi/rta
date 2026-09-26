package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// D2: a run refused before it ever starts — resolveProfile failing on a
// name nothing configures — must render like any other refusal, not leave
// whatever was drawn before untouched. Setting m.result is not what puts
// anything on screen; renderResult is, and startRun's verr branch used to
// skip it.
func TestARunRefusedByProfileResolutionRendersItsError(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = sized.(Model)
	get := dbPlugin().Capabilities[0]

	// Something already on screen, the way a session that has run at least
	// once always has something under it — the shape the bug actually hits:
	// a *stale* result staying up, not a blank one.
	m.mode = modeResult
	m.current = get
	m.result = resultMsg{cap: get, view: nil}
	m.renderResult()
	before := m.viewport.View()

	next := m.startRun(get, map[string]any{profileInput: "does-not-exist"}, false)
	if next != nil {
		t.Fatal("startRun returned a command for a run it refused before launching")
	}
	if m.mode != modeResult {
		t.Fatalf("mode = %v, want modeResult", m.mode)
	}
	if m.result.err == nil {
		t.Fatal("no error recorded on the refused run")
	}
	after := m.viewport.View()
	if after == before {
		t.Error("the viewport was not redrawn — the previous result is still on screen")
	}
	if !strings.Contains(after, m.result.err.Message) {
		t.Errorf("the refusal never reached the screen:\n%s", after)
	}
}

// D2's other half: refreshInPlace runs off a ticking chain that only
// reschedules itself from a completed run's own resultMsg. A resolve
// failure here launches no run, so with nothing rescheduling by hand, a
// live view that hit this once would stop refreshing for the rest of the
// session with no visible sign anything had gone wrong.
func TestARefreshInPlaceResolveFailureStillReschedulesTheNextTick(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	get := dbPlugin().Capabilities[0]
	genBefore := m.tickGen

	cmd := m.refreshInPlace(get, map[string]any{profileInput: "does-not-exist"}, false)
	if cmd == nil {
		t.Fatal("refreshInPlace returned no command — the live view stops ticking here")
	}
	if m.tickGen == genBefore {
		t.Errorf("tickGen unchanged at %d — nothing was rescheduled", m.tickGen)
	}
	if m.flash == "" {
		t.Error("no flash noting the failed refresh — fully silent")
	}
	// The command itself is tea.Tick, which sleeps for the real refresh
	// interval before firing — not run here, since the reschedule (the
	// property under test) already happened synchronously above, and
	// waiting out a real interval would make this test as slow as the
	// interval it is testing around.
}

// A value the operator's config supplies and the host's guard refuses is
// refused as the config's on both paths here that run a handler, a run and a
// tile. Built with plugin.Resolve, neither request knew where the value came
// from, and a tile refreshing every few seconds refused a flag nobody typed.
func TestARefusedConfigValueNamesTheKeyItCameFrom(t *testing.T) {
	c := plugin.Capability{ID: "demo.encode", Summary: "encodes", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "encoding", Type: plugin.String, Default: "hex", Config: "encoding",
			Options: []string{"hex", "base32"}, Help: "encoding"}},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			return view.Text{Body: req.String("encoding")}, nil
		},
	}
	c.Run = plugin.GuardInputs(c)
	cfg := statedConfig{values: map[string]any{"encoding": "b64"}}
	const want = "which the config's plugins.demo.encoding sets"

	rm := runCmd(context.Background(), 1, c, nil, false, cfg, "", nil, config.Connection{}, false)().(resultMsg)
	if rm.err == nil || !strings.Contains(rm.err.Message, want) {
		t.Errorf("run: %+v", rm.err)
	}
	tm := tileCmd(0, tile{cap: c}, cfg, "", nil, config.Connection{})().(tileMsg)
	if tm.err == nil || !strings.Contains(tm.err.Message, want) {
		t.Errorf("tile: %+v", tm.err)
	}
}

// …and for an installed plugin, under the heading the value was read from.
// Its section has to be written `demo@<pin>:`, which is what the CLI and mcp
// serve name; the TUI handed its requests the values alone, so a run, a live
// refresh and a tile all named plugins.demo.encoding — a key the file for a
// pinned plugin cannot have, sending the operator after a line that is not
// there.
func TestARefusedPinnedConfigValueNamesThePinnedHeading(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{{
		ID: "demo.encode", Summary: "encodes", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "encoding", Type: plugin.String, Default: "hex", Config: "encoding",
			Options: []string{"hex", "base32"}, Help: "encoding"}},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			return view.Text{Body: req.String("encoding")}, nil
		},
	}}}, registry.Origin{Path: "/usr/local/bin/rta-plugin-demo", Digest: "1a2b3c4d5e6f"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(config.Config{Plugins: map[string]map[string]any{
		"demo@1a2b3c4d5e6f": {"encoding": "b64"},
	}}); err != nil {
		t.Fatal(err)
	}
	written, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	resolver, problems := pluginconf.Resolve(written, reg.Origin)
	if len(problems) > 0 {
		t.Fatalf("the section was not honoured: %v", problems)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "demo.encode"}}}, resolver)
	c, _ := reg.Capability("demo.encode")
	const want = "which the config's plugins.demo@1a2b3c4d5e6f.encoding sets"

	refused := func(path string, cmd tea.Cmd) {
		t.Helper()
		var got *view.Error
		collect(t, cmd, func(msg tea.Msg) {
			switch msg := msg.(type) {
			case resultMsg:
				got = msg.err
			case tileMsg:
				got = msg.err
			}
		})
		if got == nil || !strings.Contains(got.Message, want) {
			t.Errorf("%s: %+v, want a refusal ending %q", path, got, want)
		}
	}
	refused("run", m.startRun(c, nil, false))
	refused("live refresh", m.refreshInPlace(c, nil, false))
	// Every command but the last, which is the tick arming the next round
	// five seconds out.
	batch := refreshTiles(m.tiles, 1, m.pluginCfg, m.connFor)().(tea.BatchMsg)
	refused("tile", tea.Batch(batch[:len(batch)-1]...))
}
