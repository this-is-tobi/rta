package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// deadlineCap answers with the deadline its context carries, so a test can
// read what bounds a run rather than wait for the bound to fire.
func deadlineCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.deadline", Summary: "reports its deadline", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
				Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
				Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
		},
		Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
			if d, ok := ctx.Deadline(); ok {
				return view.Text{Body: "until " + d.Format(time.RFC3339Nano)}, nil
			}
			return view.Text{Body: "unbounded"}, nil
		},
	}
}

// deadlineOf reads the deadline back out of a deadlineCap result.
func deadlineOf(t *testing.T, msg tea.Msg) (time.Time, bool) {
	t.Helper()
	res, ok := msg.(resultMsg)
	if !ok {
		t.Fatalf("got %T, want resultMsg", msg)
	}
	if res.err != nil {
		t.Fatal(res.err)
	}
	body := res.view.(view.Text).Body
	if body == "unbounded" {
		return time.Time{}, false
	}
	d, err := time.Parse(time.RFC3339Nano, body[len("until "):])
	if err != nil {
		t.Fatal(err)
	}
	return d, true
}

// A run that opened a port-forward holds a hole into the operator's cluster
// for as long as it runs, and the running screen is modal to the keyboard,
// not to the clock: an external plugin whose handler never returns would
// hold that forward open for as long as nobody is at the keyboard. So the
// forward's lifetime — the run's context — keeps a ceiling even though the
// run itself carries none, and the ceiling is one rta can know, because it
// bounds an open forward rather than anybody's patience.
func TestARunThatOpenedAForwardIsBounded(t *testing.T) {
	fakeForward(t)
	conn := config.Connection{Kube: "homelab/databases/svc/postgres:5432"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, ok := deadlineOf(t, runCmd(ctx, 1, deadlineCap(), nil, false, nil, "homelab", nil, conn, false)())
	if !ok {
		t.Fatal("a run through a forward carries no deadline — the forward stays open until the handler decides")
	}
	// Measured after the call: the ceiling starts when the forward is up,
	// which is after the fake kubectl's own startup.
	if left := time.Until(d); left > forwardCeiling || left < forwardCeiling-time.Minute {
		t.Errorf("the forwarded run is bounded at %v from now, want about %v", left, forwardCeiling)
	}
}

// Quitting with a run in flight used to leave its context alive: tea.Quit
// ended the program, the kubectl child of a tunnelled run sits in its own
// process group, and nothing had cancelled the context that would have
// killed it. Cancelling on the way out is best effort — the process is
// leaving — but it is the only thing that makes the forward's teardown even
// reachable from ctrl+c.
func TestQuittingMidRunReleasesTheHandler(t *testing.T) {
	slow := plugin.Capability{
		ID: "demo.slow", Summary: "takes its time", Safety: plugin.Read,
		Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	started, cmd := sized.(Model).open(slow)
	if started.(Model).mode != modeRunning {
		t.Fatalf("mode = %v, want running", started.(Model).mode)
	}
	done := make(chan struct{})
	go func() { messagesOf(cmd); close(done) }()

	started.(Model).Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept running after ctrl+c — a forward it held would stay open past the program")
	}
}

// The thirty-second deadline was a TUI-only invention: the CLI runs a
// capability under a context with no deadline, MCP runs it under the
// request's, and only the TUI cut runs off — at a number several
// capabilities' own validated inputs exceed. net.trace's defaults are thirty
// hops of three probes of two seconds; http.* lets a caller state six
// hundred. Picking a bigger number does not fix it, because the ceiling
// would have to exceed the largest timeout any plugin in the registry lets
// a caller state, and that number is not rta's to know. What bounds a run
// asked for from a form is what bounds it on the CLI: the capability's own
// timeout input, its own budgets, and esc.
func TestAnAskedForRunCarriesNoDeadline(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	c := deadlineCap()
	c.Inputs = nil // a form would open otherwise, and the deadline is the point
	_, cmd := sized.(Model).open(c)
	for _, msg := range messagesOf(cmd) {
		if _, isResult := msg.(resultMsg); !isResult {
			continue
		}
		if d, bounded := deadlineOf(t, msg); bounded {
			t.Fatalf("an asked-for run is cut off at %v — the operator never set that", time.Until(d).Round(time.Second))
		}
		return
	}
	t.Fatal("the run produced no result")
}

// A cancellable context with no deadline has nothing that releases it when
// the run lands, where the thirty-second timer used to fire and clean up
// after itself. The result's arrival is what ends the run, so it is what
// releases the context — after the sequence check, since a stale result
// must not cancel the run the person moved on to.
func TestAFinishedRunReleasesItsContext(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	c := deadlineCap()
	c.Inputs = nil
	started, _ := sized.(Model).open(c)
	sm := started.(Model)
	if sm.cancelRun == nil {
		t.Fatal("no cancel recorded for the run in flight")
	}
	landed, _ := sm.Update(resultMsg{cap: c, view: view.Text{Body: "done"}, seq: sm.runSeq})
	if landed.(Model).cancelRun != nil {
		t.Error("the run landed and its context is still held")
	}
	// A stale result belongs to nobody and must not touch the current run:
	// a second run replaces the first, and the first's result arrives late.
	again, _ := sm.open(c)
	am := again.(Model)
	stale, _ := am.Update(resultMsg{cap: c, view: view.Text{Body: "late"}, seq: sm.runSeq})
	if stale.(Model).cancelRun == nil {
		t.Error("a stale result released the current run's context")
	}
}

// A tile's deadline is the dashboard's, and it used to fire in the handler's
// own words: a capability that returns ctx.Err() verbatim reached the tile
// as "<cap>.failed  context deadline exceeded", which names neither the
// deadline that fired nor the fact that opening the tile on its own screen
// has none. Lowered rather than waited for, saved and restored the way
// fetchFromCluster moves completeTimeout — and no t.Parallel, since it is a
// package-level value.
func TestADashboardTileThatMissesItsDeadlineNamesTheDeadlineAndTheWayOut(t *testing.T) {
	saved := refreshTimeout
	refreshTimeout = 50 * time.Millisecond
	t.Cleanup(func() { refreshTimeout = saved })
	stuck := tile{cap: plugin.Capability{
		ID: "demo.stuck", Summary: "never answers", Safety: plugin.Read,
		Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}
	msg := tileCmd(0, stuck, nil, "", nil, config.Connection{})().(tileMsg)
	if msg.err == nil {
		t.Fatal("a tile that never answered reported success")
	}
	if msg.err.Code != "tui.refresh.timeout" {
		t.Errorf("code = %s, want tui.refresh.timeout: %s", msg.err.Code, msg.err.Message)
	}
	if !strings.Contains(msg.err.Message, refreshTimeout.String()) {
		t.Errorf("the message should name the deadline that fired: %q", msg.err.Message)
	}
	if !strings.Contains(msg.err.Hint, "enter") {
		t.Errorf("the hint should name the way out: %q", msg.err.Hint)
	}
}

// The running screen's footer said "esc  leave it running", and esc has
// never done that: runningKeys cancels the context and flashes "cancelled".
// With no deadline on an asked-for run the wrong verb matters more — the
// key that ends a hung run is the one the bar has to name correctly.
func TestTheRunningFooterOffersToStopTheRunRatherThanLeaveIt(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m.mode = modeRunning
	var stop *hintItem
	for i, it := range m.footerItems(modeRunning) {
		for _, k := range it.keys {
			if k == "esc" {
				stop = &m.footerItems(modeRunning)[i]
			}
		}
	}
	if stop == nil {
		t.Fatal("the running screen advertises nothing on esc")
	}
	if strings.Contains(stop.label, "leave") || !strings.Contains(stop.label, "stop") {
		t.Errorf("esc is labelled %q, and what it does is stop the run", stop.label)
	}
	// The binding it advertises is the one runningKeys acts on: pressing it
	// with a run in flight cancels that run.
	c := deadlineCap()
	c.Inputs = nil
	started, _ := m.open(c)
	sm := started.(Model)
	after, _ := sm.Update(keyMsg("esc"))
	if after.(Model).mode == modeRunning || after.(Model).flash != "cancelled" {
		t.Errorf("esc on the running screen: mode %v, flash %q — want the run cancelled", after.(Model).mode, after.(Model).flash)
	}
}

// A live view refreshes under the result screen, so a run can be in flight
// there too: quit cancels it from every screen, not only the running one.
func TestQuittingFromAResultReleasesTheRefreshInFlight(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	sm := sized.(Model)
	sm.mode = modeResult
	released := false
	sm.cancelRun = func() { released = true }
	sm.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !released {
		t.Fatal("ctrl+c on the result screen left the refresh in flight running")
	}
}

// The dry run that previews a destructive run is unbounded on the same
// reasoning as the run, and reaches its context through startConfirm rather
// than startRun.
func TestAPreviewCarriesNoDeadlineEither(t *testing.T) {
	m := New(testRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	c := deadlineCap()
	c.Inputs = nil
	c.Safety = plugin.Destructive
	_, cmd := sized.(Model).open(c)
	for _, msg := range messagesOf(cmd) {
		p, isPreview := msg.(previewMsg)
		if !isPreview {
			continue
		}
		if p.err != nil {
			t.Fatal(p.err)
		}
		if body := p.view.(view.Text).Body; body != "unbounded" {
			t.Fatalf("the preview ran under a deadline: %s", body)
		}
		return
	}
	t.Fatal("the dry run produced no preview")
}

// The forward's own expiry reaches the tile in the dashboard's words too: the
// check sits above both error branches, and this is the Dial one. A kubectl
// that never reports a forward is what makes Dial wait out the deadline.
func TestATileWhoseForwardNeverComesUpNamesTheDashboardsDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"),
		[]byte("#!/bin/sh\nwhile true; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	saved := refreshTimeout
	refreshTimeout = 150 * time.Millisecond
	t.Cleanup(func() { refreshTimeout = saved })
	var seen string
	ti := reachedTile(t, &seen)
	msg := tileCmd(0, ti, nil, "homelab", nil, config.Connection{Kube: "homelab/databases/svc/postgres:5432"})().(tileMsg)
	if msg.err == nil || msg.err.Code != "tui.refresh.timeout" {
		t.Fatalf("a forward that never came up: %v, want tui.refresh.timeout", msg.err)
	}
	if seen != "" {
		t.Errorf("the handler ran anyway, against %s", seen)
	}
}
