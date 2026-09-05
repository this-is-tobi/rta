package net

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	stdnet "net"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// bannerLimit caps what we read back. A banner is a greeting, not a payload;
// anything past this is the service streaming, which is not what we asked.
const bannerLimit = 4096

// unescape interprets the few escapes worth typing at a shell prompt, so
// --send "EHLO rta\r\n" reaches the wire as the protocol expects.
func unescape(s string) string {
	r := strings.NewReplacer(`\r`, "\r", `\n`, "\n", `\t`, "\t", `\\`, `\`)
	return r.Replace(s)
}

// printable renders what came back as text, keeping the line structure and
// replacing control bytes so a binary protocol cannot scramble the terminal.
func printable(b []byte) string {
	var sb strings.Builder
	for _, c := range string(b) {
		switch {
		case c == '\n' || c == '\t':
			sb.WriteRune(c)
		case c == '\r':
			// Swallowed: protocols pair it with \n, and it only confuses output.
		case c < 0x20 || c == 0x7f:
			sb.WriteRune('·')
		default:
			sb.WriteRune(c)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// drainWait is how long we keep listening after the first byte arrives —
// long enough to catch the rest of a multi-line greeting, short enough that
// a talkative service does not hold the whole wait window twice.
const drainWait = 200 * time.Millisecond

// readBanner collects whatever the service volunteers, waiting up to wait for
// it to say anything at all. A timeout is the normal ending, not a failure:
// silence is itself an answer, and the caller reports it as one.
//
// wait bounds the whole call, not just the first byte. A remote endpoint
// chooses the host and port an agent points this at, and used to be able to
// hold the connection open for up to bannerLimit reads of one trickled byte
// each — every read after the first got a fresh drainWait (200ms) no matter
// how long the call had already run, so a server sending exactly one byte
// every <200ms kept the call alive for up to 4096*200ms, about 13.6 minutes,
// regardless of what --wait actually asked for (documented, capped at 120s,
// as "seconds to wait for a reply"). deadline is the one ceiling every
// per-read patience now shrinks to fit inside, so the promise --wait makes
// is the promise it keeps.
func readBanner(conn stdnet.Conn, wait time.Duration) ([]byte, error) {
	var out []byte
	deadline := time.Now().Add(wait)
	patience := wait
	for len(out) < bannerLimit {
		if err := conn.SetReadDeadline(time.Now().Add(patience)); err != nil {
			return out, err
		}
		buf := make([]byte, min(4096, bannerLimit-len(out)))
		n, err := conn.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			var netErr stdnet.Error
			if errors.Is(err, io.EOF) || (errors.As(err, &netErr) && netErr.Timeout()) {
				return out, nil
			}
			if len(out) > 0 {
				return out, nil // a partial greeting still tells us who is there
			}
			return nil, err
		}
		patience = min(drainWait, time.Until(deadline))
		if patience <= 0 {
			return out, nil // --wait's own ceiling, not a trickling peer's, ends this
		}
	}
	return out, nil
}

// runProbe listens; runSend speaks first. They are one function because the
// connection, the optional TLS handshake and the reply-reading are identical —
// the only difference is whether anything is written, which is precisely the
// difference that decides the safety class, so it is a parameter here and two
// capabilities outside.
func runProbe(ctx context.Context, req plugin.Request) (view.View, error) {
	return probe(ctx, req, "")
}

func runSend(ctx context.Context, req plugin.Request) (view.View, error) {
	data := req.String("data")
	if strings.TrimSpace(data) == "" {
		return nil, view.Errorf("net.send.nodata", "nothing to send").
			WithHint("pass --data, or use `rta net probe` to listen without speaking")
	}
	// A dry run must not reach the network — the same rule http.post learned
	// the hard way, and this capability is the one with the
	// strongest claim to it: its own declaration calls it a remote write
	// primitive strictly more capable than http.post. It shipped without the
	// branch, so `rta net send --dry-run --host redis --port 6379 --data
	// 'FLUSHALL\r\n'` emptied the Redis and then reported what would happen.
	//
	// Everything is resolved before the refusal so the report is the real
	// one: the address it would have dialled, and the payload after escape
	// interpretation, which is the half people get wrong.
	if req.DryRun {
		host := req.String("host")
		port := req.Int("port")
		if port < 1 || port > 65535 {
			return nil, view.Errorf("net.probe.badport", "port %d is out of range", port).
				WithHint("use a port between 1 and 65535")
		}
		payload := unescape(data)
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "dry run", Value: "nothing was sent"},
			{Key: "target", Value: stdnet.JoinHostPort(host, strconv.Itoa(port))},
			{Key: "tls", Value: fmt.Sprintf("%t", req.Bool("tls"))},
			{Key: "bytes", Value: strconv.Itoa(len(payload))},
			{Key: "payload", Value: strconv.Quote(payload)},
		}}, nil
	}
	return probe(ctx, req, data)
}

func probe(ctx context.Context, req plugin.Request, send string) (view.View, error) {
	host := req.String("host")
	port := req.Int("port")
	if port < 1 || port > 65535 {
		return nil, view.Errorf("net.probe.badport", "port %d is out of range", port).
			WithHint("use a port between 1 and 65535")
	}
	timeout := time.Duration(max(req.Int("timeout"), 1)) * time.Second
	wait := time.Duration(max(req.Int("wait"), 1)) * time.Second
	address := stdnet.JoinHostPort(host, strconv.Itoa(port))

	start := time.Now()
	conn, err := (&stdnet.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, view.Errorf("net.probe.unreachable", "connecting to %s: %v", address, err).
			WithHint("the port may be closed or filtered — `rta net port " + host + "` scans a range")
	}
	defer conn.Close()
	connected := time.Since(start)

	pairs := []view.Pair{
		{Key: "target", Value: address},
		{Key: "address", Value: conn.RemoteAddr().String()},
		{Key: "connect", Value: connected.Round(time.Millisecond).String()},
		{Key: "local", Value: conn.LocalAddr().String()},
	}

	var stream stdnet.Conn = conn
	if req.Bool("tls") {
		handshake := time.Now()
		// InsecureSkipVerify: this is a diagnostic. Reporting what a host
		// negotiates must work even when its certificate does not validate —
		// `rta audit web` is the capability that judges the certificate.
		tc := tls.Client(conn, &tls.Config{ServerName: host, InsecureSkipVerify: true}) //nolint:gosec
		// ctx alone carries no deadline — every caller of probe (the CLI
		// and the MCP bridge alike) hands it a context that only cancels on
		// process shutdown — so without this, a peer that accepts the TCP
		// connection and then never sends a TLS record hangs
		// HandshakeContext forever, holding a goroutine and a socket open
		// with no grant needed and none of --allow-write's gating involved,
		// since this capability is Read. timeout already bounds the dial
		// above; it bounds the handshake the same way.
		hctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := tc.HandshakeContext(hctx); err != nil {
			return nil, view.Errorf("net.probe.tls", "TLS handshake with %s: %v", address, err).
				WithHint("drop --tls to inspect the plain connection")
		}
		state := tc.ConnectionState()
		pairs = append(pairs,
			view.Pair{Key: "tls", Value: tls.VersionName(state.Version)},
			view.Pair{Key: "cipher", Value: tls.CipherSuiteName(state.CipherSuite)},
			view.Pair{Key: "handshake", Value: time.Since(handshake).Round(time.Millisecond).String()},
		)
		if state.NegotiatedProtocol != "" {
			pairs = append(pairs, view.Pair{Key: "alpn", Value: state.NegotiatedProtocol})
		}
		if len(state.PeerCertificates) > 0 {
			leaf := state.PeerCertificates[0]
			pairs = append(pairs,
				view.Pair{Key: "certificate", Value: leaf.Subject.CommonName},
				view.Pair{Key: "expires", Value: leaf.NotAfter.Format("2006-01-02")})
		}
		stream = tc
	}

	if send != "" {
		payload := unescape(send)
		if err := stream.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return nil, view.Errorf("net.send.write", "%v", err)
		}
		if _, err := stream.Write([]byte(payload)); err != nil {
			return nil, view.Errorf("net.send.write", "sending to %s: %v", address, err)
		}
		pairs = append(pairs, view.Pair{Key: "sent", Value: format.Bytes(uint64(len(payload)))})
	}

	banner, err := readBanner(stream, wait)
	if err != nil {
		return nil, view.Errorf("net.probe.read", "reading from %s: %v", address, err)
	}
	pairs = append(pairs, view.Pair{Key: "received", Value: format.Bytes(uint64(len(banner)))})

	response := view.Text{Body: printable(banner)}
	if len(banner) == 0 {
		response = view.Text{Body: fmt.Sprintf(
			"The port is open but said nothing in %s.\n\n"+
				"Many protocols expect the client to speak first — try:\n"+
				"  rta net send %s %d --data \"GET / HTTP/1.0\\r\\n\\r\\n\"", wait, host, port)}
	}
	return view.Sections{Items: []view.Section{
		{ID: "connection", Title: "connection", View: view.KeyValue{Pairs: pairs}},
		{ID: "response", Title: "response", View: response},
	}}, nil
}
