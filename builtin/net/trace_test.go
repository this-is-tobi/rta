package net

import (
	"context"
	"encoding/binary"
	"fmt"
	stdnet "net"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// quotedIPv4 builds the payload an ICMP error carries: the IP header of the
// packet that caused it, plus the first 8 bytes of that packet's own header.
func quotedIPv4(ihlWords byte, seq uint16) []byte {
	header := make([]byte, int(ihlWords)*4)
	header[0] = 0x40 | ihlWords // version 4, IHL in 32-bit words
	icmpHeader := make([]byte, 8)
	binary.BigEndian.PutUint16(icmpHeader[4:6], 0xbeef) // id: kernel-chosen, ignored
	binary.BigEndian.PutUint16(icmpHeader[6:8], seq)
	return append(header, icmpHeader...)
}

// Matching a hop reply to its probe rests entirely on the sequence number
// recovered from the quoted packet — the echo ID is unusable on the
// unprivileged sockets this runs on, since the kernel picks it.
func TestQuotedSeqRecoversTheProbe(t *testing.T) {
	// A plain 20-byte header, and one carrying options (24 bytes).
	for _, ihl := range []byte{5, 6} {
		got, ok := quotedSeq(quotedIPv4(ihl, 4242), true)
		if !ok || got != 4242 {
			t.Errorf("ihl=%d: quotedSeq = %d, ok=%v, want 4242", ihl, got, ok)
		}
	}
}

func TestQuotedSeqIPv6(t *testing.T) {
	data := make([]byte, 40+8)
	binary.BigEndian.PutUint16(data[46:48], 7)
	got, ok := quotedSeq(data, false)
	if !ok || got != 7 {
		t.Errorf("quotedSeq = %d, ok=%v, want 7", got, ok)
	}
}

// A truncated or nonsensical quote must be rejected rather than read out of
// bounds — this parses bytes that came off the wire from a stranger.
func TestQuotedSeqRejectsTruncated(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":         {},
		"header only":   quotedIPv4(5, 1)[:20],
		"short ihl":     {0x41, 0, 0, 0, 0, 0, 0, 0},
		"partial quote": quotedIPv4(5, 1)[:24],
		"short ipv6":    make([]byte, 40),
	} {
		if _, ok := quotedSeq(data, !strings.Contains(name, "ipv6")); ok {
			t.Errorf("%s: accepted a malformed quote", name)
		}
	}
}

func TestHopRTTCell(t *testing.T) {
	for name, tc := range map[string]struct {
		h    hop
		want string
	}{
		"silent":  {hop{lost: 3}, "*"},
		"replied": {hop{rtts: []time.Duration{time.Millisecond * 12}}, "12.0 ms"},
		"partial": {hop{rtts: []time.Duration{2 * time.Millisecond}, lost: 1}, "2.0 * ms"},
	} {
		if got := tc.h.rttCell(); got != tc.want {
			t.Errorf("%s: rttCell = %q, want %q", name, got, tc.want)
		}
	}
}

func TestHopStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		h    hop
		want string
	}{
		"target":  {hop{addr: "1.1.1.1", final: true}, "target"},
		"silent":  {hop{lost: 3}, "silent"},
		"partial": {hop{addr: "10.0.0.1", lost: 1}, "warn"},
		"clean":   {hop{addr: "10.0.0.1"}, "ok"},
	} {
		if got := tc.h.status(); got != tc.want {
			t.Errorf("%s: status = %q, want %q", name, got, tc.want)
		}
	}
}

func TestTraceBadHostIsCoded(t *testing.T) {
	_, err := runTrace(context.Background(), req(map[string]any{
		"host": "no-such-host.invalid", "max-hops": 1, "probes": 1, "timeout": 1,
	}))
	ve := view.AsError(err, "x")
	if ve.Code != "net.trace.resolve" {
		t.Errorf("want net.trace.resolve, got %+v", ve)
	}
}

// Tracing the loopback is the shortest possible route: one hop, and it is
// the target. It exercises the whole send/receive path without leaving the
// machine — but ICMP sockets are not always available in a sandbox.
func TestTraceLoopbackReachesTargetInOneHop(t *testing.T) {
	v, err := runTrace(context.Background(), req(map[string]any{
		"host": "127.0.0.1", "max-hops": 5, "probes": 1, "timeout": 1, "resolve": false,
	}))
	if err != nil {
		if ve := view.AsError(err, "x"); ve.Code == "net.trace.socket" {
			t.Skipf("unprivileged ICMP unavailable here: %v", ve.Message)
		}
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	route, ok := s.Items[1].View.(view.Table)
	if !ok {
		t.Fatalf("route section = %s", view.TypeOf(s.Items[1].View))
	}
	if len(route.Rows) != 1 {
		t.Fatalf("loopback route = %v, want one hop", route.Rows)
	}
	last := route.Rows[0]
	if last[1] != "127.0.0.1" || last[4] != "target" {
		t.Errorf("hop = %v, want the loopback marked as the target", last)
	}
}

// route is a fake tracer: every TTL answers from a router of its own, never
// the target, and the router at cancelAt cancels the run as it answers — the
// way esc in the TUI, a tile's deadline or ctrl+c interrupts a real trace
// between two hops.
type route struct {
	cancelAt int
	cancel   context.CancelFunc
}

func (r *route) probe(_ context.Context, ttl, _ int, _ time.Duration) (probeResult, error) {
	if ttl == r.cancelAt {
		r.cancel()
	}
	return probeResult{addr: fmt.Sprintf("10.0.0.%d", ttl), rtt: time.Millisecond}, nil
}

func (r *route) Close() error { return nil }

func withRoute(t *testing.T, r *route) {
	t.Helper()
	old := newTracer
	newTracer = func(stdnet.IP) (prober, error) { return r, nil }
	t.Cleanup(func() { newTracer = old })
}

// A run stopped between hops keeps the hops it collected. Three routers
// answered before the cancel — the third with one of its two probes — and
// that is the route, with the summary saying where and why it stopped,
// rather than the error alone with the work thrown away.
func TestTraceStoppedMidwayKeepsTheHopsItCollected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	withRoute(t, &route{cancelAt: 3, cancel: cancel})
	v, err := runTrace(ctx, req(map[string]any{
		"host": "127.0.0.1", "max-hops": 10, "probes": 2, "timeout": 1, "resolve": false,
	}))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	hops := s.Items[1].View.(view.Table)
	if len(hops.Rows) != 3 || hops.Rows[2][1] != "10.0.0.3" || hops.Rows[2][3] != "1.0 ms" {
		t.Fatalf("route = %v, want the three hops that answered, the last with its one probe", hops.Rows)
	}
	summary := pairs(t, s.Items[0].View)
	if summary["reached"] != "no" || summary["hops"] != "3" {
		t.Errorf("summary = %v", summary)
	}
	if note := summary["note"]; !strings.Contains(note, "stopped after 3 hops") || !strings.Contains(note, "cancelled") {
		t.Errorf("note = %q, want where the trace stopped and why", note)
	}
}

// Stopped before the first probe went out there is no route to show, and
// the error says the run was stopped rather than that the trace failed.
func TestTraceStoppedBeforeTheFirstHopIsCoded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	withRoute(t, &route{})
	_, err := runTrace(ctx, req(map[string]any{"host": "127.0.0.1", "max-hops": 3, "probes": 1, "timeout": 1}))
	if ve := view.AsError(err, "x"); ve.Code != "net.trace.stopped" {
		t.Errorf("want net.trace.stopped, got %+v", ve)
	}
}
