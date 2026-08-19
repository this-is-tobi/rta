// Package http is the built-in REST client plugin: request any endpoint with
// auth, inspect status/headers/timing/body. Zero configuration; saved
// collections and OAuth2/SigV4 come later with profiles.
package http

import (
	"context"
	"encoding/json"
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

// Plugin returns the http plugin declaration.
func Plugin() plugin.Plugin {
	common := []plugin.Field{
		{Name: "url", Type: plugin.String, Positional: true, Required: true, Help: "request URL"},
		{Name: "header", Type: plugin.StringSlice, Suggest: suggestHeaders,
			Help: "request header, repeatable: -H 'Key: Value'"},
		{Name: "bearer", Type: plugin.String, Help: "bearer token (Authorization: Bearer ...)"},
		{Name: "basic", Type: plugin.String, Help: "basic auth as user:password"},
		{Name: "timeout", Type: plugin.Int, Default: 30, Min: 1, Max: 600, Help: "request timeout in seconds"},
	}
	withBody := append([]plugin.Field{}, common...)
	withBody = append(withBody, plugin.Field{
		Name: "data", Type: plugin.String, Help: "request body (use @file to read from a file, - for stdin)",
	})
	return plugin.Plugin{
		Name:    "http",
		Summary: "REST client: request endpoints, inspect responses",
		Capabilities: []plugin.Capability{
			{
				ID: "http.get", Summary: "GET a URL and show status, timing and body",
				Safety: plugin.Read, Idempotent: true,
				Inputs: common,
				Run:    runMethod(stdhttp.MethodGet),
			},
			{
				ID: "http.head", Summary: "HEAD a URL and show status, timing and headers",
				Safety: plugin.Read, Idempotent: true,
				Inputs: common,
				Run:    runMethod(stdhttp.MethodHead),
			},
			{
				// POST/PUT/DELETE mutate remote state: write class, confirmed
				// on production profiles once those exist.
				ID: "http.post", Summary: "POST to a URL with an optional body",
				Safety: plugin.Write,
				Inputs: withBody,
				Run:    runMethod(stdhttp.MethodPost),
			},
			{
				ID: "http.put", Summary: "PUT to a URL with an optional body",
				Safety: plugin.Write, Idempotent: true,
				Inputs: withBody,
				Run:    runMethod(stdhttp.MethodPut),
			},
			{
				ID: "http.delete", Summary: "DELETE a URL",
				Safety: plugin.Write, Idempotent: true,
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

	resp, err := stdhttp.DefaultClient.Do(httpReq)
	if err != nil {
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
	if !dnsDone.IsZero() && !connectDone.IsZero() && !firstByte.IsZero() {
		pairs = append(pairs, view.Pair{Key: "timing", Value: fmt.Sprintf(
			"dns %s · connect %s · ttfb %s",
			dnsDone.Sub(start).Round(time.Millisecond),
			connectDone.Sub(start).Round(time.Millisecond),
			firstByte.Sub(start).Round(time.Millisecond),
		)})
	}
	pairs = append(pairs,
		view.Pair{Key: "content-type", Value: resp.Header.Get("Content-Type")},
		view.Pair{Key: "size", Value: fmt.Sprintf("%d B", len(bodyBytes))},
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
