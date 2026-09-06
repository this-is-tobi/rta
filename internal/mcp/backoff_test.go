package mcp

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func clocked(free int, window, step, max time.Duration) (*backoff, *time.Time) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	b := newBackoff(free, window, step, max)
	b.now = func() time.Time { return now }
	return b, &now
}

func TestTheFirstFailuresAreFreeAndTheRestDoubleToACap(t *testing.T) {
	b, _ := clocked(3, time.Minute, 100*time.Millisecond, 350*time.Millisecond)
	want := []time.Duration{0, 0, 0, 100 * time.Millisecond, 200 * time.Millisecond, 350 * time.Millisecond, 350 * time.Millisecond}
	for i, w := range want {
		if got := b.delay("k"); got != w {
			t.Fatalf("failure %d: delay = %s, want %s", i+1, got, w)
		}
	}
}

func TestAQuietWindowForgetsTheCaller(t *testing.T) {
	b, now := clocked(1, time.Minute, 100*time.Millisecond, time.Second)
	b.delay("k")
	if got := b.delay("k"); got != 100*time.Millisecond {
		t.Fatalf("second failure = %s, want the first step", got)
	}
	*now = now.Add(2 * time.Minute)
	if got := b.delay("k"); got != 0 {
		t.Fatalf("after a quiet window = %s, want free again", got)
	}
}

func TestCallersAreChargedSeparately(t *testing.T) {
	b, _ := clocked(1, time.Minute, 100*time.Millisecond, time.Second)
	b.delay("a")
	b.delay("a")
	if got := b.delay("b"); got != 0 {
		t.Fatalf("b's first failure = %s, charged for a's", got)
	}
}

func TestManyDoublingsStayAtTheCap(t *testing.T) {
	b, _ := clocked(0, time.Minute, time.Millisecond, time.Second)
	var got time.Duration
	for i := 0; i < 80; i++ {
		got = b.delay("k")
	}
	if got != time.Second {
		t.Fatalf("after 80 failures = %s, want the cap", got)
	}
}

func TestAFullTableDropsTheIdleAndStopsTrackingTheRest(t *testing.T) {
	b, now := clocked(0, time.Minute, time.Millisecond, time.Second)
	for i := 0; i < maxKeys; i++ {
		b.delay(string(rune('a'+i%26)) + string(rune(i)))
	}
	if got := b.delay("one more"); got != 0 {
		t.Fatalf("past the table = %s, want untracked", got)
	}
	*now = now.Add(2 * time.Minute)
	if got := b.delay("after the window"); got != time.Millisecond {
		t.Fatalf("first failure after pruning = %s, want it tracked again at the first step", got)
	}
	if len(b.strikes) != 1 {
		t.Fatalf("%d callers remembered after a quiet window, want 1", len(b.strikes))
	}
}

func TestANilBackoffCostsNothing(t *testing.T) {
	var b *backoff
	if got := b.delay("k"); got != 0 {
		t.Fatal(got)
	}
	b.hold(context.Background(), "k")
}

func TestHoldReturnsWhenTheCallerGoesAway(t *testing.T) {
	b := newBackoff(0, time.Minute, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	b.hold(ctx, "k")
	if time.Since(start) > time.Second {
		t.Fatal("hold outlived its context")
	}
}

func TestRemoteHostDropsThePortAndIgnoresForwardedHeaders(t *testing.T) {
	req := &http.Request{RemoteAddr: "10.0.0.7:51234", Header: http.Header{"X-Forwarded-For": {"1.2.3.4"}}}
	if got := remoteHost(req); got != "10.0.0.7" {
		t.Fatalf("remoteHost = %q", got)
	}
	req.RemoteAddr = "[::1]:8443"
	if got := remoteHost(req); got != "::1" {
		t.Fatalf("remoteHost = %q", got)
	}
	if got := remoteHost(nil); got != "" {
		t.Fatalf("remoteHost(nil) = %q", got)
	}
}
