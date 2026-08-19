// Package cert is the built-in x509/TLS inspection plugin: certificate
// details, chain walk, expiry checks and TLS handshake reports — all stdlib,
// zero configuration.
package cert

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/x509check"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// defaultTimeoutSeconds bounds every dial cert makes. Ten seconds is
// generous for a TLS handshake and short enough that a host behind a
// firewall that drops packets rather than refusing them costs ten seconds
// instead of the OS connect timeout, which on Linux is a little over two
// minutes.
const defaultTimeoutSeconds = 10

// expiryConcurrency bounds runExpiry's fan-out. The limit is not about
// sockets — forty of those is nothing — it is that an unbounded fan-out over
// a host list read from a file looks like a scan from the other end, and rta
// is not a scanner. Sixteen turns a forty-host run into three rounds rather
// than forty.
const expiryConcurrency = 16

// Plugin returns the cert plugin declaration.
func Plugin() plugin.Plugin {
	targetField := plugin.Field{
		Name: "target", Type: plugin.String, Positional: true, Required: true,
		Help: "host[:port] to connect to, or a path to a PEM file",
	}
	timeoutField := plugin.Field{
		Name: "timeout", Type: plugin.Int, Default: defaultTimeoutSeconds, Min: 1, Max: 120,
		Help: "connect timeout in seconds",
	}
	return plugin.Plugin{
		Name:    "cert",
		Summary: "X.509 and TLS inspection: certificates, chains, expiry",
		Capabilities: []plugin.Capability{
			{
				ID:         "cert.inspect",
				Summary:    "Show certificate details for a host or PEM file",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs:     []plugin.Field{targetField, timeoutField},
				Run:        runInspect,
			},
			{
				ID:         "cert.chain",
				Summary:    "Show the certificate chain presented by a host",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs:     []plugin.Field{targetField, timeoutField},
				Run:        runChain,
			},
			{
				ID:         "cert.expiry",
				Summary:    "Check certificate expiry for one or more hosts",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "targets", Type: plugin.StringSlice, Positional: true, Required: true,
						Help: "hosts to check (host[:port])"},
					{Name: "warn-days", Type: plugin.Int, Default: x509check.DefaultWarnDays,
						Help: "flag certificates expiring within this many days"},
					timeoutField,
				},
				Run: runExpiry,
			},
			{
				ID:         "cert.tls",
				Summary:    "Report the negotiated TLS version and cipher for a host",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs:     []plugin.Field{targetField, timeoutField},
				Run:        runTLS,
			},
		},
	}
}

// dialTimeout reads the declared timeout as a duration. plugin.Resolve fills
// the default and clamps to the declared bounds before any handler runs, so
// the only way this sees a zero is a Request assembled by hand — and a zero
// would become an already-expired deadline that fails every dial instantly,
// which is a worse answer than the one the declaration promises.
func dialTimeout(req plugin.Request) time.Duration {
	secs := req.Int("timeout")
	if secs <= 0 {
		secs = defaultTimeoutSeconds
	}
	return time.Duration(secs) * time.Second
}

// leafCerts fetches the peer chain from a live host or parses a PEM file.
func loadCerts(ctx context.Context, target string, timeout time.Duration) ([]*x509.Certificate, *tls.ConnectionState, error) {
	if _, err := os.Stat(target); err == nil {
		certs, err := readPEM(target)
		return certs, nil, err
	}
	addr := target
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, nil, view.Errorf("cert.target.invalid", "invalid target %q: %v", target, err).
			WithHint("use host, host:port, or a PEM file path")
	}
	dialer := &tls.Dialer{Config: &tls.Config{
		ServerName: host,
		// We are inspecting, not trusting: report what the host presents even
		// if the chain is invalid — verification status is part of the output.
		InsecureSkipVerify: true, //nolint:gosec
	}}
	// The caller's context carries no deadline of its own — the CLI runs
	// handlers on a bare context and an MCP client sets none either — so
	// without this the dial waits out the OS connect timeout, minutes per
	// target, against a host that drops packets instead of refusing them.
	// Cancelling after a successful handshake does not affect the returned
	// connection, so the deferred cancel is safe to hold to the whole call.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, view.Errorf("cert.dial.failed", "connecting to %s: %v", addr, err).
			WithHint("check the host is reachable and speaks TLS on this port")
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, nil, view.Errorf("cert.none", "no certificates presented by %s", addr)
	}
	return state.PeerCertificates, &state, nil
}

func readPEM(path string) ([]*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, view.Errorf("cert.file.unreadable", "reading %s: %v", path, err)
	}
	var certs []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, view.Errorf("cert.parse.failed", "parsing certificate in %s: %v", path, err)
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, view.Errorf("cert.file.empty", "no CERTIFICATE blocks found in %s", path)
	}
	return certs, nil
}

// verify reports whether the presented chain validates against system roots.
func verify(certs []*x509.Certificate, host string) string {
	return chainVerdict(x509check.Chain(certs, host))
}

// chainVerdict names the passing case. x509check.Chain answers with a reason
// or with nothing at all, which is the right shape for a grader deciding
// pass/fail; a key/value row is read by a person, and a blank value next to
// "chain" reads as a check that did not run rather than one that passed.
func chainVerdict(reason string) string {
	if reason == "" {
		return "valid"
	}
	return reason
}

func hostOf(target string) string {
	if _, err := os.Stat(target); err == nil {
		return ""
	}
	host := target
	if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}
	return host
}

func runInspect(ctx context.Context, req plugin.Request) (view.View, error) {
	target := req.String("target")
	certs, _, err := loadCerts(ctx, target, dialTimeout(req))
	if err != nil {
		return nil, err
	}
	leaf := certs[0]
	sum := sha256.Sum256(leaf.Raw)
	pairs := []view.Pair{
		{Key: "subject", Value: leaf.Subject.String()},
		{Key: "issuer", Value: leaf.Issuer.String()},
		{Key: "serial", Value: leaf.SerialNumber.String()},
		{Key: "not-before", Value: leaf.NotBefore.Format(time.RFC3339)},
		{Key: "not-after", Value: leaf.NotAfter.Format(time.RFC3339)},
		{Key: "expires-in", Value: humanUntil(leaf.NotAfter)},
		{Key: "sha256", Value: hex.EncodeToString(sum[:])},
		{Key: "sig-alg", Value: leaf.SignatureAlgorithm.String()},
		{Key: "chain", Value: verify(certs, hostOf(target))},
	}
	if len(leaf.DNSNames) > 0 {
		pairs = append(pairs, view.Pair{Key: "dns-names", Value: strings.Join(leaf.DNSNames, ", ")})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

func runChain(ctx context.Context, req plugin.Request) (view.View, error) {
	certs, _, err := loadCerts(ctx, req.String("target"), dialTimeout(req))
	if err != nil {
		return nil, err
	}
	// Chain renders leaf-first, each certificate nested under its issuer's
	// presenter position.
	var build func(i int) []view.Node
	build = func(i int) []view.Node {
		if i >= len(certs) {
			return nil
		}
		c := certs[i]
		return []view.Node{{
			Label:    c.Subject.CommonName,
			Detail:   fmt.Sprintf("expires %s (%s)", c.NotAfter.Format("2006-01-02"), humanUntil(c.NotAfter)),
			Children: build(i + 1),
		}}
	}
	return view.Tree{Roots: build(0)}, nil
}

func runExpiry(ctx context.Context, req plugin.Request) (view.View, error) {
	targets := req.StringSlice("targets")
	warnDays := req.Int("warn-days")
	timeout := dialTimeout(req)

	// One filtered host used to stall every host listed after it: the walk
	// was sequential, so checking forty hosts when one of them drops packets
	// cost a full timeout before the thirty-ninth was even dialed. Rows are
	// written into a slot rather than appended, so the answer still comes
	// back in the order the targets were given no matter who replies first.
	rows := make([][]string, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, expiryConcurrency)
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i] = expiryRow(ctx, target, warnDays, timeout)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Target"},
		{Name: "Expires", Kind: view.KindTimestamp},
		{Name: "In", Kind: view.KindDuration},
		{Name: "Status", Kind: view.KindStatus},
	}}
	t.Rows = append(t.Rows, rows...)
	t.Total = len(t.Rows)
	return t, nil
}

// expiryRow grades one target. An unreachable host is a row saying so rather
// than an error that throws away the thirty-nine hosts that did answer.
func expiryRow(ctx context.Context, target string, warnDays int, timeout time.Duration) []string {
	certs, _, err := loadCerts(ctx, target, timeout)
	if err != nil {
		return []string{target, "-", "-", "ERROR: " + view.AsError(err, "cert.load").Message}
	}
	leaf := certs[0]
	status := "ok"
	switch {
	case time.Now().After(leaf.NotAfter):
		status = "EXPIRED"
	case x509check.Expiring(leaf.NotAfter, warnDays):
		status = fmt.Sprintf("WARN <%dd", warnDays)
	}
	return []string{
		target,
		leaf.NotAfter.Format("2006-01-02"),
		humanUntil(leaf.NotAfter),
		status,
	}
}

func runTLS(ctx context.Context, req plugin.Request) (view.View, error) {
	target := req.String("target")
	_, state, err := loadCerts(ctx, target, dialTimeout(req))
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, view.Errorf("cert.tls.filetarget", "%q is a file; cert tls needs a live host", target).
			WithHint("pass host[:port] instead")
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "version", Value: tls.VersionName(state.Version)},
		{Key: "cipher", Value: tls.CipherSuiteName(state.CipherSuite)},
		{Key: "alpn", Value: orDash(state.NegotiatedProtocol)},
		{Key: "server-name", Value: orDash(state.ServerName)},
	}}, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return fmt.Sprintf("expired %dd ago", int(-d.Hours())/24)
	}
	days := int(d.Hours()) / 24
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
