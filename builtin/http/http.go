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
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"

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
	target := []plugin.Field{
		{Name: "url", Type: plugin.String, Positional: true, Required: true, Help: "request URL"},
		// Worded for every surface it reaches. It said "-H 'Key: Value'",
		// curl's flag, which rta never had (the CLI spells it --header), and
		// a help string is also an agent's tool description and a form's
		// label, where no flag is typed at all.
		{Name: "header", Type: plugin.StringSlice, Suggest: suggestHeaders,
			Help: "request header as 'Key: Value', repeatable"},
	}
	options := []plugin.Field{
		// The four ways to authenticate are one block of a form. They are declared
		// into it rather than left to the fallback because that names the
		// connection of a capability whose config states one, and a request has
		// none: its address is the call.
		//
		// Secret, not String, and it always should have been: both are
		// credentials, so both belong masked in a form rather than drawn in
		// the clear, and neither is a value anything should keep. Declaring
		// them plainly is what let internal/recent write a bearer token to
		// disk and offer it back on a completion list.
		//
		// Not Local: an agent granted a URL may legitimately supply its own
		// authorization for it, which is what Local would take away.
		{Name: "bearer", Type: plugin.Secret, Group: "credentials", Help: "bearer token (Authorization: Bearer ...)"},
		{Name: "basic", Type: plugin.Secret, Group: "credentials", Help: "basic auth as user:password"},
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
		{Name: "bearer-file", Type: plugin.Path, Local: true, Group: "credentials",
			Help: "read the bearer token from this file (/dev/stdin for a pipe), which a shell does not keep and ps does not show"},
		{Name: "basic-file", Type: plugin.Path, Local: true, Group: "credentials",
			Help: "read user:password from this file (/dev/stdin for a pipe), which a shell does not keep and ps does not show"},
		// Local, for the reason the two files above are: it widens what the request
		// may reach, and only the person at the terminal may ask (withOwnNetwork).
		{Name: "local-network", Type: plugin.Bool, Local: true,
			Help: "allow a loopback or private address — a service of your own; never offered to agents"},
		{Name: "timeout", Type: plugin.Duration, Config: "timeout", Default: "30s", Min: "1s", Max: "10m", Help: "request timeout"},
	}
	common := slices.Concat(target, options)
	body := []plugin.Field{{
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
		{Name: "data-file", Type: plugin.Path, Local: true,
			Help: "read the request body from this file (/dev/stdin for a pipe) in place of data"},
	}
	// The body follows the url and the header and comes before the credentials
	// and the timeout. A form asks in the order the inputs are declared, and the
	// body is what a POST is for: declared last it was the ninth box, eight
	// enters from the url.
	withBody := slices.Concat(target, body, options)
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
				ID: "http.get", Summary: "GET a URL and show status, timing and body", Keywords: []string{"api", "endpoint", "request", "fetch", "curl", "download"},
				Safety: plugin.Read, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: common,
				// The body alone when the answer goes to a pipe, so
				// `http get URL | jq .` is handed the document and the status stays on stderr.
				Primary: "body",
				Run:     runMethod(stdhttp.MethodGet),
			},
			{
				ID: "http.head", Summary: "HEAD a URL and show status, timing, type and declared size", Keywords: []string{"api", "endpoint", "request"},
				Safety: plugin.Read, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: common,
				Run:    runMethod(stdhttp.MethodHead),
			},
			// POST and PUT change what the URL names: writes, as an overwrite
			// is (kv.set). DELETE removes it, and nothing rta can offer brings
			// it back, so it is destructive: a person is shown the request and
			// asked before it is sent, a script says --yes, and an agent's
			// client is told the tool destroys (MCP's destructiveHint). It was
			// a write beside the other two, classed by method rather than by
			// what each one does, which told every client that a DELETE only
			// ever adds.
			//
			// They need the grant for every reason GET does, and the two with
			// a body for one more. Leaving them ungated while GET is gated would
			// have been backwards — one switch for the whole registry, so an
			// operator who turned it on for `note add` would have handed an
			// agent an unlimited, unlogged POST to anywhere, which is a larger
			// egress channel than the one two capabilities above are gated
			// for. DELETE's class asks for the grant itself, as kv.rm's does.
			{
				ID: "http.post", Summary: "POST to a URL with an optional body", Keywords: []string{"api", "endpoint", "request", "submit"},
				Safety:     plugin.Write,
				NeedsGrant: true, Scope: "url",
				Inputs: withBody,
				// The body alone when the answer goes to a pipe, so
				// `http get URL | jq .` is handed the document and the status stays on stderr.
				Primary: "body",
				Run:     runMethod(stdhttp.MethodPost),
			},
			{
				ID: "http.put", Summary: "PUT to a URL with an optional body", Keywords: []string{"api", "endpoint", "request"},
				Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "url",
				Inputs: withBody,
				// The body alone when the answer goes to a pipe, so
				// `http get URL | jq .` is handed the document and the status stays on stderr.
				Primary: "body",
				Run:     runMethod(stdhttp.MethodPut),
			},
			{
				ID: "http.delete", Summary: "DELETE a URL", Keywords: []string{"api", "endpoint", "request"},
				Safety: plugin.Destructive, Idempotent: true,
				Scope:  "url",
				Inputs: common,
				// The body alone when the answer goes to a pipe, so
				// `http get URL | jq .` is handed the document and the status stays on stderr.
				Primary: "body",
				Run:     runMethod(stdhttp.MethodDelete),
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
	// The server answered, in plain http, to a request that spoke TLS: it was
	// reached, so reachability is not the question, and the scheme is the fix. A
	// name that might be private keeps the https a URL without a scheme gets
	// (withScheme), which is how a service of one's own on http lands here.
	if strings.Contains(err.Error(), "server gave HTTP response to HTTPS client") {
		hint := "the server answered in plain http; write the URL with http://"
		if parsed, perr := neturl.Parse(url); perr == nil && parsed.Host != "" {
			hint = "the server answered in plain http; write the URL as http://" + parsed.Host + parsed.RequestURI()
		}
		return verr.WithHint(hint).WithInput("url")
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
			// The URL is the input to change only when the resolver answered that
			// its host does not exist, which is what a mistyped name gets. A
			// resolver that did not answer names none, since the URL may be right
			// and the network is what failed; and nor does a name that is not the
			// URL's, which the proxy the environment names can be, looked up on
			// the way to the host.
			if dnsErr.IsNotFound && strings.EqualFold(dnsErr.Name, parsed.Hostname()) {
				verr = verr.WithInput("url")
			}
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

// credentialInput is where the credential under name comes from: its input,
// or the file named by name+"-file", never both — two answers to one question
// are a refusal, not a guess. Nothing is read: a dry run names the file and
// leaves what is in it where it is (dryRunOf).
func credentialInput(req plugin.Request, name string) (value, file string, verr *view.Error) {
	value, file = req.String(name), req.String(name+"-file")
	if value != "" && file != "" {
		return "", "", view.Errorf("http.auth.twice", "%s is given as a value and as a file", name).
			WithHint("give one: " + req.Surface().InputName(name) + " or " + req.Surface().InputName(name+"-file"))
	}
	return value, file, nil
}

// credential is the credential the call carries under name (credentialInput).
// A file is read as the CLI reads any path, so /dev/stdin is a pipe, and a
// line break the editor or `echo` left at its end is no part of a token.
func credential(req plugin.Request, name string) (string, *view.Error) {
	value, file, verr := credentialInput(req, name)
	if verr != nil || file == "" {
		return value, verr
	}
	data, err := pathin.Read(req, file, maxCredential)
	var tooLarge *pathin.TooLargeError
	switch {
	case errors.As(err, &tooLarge):
		return "", view.Errorf("http.auth.file", "%s is more than a %s holds", file, name).WithInput(name + "-file")
	case err != nil:
		return "", pipeSpelling(view.Errorf("http.auth.file", "reading the %s from %s: %v", name, file, err).
			WithInput(pathin.Input(name+"-file", err)), file)
	}
	if got := strings.TrimRight(string(data), "\r\n"); got != "" {
		return got, nil
	}
	return "", view.Errorf("http.auth.file", "%s holds no %s", file, name).
		WithHint("an empty file, or nothing was piped to it").WithInput(name + "-file")
}

// setCredential puts the credential under name on r, in the header it is sent
// in.
func setCredential(r *stdhttp.Request, name, value string) {
	switch name {
	case "bearer":
		r.Header.Set("Authorization", "Bearer "+value)
	case "basic":
		user, pass, _ := strings.Cut(value, ":")
		r.SetBasicAuth(user, pass)
	}
}

// pipeSpelling points a `-` that named a file at the spelling a Path input
// takes for a pipe. curl and most of what a person has typed before read
// standard input from `-`, and "open -: no such file or directory" gives no way
// from there to the answer; accepting it here as well would leave this client
// with two spellings where the rest of the guide has one.
func pipeSpelling(e *view.Error, file string) *view.Error {
	if file == "-" {
		return e.WithHint("a pipe is /dev/stdin, as it is for every path rta reads")
	}
	return e
}

// curlFileForm refuses a body that is curl's way of naming a file, `--data
// @payload.json` or `--data @-`, where the person who typed it has such a file
// or means a pipe. Sent as written it was the text "@payload.json", which the
// server answers with an error about its body and nothing about this command
// line, and the person typed what they have typed for years.
//
// The file form is not offered in its place, for the reason data-file exists:
// the same text over MCP is the agent's own and must never read a path, so the
// surface that is an agent's is left alone and never looks at the disk. A body
// that really begins with @ and names a file goes in through data-file.
func curlFileForm(req plugin.Request, data string) *view.Error {
	name, found := strings.CutPrefix(data, "@")
	if !found || name == "" || req.Surface() == plugin.SurfaceMCP {
		return nil
	}
	if name == "-" {
		name = "/dev/stdin"
	} else if !namesAFile(req, name) {
		return nil
	}
	return view.Errorf("http.data.atfile", "the body %q is how curl names a file, and %s sends text as written", data, req.Surface().InputName("data")).
		WithHint("to send the file, give it to " + req.Surface().InputName("data-file") + ": " + name).WithInput("data")
}

func namesAFile(req plugin.Request, name string) bool {
	info, err := pathin.Stat(req, name)
	return err == nil && !info.IsDir()
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
		if verr := curlFileForm(req, data); verr != nil {
			return "", verr
		}
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
			file, maxRequestBody>>20).WithInput("data-file")
	case err != nil:
		return "", pipeSpelling(view.Errorf("http.data.file", "reading the body from %s: %v", file, err).
			WithInput(pathin.Input("data-file", err)), file)
	}
	return string(got), nil
}

// withScheme is the URL a call names with the scheme it left out filled in:
// https, except for a host that is a service of one's own, which a person who
// asked for the local network (own) is almost never reaching over TLS.
//
// Decided from the text alone — localhost, or an address that is loopback or
// private — and never by looking the name up: the scheme is fixed before the
// connection is made, and a name that answers with a private address now and a
// public one a moment later (a rebinding record) must not be able to turn a
// request meant to be encrypted into a plain one carrying its credentials. A
// name that merely might be private, grafana.internal, keeps https, and the one
// who knows better writes http://. Port 443 keeps https as well: nobody means
// plain text there.
//
// Only the one name, and not the ones under it. RFC 6761 asks a resolver to
// answer every name under .localhost with the loopback address, and asks no
// more than that: this binary has no resolver of its own that does, so what
// svc.localhost means is whatever the DNS server it reaches says, and one that
// answers every name that does not exist with a public address, as an ISP's
// does, would have been sent the request in plain text with its bearer token.
// localhost itself is in the hosts file of every machine, which is read before
// any server is asked.
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
	if host == "localhost" {
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
	timeout := req.Duration("timeout")
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
		return nil, view.Errorf("http.request.invalid", "building request: %v", err).WithInput("url")
	}
	// What net/http itself will not send is refused here, before anything
	// leaves, a dry run included. A scheme this client does not speak, a URL
	// with no host, `http:///x`, and a header name with a space in it or a value
	// holding a line break each built a request; each failed only inside the
	// transport, after the address guard had looked the host up, as a request
	// that "failed", under the hint for a name that did not resolve when it did
	// not, and a dry run previewed a request that could never be sent.
	if s := httpReq.URL.Scheme; s != "http" && s != "https" {
		return nil, view.Errorf("http.request.invalid", "%s is not an http or https URL", url).
			WithHint("this client speaks http and https; write the URL with one of them").WithInput("url")
	}
	if httpReq.URL.Host == "" {
		return nil, view.Errorf("http.request.invalid", "%s names no host", url).
			WithHint("write the URL with the host in it, as https://api.example.com/items").WithInput("url")
	}
	for _, h := range wholeHeaders(req.Surface(), req.StringSlice("header")) {
		key, value, found := strings.Cut(h, ":")
		if !found {
			return nil, view.Errorf("http.header.invalid", "invalid header %q", h).
				WithHint("use 'Key: Value' form").WithInput("header")
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		// Named and not quoted whole: the value is where a credential goes.
		switch {
		case !httpguts.ValidHeaderFieldName(key):
			return nil, view.Errorf("http.header.invalid", "%q is not a header name net/http will send", key).
				WithHint("a header name is one word of letters, digits and dashes, as Content-Type").WithInput("header")
		case !httpguts.ValidHeaderFieldValue(value):
			return nil, view.Errorf("http.header.invalid", "the value of %s is not one net/http will send", key).
				WithHint("a header value is one line, with no control characters in it").WithInput("header")
		}
		httpReq.Header.Set(key, value)
	}

	// A dry run must not reach the network. POST, PUT and DELETE change
	// somebody else's system, and a --dry-run that sends the request anyway
	// is worse than none at all: it reports what "would" happen after it has
	// already happened.
	//
	// A GET and a HEAD are held to it as well, although they change nothing
	// there: every command accepts --dry-run, and the person who adds it to
	// a request carrying a token is asking to see where it would go before it
	// goes. A read that ran anyway sent that token to a mistyped host, which
	// the preview is for.
	//
	// So it returns before the credentials are read and before the host is
	// looked up, both of which are dryRunOf's to explain.
	if req.DryRun {
		return dryRunOf(req, method, url, httpReq, data)
	}
	for _, name := range []string{"bearer", "basic"} {
		value, verr := credential(req, name)
		if verr != nil {
			return nil, verr
		}
		if value != "" {
			setCredential(httpReq, name, value)
		}
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
	cutAtTheCap := false
	if len(bodyBytes) > 0 && method != stdhttp.MethodHead {
		body, cut := formatBody(bodyBytes, resp.Header.Get("Content-Type"), truncated)
		// A pipe is handed the body the server sent, up to the cap, and not
		// what a screen shows of it: a program asked for the document, and
		// `jq .` or `> file` of a cut or re-indented one carried on with it.
		// A body that is not text keeps its dump, which no view can carry as
		// the bytes they are.
		if req.PipedPrimary() {
			if fetched, ok := bodyAsFetched(bodyBytes, truncated); ok {
				body, cut, cutAtTheCap = fetched, truncated, truncated
			}
		}
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
	answer := view.KeyValue{Pairs: pairs}
	if cutAtTheCap {
		return nil, &plugin.AnsweredError{View: answer, Err: bodyCutAtTheCap(req.Surface(), size)}
	}
	return answer, nil
}

// bodyAsFetched is body as the server sent it, or ok false for one that is not
// text. A body cut at the cap loses the character it was cut in, as it does on
// a screen.
func bodyAsFetched(body []byte, truncated bool) (fetched string, ok bool) {
	if truncated {
		body = withoutPartialRune(body)
	}
	if !format.PlainText(body) {
		return "", false
	}
	return string(body), true
}

// bodyCutAtTheCap is the failure of a pipe that was handed the first megabyte
// of a longer response. What was written is a prefix of a document, and a
// script that took it for the document would carry on with half of one.
func bodyCutAtTheCap(sf plugin.Surface, size string) *view.Error {
	return view.Errorf("http.body.truncated",
		"the response body is %s, and rta reads at most 1 MiB of one: the first 1 MiB was written", size).
		WithHint("ask for the rest in parts with a Range header (" + sf.InputName("header") +
			" 'Range: bytes=1048576-'), or fetch it with a tool that streams, such as curl")
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

// dryRunOf is what a dry run answers: the request as it would leave, built
// without reading a credential file and without looking the host up.
//
// A file is named rather than read. A preview that read a pipe or a process
// substitution left nothing in it for the run the person then said yes to,
// which failed on a file that "holds no bearer" — and a DELETE is asked about
// with its preview in front of the question, at a terminal and in the TUI, so
// its preview is always read before its run.
//
// The host is not looked up, because a lookup sends the name to a resolver,
// which is reaching the network, and the preview of a parked agent call
// (internal/mcp's propose) runs before anybody has agreed to anything: a name
// the agent chose went out with nobody's leave, and a name is enough to carry
// what an agent wants to send somewhere. The address is held to the guard when
// the request is sent, as it always was (checkDestination).
func dryRunOf(req plugin.Request, method, url string, httpReq *stdhttp.Request, data string) (view.View, error) {
	var from []view.Pair
	for _, name := range []string{"bearer", "basic"} {
		value, file, verr := credentialInput(req, name)
		if verr != nil {
			return nil, verr
		}
		if file != "" {
			// The header is shown masked, as one carrying a value given in
			// the call is, and where it would come from beside it.
			value = "unread"
			from = append(from, view.Pair{Key: name + " from", Value: file})
		}
		if value != "" {
			setCredential(httpReq, name, value)
		}
	}
	setDefaultHeaders(httpReq.Header, data)
	if method == stdhttp.MethodDelete {
		return wouldDelete(req, url, httpReq.Header, from), nil
	}
	return dryRunView(method, url, httpReq, data, from), nil
}

// wouldDelete is a DELETE's dry run: one line, as every destructive
// capability's is, because it is the line a person is asked about before it
// runs, at a terminal and in the TUI, and the one an operator reads on a parked
// agent call, which shows a preview of one line and nothing else
// (internal/mcp's propose).
//
// The credential it would carry is named by where it comes from, and every
// other header the call gives by its name alone: a value is the caller's to
// know, and the one that matters here is masked everywhere else.
func wouldDelete(req plugin.Request, url string, header stdhttp.Header, from []view.Pair) view.Text {
	line := "would send DELETE " + url
	var with []string
	files := map[string]string{}
	for _, p := range from {
		files[strings.TrimSuffix(p.Key, " from")] = p.Value
	}
	switch auth := header.Get("Authorization"); {
	case files["basic"] != "":
		with = append(with, "the user:password in "+files["basic"])
	case req.String("basic") != "":
		with = append(with, "a user and password")
	case files["bearer"] != "":
		with = append(with, "the bearer token in "+files["bearer"])
	case req.String("bearer") != "":
		with = append(with, "a bearer token")
	case auth != "":
		with = append(with, "the Authorization header given")
	}
	var named []string
	for _, h := range wholeHeaders(req.Surface(), req.StringSlice("header")) {
		key, _, _ := strings.Cut(h, ":")
		if key = stdhttp.CanonicalHeaderKey(strings.TrimSpace(key)); key != "Authorization" {
			named = append(named, key)
		}
	}
	switch len(named) {
	case 0:
	case 1:
		with = append(with, "the header "+named[0])
	default:
		with = append(with, "the headers "+strings.Join(named, ", "))
	}
	if len(with) > 0 {
		line += " with " + strings.Join(with, " and ")
	}
	return view.Text{Body: line}
}

// dryRunView shows the request that was not sent, in enough detail to check
// it — including the headers, with anything carrying a credential masked, and
// the file a credential would be read from (from) beside them. The point is
// to inspect a request before it leaves, not to reprint the secret you are
// about to send.
func dryRunView(method, url string, httpReq *stdhttp.Request, data string, from []view.Pair) view.View {
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
	pairs = append(pairs, from...)
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
