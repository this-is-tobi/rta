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

	"github.com/this-is-tobi/rta/builtin/internal/x509check"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
var (
	grpTransport = findings.Group{ID: "transport", Title: "transport & tls"}
	grpHeaders   = findings.Group{ID: "headers", Title: "security headers"}
	grpCORS      = findings.Group{ID: "cors", Title: "cross-origin"}
	grpCookies   = findings.Group{ID: "cookies", Title: "cookies"}
	grpExposure  = findings.Group{ID: "exposure", Title: "information exposure"}
)

var groupOrder = []findings.Group{grpTransport, grpHeaders, grpCORS, grpCookies, grpExposure}

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
	// Refused rather than trimmed, as audit.mail refuses it, and for the
	// reason it does: the grant gate, the consent prompt and the ledger row
	// all quote the argument verbatim, and `staging.example.com@10.0.0.9`
	// reads as staging.example.com to whoever approves it while the request
	// goes to 10.0.0.9 — carrying the prefix as a Basic credential nobody
	// meant to send. A value that has to be read twice to find its host has
	// no business being the thing a grant names.
	if u.User != nil {
		return nil, view.Errorf("audit.web.badhost",
			"%q carries credentials before the host, so the host it audits is not the one it reads as",
			req.String("host")).
			WithHint("pass the host itself — the part after the @")
	}
	// A value with no host in it — `//x`, a path, `:8443` — was requested as
	// it stood: refused as unreachable, a hint to check the host was up, or,
	// for a bare port, dialled on this machine, which is not what it reads as.
	if u.Hostname() == "" {
		return nil, view.Errorf("audit.web.badhost", "%q names no host", req.String("host")).
			WithHint("pass a host like example.com or a full https:// URL")
	}
	timeout := time.Duration(req.Int("timeout")) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Inspect, don't trust: skip verification so a bad chain still yields a
	// report; validity is graded from the presented certificates instead.
	//
	// Nothing guards where this connects, and that is a decision rather than
	// an omission — it has now been reported twice as a missing SSRF guard,
	// so the reasoning lives here. builtin/http wires dialGuarded into its
	// transport to refuse loopback, RFC1918 and link-local; this client
	// deliberately does not, because "this agent may audit staging.internal
	// for the next fifteen minutes" — the sentence Scope: "host" was written
	// for, in audit.go's own words — names a private address by definition.
	// net.probe, net.port and cert.expiry all dial bare for the same reason;
	// http.get is the exception, not the rule, and it earns the exception by
	// buffering up to a megabyte of response body. This reads none: the body
	// is closed unread, and what reaches the caller is header values, cookie
	// names, TLS facts and a Location. Pointed at a cloud metadata endpoint
	// it returns the server banner and nothing else, because the credentials
	// there live in a body this never opens.
	//
	// What that leaves open, said plainly because it is easy to rediscover
	// and mistake for a bug: the grant authorizes a *name*, and this line is
	// the first thing that ever resolves it. The operator approved a host
	// minutes ago — a grant lives for its TTL — so a name answering with one
	// address then and another now sends the request somewhere they did not
	// picture, and a lying record does that without needing to win any race.
	// Closing it means resolving at approval time and carrying the address
	// into the request, which is one change across consent, grant and the
	// request shape covering all four capabilities at once, not a transport
	// swapped in here.
	client := &stdhttp.Client{
		Timeout: timeout,
		// A grant on this capability names one host (Scope: "host"), and the
		// bridge checks it once before Run starts. Following a 3xx off that
		// host would spend the operator's authorization somewhere they never
		// named — see followSameHost.
		CheckRedirect: followSameHost(u),
		Transport: &stdhttp.Transport{
			TLSClientConfig: auditTLSConfig(),
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
			WithHint("check the host is reachable over HTTPS; " + req.Surface().InputName("timeout") + " extends the deadline")
	}
	defer resp.Body.Close()

	r := &findings.Report{}
	// Every check below reads the response that came back, so the hostname
	// they are told about has to be the one it came from. Reading the
	// certificate against the URL that was *asked for* is how a valid
	// certificate for www.example.com got reported as invalid for
	// example.com.
	landed := resp.Request.URL
	auditRedirect(req.Surface(), r, u, resp)
	auditTransport(r, u, resp)
	auditTLS(r, resp.TLS, landed.Hostname())
	auditSecurityHeaders(r, resp.Header)
	// A 3xx is not a document, and grading it against a document's checklist
	// invents findings: a 301 to www needs no CSP and no framing policy, and
	// saying it does buries the one row that matters — where it points.
	if redirectTarget(resp) == nil {
		auditDocumentHeaders(r, resp.Header)
	}
	auditCORS(r, resp.Header)
	auditCookies(r, resp)
	auditExposure(r, resp.Header)

	if req.Bool("detail") {
		return detailedWeb(ctx, req, r, u, resp)
	}
	return r.Table(true), nil
}

// auditTLSConfig is the client side of an audit's handshake: it offers every
// protocol and suite Go can speak, and verifies nothing.
//
// An audit asks what a host negotiates, so it cannot hold the host to the
// floor a client that trusts the answer would. Go's client refuses below TLS
// 1.2 and no longer proposes 3DES or RSA key exchange, and a host speaking
// only those failed the handshake: the audit answered that the host was
// unreachable, with a hint to check that it was, about a host that answered
// — and the tls-version row that grades a deprecated protocol could never be
// reached. Offering them costs nothing against a host that has better,
// since the host chooses; against one that does not, what it chose is the
// finding. Nothing crosses this connection but a GET, and its body is never
// read.
func auditTLSConfig() *tls.Config {
	all := append(tls.CipherSuites(), tls.InsecureCipherSuites()...)
	suites := make([]uint16, len(all))
	for i, s := range all {
		suites[i] = s.ID
	}
	return &tls.Config{
		InsecureSkipVerify: true,             //nolint:gosec // the presented chain is graded instead
		MinVersion:         tls.VersionTLS10, //nolint:gosec // a deprecated protocol is the finding
		CipherSuites:       suites,
	}
}

// detailedWeb is the full-page report: the same findings, grouped into the
// areas a hardening pass actually works through one at a time, plus the
// controls they cite. Nothing is recomputed — a detail page is an
// arrangement of what the compact view already found, which is what keeps
// the two from ever disagreeing.
func detailedWeb(ctx context.Context, req plugin.Request, r *findings.Report, requested *url.URL, resp *stdhttp.Response) (view.View, error) {
	landed := resp.Request.URL
	pairs := []view.Pair{{Key: "target", Value: landed.String()}}
	if landed.String() != requested.String() {
		// The page says which URL it is about *and* which one was asked for,
		// because a report read later is read without the command that
		// produced it.
		pairs = append(pairs, view.Pair{Key: "requested", Value: requested.String()})
	}
	pairs = append(pairs, r.Grade()...)
	if resp.TLS != nil {
		pairs = append(pairs, view.Pair{Key: "tls", Value: tls.VersionName(resp.TLS.Version) +
			" · " + tls.CipherSuiteName(resp.TLS.CipherSuite)})
	}
	pairs = append(pairs, view.Pair{Key: "status", Value: resp.Status})
	// What one request cannot answer, and what does — the plugin-wide
	// convention, see deeper.go. On the detail page rather than the compact
	// table: the page is what somebody opens because the summary was not
	// enough, and a list of other people's tools is not a finding.
	pairs = append(pairs, webDeeper(landed.Host)...)

	return r.Page(ctx, req, groupOrder, view.KeyValue{Pairs: pairs}), nil
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

// auditTransport grades the transport the graded response actually arrived
// over, and names its relation to the URL that was asked for.
//
// It used to read the scheme off the requested URL while every other check
// read the response, so `audit web http://host` that upgrades to HTTPS was
// graded "plaintext HTTP — traffic is unencrypted" on the line directly above
// "tls-version ok TLS 1.3". An upgrade is the behaviour the check exists to
// ask for; a report that calls it a failure — and contradicts itself in the
// next row — teaches the reader to stop reading.
//
// The same swap makes the downgrade reportable. https that redirects to
// plaintext used to reach the same "plaintext HTTP" row as a site that was
// never encrypted at all, and the two are not the same finding: one has no
// certificate, the other has one and sends you past it.
func auditTransport(r *findings.Report, requested *url.URL, resp *stdhttp.Response) {
	landed := resp.Request.URL
	switch {
	case resp.TLS != nil && landed.Scheme == "https":
		if requested.Scheme == "http" {
			r.Add(grpTransport, "transport", findings.OK,
				"HTTPS — the plaintext URL redirects here, which is the upgrade to want", refCleartext)
			return
		}
		r.Add(grpTransport, "transport", findings.OK, "HTTPS", refCleartext)
	case requested.Scheme == "https":
		r.Add(grpTransport, "transport", findings.Fail,
			"downgraded to plaintext — "+requested.String()+" redirects to "+landed.String()+
				", so traffic that started encrypted does not stay that way", refCleartext)
	default:
		detail := "plaintext HTTP — traffic is unencrypted"
		if redirectTarget(resp) == nil {
			// Only assertable when there is no redirect at all; when there is,
			// the redirect row is what says where it goes.
			detail += ", and nothing redirects to HTTPS"
		}
		r.Add(grpTransport, "transport", findings.Fail, detail, refCleartext)
	}
}

func auditTLS(r *findings.Report, state *tls.ConnectionState, host string) {
	if state == nil {
		return
	}
	switch state.Version {
	case tls.VersionTLS13:
		r.Add(grpTransport, "tls-version", findings.OK, "TLS 1.3", refWeakCrypto)
	case tls.VersionTLS12:
		r.Add(grpTransport, "tls-version", findings.OK, "TLS 1.2", refWeakCrypto)
	default:
		r.Add(grpTransport, "tls-version", findings.Fail, tls.VersionName(state.Version)+" — deprecated, upgrade to TLS 1.2+", refWeakCrypto)
	}
	r.Add(grpTransport, "tls-cipher", cipherGrade(state.CipherSuite), tls.CipherSuiteName(state.CipherSuite), refWeakCrypto)

	if len(state.PeerCertificates) == 0 {
		r.Add(grpTransport, "certificate", findings.Fail, "no certificate presented", refCertValidation)
		return
	}
	leaf := state.PeerCertificates[0]
	if s := x509check.Chain(state.PeerCertificates, host); s != "" {
		r.Add(grpTransport, "cert-chain", findings.Fail, s, refCertValidation)
	} else {
		r.Add(grpTransport, "cert-chain", findings.OK, "valid for "+host, refCertValidation)
	}
	// The warning window is the shared default rather than a number local to
	// this file. It used to be 15 days here against `cert expiry`'s 30, so a
	// certificate 20 days out was "ok" from the audit and "WARN <30d" from
	// the cert check — same host, same minute, two answers.
	switch {
	case time.Now().After(leaf.NotAfter):
		r.Add(grpTransport, "cert-expiry", findings.Fail, "expired "+leaf.NotAfter.Format("2006-01-02"), refCertValidation)
	case x509check.Expiring(leaf.NotAfter, x509check.DefaultWarnDays):
		r.Add(grpTransport, "cert-expiry", findings.Warn, fmt.Sprintf("expires %s (<%dd)",
			leaf.NotAfter.Format("2006-01-02"), x509check.DefaultWarnDays), refCertValidation)
	default:
		r.Add(grpTransport, "cert-expiry", findings.OK, fmt.Sprintf("valid until %s (%dd)",
			leaf.NotAfter.Format("2006-01-02"), int(time.Until(leaf.NotAfter).Hours())/24), refCertValidation)
	}
	r.Add(grpTransport, "cert-signature", sigAlgGrade(leaf.SignatureAlgorithm), leaf.SignatureAlgorithm.String(), refWeakCrypto)
}

// cipherGrade favors AEAD suites (GCM, ChaCha20-Poly1305) — TLS 1.3 offers
// nothing else, so this only ever bites on a TLS 1.2 negotiation that picked
// a CBC-mode or other non-AEAD suite, which Go's client will still accept.
func cipherGrade(id uint16) string {
	// A suite Go itself lists as insecure — RC4, 3DES, CBC with SHA-256 — is
	// broken rather than dated, and the audit offers them precisely so that
	// a host choosing one is graded for it (see auditTLSConfig).
	for _, s := range tls.InsecureCipherSuites() {
		if s.ID == id {
			return findings.Fail
		}
	}
	name := tls.CipherSuiteName(id)
	if strings.Contains(name, "GCM") || strings.Contains(name, "CHACHA20_POLY1305") {
		return findings.OK
	}
	return findings.Warn
}

// sigAlgGrade flags a certificate signed with a broken or deprecated hash —
// SHA-1 collisions have been practical since 2017, MD5/MD2 far longer.
func sigAlgGrade(alg x509.SignatureAlgorithm) string {
	switch alg {
	case x509.MD2WithRSA, x509.MD5WithRSA, x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1:
		return findings.Fail
	default:
		return findings.OK
	}
}

// headerCheck grades one security header — or, for csp/x-frame-options,
// more than one header together, which is why grade reads the whole
// response rather than a single already-extracted value.
type headerCheck struct {
	label string
	ref   findings.Reference
	grade func(h stdhttp.Header) (status, detail string)
}

// presence is the common "any value is good, absent is a warning" grader.
func presence(name, missing string) func(stdhttp.Header) (string, string) {
	return func(h stdhttp.Header) (string, string) {
		if v := h.Get(name); v != "" {
			return findings.OK, v
		}
		return findings.Warn, missing
	}
}

// info is presence with a lower bar for absence: hardening a site does not
// yet universally expect these, so missing one is worth naming, not failing.
func info(name, missing string) func(stdhttp.Header) (string, string) {
	return func(h stdhttp.Header) (string, string) {
		if v := h.Get(name); v != "" {
			return findings.OK, v
		}
		return findings.Info, missing
	}
}

// securityHeaders apply to any response a host sends, redirects included.
// HSTS is the whole list: it is a promise about the origin rather than about
// a page, and the 301 an apex serves is exactly where a site forgets to make
// it — which is the one finding that would be lost by skipping a redirect's
// headers wholesale.
var securityHeaders = []headerCheck{
	{"hsts", refMisconfig, func(h stdhttp.Header) (string, string) { return gradeHSTS(h.Get("Strict-Transport-Security")) }},
}

// documentHeaders describe a rendered document, so they are graded only when
// the response is one. A 301 with no Content-Security-Policy is not a
// finding, and reporting it as one puts seven invented warnings around the
// single row that matters.
//
// Two slices rather than a flag on each row: the split is the rule, and a
// rule that lives in one place cannot drift from the table it describes.
var documentHeaders = []headerCheck{
	{"csp", refMisconfig, func(h stdhttp.Header) (string, string) { return gradeCSP(h.Get("Content-Security-Policy")) }},
	{"x-content-type-options", refMisconfig, func(h stdhttp.Header) (string, string) {
		v := h.Get("X-Content-Type-Options")
		if strings.EqualFold(strings.TrimSpace(v), "nosniff") {
			return findings.OK, "nosniff"
		}
		if v == "" {
			return findings.Warn, "missing — set to nosniff"
		}
		return findings.Warn, "unexpected value: " + v
	}},
	{"x-frame-options", refClickjacking, func(h stdhttp.Header) (string, string) {
		// Every line of each, not the first: a browser enforces every CSP a
		// response sends and reads every X-Frame-Options, and Get answers
		// only the first — a response whose second CSP said `frame-ancestors
		// *` beside a DENY read ok for a page any site can frame.
		return gradeFraming(strings.Join(h.Values("X-Frame-Options"), ", "),
			strings.Join(h.Values("Content-Security-Policy"), ", "))
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

func auditSecurityHeaders(r *findings.Report, h stdhttp.Header) { gradeHeaders(r, h, securityHeaders) }

func auditDocumentHeaders(r *findings.Report, h stdhttp.Header) { gradeHeaders(r, h, documentHeaders) }

func gradeHeaders(r *findings.Report, h stdhttp.Header, checks []headerCheck) {
	for _, hc := range checks {
		status, detail := hc.grade(h)
		r.Add(grpHeaders, hc.label, status, detail, hc.ref)
	}
}

// gradeHSTS goes beyond presence: OWASP's HSTS cheat sheet recommends a
// two-year max-age with includeSubDomains; the Chromium HSTS preload list
// (hstspreload.org) requires at least one year to even be considered. A
// header that is there but too short-lived to matter reads as "missing" to
// an attacker waiting it out.
func gradeHSTS(v string) (string, string) {
	if v == "" {
		return findings.Fail, "missing — no HSTS, downgrade attacks possible"
	}
	maxAge := hstsMaxAge(v)
	switch {
	case maxAge == hstsNoMaxAge:
		return findings.Warn, "present but no max-age — a browser ignores the header without one: " + v
	case maxAge == hstsUnreadable:
		return findings.Warn, "present but max-age unreadable — a browser ignores the header: " + v
	case maxAge <= 0:
		return findings.Warn, "present but max-age<=0 — disables HSTS, effectively missing: " + v
	case maxAge < hstsPreloadMinAge:
		return findings.Warn, fmt.Sprintf("max-age too short for preload eligibility (%ds < 1y): %s", maxAge, v)
	case !hstsDirective(v, "includesubdomains"):
		return findings.Warn, "no includeSubDomains — sibling subdomains stay exposed: " + v
	default:
		return findings.OK, v
	}
}

// What hstsMaxAge answers when it has no number: the directive is absent, or
// it is there and says nothing a browser can read. Negative, so no real
// max-age is either.
const (
	hstsNoMaxAge   = -1
	hstsUnreadable = -2
)

// hstsMaxAge extracts the max-age directive's value, or hstsNoMaxAge or
// hstsUnreadable.
//
// Read directive by directive, as RFC 6797 §6.1 writes them: a name, optional
// whitespace, `=`, optional whitespace, and a token or a quoted string. The
// literal `max-age=` it used to search for missed `max-age="63072000"` and
// `max-age = 63072000`, and a two-year policy written either way was called
// "disables HSTS, effectively missing".
func hstsMaxAge(header string) int {
	for _, d := range strings.Split(header, ";") {
		name, value, ok := strings.Cut(d, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "max-age") {
			continue
		}
		if !ok {
			return hstsUnreadable
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return hstsUnreadable
		}
		return n
	}
	return hstsNoMaxAge
}

// hstsDirective reports whether the header carries the valueless directive
// name — as a directive, not as text somewhere in another's value.
func hstsDirective(header, name string) bool {
	for _, d := range strings.Split(header, ";") {
		if strings.EqualFold(strings.TrimSpace(d), name) {
			return true
		}
	}
	return false
}

// gradeCSP checks for the specific weaknesses OWASP's CSP cheat sheet calls
// out — 'unsafe-inline'/'unsafe-eval', wildcard sources, and the absence of
// object-src/base-uri/frame-ancestors — rather than treating any non-empty
// policy as sufficient. A CSP that allows 'unsafe-inline' provides close to
// no XSS defense at all, and "CSP present" alone would have said it did.
func gradeCSP(v string) (string, string) {
	if v == "" {
		return findings.Warn, "missing — no CSP, weaker XSS defense"
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
		return findings.OK, v
	}
	return findings.Warn, "weak: " + strings.Join(weak, ", ")
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
//
// **A header counts when a browser enforces it and it keeps some site out.**
// Presence alone used to count: ALLOW-FROM — ignored by every current
// browser, as said above — and ALLOWALL, which is no directive at all, read
// ok, and so did `frame-ancestors *` and `frame-ancestors https:`, which let
// any site frame the page. A stated frame-ancestors also makes a browser
// ignore X-Frame-Options beside it, so a DENY next to a permissive one keeps
// nobody out.
func gradeFraming(xfo, csp string) (string, string) {
	stated, restricts, directive := cspFrameAncestors(csp)
	switch {
	case stated && restricts && xfo != "":
		return findings.OK, "CSP " + directive + " (+ X-Frame-Options " + xfo + ")"
	case stated && restricts:
		return findings.OK, "no X-Frame-Options, but CSP " + directive + " covers it"
	case stated:
		return findings.Fail, "CSP " + directive + " lets any site frame this page — and a browser " +
			"ignores X-Frame-Options beside it"
	case xfoRestricts(xfo):
		return findings.OK, xfo
	case strings.HasPrefix(strings.ToUpper(strings.TrimSpace(xfo)), "ALLOW-FROM"):
		return findings.Fail, "X-Frame-Options " + xfo + " is ignored by current browsers, so any site " +
			"can frame this page — CSP frame-ancestors is what names a partner"
	case xfo != "":
		return findings.Fail, "X-Frame-Options " + xfo + " is not a value browsers enforce, so any site " +
			"can frame this page — DENY or SAMEORIGIN, or CSP frame-ancestors"
	default:
		return findings.Fail, "no X-Frame-Options and no CSP frame-ancestors — clickjacking is not defended against"
	}
}

// xfoRestricts reports whether an X-Frame-Options value is one browsers
// enforce: DENY or SAMEORIGIN, and a list of them, which a response carrying
// the header twice arrives as.
func xfoRestricts(xfo string) bool {
	if strings.TrimSpace(xfo) == "" {
		return false
	}
	for _, v := range strings.Split(xfo, ",") {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "DENY", "SAMEORIGIN":
		default:
			return false
		}
	}
	return true
}

// cspFrameAncestors reads the frame-ancestors directives of a CSP: whether
// one is stated, whether one keeps some site out, and the directive to name.
//
// Every policy in the header — a response sending two arrives as one value
// joined by a comma, and a browser enforces both — so one that restricts is
// enough. Within a policy the first frame-ancestors is the one enforced.
func cspFrameAncestors(csp string) (stated, restricts bool, directive string) {
	for _, policy := range strings.Split(csp, ",") {
		for _, d := range strings.Split(policy, ";") {
			fields := strings.Fields(d)
			if len(fields) == 0 || !strings.EqualFold(fields[0], "frame-ancestors") {
				continue
			}
			shown := strings.Join(fields, " ")
			if !stated || (!restricts && !anyAncestor(fields[1:])) {
				directive = shown
			}
			stated = true
			restricts = restricts || !anyAncestor(fields[1:])
			break
		}
	}
	return stated, restricts, directive
}

// anyAncestor reports whether a frame-ancestors source list lets any site
// frame the page: `*`, a bare scheme such as `https:`, or a host of `*` —
// each matches every origin there is. An empty list matches nothing, which
// is 'none'.
func anyAncestor(sources []string) bool {
	for _, s := range sources {
		s = strings.ToLower(strings.Trim(s, `'"`))
		if s == "*" {
			return true
		}
		if strings.HasSuffix(s, ":") && !strings.ContainsAny(strings.TrimSuffix(s, ":"), ":/*.") {
			return true // a scheme-source
		}
		host := s
		if _, after, ok := strings.Cut(s, "://"); ok {
			host = after
		}
		if i := strings.IndexAny(host, ":/"); i >= 0 {
			host = host[:i]
		}
		if host == "*" {
			return true
		}
	}
	return false
}

// auditCORS grades what the host does with an Origin it cannot possibly
// recognize (corsProbeOrigin): reflecting it back — especially alongside
// credentials — is the classic misconfiguration that turns an
// authenticated API into one any other site can call on a victim's behalf.
// Silent when the response carries no CORS headers at all: most sites
// legitimately don't, and that is not itself a finding.
func auditCORS(r *findings.Report, h stdhttp.Header) {
	allowOrigin := h.Get("Access-Control-Allow-Origin")
	if allowOrigin == "" {
		return
	}
	creds := strings.EqualFold(strings.TrimSpace(h.Get("Access-Control-Allow-Credentials")), "true")
	switch {
	case allowOrigin == corsProbeOrigin && creds:
		r.Add(grpCORS, "cors", findings.Fail, "reflects an arbitrary Origin with credentials allowed — cross-origin account takeover risk", refCORS)
	case allowOrigin == corsProbeOrigin:
		r.Add(grpCORS, "cors", findings.Warn, "reflects an arbitrary Origin ("+corsProbeOrigin+") — confirm this is intentional", refCORS)
	case allowOrigin == "*" && creds:
		r.Add(grpCORS, "cors", findings.Warn, "wildcard origin with credentials allowed — browsers reject this combination, but it signals a misconfiguration", refCORS)
	case allowOrigin == "*":
		r.Add(grpCORS, "cors", findings.Info, "open to all origins (*) — fine for public, unauthenticated resources", refCORS)
	default:
		r.Add(grpCORS, "cors", findings.OK, "restricted to: "+allowOrigin, refCORS)
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

func auditExposure(r *findings.Report, h stdhttp.Header) {
	for _, e := range exposureHeaders {
		v := h.Get(e.name)
		if v == "" {
			continue
		}
		status := findings.Info
		// A version number in the banner is the real risk: it hands an
		// attacker a CVE shortlist.
		versioned := strings.ContainsAny(v, "0123456789")
		if e.name == "Via" {
			versioned = viaNamesAVersion(v)
		}
		if versioned {
			status = findings.Warn
		}
		r.Add(grpExposure, e.label, status, "discloses: "+v, refInfoExposure)
	}
	// X-XSS-Protection is legacy: modern guidance (including OWASP's own) is
	// that CSP supersedes it and the header itself has been the source of
	// browser-specific XSS bugs in the past — worth naming when present so a
	// hardening pass knows it is inherited config, not something to add.
	if v := h.Get("X-XSS-Protection"); v != "" {
		r.Add(grpExposure, "x-xss-protection", findings.Info, "present but deprecated — superseded by CSP: "+v, refMisconfig)
	}
}

// viaNamesAVersion reports whether a Via value names a product's version.
//
// Every hop starts with the HTTP version it arrived over and then names the
// proxy — `1.1 vegur`, `1.1 d1a2b3c4.cloudfront.net (CloudFront)` — so the
// digit test the other banners get warned on every proxied site for
// disclosing "1.1", or a hostname, neither of which names any software. What
// does is a comment, `(Varnish/6.0)`, or a proxy that names itself as a
// product, `Squid/3.5.20`, where a hop's name should be.
func viaNamesAVersion(v string) bool {
	for _, hop := range strings.Split(v, ",") {
		_, rest, _ := strings.Cut(strings.TrimSpace(hop), " ")
		by, comment, _ := strings.Cut(strings.TrimSpace(rest), " ")
		if strings.ContainsAny(comment, "0123456789") {
			return true
		}
		if _, product, ok := strings.Cut(by, "/"); ok && strings.ContainsAny(product, "0123456789") {
			return true
		}
	}
	return false
}

// cookieAttrs are graded one per attribute rather than one per cookie: each
// missing attribute is its own weakness with its own CWE, and "which
// cookies are missing HttpOnly" is the question a fix is organized around.
var cookieAttrs = []struct {
	check   string
	ref     findings.Reference
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

func auditCookies(r *findings.Report, resp *stdhttp.Response) {
	cookies := resp.Cookies()
	// **resp.Cookies() is what Go could parse, not what the server sent.**
	// Go's scanner drops a Set-Cookie line it cannot read without a word —
	// an invalid token in the name, a missing "=" — while a browser's more
	// forgiving parser still accepts and stores it. Grading only the
	// parseable ones and closing with "all N cookies set" then states that
	// every cookie this site sets is hardened, about a set missing the one
	// nobody could look at, which is the likeliest one to be the odd one out.
	if sent := len(resp.Header.Values("Set-Cookie")); sent > len(cookies) {
		r.Add(grpCookies, "cookie-unread", findings.Warn,
			fmt.Sprintf("%d of %d Set-Cookie headers could not be parsed, so the rows here cover the rest — "+
				"a browser is more forgiving than this reader and may well be holding them",
				sent-len(cookies), sent), findings.Reference{})
	}
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
			r.Add(grpCookies, attr.check, findings.OK, "all "+findings.Plural(len(cookies), "cookie")+" set", attr.ref)
			continue
		}
		sort.Strings(weak)
		r.Add(grpCookies, attr.check, findings.Warn,
			strings.Join(weak, ", ")+" — "+attr.why, attr.ref)
	}
}
