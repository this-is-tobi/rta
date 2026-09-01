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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/x509check"
	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
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
	// Path rather than String, because the input may name a file and the type
	// is what says so. loadCerts stats the target and reads it as PEM when it
	// exists, which made this a file existence oracle over MCP: the type is
	// the hook the host confines path arguments by (internal/pathguard), so a
	// field that can be a path and does not say Path is a field nothing
	// guards. A host:port still works — it resolves under the root like any
	// relative value — and file completion is an improvement for the PEM case
	// rather than a cost.
	//
	// That resolution is also, today, the only thing standing between an MCP
	// caller and an ungated live-host dial through this field: a bare
	// "host:port" is not a filesystem path, so nothing here refuses it, and
	// pathguard's own resolve() treats it exactly like a relative path — it
	// gets the server's working directory prepended before the bounds check
	// runs (internal/mcp/bridge.go's checkPaths substitutes that judged value
	// back into the request). "<cwd>/host:port" can never split back into a
	// dialable host:port — SplitHostPort takes everything before the last
	// colon as the host, slashes included, which no resolver accepts — so the
	// live-host branch of cert.inspect/chain/pem/tls is not reachable over
	// MCP in practice. TestLiveHostTargetIsUndialableOnceRoutedThroughThePathGate
	// (cert_test.go) pins that rather than leaving it assumed. It is not a
	// deliberate gate, though: it is a side effect of a check built for
	// filesystem paths landing on a field that is sometimes something else. If
	// pathguard's handling of a non-path value ever changes, these four need
	// the same NeedsGrant + Scope cert.expiry already carries, for the same
	// reason net.probe and net.port do — see their declarations below and in
	// builtin/net/net.go.
	targetField := plugin.Field{
		Name: "target", Type: plugin.Path, Positional: true, Required: true,
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
				ID:         "cert.pem",
				Summary:    "Print the certificate chain as PEM",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "The bytes, not a description of them. `cert chain` draws what a host " +
					"presents so a person can read it; this hands the same certificates back in the " +
					"form every other tool takes one — a Kubernetes ConfigMap, a Dockerfile COPY, " +
					"`update-ca-certificates`, a paste into somebody's terminal.\n\n" +
					"--include issuers is the one to reach for behind a private CA: it drops the leaf " +
					"and leaves the chain that has to be *trusted*, which is what a ca-bundle is. " +
					"chain (the default) is everything the host presented, leaf is the end-entity " +
					"certificate alone.\n\n" +
					"A presented chain is what the host chose to send and is not always complete — a " +
					"server that omits its intermediate presents a leaf that validates nowhere else, " +
					"and this reports what arrived rather than filling the gap from a trust store, " +
					"because a bundle that silently differs from what the server serves is how a " +
					"working local test hides a broken deployment.\n\n" +
					"Read, and it stays read from anywhere but a terminal: --out names a path on " +
					"*this* machine, so it is a person's flag only and an MCP caller always gets the " +
					"PEM back in the response.",
				Inputs: []plugin.Field{
					targetField,
					{Name: "include", Type: plugin.String, Default: "chain",
						Options: []string{"chain", "issuers", "leaf"},
						Help:    "which certificates to print"},
					// Local, the same rule --out follows everywhere: a
					// destination is a destination. Nothing here is secret — a
					// certificate is what a host hands to anybody who connects —
					// but "which of this machine's files gets overwritten" is not
					// a question a remote caller gets to answer, whatever it is
					// being overwritten with.
					{Name: "out", Type: plugin.Path, Local: true,
						Help: "write the PEM to this file (0644) instead of printing it"},
					timeoutField,
				},
				Run: runPEM,
			},
			{
				ID:         "cert.expiry",
				Summary:    "Check certificate expiry for one or more hosts",
				Safety:     plugin.Read,
				Idempotent: true,
				// `targets` is a StringSlice (see expiryRow's comment for why —
				// no Path type exists for a slice), which means it never passes
				// through the MCP path gate at all: unlike targetField above,
				// nothing rewrites or refuses it, so this dials whatever host an
				// MCP caller lists, straight through, banner-and-all in the
				// certificate fields that come back. Same shape as net.probe and
				// net.port, same fix: a grant, scoped to host — and Scope already
				// handles a []string field natively, checking every listed target
				// individually rather than one for all of them.
				NeedsGrant: true,
				Scope:      "targets",
				Description: "Same reasoning as `net probe`: the hosts are the caller's choice, and " +
					"what a certificate says about itself — subject, issuer, DNS names — is read as " +
					"tool output the same way a banner is. Needs a grant, one per host listed.",
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

// loadCerts fetches the peer chain from a live host or parses a PEM file.
//
// The file branch is for the capabilities that declare a Path input and are
// therefore confined at the MCP boundary. Anything whose target is
// a host must call dialCerts instead — see expiryRow.
func loadCerts(ctx context.Context, target string, timeout time.Duration) ([]*x509.Certificate, *tls.ConnectionState, error) {
	if _, err := os.Stat(target); err == nil {
		certs, err := readPEM(target)
		return certs, nil, err
	}
	return dialCerts(ctx, target, timeout)
}

// dialCerts fetches the peer chain from a live host, and never touches the
// filesystem.
func dialCerts(ctx context.Context, target string, timeout time.Duration) ([]*x509.Certificate, *tls.ConnectionState, error) {
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

// runPEM hands back the certificates themselves, in the encoding every other
// tool on the machine accepts one in.
func runPEM(ctx context.Context, req plugin.Request) (view.View, error) {
	target := req.String("target")
	certs, _, err := loadCerts(ctx, target, dialTimeout(req))
	if err != nil {
		return nil, err
	}
	chosen, verr := include(certs, req.String("include"))
	if verr != nil {
		return nil, verr
	}
	body, verr := encodePEM(chosen)
	if verr != nil {
		return nil, verr
	}

	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return view.Text{Body: body}, nil
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would write %s from %s to %s",
			plural(len(chosen), "certificate", "certificates"), target, out)}, nil
	}
	path := expandHome(out)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, view.Errorf("cert.out.unwritable", "creating %s: %v", filepath.Dir(path), err)
	}
	// 0644 and not 0600: this is a certificate, which is public by
	// construction, and the file's whole purpose is to be read by something
	// else — a container build, a system trust store, another user's tool.
	// Atomic, so a half-written bundle never exists for anything to pick up.
	if err := atomicfile.Write(path, []byte(body), 0o644); err != nil {
		return nil, view.Errorf("cert.out.unwritable", "writing %s: %v", path, err)
	}
	return view.Text{Body: fmt.Sprintf("wrote %s from %s to %s (%d bytes, mode 0644)",
		plural(len(chosen), "certificate", "certificates"), target, out, len(body))}, nil
}

// include narrows a presented chain to what was asked for.
//
// "issuers" is the interesting one and the reason this is not a boolean: what
// a private CA needs installed is everything *except* the leaf, which is the
// bundle a trust store takes. A chain of one certificate has no issuers in it,
// and saying so beats handing back an empty file that fails later somewhere
// with no explanation.
func include(certs []*x509.Certificate, which string) ([]*x509.Certificate, *view.Error) {
	switch which {
	case "", "chain":
		return certs, nil
	case "leaf":
		return certs[:1], nil
	case "issuers":
		if len(certs) < 2 {
			return nil, view.Errorf("cert.chain.leafonly",
				"only the leaf certificate was presented, so there are no issuers to print").
				WithHint("many servers omit their intermediates; --include chain prints what did arrive")
		}
		return certs[1:], nil
	}
	return nil, view.Errorf("cert.include.invalid", "unknown --include %q", which).
		WithHint("one of: chain, issuers, leaf")
}

func encodePEM(certs []*x509.Certificate) (string, *view.Error) {
	var b strings.Builder
	for _, c := range certs {
		if err := pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}); err != nil {
			return "", view.Errorf("cert.encode.failed", "encoding %s: %v", c.Subject.CommonName, err)
		}
	}
	return b.String(), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// expandHome resolves a leading ~/ in a path a person typed. The shell does it
// for an unquoted argument and not for a quoted one, and --out is exactly the
// flag somebody quotes.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
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
	// A worker pool rather than a goroutine per target queueing on a
	// semaphore. Same correction net.port needed, and for the same reason:
	// taking the semaphore *inside* the goroutine bounds the dials and leaves
	// the fan-out equal to the input length. It matters far less here — a
	// target is bytes the caller had to send, where net.port's "1-65535" is
	// seven bytes for 65,535 of them — but a bound that is not a bound is
	// worth having in one shape rather than two.
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(expiryConcurrency, len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				rows[i] = expiryRow(ctx, targets[i], warnDays, timeout)
			}
		}()
	}
feed:
	for i := range targets {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
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
	// dialCerts, not loadCerts. Sharing loadCerts gave this capability a
	// file-reading branch its own Help never claimed ("hosts to check
	// (host[:port])") — and `targets` is a StringSlice, which the MCP path
	// gate cannot hook because it only looks at Field.Path. So over MCP, with
	// no flag and no grant, cert.expiry answered "does this path exist, is it
	// PEM, is it readable, is it a directory" for anywhere on the machine
	// while its sibling cert.inspect was refused for the same string.
	//
	// Removing the branch rather than retyping the field: there is no path
	// type for a slice today, the Help already describes a host, and a
	// capability whose declared inputs cannot express what it does is the
	// thing that made this invisible.
	certs, _, err := dialCerts(ctx, target, timeout)
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
