package net

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	stdnet "net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// tracePayload rides along in every probe so a capture shows who sent it.
var tracePayload = []byte("rta net trace")

// tracer sends echo probes with a chosen TTL and reads back whatever ICMP
// says about them. It uses an unprivileged datagram ICMP socket — the same
// mechanism net.ping relies on — so tracing needs no root.
type tracer struct {
	conn     *icmp.PacketConn
	dst      stdnet.Addr
	echoType icmp.Type
	proto    int
	v4       bool
	id       int
}

// prober is what runTrace needs from a tracer: one probe at a time, and a
// close. It is the seam a test fakes a route through, since a real one
// needs an ICMP socket and a network with more than one hop on it.
type prober interface {
	probe(ctx context.Context, ttl, seq int, timeout time.Duration) (probeResult, error)
	Close() error
}

var newTracer = func(ip stdnet.IP) (prober, error) {
	t, err := openTracer(ip)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func openTracer(ip stdnet.IP) (*tracer, error) {
	id := os.Getpid() & 0xffff
	if ip4 := ip.To4(); ip4 != nil {
		conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
		if err != nil {
			return nil, err
		}
		return &tracer{
			conn: conn, dst: &stdnet.UDPAddr{IP: ip4}, echoType: ipv4.ICMPTypeEcho,
			proto: ipv4.ICMPTypeEcho.Protocol(), v4: true, id: id,
		}, nil
	}
	conn, err := icmp.ListenPacket("udp6", "::")
	if err != nil {
		return nil, err
	}
	return &tracer{
		conn: conn, dst: &stdnet.UDPAddr{IP: ip}, echoType: ipv6.ICMPTypeEchoRequest,
		proto: ipv6.ICMPTypeEchoRequest.Protocol(), id: id,
	}, nil
}

func (t *tracer) Close() error { return t.conn.Close() }

func (t *tracer) setTTL(ttl int) error {
	if t.v4 {
		return t.conn.IPv4PacketConn().SetTTL(ttl)
	}
	return t.conn.IPv6PacketConn().SetHopLimit(ttl)
}

// quotedSeq recovers the sequence number of the probe an ICMP error is
// about. The error quotes the packet that caused it — original IP header
// plus the first 8 bytes of its payload — and the sequence number is the one
// field we control end to end. (The echo ID is not: an unprivileged datagram
// socket lets the kernel choose it, so matching on ID would never work.)
func quotedSeq(data []byte, v4 bool) (int, bool) {
	headerLen := 40 // IPv6 headers are fixed-size
	if v4 {
		if len(data) < 1 {
			return 0, false
		}
		headerLen = int(data[0]&0x0f) * 4
	}
	// The quoted ICMP header: type, code, checksum, id, seq.
	if headerLen < 20 || len(data) < headerLen+8 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(data[headerLen+6 : headerLen+8])), true
}

// peerAddr renders the address an ICMP message came from.
func peerAddr(addr stdnet.Addr) string {
	switch a := addr.(type) {
	case *stdnet.UDPAddr:
		return a.IP.String()
	case *stdnet.IPAddr:
		return a.IP.String()
	}
	return addr.String()
}

// probeResult is what one probe learned: who answered, how fast, and whether
// that answer came from the destination itself.
type probeResult struct {
	addr  string // "" when nothing answered in time
	rtt   time.Duration
	final bool
}

// probe sends one echo at the given TTL and waits for either the router that
// dropped it (time exceeded) or the destination (echo reply). A probe that
// goes unanswered is not an error: plenty of routers decline to speak.
func (t *tracer) probe(ctx context.Context, ttl, seq int, timeout time.Duration) (probeResult, error) {
	if err := t.setTTL(ttl); err != nil {
		return probeResult{}, err
	}
	msg := icmp.Message{
		Type: t.echoType, Code: 0,
		Body: &icmp.Echo{ID: t.id, Seq: seq, Data: tracePayload},
	}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return probeResult{}, err
	}
	start := time.Now()
	if _, err := t.conn.WriteTo(wire, t.dst); err != nil {
		return probeResult{}, err
	}

	deadline := start.Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	buf := make([]byte, 1500)
	for {
		if err := t.conn.SetReadDeadline(deadline); err != nil {
			return probeResult{}, err
		}
		n, peer, err := t.conn.ReadFrom(buf)
		if err != nil {
			return probeResult{}, nil //nolint:nilerr // a deadline is a silent hop, which is a result and not a failure
		}
		m, perr := icmp.ParseMessage(t.proto, buf[:n])
		if perr != nil {
			continue
		}
		switch body := m.Body.(type) {
		case *icmp.TimeExceeded:
			if s, ok := quotedSeq(body.Data, t.v4); ok && s == seq {
				return probeResult{addr: peerAddr(peer), rtt: time.Since(start)}, nil
			}
		case *icmp.DstUnreach:
			// The route ends here, one way or another.
			if s, ok := quotedSeq(body.Data, t.v4); ok && s == seq {
				return probeResult{addr: peerAddr(peer), rtt: time.Since(start), final: true}, nil
			}
		case *icmp.Echo:
			if body.Seq == seq {
				return probeResult{addr: peerAddr(peer), rtt: time.Since(start), final: true}, nil
			}
		}
	}
}

// hop collects every probe sent at one TTL.
type hop struct {
	ttl   int
	addr  string
	name  string
	rtts  []time.Duration
	lost  int
	final bool
}

// rttCell summarizes a hop's timings the way traceroute does: each probe,
// then the unit once.
func (h hop) rttCell() string {
	if len(h.rtts) == 0 {
		return "*"
	}
	parts := make([]string, 0, len(h.rtts)+h.lost)
	for _, rtt := range h.rtts {
		parts = append(parts, fmt.Sprintf("%.1f", float64(rtt.Microseconds())/1000))
	}
	for range h.lost {
		parts = append(parts, "*")
	}
	return strings.Join(parts, " ") + " ms"
}

func (h hop) status() string {
	switch {
	case h.final:
		return "target"
	case h.addr == "":
		return "silent"
	case h.lost > 0:
		return "warn"
	default:
		return "ok"
	}
}

// resolveHops reverse-resolves hop addresses concurrently and best-effort:
// a name is a convenience, never worth failing or stalling a trace for.
func resolveHops(ctx context.Context, hops []hop) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range hops {
		if hops[i].addr == "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			names, err := stdnet.DefaultResolver.LookupAddr(ctx, hops[i].addr)
			if err == nil && len(names) > 0 {
				hops[i].name = strings.TrimSuffix(names[0], ".")
			}
		}(i)
	}
	wg.Wait()
}

func runTrace(ctx context.Context, req plugin.Request) (view.View, error) {
	host := req.String("host")
	maxHops := min(max(req.Int("max-hops"), 1), 64)
	probes := min(max(req.Int("probes"), 1), 10)
	timeout := time.Duration(max(req.Int("timeout"), 1)) * time.Second

	ips, err := stdnet.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, view.Errorf("net.trace.resolve", "resolving %s: %v", host, err)
	}
	target := ips[0]

	tr, err := newTracer(target)
	if err != nil {
		return nil, view.Errorf("net.trace.socket", "opening an ICMP socket: %v", err).
			WithHint("on Linux, unprivileged ICMP may need: sysctl -w net.ipv4.ping_group_range=\"0 2147483647\"")
	}
	defer func() { _ = tr.Close() }()

	// A stopped run keeps the hops it collected. esc in the TUI, a tile's
	// refresh deadline and ctrl+c all end the context between two probes,
	// and the routers that answered before that are the answer the person
	// was waiting for — a partial route with a note saying where it stopped,
	// not the error alone with the work thrown away. The hop in progress
	// keeps the probes it had sent: fewer timings than asked for is still
	// what that router said.
	var hops []hop
	var stopped error
	seq := 0
probing:
	for ttl := 1; ttl <= maxHops; ttl++ {
		h := hop{ttl: ttl}
		sent := 0
		for range probes {
			if stopped = ctx.Err(); stopped != nil {
				if sent > 0 {
					hops = append(hops, h)
				}
				break probing
			}
			seq = (seq + 1) & 0xffff
			res, err := tr.probe(ctx, ttl, seq, timeout)
			if err != nil {
				return nil, view.Errorf("net.trace.failed", "probing hop %d: %v", ttl, err)
			}
			sent++
			switch res.addr {
			case "":
				h.lost++
			default:
				h.addr = res.addr
				h.rtts = append(h.rtts, res.rtt)
				h.final = h.final || res.final
			}
		}
		hops = append(hops, h)
		if h.final {
			break
		}
	}
	if stopped != nil && len(hops) == 0 {
		return nil, view.Errorf("net.trace.stopped", "stopped before the first probe went out: %s", stopReason(stopped))
	}
	if req.Bool("resolve") {
		resolveHops(ctx, hops)
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Hop", Kind: view.KindNumber},
		{Name: "Address"},
		{Name: "Host"},
		{Name: "RTT", Kind: view.KindDuration},
		{Name: "State", Kind: view.KindStatus},
	}}
	for _, h := range hops {
		addr := h.addr
		if addr == "" {
			addr = "*"
		}
		t.Rows = append(t.Rows, []string{strconv.Itoa(h.ttl), addr, h.name, h.rttCell(), h.status()})
	}
	t.Total = len(t.Rows)

	reached := len(hops) > 0 && hops[len(hops)-1].final
	summary := view.KeyValue{Pairs: []view.Pair{
		{Key: "target", Value: fmt.Sprintf("%s (%s)", host, target)},
		{Key: "hops", Value: strconv.Itoa(len(hops))},
		{Key: "probes", Value: fmt.Sprintf("%d per hop, %s timeout", probes, timeout)},
		{Key: "reached", Value: map[bool]string{true: "yes", false: "no"}[reached]},
	}}
	switch {
	case stopped != nil:
		summary.Pairs = append(summary.Pairs, view.Pair{Key: "note",
			Value: fmt.Sprintf("stopped after %s: %s", format.CountOf(len(hops), "hop"), stopReason(stopped))})
	case !reached:
		summary.Pairs = append(summary.Pairs, view.Pair{Key: "note",
			Value: "stopped after " + format.CountOf(len(hops), "hop") + " without reaching the target"})
	}
	return view.Sections{Items: []view.Section{
		{ID: "trace", Title: "trace", View: summary},
		{ID: "route", Title: "route", View: t},
	}}, nil
}

// stopReason names why a run ended early in the words of whoever ended it:
// esc and ctrl+c cancel, a tile's refresh has a deadline.
func stopReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "the deadline passed"
	}
	return "the run was cancelled"
}
