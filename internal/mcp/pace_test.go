package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

// owed is what the next call of key would wait, taking its token.
func owed(p *pacer, key string) time.Duration { return p.take(p.bucketFor(key)) }

func TestAPacerLetsABurstThroughAndThenHoldsToTheRate(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newPacer(3, 2, 8)
	p.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if d := owed(p, "a"); d != 0 {
			t.Fatalf("call %d of the burst was held %v", i+1, d)
		}
	}
	// Empty: the next owes one token, which is half a second at two a second,
	// and the one after owes that and another.
	if d := owed(p, "a"); d != 500*time.Millisecond {
		t.Fatalf("the first call past the burst is held %v, want 500ms", d)
	}
	if d := owed(p, "a"); d != time.Second {
		t.Fatalf("the second call past the burst is held %v, want 1s", d)
	}
	// Someone else is not held for it.
	if d := owed(p, "b"); d != 0 {
		t.Fatalf("another caller was held %v for a's calls", d)
	}
	// Time repays the debt: after ten seconds the bucket is full again.
	now = now.Add(10 * time.Second)
	for i := 0; i < 3; i++ {
		if d := owed(p, "a"); d != 0 {
			t.Fatalf("call %d after the debt was repaid was held %v", i+1, d)
		}
	}
}

func TestAPacerForgetsACallerWhoseBucketRefilled(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newPacer(2, 1, 4)
	p.now = func() time.Time { return now }
	owed(p, "a")
	now = now.Add(time.Minute)
	p.prune(now)
	if len(p.buckets) != 0 {
		t.Fatalf("a caller idle for a minute is still remembered: %v", p.buckets)
	}
}

func TestANilPacerHoldsNothing(t *testing.T) {
	var p *pacer
	if err := p.wait(context.Background(), "a"); err != nil {
		t.Fatalf("a nil pacer held a call: %v", err)
	}
}

// A call given up on while it waits is not run, and says so rather than
// holding the place it was waiting in.
func TestACallThatIsGivenUpOnWhileItWaitsDoesNotRun(t *testing.T) {
	p := newPacer(1, 0.5, 4)
	if err := p.wait(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := p.wait(ctx, "a"); err == nil {
		t.Fatal("a call that owed two seconds returned before its caller gave up")
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("a call given up on at 50ms waited %v", took)
	}
	if got := len(p.buckets["a"].waiting); got != 0 {
		t.Fatalf("a call given up on kept its place: %d held", got)
	}
}

// Calls sent without waiting for the replies all run at once, each in a
// goroutine of its own, and a wait that came after their rows held the replies
// and nothing else: the rate held a loop that read each answer and let one that
// did not through at whatever the record's lock allowed. The wait comes first,
// and the calls past the places wait for a place, so the rate is the rate for
// both.
func TestCallsSentWithoutWaitingForRepliesAreHeldToTheRateToo(t *testing.T) {
	p := newPacer(2, 100, 4)
	const calls = 42
	started := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.wait(context.Background(), "a")
		}()
	}
	wg.Wait()
	// Forty calls past the burst at a hundred a second.
	if took := time.Since(started); took < 300*time.Millisecond {
		t.Fatalf("%d calls at once took %v: a flood that does not wait for replies is not held to the rate", calls, took)
	}
}

// A scripted client calling a free read in a loop wrote a row per call as fast
// as the transport carried them and replaced the record's whole history in
// minutes. Past its burst a caller is held to the rate, a call that needs a
// grant is not part of it, and a refusal has its own backoff.
func TestACallThatNeedsNoGrantIsPacedPastTheBurst(t *testing.T) {
	s := connect(t, Options{pace: newPacer(2, 20, 4)})

	started := time.Now()
	for i := 0; i < 8; i++ {
		if res := callTool(t, s, "demo_item_list", map[string]any{"name": "x"}); res.IsError {
			t.Fatalf("call %d was refused", i+1)
		}
	}
	// Six calls past a burst of two, each owing a twentieth of a second.
	if took := time.Since(started); took < 250*time.Millisecond {
		t.Fatalf("eight calls took %v: a loop of free reads is not held to the rate", took)
	}
}

// The same through the server, with the calls in flight together, and the
// measure is the record: a hold after a call's row only delays the reply, and
// every row was written at once.
func TestAPipelinedLoopOfFreeCallsWritesRowsAtTheRate(t *testing.T) {
	s := connect(t, Options{pace: newPacer(2, 40, 4)})

	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.CallTool(context.Background(), &sdk.CallToolParams{
				Name: "demo_item_list", Arguments: map[string]any{"name": "x"}})
		}()
	}
	time.Sleep(250 * time.Millisecond)
	// A burst of two and ten more in a quarter of a second at forty a second;
	// the room is for a slow scheduler, and the loop it guards against wrote
	// all sixty.
	entries, err := agentlog.Read(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 30 {
		t.Errorf("%d rows in a quarter of a second at 40 a second: a pipelined loop is not held to the rate", len(entries))
	}
	wg.Wait()
}

func TestAGrantedCallIsNotPaced(t *testing.T) {
	s := connect(t, Options{pace: newPacer(1, 1, 4)})
	allow(t, "demo.item.set", "")
	started := time.Now()
	for i := 0; i < 5; i++ {
		if res := callTool(t, s, "demo_item_set", map[string]any{}); res.IsError {
			t.Fatalf("call %d was refused", i+1)
		}
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("five granted calls took %v: authority is spent per use, not paced", took)
	}
}
