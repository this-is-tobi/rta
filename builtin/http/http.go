// Package http is the built-in REST client plugin: request any endpoint with
// auth, inspect status/headers/timing/body. Zero configuration; saved
// collections and OAuth2/SigV4 come later with profiles.
package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdnet "net"
	stdhttp "net/http"
	"net/http/httptrace"
	"net/netip"
	neturl "net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/internal/headerlist"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
		// Worded for every surface it reaches. It said "-H 'Key: Value'",
		// curl's flag, which rta never had (the CLI spells it --header), and
		// a help string is also an agent's tool description and a form's
		// label, where no flag is typed at all.
		{Name: "header", Type: plugin.StringSlice, Suggest: suggestHeaders,
			Help: "request header as 'Key: Value', repeatable"},
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
		// Local, unlike the two above, and for the opposite reason: a path is a
		// place on this machine, and an agent that could name one would read any
		// file the server can into a request it sends to a host of its own
		// choosing. A person at a terminal keeps it, and `--bearer-file
		// /dev/stdin` is how a token is piped in (pathin reads a stream where
		// the CLI is, as `fs hash /dev/stdin` does), which is what the flag is
		// for: --bearer is in the shell's history and, while the call runs, in a
		// process table every user on the machine can read.
		//
		// Not the environment, though kv's passphrase has one: RTA_HTTP_GET_BEARER
		// would follow every granted URL an agent names, so a token exported for
		// one host went to whichever other the agent was allowed, the redirect
		// Resolve skips a profile to avoid. A file or a pipe is chosen per call.
		{Name: "bearer-file", Type: plugin.Path, Local: true,
			Help: "read the bearer token from this file (/dev/stdin for a pipe), which a shell does not keep and ps does not show"},
		{Name: "basic-file", Type: plugin.Path, Local: true,
			Help: "read user:password from this file (/dev/stdin for a pipe), which a shell does not keep and ps does not show"},
		// Local, for the reason the two files above are: it widens what the request
		// may reach, and only the person at the terminal may ask (withOwnNetwork).
		{Name: "local-network", Type: plugin.Bool, Local: true,
			Help: "allow a loopback or private address — a service of your own; never offered to agents"},
		{Name: "timeout", Type: plugin.Int, Config: "timeout", Default: 30, Min: 1, Max: 600, Help: "request timeout in seconds"},
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
		Name: "data", Type: plugin.Text,
		Help: "request body, sent as given; a JSON object or array goes out as application/json unless a Content-Type header says otherwise",
	},
		// Local, as the two credential files above are and for their reason: a
		// path is a place on this machine, and an agent that could name one would
		// send any file the server can read to a host of its own choosing. The
		// body stays an input an agent supplies; only where it is read from is
		// the person's. Not an `@file` spelled into data, as curl has it: that
		// would be a value the CLI reads from disk and MCP sends as it is, and the
		// same text meaning two things on two surfaces is how a path ends up read
		// for an agent.
		plugin.Field{Name: "data-file", Type: plugin.Path, Local: true,
			Help: "read the request body from this file (/dev/stdin for a pipe) in place of data"})
	return plugin.Plugin{
		Name:    "http",
		Summary: "Request any endpoint and inspect the response — a REST client",
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
				ID: "http.head", Summary: "HEAD a URL and show status, timing, type and declared size",
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
			// backwards — one switch for the whole registry,
			// so an operator who turned it on for `note add` would have handed
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
			statusCapability(),
		},
	}
}

// requestFailed is the refusal for a request that failed for any other
// reason than the address guard. A certificate Apple's verifier refused by a
// rule of its own gets that rule and its fix for a hint (plugin.
// CertPolicyHint): a ten-year certificate, the usual one for a lab, was
// "not standards compliant", and the reader was told to check the URL was
// reachable, which it was.
func requestFailed(sf plugin.Surface, method, url string, err error) *view.Error {
	verr := view.Errorf("http.request.failed", "%s %s: %v", method, url, err)
	if hint := plugin.CertPolicyHint(err); hint != "" {
		return verr.WithHint(hint)
	}
	// Nothing was sent: the transport refused the URL's scheme before it
	// dialled, which reachability and a longer deadline cannot change.
	if strings.Contains(err.Error(), "unsupported protocol scheme") {
		return verr.WithHint("this client speaks http and https; write the URL with one of them")
	}
	// The server answered, in plain http, to a request that spoke TLS: it was
	// reached, so reachability is not the question, and the scheme is the fix. A
	// name that might be private keeps the https a URL without a scheme gets
	// (withScheme), which is how a service of one's own on http lands here.
	if strings.Contains(err.Error(), "server gave HTTP response to HTTPS client") {
		hint := "the server answered in plain http; write the URL with http://"
		if parsed, perr := neturl.Parse(url); perr == nil && parsed.Host != "" {
			hint = "the server answered in plain http; write the URL as http://" + parsed.Host + parsed.RequestURI()
		}
		return verr.WithHint(hint)
	}
	// The handshake got an answer, and the certificate in it did not pass: the
	// host is reachable and a longer deadline would change nothing. This client
	// has no way round a certificate (it is the point of it), so the way
	// forward is to see what the server presented — a tool of this binary's own.
	var unverified *tls.CertificateVerificationError
	if errors.As(err, &unverified) {
		hint := "the server answered, but its certificate did not pass this machine's checks"
		if parsed, perr := neturl.Parse(url); perr == nil && parsed.Host != "" {
			hint += " — `" + sf.Call("cert.chain", plugin.Arg{Name: "target", Value: parsed.Host, Positional: true}) +
				"` shows what it presented"
		}
		return verr.WithHint(hint)
	}
	// A name the resolver could not answer for sent nothing: a longer deadline
	// cannot make a mistyped host exist, and the tool that says whether the name
	// or the lookup failed is this binary's own.
	var dnsErr *stdnet.DNSError
	if errors.As(err, &dnsErr) {
		hint := "the name did not resolve, so nothing was sent"
		if parsed, perr := neturl.Parse(url); perr == nil && parsed.Hostname() != "" {
			hint += " — `" + sf.Call("net.dns", plugin.Arg{Name: "name", Value: parsed.Hostname(), Positional: true}) +
				"` asks the resolver for it directly"
		}
		return verr.WithHint(hint)
	}
	// The deadline is only what a request that ran out of time can change; a
	// refused or reset connection answered at once.
	var netErr stdnet.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return verr.WithHint("check the URL is reachable; " + sf.InputName("timeout") + " extends the deadline")
	}
	return verr.WithHint("check the URL is reachable")
}

// blockedRefusal is the refusal for a request err says was stopped by the
// address guard in ssrf.go, or nil when err is some other failure. One
// place for it, since the destination is checked before a route is picked
// and again as each connection is dialed, and the reader of either refusal
// is owed the same account of it.
//
// Worded for who asked. An agent is told a grant does not move it, so it
// does not go asking for one; a person at the terminal was told the same
// about a grant nobody had issued, when what they need to know is that
// the guard holds for them too.
func blockedRefusal(sf plugin.Surface, method, url string, err error) *view.Error {
	var blocked *blockedAddrError
	if !errors.As(err, &blocked) {
		return nil
	}
	hint := "rta's http client connects only to public addresses — never loopback, private, " +
		"link-local, shared (100.64.0.0/10) or reserved ones, where cloud metadata endpoints live"
	if sf == plugin.SurfaceMCP {
		hint += " — and a grant naming this URL does not change that"
	} else if reasonFor(blocked.ip, true) == "" {
		// A loopback or private address is what a service of one's own is on,
		// which a person at the terminal may ask for by name (withOwnNetwork).
		hint += " — at the terminal too, unless you ask for it: " + sf.InputName("local-network") +
			" allows a loopback or private address, a service of your own, and never an address cloud " +
			"metadata lives at"
	} else {
		hint += " — at the terminal too, with no way round it: nothing of your own is at an address like that"
	}
	return view.Errorf("http.request.blocked", "%s %s: %v", method, url, err).WithHint(hint)
}

func runMethod(method string) plugin.Handler {
	return func(ctx context.Context, req plugin.Request) (view.View, error) {
		return doRequest(ctx, method, req)
	}
}

// wholeHeaders gives back the headers a person meant from the list a surface
// that splits on commas made of them (see headerlist.Join). Only on the
// surfaces that cut: over MCP the list arrives as the caller wrote it, and an
// entry that is not a header is the caller's mistake to be told of.
func wholeHeaders(s plugin.Surface, pieces []string) []string {
	if s == plugin.SurfaceMCP {
		return pieces
	}
	return headerlist.Join(pieces)
}

// maxCredential is more than any token or user:password pair a person keeps
// in a file.
const maxCredential = 64 << 10

// credential is the credential the call carries under name, from its input
// or from the file named by name+"-file", never both: two answers to one
// question are a refusal, not a guess. The file is read as the CLI reads any
// path, so /dev/stdin is a pipe, and a line break the editor or `echo` left
// at its end is no part of a token.
func credential(req plugin.Request, name string) (string, *view.Error) {
	value, file := req.String(name), req.String(name+"-file")
	switch {
	case file == "":
		return value, nil
	case value != "":
		return "", view.Errorf("http.auth.twice", "%s is given as a value and as a file", name).
			WithHint("give one: " + req.Surface().InputName(name) + " or " + req.Surface().InputName(name+"-file"))
	}
	data, err := pathin.Read(req, file, maxCredential)
	var tooLarge *pathin.TooLargeError
	switch {
	case errors.As(err, &tooLarge):
		return "", view.Errorf("http.auth.file", "%s is more than a %s holds", file, name)
	case err != nil:
		return "", view.Errorf("http.auth.file", "reading the %s from %s: %v", name, file, err)
	}
	if got := strings.TrimRight(string(data), "\r\n"); got != "" {
		return got, nil
	}
	return "", view.Errorf("http.auth.file", "%s holds no %s", file, name).
		WithHint("an empty file, or nothing was piped to it")
}

// maxRequestBody is more than any JSON payload somebody keeps in a file and
// hands to an API, and well under what a pipe left running would fill memory
// with.
const maxRequestBody = 8 << 20

// requestBody is the body the call carries, from its data input or from the
// file named by data-file, never both: two answers to one question are a
// refusal, not a guess.
func requestBody(req plugin.Request) (string, *view.Error) {
	data, file := req.String("data"), req.String("data-file")
	switch {
	case file == "":
		return data, nil
	case data != "":
		return "", view.Errorf("http.data.twice", "the body is given as a value and as a file").
			WithHint("give one: " + req.Surface().InputName("data") + " or " + req.Surface().InputName("data-file"))
	}
	got, err := pathin.Read(req, file, maxRequestBody)
	var tooLarge *pathin.TooLargeError
	switch {
	case errors.As(err, &tooLarge):
		return "", view.Errorf("http.data.file", "%s is more than %d MiB, which is the most a request body takes",
			file, maxRequestBody>>20)
	case err != nil:
		return "", view.Errorf("http.data.file", "reading the body from %s: %v", file, err)
	}
	return string(got), nil
}

// withScheme is the URL a call names with the scheme it left out filled in:
// https, except for a host that is a service of one's own, which a person who
// asked for the local network (own) is almost never reaching over TLS.
//
// Decided from the text alone — localhost, anything under it, or an address
// that is loopback or private — and never by looking the name up: the scheme is
// fixed before the connection is made, and a name that answers with a private
// address now and a public one a moment later (a rebinding record) must not be
// able to turn a request meant to be encrypted into a plain one carrying its
// credentials. A name that merely might be private, grafana.internal, keeps
// https, and the one who knows better writes http://. Port 443 keeps https as
// well: nobody means plain text there.
func withScheme(url string, own bool) string {
	if strings.Contains(url, "://") {
		return url
	}
	if own {
		if parsed, err := neturl.Parse("//" + url); err == nil && parsed.Port() != "443" && namesOwnHost(parsed.Hostname()) {
			return "http://" + url
		}
	}
	return "https://" + url
}

func namesOwnHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	return (addr.IsLoopback() || addr.IsPrivate()) && reasonFor(stdnet.IP(addr.AsSlice()), true) == ""
}

// setDefaultHeaders gives a request what a REST client's server expects of
// one and the caller did not write: that JSON is wanted back, who is asking,
// and, for a body that is a JSON object or array, that it is JSON.
//
// A header the caller wrote is never touched, whatever case its name was spelled
// in (Header.Set canonicalised it), and an empty one is how a default is taken
// away: net/http sends no User-Agent at all for an empty one.
//
// An object or array and not any text json.Valid accepts: 42, true and "text"
// are valid JSON and are far likelier a line of text than a JSON scalar, and
// claiming a type for a body the caller never called JSON is the guess this
// default must not make.
func setDefaultHeaders(h stdhttp.Header, body string) {
	fallback := func(name, value string) {
		if _, set := h[name]; !set {
			h.Set(name, value)
		}
	}
	fallback("Accept", "application/json, */*")
	fallback("User-Agent", userAgent())
	if isJSONDocument(body) {
		fallback("Content-Type", "application/json")
	}
}

func isJSONDocument(body string) bool {
	trimmed := strings.TrimSpace(body)
	return (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed))
}

// userAgent names this client and the build asking, the string `rta --version`
// reports; a run that recorded none is just rta.
func userAgent() string {
	if v := session.Self(); v != "" {
		return "rta/" + v
	}
	return "rta"
}

func doRequest(ctx context.Context, method string, req plugin.Request) (view.View, error) {
	// And never over MCP, whatever arrived: the input is Local and the bridge
	// refuses it there, so this is the second lock on the same door.
	own := req.Bool("local-network") && req.Surface() != plugin.SurfaceMCP
	url := withScheme(req.String("url"), own)
	timeout := time.Duration(req.Int("timeout")) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if own {
		ctx = withOwnNetwork(ctx)
	}

	data, verr := requestBody(req)
	if verr != nil {
		return nil, verr
	}
	var body io.Reader
	if data != "" {
		body = strings.NewReader(data)
	}
	httpReq, err := stdhttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, view.Errorf("http.request.invalid", "building request: %v", err)
	}
	for _, h := range wholeHeaders(req.Surface(), req.StringSlice("header")) {
		key, value, found := strings.Cut(h, ":")
		if !found {
			return nil, view.Errorf("http.header.invalid", "invalid header %q", h).
				WithHint("use 'Key: Value' form")
		}
		httpReq.Header.Set(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	bearer, verr := credential(req, "bearer")
	if verr != nil {
		return nil, verr
	}
	basic, verr := credential(req, "basic")
	if verr != nil {
		return nil, verr
	}
	if bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearer)
	}
	if basic != "" {
		user, pass, _ := strings.Cut(basic, ":")
		httpReq.SetBasicAuth(user, pass)
	}
	setDefaultHeaders(httpReq.Header, data)

	// Checked before anything picks a route, proxy or not — see ssrf.go's
	// checkDestination for why a proxy makes this the only check the real
	// destination ever gets, and withTrustedProxy for the other half: the
	// proxy itself, once, is not the caller's choice and must not be
	// refused as if it were.
	if err := checkDestination(ctx, httpReq.URL); err != nil {
		if verr := blockedRefusal(req.Surface(), method, url, err); verr != nil {
			return nil, verr
		}
		return nil, requestFailed(req.Surface(), method, url, err)
	}
	httpReq = withTrustedProxy(httpReq)

	// A dry run must not reach the network. POST, PUT and DELETE are writes
	// on somebody else's system, and a --dry-run that sends the request
	// anyway is worse than none at all: it reports what "would" happen after
	// it has already happened.
	if req.DryRun && method != stdhttp.MethodGet && method != stdhttp.MethodHead {
		return dryRunView(method, url, httpReq, data), nil
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
		if verr := blockedRefusal(req.Surface(), method, url, err); verr != nil {
			return nil, verr
		}
		return nil, requestFailed(req.Surface(), method, url, err)
	}
	defer resp.Body.Close()
	// One byte past the cap, so that a body of exactly maxBody bytes is told
	// from a longer one: reading to the cap alone called it truncated for
	// filling the read.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, view.Errorf("http.body.read", "reading response body: %v", err)
	}
	truncated := len(bodyBytes) > maxBody
	if truncated {
		bodyBytes = bodyBytes[:maxBody]
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
	// The size is the response's, not the read's. Past the cap bodyBytes is
	// a prefix of the real body, and its length shown as "size" with nothing
	// else said read as the true size — a 5 MB body cut to 1 MiB is a
	// partial answer nobody could tell apart from a complete one. HEAD reads
	// no body, so it never reaches the cap.
	size := sizeOf(resp, method, len(bodyBytes), truncated)
	sizeValue := size
	if truncated {
		sizeValue += " (truncated to the first 1 MiB)"
	}
	pairs = append(pairs,
		view.Pair{Key: "content-type", Value: resp.Header.Get("Content-Type")},
		view.Pair{Key: "size", Value: sizeValue},
	)
	if len(bodyBytes) > 0 && method != stdhttp.MethodHead {
		body, cut := formatBody(bodyBytes, resp.Header.Get("Content-Type"), truncated)
		// How much of the body is shown is said beside it, never in it. The
		// note was appended to the text it described, and every renderer
		// cleans a value with ansi.Strip, which reads an OSC or DCS left open
		// in the data as running to the end of the string: a body holding
		// ESC ] before the cut took the note with it and read as whole, in
		// pretty output, the TUI and to a model. It also counted what was
		// left against the megabyte read rather than the response, so a
		// 3 MiB body was said to have 1044480 more bytes.
		if cut {
			pairs = append(pairs, view.Pair{Key: "body shown",
				Value: fmt.Sprintf("the first %d B of %s", len(body), size)})
		}
		pairs = append(pairs, view.Pair{Key: "body", Value: body})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// sizeOf is the size of a response body as far as it is known: the bytes
// read when they were all of it, what Content-Length declared when they were
// not, and otherwise only that it is more than was read — a chunked body, or
// one net/http decompressed, has no length until it has all been read.
//
// A HEAD response is the exception: it has no body to read, and its
// Content-Length is the length of the body a GET would receive — usually the
// very thing a HEAD is sent to learn. Counting the empty read, the size said
// "0 B" beside a server declaring megabytes.
func sizeOf(resp *stdhttp.Response, method string, read int, truncated bool) string {
	switch {
	case method == stdhttp.MethodHead && resp.ContentLength >= 0:
		return fmt.Sprintf("%d B", resp.ContentLength)
	case method == stdhttp.MethodHead:
		return "not declared"
	case !truncated:
		return fmt.Sprintf("%d B", read)
	case resp.ContentLength > int64(read):
		return fmt.Sprintf("%d B", resp.ContentLength)
	}
	return fmt.Sprintf("more than %d B", read)
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

// formatBody pretty-prints JSON responses, truncates the rest of the text
// sensibly, and dumps what is not text. cut says the value is the first
// len(value) bytes of a longer text, and nothing else: the caller says so
// beside it. A dump says itself how much of the bytes it shows.
//
// JSON is re-indented, never re-encoded. Decoding it into map[string]any and
// marshalling it back — what this did — hands every number through float64,
// so a numeric id past 2^53 came back as a different id and a large one in
// exponent form; it escapes <, > and & as \u003c and friends; it sorts the
// keys the server sent in its own order; and it merges a key sent twice. A
// debugging client that shows a response other than the one it received has
// failed at the one thing it is for. json.Indent changes whitespace and
// nothing else.
//
// Two things either side of it. A byte order mark, which .NET and IIS put
// before their JSON, is not JSON, so Indent refused the body and it fell
// through to the text path unindented; it goes. And the newline almost every
// server ends a body with is trailing space Indent copies, which the layout
// drew as a last line of indentation alone; it goes too.
//
// truncated says body is the first maxBody bytes of a longer response. That
// cut is at a byte offset, so past the cap any text that is not ASCII almost
// always ends partway through a character — and a megabyte of Japanese was
// dumped as bytes that are not UTF-8. The fragment is dropped before asking.
// Nor is a cut body laid out as JSON. It is the start of a document, and
// Indent takes a start that parses on its own — a long number, a value and
// the padding after it — for the whole of one, which was then drawn as
// complete with nothing beside it to say how much was left out.
//
// Only a body that is UTF-8 is indented. JSON is UTF-8 by definition (RFC
// 8259), and json.Indent does not check: a string holding the raw bytes 0x9B
// and 0x9D, 8-bit CSI and OSC, went into the value as they came, where
// decoding and re-encoding had turned them into U+FFFD. A body that is not
// UTF-8 is not JSON, whatever its label says, and is dumped like any other
// body that is not text.
func formatBody(body []byte, contentType string, truncated bool) (value string, cut bool) {
	if truncated {
		body = withoutPartialRune(body)
	}
	if !truncated && strings.Contains(contentType, "json") && utf8.Valid(body) {
		src := bytes.TrimPrefix(body, utf8BOM)
		var pretty bytes.Buffer
		if indentFits(src, maxIndented(len(src))) && json.Indent(&pretty, src, "", "  ") == nil {
			return strings.TrimRight(pretty.String(), " \t\r\n"), false
		}
	}
	// A binary body is identified by its first bytes — PNG, gzip and a
	// DER certificate all announce themselves in the first line — and read
	// no further in a response view, so its dump is sixteen lines, not the
	// 256 the text limit would give it.
	const maxDumped = 256
	if !format.PlainText(body) {
		return format.Dump(body, maxDumped), false
	}
	head, rest := format.Head(string(body), maxShown)
	return head, rest > 0 || truncated
}

// maxShown is how much of a body that is text, and not JSON laid out, a
// response view shows.
const maxShown = 4096

var utf8BOM = []byte("\xef\xbb\xbf")

// maxIndented is how large the indented form of an n-byte body may be before
// it is shown as sent instead: four times the body, and 64 KiB more so a
// small one is never refused. Indenting is not bounded by the body, which is
// what this is for. Each line is indented two spaces per level and JSON may
// nest 10000 levels, so 22 KB of brackets and zeros asked json.Indent for
// 220 MB and a 1 MiB body for about 10 GB — an out-of-memory kill of the
// process, which for `rta mcp serve` is every tool an agent had open, on a
// body any fetched server chooses. What real responses need is far less: an
// API's own pretty-printing is well under twice its compact form, and a flat
// array of one-digit numbers, about the worst an ordinary body gets, is two
// and a half times.
func maxIndented(n int) int { return 4*n + 64<<10 }

// indentFits reports whether json.Indent, with no prefix and a two-space
// indent, would write at most limit bytes for src — without writing them. It
// follows appendIndent in encoding/json byte for byte: whitespace outside a
// string is dropped, a colon gains a space, a comma and a close bracket a
// newline and the indent of their level, and an open bracket one on the
// level inside it, unless the bracket closes at once.
//
// Whitespace after the value is the exception, and the one every body has:
// Indent's scanner reports it as the end of the value, not as space to skip,
// so Indent copies it as it stands — the newline a server ends a body with
// included. Left out, the count came one byte short of nearly every body.
//
// It is a measure and not a parser. On JSON that is not valid it counts
// something, and Indent, which runs only after this says yes, refuses it.
func indentFits(src []byte, limit int) bool {
	size, depth := 0, 0
	inString, escaped, opened, started := false, false, false, false
	for _, c := range src {
		if inString {
			size++
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			if started && depth == 0 && !opened {
				size++
			}
			continue
		}
		started = true
		if opened && c != '}' && c != ']' {
			opened = false
			depth++
			size += 1 + 2*depth
		}
		switch c {
		case '"':
			inString = true
			size++
		case '{', '[':
			opened = true
			size++
		case ',':
			size += 2 + 2*depth
		case ':':
			size += 2
		case '}', ']':
			if opened {
				opened = false
			} else {
				depth = max(depth-1, 0)
				size += 1 + 2*depth
			}
			size++
		default:
			size++
		}
		if size > limit {
			return false
		}
	}
	return size <= limit
}

// withoutPartialRune drops the start of a character cut off at the end of b,
// and leaves b as it is when its last character is whole — or is not UTF-8
// at all, which is for PlainText to say.
func withoutPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i > len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return b
			}
			return b[:i]
		}
	}
	return b
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
