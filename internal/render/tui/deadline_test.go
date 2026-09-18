package tui

import (
	"context"
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
