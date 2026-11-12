package net

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	stdnet "net"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/x509check"
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
// regardless of what --wait actually asked for (documented, capped at 2m,
// as how long to wait for a reply). deadline is the one ceiling every
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
			WithHint("give it in " + req.Surface().InputName("data") + ", or use " +
				req.Surface().CapabilityName("net.probe") + " to listen without speaking").WithInput("data")
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
	// interpretation, which is the half people get wrong — quoted, so the
	// line breaks it sends stay on the one line a destructive capability's
	// dry run is, which a person is asked about and an operator reads on a
	// parked agent call.
	if req.DryRun {
		host := req.String("host")
		if verr := dottedNumber(host); verr != nil {
			return nil, verr
		}
		port := req.Int("port")
		if port < 1 || port > 65535 {
			return nil, view.Errorf("net.probe.badport", "port %d is out of range", port).
				WithHint("use a port between 1 and 65535").WithInput("port")
		}
		payload := unescape(data)
		over := ""
		if sendsOverTLS(req) {
			if _, verr := sendTLSConfig(req, host); verr != nil {
				return nil, verr
			}
			over = " over TLS"
		}
		return view.Text{Body: fmt.Sprintf("would send %s to %s%s: %s", format.CountOf(len(payload), "byte"),
			stdnet.JoinHostPort(host, strconv.Itoa(port)), over, strconv.Quote(payload))}, nil
	}
	return probe(ctx, req, data)
}

// sendsOverTLS reports whether a send asks for TLS: `tls`, or a CA or a
// server name, which mean something over TLS alone. Read as asking for it,
// rather than ignored until `tls` is also typed: a payload the caller meant
// for a checked peer must never leave in the clear because one switch was
// forgotten.
func sendsOverTLS(req plugin.Request) bool {
	return req.Bool("tls") || req.String("ca-file") != "" || req.String("tls-server-name") != ""
}

// sendTLSConfig is the handshake a send writes behind: TLS 1.2 or newer, with
// a peer whose certificate checks out for the name it is reached by. The name
// is the host the call names unless tls-server-name gives another, and the
// certificate is checked against the system's roots unless ca-file names a CA
// in their place.
//
// The peer used to go unchecked, as the probe's still does: a send then wrote
// the caller's bytes, a password in an AUTH line among them, to whatever
// answered on the address, an interception included. TLS without the check
// keeps the payload from anybody who only listens, and hands it to whoever
// answers. The probe stays as it was, since it writes nothing and reports
// what a host negotiates whatever its certificate is.
func sendTLSConfig(req plugin.Request, host string) (*tls.Config, *view.Error) {
	pool, verr := plugin.CAPool(req, "net", "ca-file")
	if verr != nil {
		return nil, verr
	}
	name := host
	if n := req.String("tls-server-name"); n != "" {
		name = n
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name, RootCAs: pool}, nil
}

// unverified words a handshake a send refused because the peer's certificate
// did not check out, by what fixes it: a CA nothing here trusts, a certificate
// for another name, or any other verdict. Nil when err is no verdict on the
// certificate, a protocol the peer does not speak or a peer that hung up,
// which the handshake's own refusal covers.
func unverified(req plugin.Request, address, name string, err error) *view.Error {
	sf := req.Surface()
	var hostErr x509.HostnameError
	var verifyErr *tls.CertificateVerificationError
	switch {
	case plugin.CertUntrusted(err):
		return view.Errorf("net.tls.untrusted", "%s presented a certificate from a CA this machine does not trust", address).
			WithHint(trustHint(sf, address))
	case errors.As(err, &hostErr) && hostErr.Certificate != nil:
		return view.Errorf("net.tls.name", "%s presented a certificate for %s, not %s",
			address, plugin.CertNames(hostErr.Certificate), name).WithHint(nameHint(sf))
	case errors.As(err, &verifyErr):
		hint := plugin.CertPolicyHint(err)
		if hint == "" {
			hint = sf.CapabilityWith("net.probe", "tls") + " shows the certificate it presents and sends nothing"
		}
		return view.Errorf("net.tls.rejected", "%s presented a certificate that does not check out: %v",
			address, verifyErr.Err).WithHint(hint)
	}
	return nil
}

// trustHint is where the CA that issued a send's peer's certificate goes.
// An agent cannot name one: ca-file is Local, so the CA a call trusts is
// never the caller's to choose, and over MCP the call is checked against the
// system's roots alone, which the operator is the one to change.
func trustHint(sf plugin.Surface, address string) string {
	if sf == plugin.SurfaceMCP {
		return "an agent's call is checked against this machine's trust store alone, which does not hold the CA " +
			"that issued it: ask the operator to add that CA to the trust store, or to make the call with ca-file " +
			"naming it"
	}
	return sf.CAHint("ca-file") + " — `" + sf.Call("cert.pem", plugin.Arg{Name: "target", Value: address, Positional: true}) +
		"` prints the chain it presents"
}

// nameHint is how a send reaches a peer by a name its certificate holds.
func nameHint(sf plugin.Surface) string {
	if sf == plugin.SurfaceMCP {
		return "call it by one of the names the certificate holds"
	}
	return "call it by one of the names the certificate holds, or " + sf.SettingName("tls-server-name") +
		" checks it for one of them while reaching this address"
}

// dialHint is what to check after a dial that failed, which depends on how far
// it got. A name the resolver could not answer for never reached a port, and
// "the port may be closed or filtered" sent whoever typed a mistyped hostname
// off to scan the ports of a host that does not exist.
func dialHint(sf plugin.Surface, host string, err error) string {
	if isResolveFailure(err) {
		return "the name did not resolve, so no port was tried — " + sf.CapabilityName("net.dns") +
			" asks the resolver for " + host + " directly"
	}
	return "the port may be closed or filtered — " + sf.CapabilityName("net.port") +
		" scans a range of " + host + "'s ports"
}

func isResolveFailure(err error) bool {
	var dnsErr *stdnet.DNSError
	return errors.As(err, &dnsErr)
}

// resolveFailed is the refusal for a name that did not resolve, for the
// capabilities that resolve it themselves — ping and trace — in one place so
// neither can go without the next step. err may be nil: a lookup that answers
// with no address and no error is a name that resolved to nothing.
func resolveFailed(code string, sf plugin.Surface, host string, err error) *view.Error {
	reason := "the name resolved to no address"
	if err != nil {
		reason = err.Error()
	}
	return view.Errorf(code, "resolving %s: %s", host, reason).
		WithHint(sf.CapabilityName("net.dns") + " asks the resolver for " + host +
			" directly, and says whether the name or the lookup is what failed")
}

// namingHost is verr naming the host input when err is the resolver's answer
// that the name does not exist, which is what a mistyped host gets. A resolver
// that did not answer, or answered with a failure of its own, names none: the
// host may be right, and the network is what failed.
// dottedNumber refuses a host written only in digits and dots that is no IPv4
// address — 192.168.1.256, 10.0.0, 192.168.01.1 — saying which part is wrong.
// It went to the resolver as a name, and came back as a host that does not
// exist, under a hint to ask the resolver again; no top-level domain is all
// digits (RFC 3696, section 2), so the lookup could never succeed, and the
// mistake is in the number.
func dottedNumber(host string) *view.Error {
	if !strings.Contains(host, ".") || strings.Trim(host, "0123456789.") != "" || stdnet.ParseIP(host) != nil {
		return nil
	}
	parts := strings.Split(host, ".")
	why := fmt.Sprintf("an address has four parts, and this has %d", len(parts))
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		switch {
		case p == "":
			why = "a part is empty"
		case n > 255:
			why = p + " is more than 255"
		case len(p) > 1 && p[0] == '0':
			why = p + " has a leading zero"
		default:
			continue
		}
		break
	}
	return view.Errorf("net.host.invalid", "%s is not an IPv4 address: %s", host, why).
		WithHint("an IPv4 address is four numbers from 0 to 255 with no leading zeros, as 192.168.1.10").WithInput("host")
}

func namingHost(verr *view.Error, err error) *view.Error {
	if !isNotFound(err) {
		return verr
	}
	return verr.WithInput("host")
}

func probe(ctx context.Context, req plugin.Request, send string) (view.View, error) {
	host := req.String("host")
	if verr := dottedNumber(host); verr != nil {
		return nil, verr
	}
	port := req.Int("port")
	if port < 1 || port > 65535 {
		return nil, view.Errorf("net.probe.badport", "port %d is out of range", port).
			WithHint("use a port between 1 and 65535").WithInput("port")
	}
	timeout := max(req.Duration("timeout"), time.Second)
	wait := max(req.Duration("wait"), time.Second)
	address := stdnet.JoinHostPort(host, strconv.Itoa(port))

	// The handshake a probe makes inspects, and the one a send makes is the
	// send's own, read before anything is dialled: a CA file that does not
	// read is refused here, not after a connection was opened for nothing.
	var handshakeCfg *tls.Config
	switch {
	case send != "" && sendsOverTLS(req):
		cfg, verr := sendTLSConfig(req, host)
		if verr != nil {
			return nil, verr
		}
		handshakeCfg = cfg
	case send == "" && req.Bool("tls"):
		handshakeCfg = x509check.InspectionTLS(host)
	}

	start := time.Now()
	conn, err := (&stdnet.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, namingHost(view.Errorf("net.probe.unreachable", "connecting to %s: %v", address, err).
			WithHint(dialHint(req.Surface(), host, err)), err)
	}
	defer func() { _ = conn.Close() }()
	connected := time.Since(start)

	pairs := []view.Pair{
		{Key: "target", Value: address},
		{Key: "address", Value: conn.RemoteAddr().String()},
		{Key: "connect", Value: connected.Round(time.Millisecond).String()},
		{Key: "local", Value: conn.LocalAddr().String()},
	}

	stream := conn
	if handshakeCfg != nil {
		handshake := time.Now()
		tc := tls.Client(conn, handshakeCfg)
		// ctx alone carries no deadline — every caller of probe (the CLI
		// and the MCP bridge alike) hands it a context that only cancels on
		// process shutdown — so without this, a peer that accepts the TCP
		// connection and then never sends a TLS record hangs
		// HandshakeContext forever, holding a goroutine and a socket open
		// with no grant needed and no gating at all,
		// since this capability is Read. timeout already bounds the dial
		// above; it bounds the handshake the same way.
		hctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := tc.HandshakeContext(hctx); err != nil {
			hint := req.Surface().WithoutInputs("tls") + ", the plain connection is inspected"
			if send != "" {
				if verr := unverified(req, address, handshakeCfg.ServerName, err); verr != nil {
					return nil, verr
				}
				// Not "without tls": that sends the payload in the clear,
				// to a host that was just asked for TLS.
				hint = req.Surface().CapabilityWith("net.probe", "tls") + " shows what the host speaks and sends " +
					"nothing; a send needs TLS 1.2 or newer"
			}
			return nil, view.Errorf("net.probe.tls", "TLS handshake with %s: %v", address, err).WithHint(hint)
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
		pairs = append(pairs, view.Pair{Key: "sent", Value: format.Bytes(len(payload))})
	}

	banner, err := readBanner(stream, wait)
	if err != nil {
		return nil, view.Errorf("net.probe.read", "reading from %s: %v", address, err)
	}
	pairs = append(pairs, view.Pair{Key: "received", Value: format.Bytes(len(banner))})

	// A port that said nothing answers with nothing, and the sentence that
	// explains the silence rides beside it for a person (view.Text.Empty).
	// It was the response itself, so -o json and an agent read rta's prose
	// where the server's bytes go — a banner no service sent, and one a
	// service could send word for word.
	response := view.Text{Body: printable(banner)}
	if len(banner) == 0 {
		response.Empty = silence(req.Surface(), send, wait, host, port)
	}
	return view.Sections{Items: []view.Section{
		{ID: "connection", Title: "connection", View: view.KeyValue{Pairs: pairs}},
		{ID: "response", Title: "response", View: response},
	}}, nil
}

// silence is what a port that said nothing is explained as, and the remedy
// depends on who spoke. net.probe only listened, and the protocols that wait
// for the client are net.send's to reach. net.send has already spoken, and
// offering it again sent somebody to repeat the call that just went
// unanswered: what is left to change is the wait, or how the request ends.
// Both are named, and net.send's call is spelled, the way sf, the surface
// asking, gives them.
func silence(sf plugin.Surface, sent string, wait time.Duration, host string, port int) string {
	if sent != "" {
		return fmt.Sprintf("The port took the %s sent and said nothing back in %s.\n\n"+
			"A slow service may answer given a longer %s, and many protocols act only on "+
			"a request ended by a line break — \\r\\n in %s is sent as one.",
			format.Bytes(len(unescape(sent))), wait, sf.InputName("wait"), sf.InputName("data"))
	}
	return fmt.Sprintf("The port is open but said nothing in %s.\n\n"+
		"Many protocols expect the client to speak first — try:\n  %s", wait,
		sf.Call("net.send", plugin.Arg{Name: "host", Value: host, Positional: true},
			plugin.Arg{Name: "port", Value: port, Positional: true},
			plugin.Arg{Name: "data", Value: `GET / HTTP/1.0\r\n\r\n`}))
}
