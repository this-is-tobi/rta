package http

import (
	"context"
	stdhttp "net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// statusCapability is the reference card for HTTP status codes, offline.
//
// It lives in this built-in and not in a plugin by the same rule that put
// eol in the binary: no network, no configuration, and a question a person
// asks in the middle of reading a response somebody else's service sent —
// which is exactly when reaching for a browser costs the most. The text is
// the standard's own (net/http's StatusText) with one sentence per code on
// what it usually means in practice, and the RFC that defines it.
func statusCapability() plugin.Capability {
	return plugin.Capability{
		ID:      "http.status",
		Summary: "What an HTTP status code means, and where it is defined",
		Safety:  plugin.Read, Idempotent: true,
		// Not a tile: every input is optional, which is what qualifies a
		// capability for the automatic dashboard, and a table of seventy
		// status codes is a reference you open on purpose, not a glance.
		NoPreview: true,
		Description: "A code prints its card; a class (`4xx`, `500-599`) or nothing prints the " +
			"table. Offline: the text is the standard's, and every code net/http knows is here.",
		Inputs: []plugin.Field{
			{Name: "code", Type: plugin.String, Positional: true,
				Help:    "a code (404), a class (4xx) or a range (500-599); every code when omitted",
				Suggest: suggestStatusCodes},
		},
		Run: runStatus,
	}
}

func suggestStatusCodes(context.Context, plugin.Request) []string {
	out := []string{"2xx\tsuccess", "3xx\tredirection", "4xx\tclient error", "5xx\tserver error"}
	for _, c := range knownStatusCodes() {
		out = append(out, strconv.Itoa(c)+"\t"+stdhttp.StatusText(c))
	}
	return out
}

// knownStatusCodes is every code net/http has a text for, ascending.
func knownStatusCodes() []int {
	var out []int
	for c := 100; c < 600; c++ {
		if stdhttp.StatusText(c) != "" {
			out = append(out, c)
		}
	}
	sort.Ints(out)
	return out
}

func runStatus(_ context.Context, req plugin.Request) (view.View, error) {
	lo, hi, verr := statusRange(req.String("code"))
	if verr != nil {
		return nil, verr
	}
	if lo == hi {
		return statusCard(lo)
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Code", Kind: view.KindNumber},
		{Name: "Text"},
		{Name: "Means"},
	}}
	for _, c := range knownStatusCodes() {
		if c < lo || c > hi {
			continue
		}
		t.Rows = append(t.Rows, []string{strconv.Itoa(c), stdhttp.StatusText(c), statusMeaning(c)})
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return nil, view.Errorf("http.status.unknown", "no status codes between %d and %d", lo, hi)
	}
	return t, nil
}

// statusRange reads "404", "4xx", "500-599" or "" into an inclusive range.
func statusRange(raw string) (lo, hi int, verr *view.Error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch {
	case raw == "":
		return 100, 599, nil
	case len(raw) == 3 && strings.HasSuffix(raw, "xx"):
		class, err := strconv.Atoi(raw[:1])
		if err != nil || class < 1 || class > 5 {
			return 0, 0, badStatus(raw)
		}
		return class * 100, class*100 + 99, nil
	case strings.Contains(raw, "-"):
		a, b, _ := strings.Cut(raw, "-")
		lo, err1 := strconv.Atoi(strings.TrimSpace(a))
		hi, err2 := strconv.Atoi(strings.TrimSpace(b))
		if err1 != nil || err2 != nil || lo > hi {
			return 0, 0, badStatus(raw)
		}
		return lo, hi, nil
	default:
		c, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, badStatus(raw)
		}
		return c, c, nil
	}
}

func badStatus(raw string) *view.Error {
	return view.Errorf("http.status.badcode", "%q is not a status code, a class or a range", raw).
		WithHint("try 404, 4xx or 500-599")
}

func statusCard(code int) (view.View, error) {
	text := stdhttp.StatusText(code)
	if text == "" {
		return nil, view.Errorf("http.status.unknown", "%d is not a status code net/http knows", code).
			WithHint("run `rta http status` for the table")
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "code", Value: strconv.Itoa(code)},
		{Key: "text", Value: text},
		{Key: "class", Value: statusClass(code)},
		{Key: "means", Value: statusMeaning(code)},
		{Key: "defined in", Value: statusRFC(code)},
	}}, nil
}

func statusClass(code int) string {
	switch code / 100 {
	case 1:
		return "1xx informational — the request continues"
	case 2:
		return "2xx success"
	case 3:
		return "3xx redirection — look somewhere else"
	case 4:
		return "4xx client error — the request is the problem"
	default:
		return "5xx server error — the server is the problem"
	}
}

// statusMeaning is the sentence the standard's short text does not give:
// what a person reading a response usually needs to know next.
func statusMeaning(code int) string {
	if m, ok := statusMeanings[code]; ok {
		return m
	}
	return "see the RFC"
}

func statusRFC(code int) string {
	switch code {
	case 102:
		return "RFC 2518 (WebDAV)"
	case 103:
		return "RFC 8297"
	case 207, 422, 423, 424, 507:
		return "RFC 4918 (WebDAV)"
	case 208:
		return "RFC 5842 (WebDAV)"
	case 226:
		return "RFC 3229"
	case 308:
		return "RFC 7538"
	case 418:
		return "RFC 2324 (an April Fools' joke, still answered by some servers)"
	case 421:
		return "RFC 9113 (HTTP/2)"
	case 425:
		return "RFC 8470"
	case 426:
		return "RFC 9110"
	case 428, 429, 431, 511:
		return "RFC 6585"
	case 451:
		return "RFC 7725"
	case 506:
		return "RFC 2295"
	case 508:
		return "RFC 5842 (WebDAV)"
	case 510:
		return "RFC 2774"
	default:
		return "RFC 9110 (HTTP Semantics)"
	}
}

var statusMeanings = map[int]string{
	100: "keep sending the body; the headers were acceptable",
	101: "the connection is switching to the protocol the client asked for (websockets, usually)",
	200: "it worked, and the body is the answer",
	201: "it worked and something now exists; Location usually says where",
	202: "accepted for processing later; nothing is promised about the outcome yet",
	204: "it worked and there is deliberately no body",
	206: "part of the resource, as the Range header asked",
	301: "moved for good; update the link, and clients may change POST to GET",
	302: "found elsewhere for now; clients may change POST to GET",
	303: "look at another URL with GET, whatever the method was",
	304: "unchanged since the version the client already holds; no body on purpose",
	307: "try again at another URL with the same method and body",
	308: "moved for good, same method and body — 301 without the POST-to-GET surprise",
	400: "the request itself is malformed; fix the request, retrying will not help",
	401: "no usable credentials; WWW-Authenticate says what kind is wanted",
	403: "the credentials are fine and still not enough; authenticating again will not help",
	404: "nothing at that path — or the server would rather not say whether there is",
	405: "the path exists but not for that method; Allow lists what does",
	406: "nothing the server has matches the Accept headers",
	408: "the client took too long to send the request",
	409: "the request conflicts with the current state (an edit on a stale version, a duplicate name)",
	410: "gone for good, and the server is saying so on purpose",
	411: "a Content-Length is required and was not sent",
	412: "a precondition header (If-Match, If-Unmodified-Since) failed",
	413: "the body is larger than the server will take",
	414: "the URL is longer than the server will take",
	415: "the body's Content-Type is not one the server accepts",
	416: "the Range asked for is outside the resource",
	417: "the Expect header cannot be met",
	418: "the server is a teapot",
	421: "sent to a server that is not configured to answer for that host",
	422: "well-formed but semantically wrong: it parsed, and then failed validation",
	423: "the resource is locked (WebDAV)",
	425: "the server will not risk a replay of an early-data request",
	426: "upgrade to the protocol named in the Upgrade header first",
	428: "the request must be conditional (send If-Match) to avoid a lost update",
	429: "too many requests; Retry-After says when to come back",
	431: "a header, or all of them together, is too large",
	451: "blocked for legal reasons",
	500: "the server hit an error it did not handle; the logs on that side say what",
	501: "the server does not implement that method at all",
	502: "a proxy or gateway got a bad answer from the server behind it",
	503: "overloaded or down for maintenance; Retry-After may say for how long",
	504: "a proxy or gateway waited for the server behind it and gave up",
	505: "the HTTP version in the request is not supported",
	507: "the server is out of storage for this request (WebDAV)",
	511: "a captive portal wants a login before the network lets traffic through",
}
