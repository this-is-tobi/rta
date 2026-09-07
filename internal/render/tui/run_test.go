package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
