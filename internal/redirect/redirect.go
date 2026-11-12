// Package redirect is the redirect policy rta's own clients follow when they
// fetch what anybody may fetch: release assets, OCI blobs, and the public
// catalogues that eol, audit.deps and the package registries read from.
//
// **Any host, never down to plain http, and never a write turned into a read.**
// net/http follows a redirect anywhere, ten times, in any direction. Another
// host is not a reason to stop here: a GitHub release asset is served from
// another host by design, the content is checked against its digest after, and
// a hop between two https hosts is as trustworthy as the first. A hop from
// https to http is: it hands the request, and the answer rta then believes —
// a version, a vulnerability list, an artifact's checksum — to anybody on the
// path. And a 301, 302 or 303 answered to a POST is sent again as a GET
// without its body, which turns osv's batch query into a request for nothing
// that can come back looking answered.
//
// Not the policy for a request that carries a credential of the operator's,
// which belongs to the address it was given: the operator channel follows no
// redirect, http.* hands every one back, and gitignore and audit.web keep to
// the host they started at.
package redirect

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
)

// maxHops is net/http's own bound, kept: a download may be redirected a few
// times on its way to a blob store, and ten is past every one of those.
const maxHops = 10

// Follow is an http.Client's CheckRedirect under this package's policy.
func Follow(next *http.Request, via []*http.Request) error {
	prev := via[len(via)-1]
	switch {
	case len(via) >= maxHops:
		return fmt.Errorf("stopped after %d redirects", len(via))
	case next.URL.Scheme != "https" && slices.ContainsFunc(via, func(r *http.Request) bool { return r.URL.Scheme == "https" }):
		return fmt.Errorf("%s answered with a redirect to %s, over plain http, which rta does not follow from https",
			origin(prev.URL), origin(next.URL))
	case next.Method != prev.Method:
		return fmt.Errorf("%s answered this %s with a redirect that would send it again as a %s without its body, "+
			"which rta does not follow", origin(prev.URL), prev.Method, next.Method)
	}
	return nil
}

// Client is a client under the policy with nothing else set, for a caller
// that bounds each request with its context.
func Client() *http.Client { return &http.Client{CheckRedirect: Follow} }

// origin is a URL as a refusal names it: scheme and host, never the path or
// query, where a signed download carries its token.
func origin(u *url.URL) string { return u.Scheme + "://" + u.Host }
