package plugin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// RedirectPolicy is how a plugin's HTTP client answers a redirect.
//
// A plugin's request carries its credential in a header, an Authorization or
// an X-Vault-Token or an api-key, and Go's client forwards every header but
// Authorization and Cookie to wherever a redirect points, and those two as
// well to another port or scheme of the same host. A client that follows any
// redirect hands the credential to an address the server named, an SSO
// proxy's sign-in page among them, and one that turns a write answered 301,
// 302 or 303 into a GET reports a write that never happened as one that
// succeeded.
type RedirectPolicy int

const (
	// RedirectSameOrigin follows a redirect to the same scheme, host and port
	// as the request it answers, an omitted port being its scheme's and a host
	// compared in any case, at most five times, and only one that keeps the
	// request what it was: a 307 or a 308 keeps a write a write with its body,
	// and a 301, 302 or 303 on one would send it again as a GET without it.
	// The same origin is the server the operator named, verified the same way,
	// so following it sends the credential nowhere it was not already going,
	// and a base path or a trailing slash behind a reverse proxy just works.
	// Anything else is a RedirectRefusal, which a plugin words with Refusal.
	RedirectSameOrigin RedirectPolicy = iota
	// RedirectNever follows none: for a request whose signature binds the
	// address it was sent to.
	RedirectNever
)

// maxRedirects is past every legitimate canonicalisation, a trailing slash, a
// base path or a sign-in hop, and short of a loop.
const maxRedirects = 5

// RedirectReason is why a redirect was not followed.
type RedirectReason int

const (
	// RedirectElsewhere is another host or port.
	RedirectElsewhere RedirectReason = iota
	// RedirectUpgrade is plain HTTP sent on to HTTPS on the same host: the
	// call already crossed the network in the clear.
	RedirectUpgrade
	// RedirectDowngrade is HTTPS sent on to plain HTTP.
	RedirectDowngrade
	// RedirectRewrite is a 301, 302 or 303 answering a write, which would
	// have sent it again as a GET without its body.
	RedirectRewrite
	// RedirectLoop is more than five redirects for one call.
	RedirectLoop
	// RedirectPolicyNever is any redirect, to a client that follows none.
	RedirectPolicyNever
)

// RedirectRefusal is a redirect a policy did not follow: the 3xx, the method
// of the request it answered, and where it came from and pointed to. From and
// To keep only a scheme, a host and a port, since the path and query of a
// sign-in redirect can carry a token and a refusal is read, and recorded.
type RedirectRefusal struct {
	Status   int
	Method   string
	From, To *url.URL
	Reason   RedirectReason
}

func (e *RedirectRefusal) Error() string {
	return fmt.Sprintf("%s answered %s with %d to %s, which was not followed", e.From, e.Method, e.Status, e.To)
}

// CheckRedirect is p as an http.Client's CheckRedirect. Go hands it the
// request a redirect would send, method already rewritten, which is how a
// write turned into a GET is caught before it goes out.
func (p RedirectPolicy) CheckRedirect(next *http.Request, via []*http.Request) error {
	prev := via[len(via)-1]
	refuse := func(reason RedirectReason) error {
		status := 0
		if next.Response != nil {
			status = next.Response.StatusCode
		}
		return &RedirectRefusal{Status: status, Method: prev.Method,
			From: originOf(prev.URL), To: originOf(next.URL), Reason: reason}
	}
	switch {
	case p == RedirectNever:
		return refuse(RedirectPolicyNever)
	case len(via) > maxRedirects:
		return refuse(RedirectLoop)
	case prev.URL.Scheme == "https" && next.URL.Scheme != "https":
		return refuse(RedirectDowngrade)
	case !sameOrigin(prev.URL, next.URL):
		if prev.URL.Scheme == "http" && next.URL.Scheme == "https" && strings.EqualFold(prev.URL.Hostname(), next.URL.Hostname()) {
			return refuse(RedirectUpgrade)
		}
		return refuse(RedirectElsewhere)
	case next.Method != prev.Method:
		return refuse(RedirectRewrite)
	}
	return nil
}

// Refusal words e for req's reader under <namespace>.redirect.refused.
// setting is the input that holds the service's URL, named the way the
// reader changes it; hint is the product's own sentence for a redirect to
// another address, what in the product sends one and what to point at
// instead, "" for none. A forward is said to be why when the call went
// through one, since its fix is the profile's.
func (e *RedirectRefusal) Refusal(req Request, namespace, setting, hint string) *view.Error {
	sf := req.Surface()
	from, to := req.Reached(e.From.String()), e.To.String()
	forward := req.Tunnel() != TunnelNone
	var msg, fix string
	switch e.Reason {
	case RedirectElsewhere:
		msg = fmt.Sprintf("%s answered with a redirect to %s, another address, which rta does not follow", from, to)
		follow := "rta follows a redirect only within the address it was given, since a credential goes with every request"
		switch {
		case forward:
			fix = "the address it named is the service's own, outside the forward the profile opened, and rta " +
				"follows a redirect only within the address it was given: give the profile a connection that reaches it"
		case hint != "":
			fix = hint + " — " + follow
		default:
			fix = "if the service answers at " + to + ", set " + sf.SettingTo(setting, to) + "; " + follow
		}
	case RedirectUpgrade:
		msg = fmt.Sprintf("%s answered with a redirect to %s: it serves this over HTTPS, and this call went out over plain HTTP", from, to)
		if forward {
			fix = "`tunnel-tls: true` on that connection addresses the forward over HTTPS — the call crossed the " +
				"network in the clear before the redirect came back"
		} else {
			fix = sf.SettingTo(setting, to) + " reaches it directly — the call and its credential crossed the network " +
				"in the clear before the redirect came back, and following it would do so again on every call"
		}
	case RedirectDowngrade:
		msg = fmt.Sprintf("%s answered with a redirect to %s, over plain HTTP, which rta never follows", from, to)
		fix = "following it would send the credential in the clear: the service, or a proxy in front of it, sends " +
			"HTTPS clients to plain HTTP, and that is theirs to fix"
	case RedirectRewrite:
		msg = fmt.Sprintf("%s answered this %s with %d, which would have sent it again as a GET without its body; "+
			"it was not sent again", from, e.Method, e.Status)
		fix = "rta follows a redirect of a write only when it is a 307 or 308, which keep it a write — a proxy in " +
			"front of the service that answers writes with 301, 302 or 303 does this"
	case RedirectLoop:
		msg = fmt.Sprintf("%s redirected this call more than five times", from)
		fix = "the service, or a proxy in front of it, redirects in a loop"
	default:
		msg = fmt.Sprintf("%s answered with a redirect to %s, which this client never follows", from, to)
		fix = "point " + sf.SettingName(setting) + " at the address the service answers on"
	}
	return view.Errorf(namespace+".redirect.refused", "%s", msg).WithHint(fix)
}

// sameOrigin reports whether a and b are one scheme, one host in any case,
// and one port, an omitted port being the scheme's.
func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// port is u's port, its scheme's when it names none.
func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// originOf is u's scheme, host and port alone.
func originOf(u *url.URL) *url.URL {
	if u == nil {
		return &url.URL{}
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}
}
