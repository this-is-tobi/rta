package audit

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/x509check"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// corsProbeOrigin is sent as the request's Origin header so the audit can
// tell a genuinely open CORS policy from one that blindly reflects whatever
// Origin it is handed — the standard technique for spotting the second,
// dangerous case from a single request: no real browser would ever send
// this origin, so a server that echoes it back is echoing anything.
const corsProbeOrigin = "https://rta-audit-probe.invalid"

// hstsPreloadMinAge is the minimum max-age (seconds) the Chromium HSTS
// preload list requires for submission — the bar the grading below uses for
// "long enough to matter", distinct from OWASP's own 2-year recommendation
// for "as strong as it should be".
const hstsPreloadMinAge = 31536000 // 1 year

// Groups order the detail page and name its sections. A check belongs to
// exactly one, decided where the check is written rather than inferred.
const (
	grpTransport = "transport & tls"
	grpHeaders   = "security headers"
	grpCORS      = "cross-origin"
	grpCookies   = "cookies"
	grpExposure  = "information exposure"
)

var groupOrder = []string{grpTransport, grpHeaders, grpCORS, grpCookies, grpExposure}

// runWeb performs a single HTTPS request and grades what the host reveals.
// One round trip yields the response headers, the negotiated TLS state and
// the presented certificate chain — everything the audit needs. The Origin
// header sent along with it (see corsProbeOrigin) is the one deliberate
// addition beyond a plain GET, and only ever used to grade what comes back —
// nothing here crawls, brute-forces, or sends a second request.
func runWeb(ctx context.Context, req plugin.Request) (view.View, error) {
	target := normalizeURL(req.String("host"))
	u, err := url.Parse(target)
	if err != nil {
		return nil, view.Errorf("audit.web.badhost", "invalid host %q: %v", req.String("host"), err).
			WithHint("pass a host like example.com or a full https:// URL")
	}
	timeout := time.Duration(req.Int("timeout")) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Inspect, don't trust: skip verification so a bad chain still yields a
	// report; validity is graded from the presented certificates instead.
	client := &stdhttp.Client{
		Timeout: timeout,
		Transport: &stdhttp.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	httpReq, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, target, nil)
	if err != nil {
		return nil, view.Errorf("audit.web.request", "building request: %v", err)
	}
	httpReq.Header.Set("User-Agent", "rta-audit/1")
	httpReq.Header.Set("Origin", corsProbeOrigin)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, view.Errorf("audit.web.unreachable", "requesting %s: %v", target, err).
			WithHint("check the host is reachable over HTTPS; use --timeout to extend the deadline")
	}
	defer resp.Body.Close()

	r := &report{}
	auditTransport(r, u, resp)
	auditTLS(r, resp.TLS, u.Hostname())
	auditSecurityHeaders(r, resp.Header)
	auditCORS(r, resp.Header)
	auditCookies(r, resp)
	auditExposure(r, resp.Header)

	if req.Bool("detail") {
		return detailedWeb(ctx, req, r, resp)
	}
	return r.table(true), nil
}

// detailedWeb is the full-page report: the same findings, grouped into the
// areas a hardening pass actually works through one at a time, plus the
// controls they cite. Nothing is recomputed — a detail page is an
// arrangement of what the compact view already found, which is what keeps
// the two from ever disagreeing.
func detailedWeb(ctx context.Context, req plugin.Request, r *report, resp *stdhttp.Response) (view.View, error) {
	pairs := append([]view.Pair{
		{Key: "target", Value: resp.Request.URL.String()},
	}, r.grade()...)
	if resp.TLS != nil {
		pairs = append(pairs, view.Pair{Key: "tls", Value: tls.VersionName(resp.TLS.Version) +
			" · " + tls.CipherSuiteName(resp.TLS.CipherSuite)})
	}
	pairs = append(pairs, view.Pair{Key: "status", Value: resp.Status})

	return detailPage(ctx, req, r, groupOrder, view.KeyValue{Pairs: pairs}), nil
}

// normalizeURL defaults a bare host to HTTPS — the audit is about how a host
// secures its transport, so HTTPS is the subject.
func normalizeURL(host string) string {
	host = strings.TrimSpace(host)
	if !strings.Contains(host, "://") {
		return "https://" + host
	}
	return host
}

func auditTransport(r *report, u *url.URL, resp *stdhttp.Response) {
	if resp.TLS == nil || u.Scheme == "http" {
		r.add(grpTransport, "transport", stFail, "plaintext HTTP — traffic is unencrypted", refCleartext)
		return
	}
	// Where we landed after redirects: an http→https upgrade is good hygiene.
	final := resp.Request.URL
	if final.Scheme == "https" {
		r.add(grpTransport, "transport", stOK, "HTTPS", refCleartext)
	} else {
		r.add(grpTransport, "transport", stFail, "final response served over "+final.Scheme, refCleartext)
	}
}

func auditTLS(r *report, state *tls.ConnectionState, host string) {
	if state == nil {
		return
	}
	switch state.Version {
	case tls.VersionTLS13:
		r.add(grpTransport, "tls-version", stOK, "TLS 1.3", refWeakCrypto)
	case tls.VersionTLS12:
		r.add(grpTransport, "tls-version", stOK, "TLS 1.2", refWeakCrypto)
	default:
		r.add(grpTransport, "tls-version", stFail, tls.VersionName(state.Version)+" — deprecated, upgrade to TLS 1.2+", refWeakCrypto)
	}
	r.add(grpTransport, "tls-cipher", cipherGrade(state.CipherSuite), tls.CipherSuiteName(state.CipherSuite), refWeakCrypto)

	if len(state.PeerCertificates) == 0 {
		r.add(grpTransport, "certificate", stFail, "no certificate presented", refCertValidation)
		return
	}
	leaf := state.PeerCertificates[0]
	if s := x509check.Chain(state.PeerCertificates, host); s != "" {
		r.add(grpTransport, "cert-chain", stFail, s, refCertValidation)
	} else {
		r.add(grpTransport, "cert-chain", stOK, "valid for "+host, refCertValidation)
	}
	// The warning window is the shared default rather than a number local to
	// this file. It used to be 15 days here against `cert expiry`'s 30, so a
	// certificate 20 days out was "ok" from the audit and "WARN <30d" from
	// the cert check — same host, same minute, two answers.
	switch {
	case time.Now().After(leaf.NotAfter):
		r.add(grpTransport, "cert-expiry", stFail, "expired "+leaf.NotAfter.Format("2006-01-02"), refCertValidation)
	case x509check.Expiring(leaf.NotAfter, x509check.DefaultWarnDays):
		r.add(grpTransport, "cert-expiry", stWarn, fmt.Sprintf("expires %s (<%dd)",
			leaf.NotAfter.Format("2006-01-02"), x509check.DefaultWarnDays), refCertValidation)
	default:
		r.add(grpTransport, "cert-expiry", stOK, fmt.Sprintf("valid until %s (%dd)",
			leaf.NotAfter.Format("2006-01-02"), int(time.Until(leaf.NotAfter).Hours())/24), refCertValidation)
	}
	r.add(grpTransport, "cert-signature", sigAlgGrade(leaf.SignatureAlgorithm), leaf.SignatureAlgorithm.String(), refWeakCrypto)
}

// cipherGrade favors AEAD suites (GCM, ChaCha20-Poly1305) — TLS 1.3 offers
// nothing else, so this only ever bites on a TLS 1.2 negotiation that picked
// a CBC-mode or other non-AEAD suite, which Go's client will still accept.
func cipherGrade(id uint16) string {
	name := tls.CipherSuiteName(id)
	if strings.Contains(name, "GCM") || strings.Contains(name, "CHACHA20_POLY1305") {
		return stOK
	}
	return stWarn
}

// sigAlgGrade flags a certificate signed with a broken or deprecated hash —
// SHA-1 collisions have been practical since 2017, MD5/MD2 far longer.
func sigAlgGrade(alg x509.SignatureAlgorithm) string {
	switch alg {
	case x509.MD2WithRSA, x509.MD5WithRSA, x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1:
		return stFail
	default:
		return stOK
	}
}

// headerCheck grades one security header — or, for csp/x-frame-options,
// more than one header together, which is why grade reads the whole
// response rather than a single already-extracted value.
type headerCheck struct {
	label string
	ref   reference
	grade func(h stdhttp.Header) (status, detail string)
}

// presence is the common "any value is good, absent is a warning" grader.
func presence(name, missing string) func(stdhttp.Header) (string, string) {
	return func(h stdhttp.Header) (string, string) {
		if v := h.Get(name); v != "" {
			return stOK, v
		}
		return stWarn, missing
	}
}

// info is presence with a lower bar for absence: hardening a site does not
// yet universally expect these, so missing one is worth naming, not failing.
func info(name, missing string) func(stdhttp.Header) (string, string) {
	return func(h stdhttp.Header) (string, string) {
		if v := h.Get(name); v != "" {
			return stOK, v
		}
		return stInfo, missing
	}
}

var securityHeaders = []headerCheck{
	{"hsts", refMisconfig, func(h stdhttp.Header) (string, string) { return gradeHSTS(h.Get("Strict-Transport-Security")) }},
	{"csp", refMisconfig, func(h stdhttp.Header) (string, string) { return gradeCSP(h.Get("Content-Security-Policy")) }},
	{"x-content-type-options", refMisconfig, func(h stdhttp.Header) (string, string) {
		v := h.Get("X-Content-Type-Options")
		if strings.EqualFold(strings.TrimSpace(v), "nosniff") {
			return stOK, "nosniff"
		}
		if v == "" {
			return stWarn, "missing — set to nosniff"
		}
		return stWarn, "unexpected value: " + v
	}},
	{"x-frame-options", refClickjacking, func(h stdhttp.Header) (string, string) {
		return gradeFraming(h.Get("X-Frame-Options"), h.Get("Content-Security-Policy"))
	}},
	{"referrer-policy", refMisconfig, presence("Referrer-Policy", "missing — referrer may leak to third parties")},
	{"permissions-policy", refMisconfig, info("Permissions-Policy", "not set — browser feature access unrestricted")},
	// Cross-origin isolation headers: newer hardening most sites have not
	// adopted yet (they matter most for pages doing SharedArrayBuffer/high-
	// resolution-timer-shaped work), so absence is informational, not a warn.
	{"coop", refMisconfig, info("Cross-Origin-Opener-Policy", "not set — a cross-origin popup can retain a reference to this page's window")},
	{"coep", refMisconfig, info("Cross-Origin-Embedder-Policy", "not set — cross-origin isolation unavailable")},
	{"corp", refMisconfig, info("Cross-Origin-Resource-Policy", "not set — this response can be embedded cross-origin")},
}

func auditSecurityHeaders(r *report, h stdhttp.Header) {
	for _, hc := range securityHeaders {
		status, detail := hc.grade(h)
		r.add(grpHeaders, hc.label, status, detail, hc.ref)
	}
}

// gradeHSTS goes beyond presence: OWASP's HSTS cheat sheet recommends a
// two-year max-age with includeSubDomains; the Chromium HSTS preload list
// (hstspreload.org) requires at least one year to even be considered. A
// header that is there but too short-lived to matter reads as "missing" to
// an attacker waiting it out.
func gradeHSTS(v string) (string, string) {
	if v == "" {
		return stFail, "missing — no HSTS, downgrade attacks possible"
	}
	lower := strings.ToLower(v)
	maxAge := hstsMaxAge(lower)
	switch {
	case maxAge <= 0:
		return stWarn, "present but max-age<=0 — disables HSTS, effectively missing: " + v
	case maxAge < hstsPreloadMinAge:
		return stWarn, fmt.Sprintf("max-age too short for preload eligibility (%ds < 1y): %s", maxAge, v)
	case !strings.Contains(lower, "includesubdomains"):
		return stWarn, "no includeSubDomains — sibling subdomains stay exposed: " + v
	default:
		return stOK, v
	}
}

// hstsMaxAge extracts the max-age directive's value, or -1 if absent/unparseable.
func hstsMaxAge(lowerHeaderValue string) int {
	idx := strings.Index(lowerHeaderValue, "max-age=")
	if idx < 0 {
		return -1
	}
	rest := lowerHeaderValue[idx+len("max-age="):]
	if end := strings.IndexAny(rest, "; \t"); end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return -1
	}
	return n
}

// gradeCSP checks for the specific weaknesses OWASP's CSP cheat sheet calls
// out — 'unsafe-inline'/'unsafe-eval', wildcard sources, and the absence of
// object-src/base-uri/frame-ancestors — rather than treating any non-empty
// policy as sufficient. A CSP that allows 'unsafe-inline' provides close to
// no XSS defense at all, and "CSP present" alone would have said it did.
func gradeCSP(v string) (string, string) {
	if v == "" {
		return stWarn, "missing — no CSP, weaker XSS defense"
	}
	lower := strings.ToLower(v)
	var weak []string
	if strings.Contains(lower, "unsafe-inline") {
		weak = append(weak, "'unsafe-inline'")
	}
	if strings.Contains(lower, "unsafe-eval") {
		weak = append(weak, "'unsafe-eval'")
	}
	if cspHasWildcardSource(lower) {
		weak = append(weak, "wildcard (*) source")
	}
	for _, directive := range []string{"object-src", "base-uri", "frame-ancestors"} {
		if !strings.Contains(lower, directive) {
			weak = append(weak, "no "+directive)
		}
	}
	if len(weak) == 0 {
		return stOK, v
	}
	return stWarn, "weak: " + strings.Join(weak, ", ")
}

// cspHasWildcardSource reports whether any directive names a bare "*" as one
// of its source values — a bare token, not merely present in the string
// (which would also match a nonce or a real hostname containing one).
func cspHasWildcardSource(lowerCSP string) bool {
	for _, directive := range strings.Split(lowerCSP, ";") {
		// A policy ending in ";" — which most real ones do — yields a final
		// segment with no fields at all, so the directive name the loop
		// below skips past is not guaranteed to exist.
		fields := strings.Fields(directive)
		if len(fields) == 0 {
			continue
		}
		for _, f := range fields[1:] { // fields[0] is the directive name itself
			if f == "*" {
				return true
			}
		}
	}
	return false
}

// gradeFraming answers the actual question — can this page be framed — not
// just "is the legacy header present". CSP's frame-ancestors is the modern,
// stronger replacement for X-Frame-Options (it supports a source list and is
// enforced consistently; XFO's ALLOW-FROM is deprecated and non-standard),
// so a page relying on frame-ancestors alone is not clickjacking-vulnerable
// even with no X-Frame-Options at all.
func gradeFraming(xfo, csp string) (string, string) {
	hasXFO := xfo != ""
	hasFrameAncestors := strings.Contains(strings.ToLower(csp), "frame-ancestors")
	switch {
	case hasXFO && hasFrameAncestors:
		return stOK, xfo + " (+ CSP frame-ancestors)"
	case hasFrameAncestors:
		return stOK, "no X-Frame-Options, but CSP frame-ancestors covers it"
	case hasXFO:
		return stOK, xfo
	default:
		return stFail, "no X-Frame-Options and no CSP frame-ancestors — clickjacking is not defended against"
	}
}

// auditCORS grades what the host does with an Origin it cannot possibly
// recognize (corsProbeOrigin): reflecting it back — especially alongside
// credentials — is the classic misconfiguration that turns an
// authenticated API into one any other site can call on a victim's behalf.
// Silent when the response carries no CORS headers at all: most sites
// legitimately don't, and that is not itself a finding.
func auditCORS(r *report, h stdhttp.Header) {
	allowOrigin := h.Get("Access-Control-Allow-Origin")
	if allowOrigin == "" {
		return
	}
	creds := strings.EqualFold(strings.TrimSpace(h.Get("Access-Control-Allow-Credentials")), "true")
	switch {
	case allowOrigin == corsProbeOrigin && creds:
		r.add(grpCORS, "cors", stFail, "reflects an arbitrary Origin with credentials allowed — cross-origin account takeover risk", refCORS)
	case allowOrigin == corsProbeOrigin:
		r.add(grpCORS, "cors", stWarn, "reflects an arbitrary Origin ("+corsProbeOrigin+") — confirm this is intentional", refCORS)
	case allowOrigin == "*" && creds:
		r.add(grpCORS, "cors", stWarn, "wildcard origin with credentials allowed — browsers reject this combination, but it signals a misconfiguration", refCORS)
	case allowOrigin == "*":
		r.add(grpCORS, "cors", stInfo, "open to all origins (*) — fine for public, unauthenticated resources", refCORS)
	default:
		r.add(grpCORS, "cors", stOK, "restricted to: "+allowOrigin, refCORS)
	}
}

// exposureHeaders leak software and versions; presence is a finding.
var exposureHeaders = []struct{ name, label string }{
	{"Server", "server-header"},
	{"X-Powered-By", "x-powered-by"},
	{"X-AspNet-Version", "aspnet-version"},
	{"X-AspNetMvc-Version", "aspnetmvc-version"},
	{"X-Generator", "x-generator"},
	{"Via", "via"},
}

func auditExposure(r *report, h stdhttp.Header) {
	for _, e := range exposureHeaders {
		v := h.Get(e.name)
		if v == "" {
			continue
		}
		status := stInfo
		// A version number in the banner is the real risk: it hands an
		// attacker a CVE shortlist.
		if strings.ContainsAny(v, "0123456789") {
			status = stWarn
		}
		r.add(grpExposure, e.label, status, "discloses: "+v, refInfoExposure)
	}
	// X-XSS-Protection is legacy: modern guidance (including OWASP's own) is
	// that CSP supersedes it and the header itself has been the source of
	// browser-specific XSS bugs in the past — worth naming when present so a
	// hardening pass knows it is inherited config, not something to add.
	if v := h.Get("X-XSS-Protection"); v != "" {
		r.add(grpExposure, "x-xss-protection", stInfo, "present but deprecated — superseded by CSP: "+v, refMisconfig)
	}
}

// cookieAttrs are graded one per attribute rather than one per cookie: each
// missing attribute is its own weakness with its own CWE, and "which
// cookies are missing HttpOnly" is the question a fix is organized around.
var cookieAttrs = []struct {
	check   string
	ref     reference
	missing func(*stdhttp.Cookie) bool
	why     string
}{
	{"cookie-secure", refCookieSecure, func(c *stdhttp.Cookie) bool { return !c.Secure },
		"sent over plaintext if the site is ever reached over http"},
	{"cookie-httponly", refCookieHTTPOnly, func(c *stdhttp.Cookie) bool { return !c.HttpOnly },
		"readable by JavaScript, so any XSS steals the session"},
	{"cookie-samesite", refCSRF, func(c *stdhttp.Cookie) bool {
		return c.SameSite == stdhttp.SameSiteNoneMode || c.SameSite == 0
	}, "attached to cross-site requests, the precondition for CSRF"},
}

func auditCookies(r *report, resp *stdhttp.Response) {
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	for _, attr := range cookieAttrs {
		var weak []string
		for _, c := range cookies {
			if attr.missing(c) {
				weak = append(weak, c.Name)
			}
		}
		if len(weak) == 0 {
			r.add(grpCookies, attr.check, stOK, fmt.Sprintf("all %d cookie(s) set", len(cookies)), attr.ref)
			continue
		}
		sort.Strings(weak)
		r.add(grpCookies, attr.check, stWarn,
			strings.Join(weak, ", ")+" — "+attr.why, attr.ref)
	}
}
