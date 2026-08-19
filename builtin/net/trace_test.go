package net

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// quotedIPv4 builds the payload an ICMP error carries: the IP header of the
// packet that caused it, plus the first 8 bytes of that packet's own header.
func quotedIPv4(ihlWords, seq int) []byte {
	header := make([]byte, ihlWords*4)
	header[0] = byte(0x40 | ihlWords) // version 4, IHL in 32-bit words
	icmpHeader := make([]byte, 8)
	binary.BigEndian.PutUint16(icmpHeader[4:6], 0xbeef) // id: kernel-chosen, ignored
	binary.BigEndian.PutUint16(icmpHeader[6:8], uint16(seq))
	return append(header, icmpHeader...)
}

// Matching a hop reply to its probe rests entirely on the sequence number
// recovered from the quoted packet — the echo ID is unusable on the
// unprivileged sockets this runs on, since the kernel picks it.
func TestQuotedSeqRecoversTheProbe(t *testing.T) {
	// A plain 20-byte header, and one carrying options (24 bytes).
	for _, ihl := range []int{5, 6} {
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
