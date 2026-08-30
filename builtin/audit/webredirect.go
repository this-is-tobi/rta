package audit

import (
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
)

// Where a web audit is allowed to go, and how it says where it went.
//
// `audit.web` is NeedsGrant with Scope "host" — "this agent may audit
// staging.internal for the next fifteen minutes" is the sentence an operator
// signs. A client that follows a 3xx wherever it points turns that sentence
// into "…and anywhere staging.internal cares to send it", with no second
// check, because the bridge authorizes the call once before Run starts.
// builtin/http reached the same conclusion first and refuses redirects
// outright for it, naming the concrete case: an authorized public endpoint
// that 302s to a cloud metadata service.
//
// Refusing outright is the wrong answer here, though, because a host audit
// that stops at the apex's 301 to www is an audit nobody can use. The scope
// itself supplies the line: a grant names a *host*, so a hop that stays on
// that host is inside what was authorized and a hop that leaves it is not.
// An http -> https upgrade is the same host on a different port and is
// followed; example.com -> www.example.com is not, and is handed back as a
// command instead.

// maxRedirects bounds a same-host chain. Five is past every legitimate
// canonicalisation (scheme, then trailing slash, then locale) and short of a
// loop worth waiting out; Go's own default of ten is a general-purpose
// number for a client that is not holding a grant.
const maxRedirects = 5

// followSameHost is the client's redirect policy, closed over the URL the
// operator named. ErrUseLastResponse hands back the 3xx itself — status,
// headers and Location — so the report can grade that response and say
// exactly where it would have gone.
func followSameHost(requested *url.URL) func(*stdhttp.Request, []*stdhttp.Request) error {
	return func(next *stdhttp.Request, via []*stdhttp.Request) error {
		if len(via) >= maxRedirects || !sameHost(requested, next.URL) {
			return stdhttp.ErrUseLastResponse
		}
		return nil
	}
}

// sameHost compares hostnames and deliberately ignores the port: an
// http://host -> https://host upgrade changes the port and is the single
// most common hop there is. DNS is case-insensitive, so the comparison is.
func sameHost(a, b *url.URL) bool { return strings.EqualFold(a.Hostname(), b.Hostname()) }

// redirectTarget is where an unfollowed response points, absolute, or nil if
// it points nowhere. A 304 is a 3xx that carries no Location, which is why
// the status class alone is not the test.
func redirectTarget(resp *stdhttp.Response) *url.URL {
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return nil
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil
	}
	abs, err := resp.Request.URL.Parse(loc)
	if err != nil {
		return nil
	}
	return abs
}

// auditRedirect names the response every other check in this report is about.
//
// Without it the report has no way to say which URL it describes, and the
// failure that hides is not a smaller truth but a different host's report
// under the wrong name: `audit web example.com` graded www.example.com's
// headers, cookies and certificate while the row above said example.com.
func auditRedirect(r *report, requested *url.URL, resp *stdhttp.Response) {
	landed := resp.Request.URL
	to := redirectTarget(resp)
	switch {
	case to != nil && !sameHost(requested, to):
		r.add(grpTransport, "redirect", stWarn,
			"not followed — "+to.String()+" is a different host, so the checks below grade this "+
				"redirect and not the page it points at. `rta audit web "+to.String()+
				"` audits that; the request stops at the host you named because a grant on this "+
				"capability names one host", reference{})
	case to != nil:
		// Same host and still redirecting: the hop bound is the only way here.
		r.add(grpTransport, "redirect", stWarn,
			"still redirecting after "+plural(maxRedirects, "hop")+" — "+landed.String()+" → "+
				to.String()+", so the checks below grade a redirect rather than a page", reference{})
	case landed.String() != requested.String():
		r.add(grpTransport, "redirect", stInfo,
			requested.String()+" → "+landed.String()+" ("+strconv.Itoa(resp.StatusCode)+
				") — same host, followed", reference{})
	}
}
