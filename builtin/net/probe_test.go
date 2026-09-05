package net

import (
	"context"
	stdnet "net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestUnescape(t *testing.T) {
	if got := unescape(`EHLO rta\r\n`); got != "EHLO rta\r\n" {
		t.Errorf("unescape = %q", got)
	}
	if got := unescape(`a\\nb`); got != `a\nb` {
		t.Errorf("escaped backslash = %q", got)
	}
}

// A service on the other end is not trusted output: control bytes must not
// reach the terminal, but the line structure that makes a banner readable
// has to survive.
func TestPrintableNeutralizesControlBytes(t *testing.T) {
	got := printable([]byte("220 mail\r\nline\ttab\x00\x1b[31mred\n"))
	if strings.ContainsAny(got, "\x00\x1b\r") {
		t.Errorf("control bytes survived: %q", got)
	}
	if !strings.Contains(got, "220 mail\nline\ttab") {
		t.Errorf("readable structure lost: %q", got)
	}
}

// listenOnce serves one connection with the given greeting (empty = silence)
// and returns its port.
func listenOnce(t *testing.T, greeting string) int {
	t.Helper()
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if greeting != "" {
			conn.Write([]byte(greeting))
		}
		// Hold the connection open so the probe's wait window, not an EOF,
		// is what ends the read.
		time.Sleep(500 * time.Millisecond)
	}()
	return ln.Addr().(*stdnet.TCPAddr).Port
}

func probeSections(t *testing.T, values map[string]any) view.Sections {
	t.Helper()
	v, err := runProbe(context.Background(), req(values))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	return s
}

func TestProbeReportsBanner(t *testing.T) {
	port := listenOnce(t, "SSH-2.0-OpenSSH_9.6\r\n")
	s := probeSections(t, map[string]any{
		"host": "127.0.0.1", "port": port, "timeout": 2, "wait": 2,
	})
	body := s.Items[1].View.(view.Text).Body
	if !strings.Contains(body, "SSH-2.0-OpenSSH_9.6") {
		t.Errorf("banner = %q", body)
	}
	kv := s.Items[0].View.(view.KeyValue)
	var target string
	for _, p := range kv.Pairs {
		if p.Key == "target" {
			target = p.Value
		}
	}
	if target != "127.0.0.1:"+strconv.Itoa(port) {
		t.Errorf("target = %q", target)
	}
}

// Silence is a real answer and the most confusing one, so it gets an
// explanation rather than an empty pane.
func TestProbeSilenceExplainsItself(t *testing.T) {
	port := listenOnce(t, "")
	s := probeSections(t, map[string]any{
		"host": "127.0.0.1", "port": port, "timeout": 2, "wait": 1,
	})
	body := s.Items[1].View.(view.Text).Body
	if !strings.Contains(body, "said nothing") || !strings.Contains(body, "net send") {
		t.Errorf("silent response = %q", body)
	}
}

// Speaking first is net.send, not net.probe: writing attacker-chosen bytes to
// an arbitrary port is a write, and shipping it as Read put it on every MCP
// server with no grant and a readOnlyHint the client was entitled to believe.
func TestSendThenRead(t *testing.T) {
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		conn.Write([]byte("ECHO:" + string(buf[:n])))
		time.Sleep(300 * time.Millisecond)
	}()

	v, err := runSend(context.Background(), plugin.NewRequest(map[string]any{
		"host": "127.0.0.1", "port": ln.Addr().(*stdnet.TCPAddr).Port,
		"data": `PING\r\n`, "timeout": 2, "wait": 2,
	}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	s := v.(view.Sections)
	if body := s.Items[1].View.(view.Text).Body; !strings.Contains(body, "ECHO:PING") {
		t.Errorf("response to --data = %q", body)
	}
}

// net.probe only ever listens now, so the write path is unreachable from it.
func TestSendRefusesAnEmptyPayload(t *testing.T) {
	_, err := runSend(context.Background(), plugin.NewRequest(map[string]any{
		"host": "127.0.0.1", "port": 1, "data": "  ",
	}, false, false))
	if err == nil {
		t.Fatal("net.send accepted nothing to send")
	}
}

func TestProbeBadPortIsCoded(t *testing.T) {
	_, err := runProbe(context.Background(), req(map[string]any{"host": "127.0.0.1", "port": 0}))
	ve := view.AsError(err, "x")
	if ve.Code != "net.probe.badport" || ve.Hint == "" {
		t.Errorf("want net.probe.badport with hint, got %+v", ve)
	}
}

func TestProbeClosedPortIsCoded(t *testing.T) {
	// Bind then release: the port is almost certainly free and refusing.
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*stdnet.TCPAddr).Port
	ln.Close()

	_, err = runProbe(context.Background(), req(map[string]any{
		"host": "127.0.0.1", "port": port, "timeout": 1, "wait": 1,
	}))
	ve := view.AsError(err, "x")
	if ve.Code != "net.probe.unreachable" || ve.Hint == "" {
		t.Errorf("want net.probe.unreachable with hint, got %+v", ve)
	}
}

// A regression test for a real bug review found: the TLS
// handshake was never bounded by the documented timeout field — only the
// TCP dial was — so a peer that accepts the connection and then never sends
// a single TLS record hung HandshakeContext forever.
func TestProbeTLSHandshakeRespectsTimeout(t *testing.T) {
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Accept the TCP connection and say nothing at all — never a single
		// TLS record — for far longer than the probe's own --timeout, so
		// only a bounded handshake context can end this.
		time.Sleep(5 * time.Second)
	}()
	port := ln.Addr().(*stdnet.TCPAddr).Port

	done := make(chan error, 1)
	go func() {
		_, err := runProbe(context.Background(), req(map[string]any{
			"host": "127.0.0.1", "port": port, "timeout": 1, "wait": 1, "tls": true,
		}))
		done <- err
	}()
	select {
	case err := <-done:
		ve := view.AsError(err, "x")
		if ve == nil || ve.Code != "net.probe.tls" {
			t.Fatalf("want net.probe.tls, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not return within 3s — the TLS handshake was not bounded by --timeout")
	}
}

// A regression test for a real bug review found: every
// read after the first got a fresh 200ms deadline no matter how long the
// call had already run, so a peer trickling one byte at a time — fully
// within its control, since host/port are caller-supplied — could hold the
// call open for up to 4096 reads * 200ms, about 13.6 minutes, regardless of
// what --wait actually asked for.
func TestProbeBannerReadRespectsWaitAgainstATricklingPeer(t *testing.T) {
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stop := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// One byte every 50ms — well inside each fresh drainWait window
		// before this fix — for far longer than --wait allows.
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := conn.Write([]byte{'x'}); err != nil {
				return
			}
		}
	}()
	defer close(stop)
	port := ln.Addr().(*stdnet.TCPAddr).Port

	done := make(chan error, 1)
	go func() {
		_, err := runProbe(context.Background(), req(map[string]any{
			"host": "127.0.0.1", "port": port, "timeout": 2, "wait": 1,
		}))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not return within 3s against a 1s --wait — a trickling peer held it open")
	}
}

// A dry run must not reach the network. net.send shipped without the branch,
// so `rta net send --dry-run` opened the connection, wrote the bytes, and
// then reported what "would" happen — the exact mistake http.post made once
// already, on the capability whose own declaration calls it
// a remote write primitive strictly more capable than http.post.
//
// The listener accepts and records; a dry run that touches it at all fails.
func TestSendDryRunNeverReachesTheNetwork(t *testing.T) {
	ln, err := stdnet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var mu sync.Mutex
	connected := 0
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connected++
			mu.Unlock()
			conn.Close()
		}
	}()

	host, portStr, _ := stdnet.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	values := map[string]any{
		"host": host, "port": port, "data": `FLUSHALL\r\n`, "timeout": 2, "wait": 1,
	}
	v, err := runSend(t.Context(), plugin.NewRequest(values, true, true))
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	mu.Lock()
	got := connected
	mu.Unlock()
	if got != 0 {
		t.Errorf("--dry-run opened %d connection(s); the payload was delivered", got)
	}

	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("dry run returned %T, want a KeyValue report", v)
	}
	var target, payload string
	for _, p := range kv.Pairs {
		switch p.Key {
		case "target":
			target = p.Value
		case "payload":
			payload = p.Value
		}
	}
	if target != ln.Addr().String() {
		t.Errorf("dry run named %q, not the address it would have dialled (%s)", target, ln.Addr())
	}
	// The escapes are interpreted in the report, because interpreting them is
	// the half people get wrong and a preview showing the literal backslash
	// would hide exactly that.
	if !strings.Contains(payload, `\r\n`) || strings.Contains(payload, `\\r`) {
		t.Errorf("payload %q should show the interpreted bytes, quoted", payload)
	}
}
