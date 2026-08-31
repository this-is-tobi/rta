package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdnet "net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// connFields are the inputs every capability here shares.
//
// Every one is Local, and that is the security property rather than a detail.
// Together they name which instance this call reaches and as whom, and an MCP
// caller may not choose that: caller values resolve above config and above the
// host's own environment, so an agent that could set `endpoint` would point
// rta at an instance of its own and have the host supply $RTA_QDRANT_API_KEY
// beside it. Config still fills these and a person at a terminal still passes
// them as ordinary flags.
func connFields() []plugin.Field {
	return []plugin.Field{
		// Qdrant serves REST on 6333 and gRPC on 6334. This speaks REST, so
		// 6334 will not answer it — which classify says out loud, because the
		// failure otherwise names neither port nor protocol.
		{Name: "endpoint", Type: plugin.String, Default: "127.0.0.1:6333", Config: "endpoint",
			Local: true, Endpoint: plugin.EndpointAddress, Help: "Qdrant REST endpoint, host[:port]"},
		// Local for the downgrade reason rather than the redirect one: an
		// agent that could set this could ask for plaintext against an
		// instance the operator configured as HTTPS.
		{Name: "tls", Type: plugin.Bool, Default: false, Config: "tls",
			Local: true, Endpoint: plugin.EndpointTLS,
			Help: "use HTTPS (a local Qdrant ordinarily does not)"},
		{Name: "api-key", Type: plugin.Secret, Local: true, EnvFallback: true,
			Help: "API key, for an instance that requires one"},
		// Local for the same reason plugins/etcd's own ca-file is: it is read
		// off this machine's disk, not held as a value rta's own store could
		// manage. Not a plugin.Secret: a CA certificate is the public half
		// of a key pair, the half a CA hands out for wide distribution so
		// anyone can verify what it signed — the same reason an OS trust
		// store ships thousands of them in the clear. ca-file only needs
		// Local for the file-read-primitive reason address does, not
		// because its contents are sensitive.
		{Name: "ca-file", Type: plugin.String, Default: "", Config: "ca-file",
			Local: true, Help: "PEM bundle to verify the server against, beyond the host's own trust store"},
	}
}

// requestTimeout bounds every call. Qdrant answers most of these instantly,
// and the ones that do not — a count over a large collection without an index
// — are the ones where waiting forever is the wrong behaviour.
const requestTimeout = 30 * time.Second

// get issues one REST call and decodes Qdrant's envelope into out.
//
// Qdrant wraps every answer in {"result": ..., "status": ..., "time": ...},
// so decoding straight into the caller's type would silently produce a zero
// value. The envelope is unwrapped here, once, rather than at each call site.
func get(ctx context.Context, req plugin.Request, path string, out any) *view.Error {
	return call(ctx, req, http.MethodGet, path, nil, out)
}

func post(ctx context.Context, req plugin.Request, path string, body, out any) *view.Error {
	return call(ctx, req, http.MethodPost, path, body, out)
}

// getRaw is for the one endpoint that has no envelope. Qdrant's root answers
// {"title": ..., "version": ...} directly, so decoding it through get would
// look for a "result" that is not there and quietly produce a zero value.
func getRaw(ctx context.Context, req plugin.Request, path string, out any) *view.Error {
	return call(ctx, req, http.MethodGet, path, nil, rawTarget{out})
}

// rawTarget marks a decode target as already-unwrapped, so call can tell the
// two shapes apart without a second parameter that every other caller would
// have to pass.
type rawTarget struct{ into any }

func call(ctx context.Context, req plugin.Request, method, path string, body, out any) *view.Error {
	base := "http://"
	// ca-file only means anything over TLS, so setting it turns TLS on the
	// same way etcd's own ca-file does — the alternative is a value that
	// silently does nothing until --tls is also typed.
	if req.Bool("tls") || req.String("ca-file") != "" {
		base = "https://"
	}
	base += req.String("endpoint")

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return view.Errorf("qdrant.request.invalid", "%v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return view.Errorf("qdrant.endpoint.invalid", "%v", err).
			WithHint("endpoint is host[:port] with no scheme — set --tls separately")
	}
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if key := req.String("api-key"); key != "" {
		httpReq.Header.Set("api-key", key)
	}

	client, verr := httpClient(req)
	if verr != nil {
		return verr
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return classify(err, req)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded read. A response body is attacker-controlled in the sense that
	// matters here — whatever is at that endpoint decides how much to send —
	// and an unbounded ReadAll is how a wrong endpoint becomes an out-of-memory.
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return classify(err, req)
	}
	if resp.StatusCode >= 300 {
		return classifyStatus(resp.StatusCode, payload, req)
	}

	if raw, ok := out.(rawTarget); ok {
		if err := json.Unmarshal(payload, raw.into); err != nil {
			return malformed(req)
		}
		return nil
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Status any             `json:"status"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return malformed(req)
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return view.Errorf("qdrant.response.unexpected", "could not read the answer: %v", err).
				WithHint("this may be a Qdrant version whose response shape has moved")
		}
	}
	return nil
}

// httpClient is http.DefaultClient unless ca-file names a CA to trust beyond
// this machine's own store, in which case it is a client built for exactly
// that — read and parsed fresh on every call, the same as every other input
// here, because this plugin keeps no client or connection across calls to
// begin with (call is the only entry point, per-request end to end).
func httpClient(req plugin.Request) (*http.Client, *view.Error) {
	ca := req.String("ca-file")
	if ca == "" {
		return http.DefaultClient, nil
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		return nil, view.Errorf("qdrant.tls.ca.unreadable", "%v", err).
			WithHint("ca-file is a path on this machine, read by rta rather than by the server")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, view.Errorf("qdrant.tls.ca.invalid", "%s holds no PEM certificate", ca).
			WithHint("this wants the CA bundle, not the server's own certificate")
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}, nil
}

// maxResponseBytes bounds one response. Points carry payloads and vectors, and
// a scroll over a collection of documents is genuinely large — this is a
// backstop against a wrong endpoint, not a working limit.
//
// A var rather than a const so a test can lower it and actually reach the
// bound. Filling 32 MB to prove a limit works is a slow test nobody runs, and
// an unreached bound is one nothing would notice the removal of.
var maxResponseBytes int64 = 32 << 20

// classifyStatus turns an HTTP failure into something an operator can act on.
// Qdrant puts a reason in the body, and it is usually the useful half.
func classifyStatus(code int, body []byte, req plugin.Request) *view.Error {
	where := req.String("endpoint")
	detail := qdrantErrorText(body)

	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return view.Errorf("qdrant.auth.failed", "%s rejected the credentials", where).
			WithHint("set $" + plugin.LocalEnvVar("qdrant.overview", "api-key") +
				" — an instance started without an API key refuses one that is sent, too")
	case http.StatusNotFound:
		return view.Errorf("qdrant.notfound", "%s: %s", where, detail).
			WithHint("`rta qdrant collection list` shows what is there")
	case http.StatusTooManyRequests:
		return view.Errorf("qdrant.ratelimited", "%s is rate limiting: %s", where, detail).
			WithHint("this is the instance's own limit, not rta's")
	case http.StatusServiceUnavailable:
		return view.Errorf("qdrant.unavailable", "%s is not serving: %s", where, detail).
			WithHint("a Qdrant loading a collection from disk answers this until it is ready")
	}
	return view.Errorf("qdrant.request.failed", "%s returned %d: %s", where, code, detail).
		WithHint("`rta explain qdrant.overview` lists every input and where each one can come from")
}

// qdrantErrorText digs the message out of Qdrant's error envelope, falling
// back to the raw body. A truncated body is better than "unknown error", and
// the whole body is worse than either — some of these run to kilobytes.
func qdrantErrorText(body []byte) string {
	var envelope struct {
		Status struct {
			Error string `json:"error"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Status.Error != "" {
		return truncate(envelope.Status.Error, 300)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "no detail given"
	}
	return truncate(text, 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// classify turns a transport failure into something an operator can act on.
func classify(err error, req plugin.Request) *view.Error {
	var already *view.Error
	if errors.As(err, &already) {
		return already
	}
	where := req.String("endpoint")

	if errors.Is(err, context.DeadlineExceeded) {
		return view.Errorf("qdrant.timeout", "%s did not answer in time", where).
			WithHint("a count or scroll over a large collection with no index does this — " +
				"narrow it, or check the collection is indexed")
	}
	var netErr *stdnet.OpError
	if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
		return view.Errorf("qdrant.conn.refused", "nothing is listening on %s", where).
			WithHint("Qdrant serves REST on 6333 and gRPC on 6334 — the gRPC port will not answer this")
	}
	var dnsErr *stdnet.DNSError
	if errors.As(err, &dnsErr) {
		return view.Errorf("qdrant.host.unknown", "no address for %q", hostOnly(where)).
			WithHint("`rta net dns " + hostOnly(where) + "` shows what DNS returns")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return view.Errorf("qdrant.timeout", "%s did not answer in time", where).
			WithHint("a firewall that drops rather than refuses looks exactly like this")
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return view.Errorf("qdrant.tls.untrusted", "%s presented a certificate nothing here trusts", where).
			WithHint("a self-signed certificate needs --tls=false for a real try, or its CA trusted " +
				"with ca-file for the real thing")
	}
	return view.Errorf("qdrant.conn.failed", "could not reach %s: %v", where, err).
		WithHint("`rta explain qdrant.overview` lists every input and where each one can come from")
}

func hostOnly(endpoint string) string {
	host, _, err := stdnet.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	return host
}

// pathFor escapes a collection name into a URL path. A name is caller-supplied
// and Qdrant accepts a wide range of them, so a raw concatenation is a path
// traversal waiting to happen: a name of "../cluster" would reach a different
// endpoint entirely.
func pathFor(format string, name string) string {
	return fmt.Sprintf(format, url.PathEscape(name))
}

// malformed is the answer when whatever is at that endpoint is not Qdrant. The
// hint names the likeliest cause rather than the likeliest-sounding one: 6334
// is the gRPC port, it is one character away from 6333, and it accepts the
// connection before failing to answer.
func malformed(req plugin.Request) *view.Error {
	return view.Errorf("qdrant.response.malformed", "%s did not answer with JSON", req.String("endpoint")).
		WithHint("Qdrant serves REST on 6333 and gRPC on 6334 — the gRPC port will not answer this")
}
