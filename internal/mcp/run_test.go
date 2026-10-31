package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// blockingRegistry is one read capability that says it has begun and then
// waits: on its context where honoursContext, for release where not.
func blockingRegistry(t *testing.T, honoursContext bool, started chan<- struct{}, release <-chan struct{}) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "hold", Summary: "hold",
		Capabilities: []plugin.Capability{{
			ID: "hold.wait", Summary: "waits", Safety: plugin.Read, Idempotent: true,
			Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
				started <- struct{}{}
				if honoursContext {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				<-release
				return view.Text{Body: "released"}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// settleRecord waits for the write of a row the test has just seen in the
// record to end, for a test whose call finishes after the test has stopped
// waiting on it: the client hung up, or the server stopped without it.
//
// **The row is not the end of the write.** agentlog.Append writes the record's
// end mark after the row, and a test that ended on seeing the row let the mark
// land in whatever data directory the next test had set by then: a mark with
// no key beside it is a record whose key is missing, so every call needing a
// grant in that test was refused with "agent-log.key is missing beside an
// existing record" — TestALapsedSwitchStopsBounding failing on a slow runner,
// for a switch it had nothing to do with. Writable takes the lock Append
// holds to its end, so it returns only once that write has.
func settleRecord(t *testing.T) {
	t.Helper()
	if err := agentlog.Writable(); err != nil {
		t.Logf("waiting for the record to settle: %v", err)
	}
}

// The SDK cuts a handler's context loose from the one the server runs under,
// so a stop asked of the server ended nothing that was running: a call went on
// for as long as it liked, and a plugin that never answered held the process
// until a second signal.
func TestStoppingTheServerCancelsTheCallsInFlight(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	started := make(chan struct{}, 1)
	stop, stopped := context.WithCancel(context.Background())
	defer stopped()
	session := connectWith(t, blockingRegistry(t, true, started, nil), Options{Shutdown: stop})

	answered := make(chan *sdk.CallToolResult, 1)
	go func() {
		res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "hold_wait"})
		if err != nil {
			t.Errorf("hold_wait: %v", err)
		}
		answered <- res
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the call never began")
	}

	stopped()

	select {
	case res := <-answered:
		if res == nil || !res.IsError {
			t.Errorf("the cancelled call answered %+v, want an error", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call was still running five seconds after the server was told to stop")
	}
}

// What does not honour its context is waited for as long as the grace and no
// longer, as the HTTP listener waits.
func TestRunReturnsWithinItsGraceWhateverACallDoes(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	// The call is still running when Run returns, and writes its row to the
	// ledger when it ends: let it end while this test's data directory is the
	// one in force, and wait for the row, or it lands in the next test's — and
	// past the row, for the end of its write (settleRecord).
	t.Cleanup(func() {
		close(release)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if rows, err := agentlog.Read(10); err == nil && len(rows) > 0 {
				settleRecord(t)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	settled := false
	saved := settle
	settle = func() func() { settled = true; return func() {} }
	t.Cleanup(func() { settle = saved })

	ctx, stopped := context.WithCancel(context.Background())
	defer stopped()
	server := NewServer(blockingRegistry(t, false, started, release), "test", Options{Shutdown: ctx})
	st, ct := sdk.NewInMemoryTransports()
	var stderr bytes.Buffer
	ran := make(chan error, 1)
	go func() { ran <- Run(ctx, server, st, 200*time.Millisecond, &stderr) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = session.CallTool(context.Background(), &sdk.CallToolParams{Name: "hold_wait"})
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the call never began")
	}

	stopped()

	select {
	case err := <-ran:
		if err != nil {
			t.Errorf("Run = %v, want nil: a stop that was asked for is not a failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run was still waiting for the call five seconds after the stop, with a grace of 200ms")
	}
	if !settled {
		t.Error("Run returned without letting a write in progress finish (shutdown.Settle)")
	}
	if !strings.Contains(stderr.String(), "calls still running") {
		t.Errorf("Run said nothing of the call it ended without: %q", stderr.String())
	}
}
