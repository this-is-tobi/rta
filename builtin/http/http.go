// Package http is the built-in REST client plugin: request any endpoint with
// auth, inspect status/headers/timing/body. Zero configuration; saved
// collections and OAuth2/SigV4 come later with profiles.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// maxBody bounds how much of a response body we buffer and show.
const maxBody = 1 << 20 // 1 MiB

// client never follows a redirect on its own. A grant covers exactly the URL
// named (Scope: "url"), checked once by the MCP bridge before Run runs at
// all — if this request then silently followed a 3xx wherever it pointed,
// an authorized call for one destination could actually reach any other
// with no second grant check involved (the concrete case: an authorized
// public status endpoint that 302s to a cloud metadata service). Returning
// ErrUseLastResponse hands back the redirect response itself — status,
// headers, its Location — so whoever asked can see exactly where it would
// have gone and, if that's fine, ask for it explicitly as its own,
// separately grant-checked call.
var client = &stdhttp.Client{
	CheckRedirect: func(*stdhttp.Request, []*stdhttp.Request) error {
		return stdhttp.ErrUseLastResponse
	},
	// See ssrf.go: a grant authorizes the URL named, not wherever its DNS
	// answer points at connection time, and guardedTransport is what checks
	// the difference.
	Transport: guardedTransport(),
}

// Plugin returns the http plugin declaration.
func Plugin() plugin.Plugin {
	common := []plugin.Field{
		{Name: "url", Type: plugin.String, Positional: true, Required: true, Help: "request URL"},
		{Name: "header", Type: plugin.StringSlice, Suggest: suggestHeaders,
			Help: "request header, repeatable: -H 'Key: Value'"},
		// Secret, not String, and it always should have been: both are
		// credentials, so both belong masked in a form rather than drawn in
		// the clear, and neither is a value anything should keep. Declaring
		// them plainly is what let internal/recent write a bearer token to
		// disk and offer it back on a completion list.
		//
		// Not Local: an agent granted a URL may legitimately supply its own
		// authorization for it, which is what Local would take away.
		{Name: "bearer", Type: plugin.Secret, Help: "bearer token (Authorization: Bearer ...)"},
		{Name: "basic", Type: plugin.Secret, Help: "basic auth as user:password"},
		{Name: "timeout", Type: plugin.Int, Default: 30, Min: 1, Max: 600, Help: "request timeout in seconds"},
	}
	withBody := append([]plugin.Field{}, common...)
	withBody = append(withBody, plugin.Field{
		// The help used to promise "@file to read from a file, - for stdin".
		// Neither was ever implemented, so a body of "@secrets.env" was sent
		// literally — and a declaration is published verbatim as an MCP tool
		// description, which makes an unimplemented promise an instruction a
		// model follows.
		// Text rather than Secret, deliberately, and the tradeoff is worth
		// naming because it is the one place this class stays open. A
		// request body is usually not a credential — it is JSON somebody
		// wants to see echoed in a dry run and in the audit line — but it
		// certainly can be one: `grant_type=client_credentials&
		// client_secret=…` is the shape of every OAuth token exchange, and
		// that lands in the sealed agent log intact.
		//
		// Masking the whole body would make the log useless for the ninety
		// per cent that is not a secret, and recognising the ten per cent by
		// looking at the value is the thing internal/recent's own comments
		// say twice does not work. So this stays legible, and the answer for
		// a caller who is sending a credential is the same as for a header:
		// put it in a field declared for it.
		Name: "data", Type: plugin.Text, Help: "request body, sent as given",
	})
	return plugin.Plugin{
		Name:    "http",
		Summary: "REST client: request endpoints, inspect responses",
		Capabilities: []plugin.Capability{
			// GET and HEAD read: they mutate nothing, and the safety class is
			// right. They still need a grant, which is the same correction
			// net.send took on strictly weaker grounds.
			//
			// Fetching a URL is bidirectional. Outbound it is arbitrary egress
			// to a host of the caller's choosing, with the caller's bytes in
			// the path, the query and the headers — which is a general channel
			// out of a machine whose other tools reach an age identity, and it
			// is what an injected agent needs to report what it found. Inbound
			// the response body arrives in the model's context, so anything
			// the fetched host wants to say is read as tool output: a
			// compromised plugin has only to deliver one line, and everything
			// after it is fetched fresh, which is the whole of static analysis
			// of the shipped artifact defeated.
			//
			// Scoped to the URL, so a person can allow one destination for
			// fifteen minutes rather than the internet indefinitely. It costs
			// CLI and TUI nothing: grants are enforced only in the MCP bridge,
			// because a person at a terminal is never gated.
			{
				ID: "http.get", Summary: "GET a URL and show status, timing and body",
				Safety: plugin.Read, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: common,
				Run:    runMethod(stdhttp.MethodGet),
			},
			{
				ID: "http.head", Summary: "HEAD a URL and show status, timing and headers",
				Safety: plugin.Read, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: common,
				Run:    runMethod(stdhttp.MethodHead),
			},
			// POST/PUT/DELETE mutate remote state: write class, confirmed on
			// production profiles once those exist.
			//
			// They need the grant for every reason GET does and one more: a
			// body. Leaving them ungated while GET is gated would have been
			// backwards — --allow-write is one switch for the whole registry,
			// so an operator who turned it on for `todo add` would have handed
			// an agent an unlimited, unlogged POST to anywhere, which is a
			// larger egress channel than the one two capabilities above are
			// gated for.
			{
				ID: "http.post", Summary: "POST to a URL with an optional body",
				Safety:     plugin.Write,
				NeedsGrant: true, Scope: "url",
				Inputs: withBody,
				Run:    runMethod(stdhttp.MethodPost),
			},
			{
				ID: "http.put", Summary: "PUT to a URL with an optional body",
				Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: withBody,
				Run:    runMethod(stdhttp.MethodPut),
			},
			{
				ID: "http.delete", Summary: "DELETE a URL",
				Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: common,
				Run:    runMethod(stdhttp.MethodDelete),
			},
		},
	}
}

func runMethod(method string) plugin.Handler {
	return func(ctx context.Context, req plugin.Request) (view.View, error) {
		return doRequest(ctx, method, req)
	}
}

func doRequest(ctx context.Context, method string, req plugin.Request) (view.View, error) {
	url := req.String("url")
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	timeout := time.Duration(req.Int("timeout")) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if data := req.String("data"); data != "" {
		body = strings.NewReader(data)
	}
	httpReq, err := stdhttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, view.Errorf("http.request.invalid", "building request: %v", err)
	}
	for _, h := range req.StringSlice("header") {
		key, value, found := strings.Cut(h, ":")
		if !found {
			return nil, view.Errorf("http.header.invalid", "invalid header %q", h).
				WithHint("use 'Key: Value' form")
		}
		httpReq.Header.Set(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	if bearer := req.String("bearer"); bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearer)
	}
	if basic := req.String("basic"); basic != "" {
		user, pass, _ := strings.Cut(basic, ":")
		httpReq.SetBasicAuth(user, pass)
	}

	// A dry run must not reach the network. POST, PUT and DELETE are writes
	// on somebody else's system, and a --dry-run that sends the request
	// anyway is worse than none at all: it reports what "would" happen after
	// it has already happened.
	if req.DryRun && method != stdhttp.MethodGet && method != stdhttp.MethodHead {
		return dryRunView(method, url, httpReq, req.String("data")), nil
	}

	// Coarse phase timing via httptrace.
	var dnsDone, connectDone, firstByte time.Time
	start := time.Now()
	trace := &httptrace.ClientTrace{
		DNSDone:              func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectDone:          func(string, string, error) { connectDone = time.Now() },
		GotFirstResponseByte: func() { firstByte = time.Now() },
	}
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(httpReq.Context(), trace))

	resp, err := client.Do(httpReq)
	if err != nil {
		var blocked *blockedAddrError
		if errors.As(err, &blocked) {
			return nil, view.Errorf("http.request.blocked", "%s %s: %v", method, url, err).
				WithHint("the destination resolves to a loopback, private, or link-local address " +
					"(this includes cloud metadata endpoints) — rta refuses to connect there even " +
					"though the grant named this URL")
		}
		return nil, view.Errorf("http.request.failed", "%s %s: %v", method, url, err).
			WithHint("check the URL is reachable; use --timeout to extend the deadline")
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, view.Errorf("http.body.read", "reading response body: %v", err)
	}
	total := time.Since(start)

	pairs := []view.Pair{
		{Key: "status", Value: resp.Status},
		{Key: "time", Value: total.Round(time.Millisecond).String()},
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		// Not followed — see client's comment. Shown explicitly so a
		// redirect is never silently invisible to whoever asked.
		pairs = append(pairs, view.Pair{Key: "location (not followed)", Value: loc})
	}
	if !dnsDone.IsZero() && !connectDone.IsZero() && !firstByte.IsZero() {
		pairs = append(pairs, view.Pair{Key: "timing", Value: fmt.Sprintf(
			"dns %s · connect %s · ttfb %s",
			dnsDone.Sub(start).Round(time.Millisecond),
			connectDone.Sub(start).Round(time.Millisecond),
			firstByte.Sub(start).Round(time.Millisecond),
		)})
	}
	sizeValue := fmt.Sprintf("%d B", len(bodyBytes))
	// io.LimitReader above caps what's read at maxBody: past it, bodyBytes
	// is a prefix of the real response, not the whole thing. Showing its
	// length as "size" with nothing else said reads as the true size — a
	// 5 MB body cut to 1 MiB is a partial answer nobody could tell apart
	// from a complete one. HEAD never has a body to cut — 0 bytes is by
	// design, not truncation — so it is excluded rather than flagged.
	if method != stdhttp.MethodHead && bodyWasTruncated(resp, len(bodyBytes)) {
		sizeValue += " (truncated, showing first 1 MiB)"
	}
	pairs = append(pairs,
		view.Pair{Key: "content-type", Value: resp.Header.Get("Content-Type")},
		view.Pair{Key: "size", Value: sizeValue},
	)
	if len(bodyBytes) > 0 && method != stdhttp.MethodHead {
		pairs = append(pairs, view.Pair{Key: "body", Value: formatBody(bodyBytes, resp.Header.Get("Content-Type"))})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// dryRunView shows the request that was not sent, in enough detail to check
// it — including the headers, with anything carrying a credential masked.
// The point is to inspect a request before it leaves, not to reprint the
// secret you are about to send.
func dryRunView(method, url string, httpReq *stdhttp.Request, data string) view.View {
	pairs := []view.Pair{
		{Key: "dry run", Value: "nothing was sent"},
		{Key: "method", Value: method},
		{Key: "url", Value: url},
	}
	names := make([]string, 0, len(httpReq.Header))
	for name := range httpReq.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	redacted := []string{}
	for _, name := range names {
		value := strings.Join(httpReq.Header.Values(name), ", ")
		key := "header:" + name
		if name == "Authorization" || name == "Cookie" || name == "Proxy-Authorization" {
			redacted = append(redacted, key)
		}
		pairs = append(pairs, view.Pair{Key: key, Value: value})
	}
	if data != "" {
		pairs = append(pairs,
			view.Pair{Key: "body size", Value: fmt.Sprintf("%d B", len(data))},
			view.Pair{Key: "body", Value: data})
	}
	return view.KeyValue{Pairs: pairs, Redacted: redacted}
}

// bodyWasTruncated reports whether captured — what io.LimitReader actually
// let through — is a prefix of the response rather than the whole thing.
// Either the cap itself was hit, or the server said up front, via
// Content-Length, that more was coming than the cap allows; a response can
// hit the second without the first only if it closed early, which is still
// bodyBytes short of what was promised and just as worth flagging.
func bodyWasTruncated(resp *stdhttp.Response, captured int) bool {
	return captured == maxBody || (resp.ContentLength >= 0 && resp.ContentLength > maxBody)
}

// formatBody pretty-prints JSON responses and truncates the rest sensibly.
func formatBody(body []byte, contentType string) string {
	if strings.Contains(contentType, "json") {
		var buf map[string]any
		if err := json.Unmarshal(body, &buf); err == nil {
			if pretty, err := json.MarshalIndent(buf, "", "  "); err == nil {
				return string(pretty)
			}
		}
		var arr []any
		if err := json.Unmarshal(body, &arr); err == nil {
			if pretty, err := json.MarshalIndent(arr, "", "  "); err == nil {
				return string(pretty)
			}
		}
	}
	const maxShown = 4096
	s := string(body)
	if len(s) > maxShown {
		return s[:maxShown] + fmt.Sprintf("\n… (%d more bytes)", len(s)-maxShown)
	}
	return s
}

// suggestHeaders offers the request headers people actually set by hand,
// already shaped as "Name: " so the completion lands mid-value rather than
// mid-spelling. Header names are a registry, not a closed set — anything may
// be sent — so these are suggestions and never a constraint.
func suggestHeaders(context.Context, plugin.Request) []string {
	return []string{
		"Accept: application/json",
		"Content-Type: application/json",
		"Authorization: Bearer ",
		"User-Agent: rta",
		"X-Request-Id: ",
		"Accept-Encoding: gzip",
		"If-None-Match: ",
	}
}
