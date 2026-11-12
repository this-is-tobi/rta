package mcp

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A call the client cancels while it runs is recorded as the client's cancel,
// not as the capability failing in its own words: the work stopped because
// its context was cancelled, and the record said `fs.slow.read: context
// canceled`, a file it could not read.
func TestACallTheClientCancelsIsRecordedAsCancelled(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	started := make(chan struct{})
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "fs", Summary: "fs", Capabilities: []plugin.Capability{{
		ID: "fs.slow", Summary: "s", Safety: plugin.Read,
		Run: func(ctx context.Context, _ plugin.Request) (view.View, error) {
			close(started)
			<-ctx.Done()
			return nil, view.Errorf("fs.slow.read", "reading big.bin: %v", ctx.Err())
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(reg, "test", Options{Origin: reg.Origin})
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.CallTool(ctx, &sdk.CallToolParams{Name: "fs_slow", Arguments: map[string]any{}})
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the call never started")
	}
	cancel()
	<-done

	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, err := agentlog.Read(10)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Cap != "fs.slow" {
				continue
			}
			if e.Outcome != agentlog.Failed || e.Code != "core.mcp.cancelled" {
				t.Fatalf("recorded %s %s %q, want failed core.mcp.cancelled", e.Outcome, e.Code, e.Reason)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancelled call was never recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
