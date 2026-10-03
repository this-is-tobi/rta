package audit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/builtin/internal/x509check"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

// auditRows runs audit.web against srv and returns Check -> row.
func auditRows(t *testing.T, srv *httptest.Server) map[string][]string {
	t.Helper()
	v, err := runWeb(t.Context(), req(map[string]any{"host": srv.URL, "timeout": 5}))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}
	out := map[string][]string{}
	for _, r := range tbl.Rows {
		out[r[0]] = r
	}
	return out
}

// tlsServer starts a TLS test server whose handler only sets headers.
func tlsServer(t *testing.T, headers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuditGradesHardenedHost(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "x", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rows := auditRows(t, srv)
	for _, check := range []string{
		"hsts", "csp", "x-content-type-options", "x-frame-options", "referrer-policy",
		"cookie-secure", "cookie-httponly", "cookie-samesite", "tls-version",
	} {
		if r, ok := rows[check]; !ok {
			t.Errorf("missing check %q", check)
		} else if r[1] != "ok" {
			t.Errorf("%s = %q (%s), want ok", check, r[1], r[2])
		}
	}
	// httptest serves a self-signed cert: the audit uses system roots, so the
	// chain correctly fails — exactly what a real audit should report.
	if r := rows["cert-chain"]; r[1] != "fail" || !strings.Contains(r[2], "INVALID") {
		t.Errorf("self-signed cert chain should fail: %v", r)
	}
}

func TestAuditFlagsMissingHeadersAndExposure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.18.0")
		w.Header().Set("X-Powered-By", "PHP/8.1.0")
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "x"}) // no flags
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rows := auditRows(t, srv)
	if rows["hsts"][1] != "fail" {
		t.Errorf("missing HSTS should fail, got %q", rows["hsts"][1])
	}
	if rows["csp"][1] != "warn" {
		t.Errorf("missing CSP should warn, got %q", rows["csp"][1])
	}
	// A version in the Server banner is the real finding: warn, not info.
	if r := rows["server-header"]; r[1] != "warn" || !strings.Contains(r[2], "nginx/1.18.0") {
		t.Errorf("server banner grading wrong: %v", r)
	}
	if r := rows["x-powered-by"]; r[1] != "warn" || !strings.Contains(r[2], "PHP/8.1.0") {
		t.Errorf("x-powered-by grading wrong: %v", r)
	}
	if rows["overall"][1] != "fail" {
		t.Errorf("overall should fail (missing HSTS): %v", rows["overall"])
	}
}

// One cookie missing three attributes is three different weaknesses with
// three different CWEs, and the fix for each is a different line of config —
// so each is its own finding, naming the cookies it applies to.
func TestAuditGradesEachCookieAttributeSeparately(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "x"})
		http.SetCookie(w, &http.Cookie{Name: "safe", Value: "y", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rows := auditRows(t, srv)
	for _, check := range []string{"cookie-secure", "cookie-httponly", "cookie-samesite"} {
		r, ok := rows[check]
		if !ok {
			t.Fatalf("missing %q", check)
		}
		if r[1] != "warn" {
			t.Errorf("%s = %q, want warn: %v", check, r[1], r)
		}
		if !strings.Contains(r[2], "sid") {
			t.Errorf("%s should name the offending cookie: %q", check, r[2])
		}
		if strings.Contains(r[2], "safe") {
			t.Errorf("%s named a compliant cookie: %q", check, r[2])
		}
	}
	// Each attribute cites its own weakness, not one lumped-together string.
	if rows["cookie-secure"][3] == rows["cookie-httponly"][3] {
		t.Error("Secure and HttpOnly must not share a reference")
	}
}

func TestAuditFlagsPlaintextHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rows := auditRows(t, srv)
	if r := rows["transport"]; r[1] != "fail" || !strings.Contains(r[2], "plaintext") {
		t.Errorf("plaintext transport should fail: %v", r)
	}
	// No TLS rows on a plaintext host.
	if _, ok := rows["tls-version"]; ok {
		t.Error("plaintext host must not report a TLS version")
	}
}

// A CSP that is present but hollow — "trust me" without the directives that
// actually block anything — must not read as a pass just because the header
// exists. This is the gap the old presence-only check had.
func TestAuditCSPWeaknessesAreNamedNotJustPresence(t *testing.T) {
	srv := tlsServer(t, map[string]string{
		"Content-Security-Policy": "default-src *; script-src 'self' 'unsafe-inline' 'unsafe-eval'",
	})
	r := auditRows(t, srv)["csp"]
	if r[1] != "warn" {
		t.Fatalf("csp = %q, want warn for a policy with unsafe-inline/unsafe-eval/wildcard: %v", r[1], r)
	}
	for _, want := range []string{"unsafe-inline", "unsafe-eval", "wildcard"} {
		if !strings.Contains(r[2], want) {
			t.Errorf("csp detail = %q, missing mention of %q", r[2], want)
		}
	}
}

// Regression: real policies end with ";", which splits into a final segment
// with no directive name at all. Slicing past a name that is not there
// panicked the whole command — a crash, not a bad grade, on one of the most
// ordinary inputs there is.
func TestAuditCSPWithTrailingSemicolonDoesNotPanic(t *testing.T) {
	srv := tlsServer(t, map[string]string{
		"Content-Security-Policy": "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none';",
	})
	if r := auditRows(t, srv)["csp"]; r[1] != "ok" {
		t.Fatalf("csp = %v, want ok for a complete policy that happens to end in ';'", r)
	}
}

// The graders read whatever a host chose to send, which is not a
// well-formed-input guarantee. None of these are valid policies; all of them
// are things a server can put on the wire, and a hardening tool that dies on
// a malformed header is a hardening tool nobody can point at production.
func TestGradersSurviveMalformedHeaderValues(t *testing.T) {
	junk := []string{
		"", ";", ";;;", " ; ; ", "*", ";*", "* ;", "default-src", "default-src;",
		"   ", "\t", "=", "max-age=", "max-age=abc", "max-age=-1", "max-age=99999999999999999999",
		"default-src 'self';;script-src *;", strings.Repeat(";", 500), strings.Repeat("* ", 500),
		"frame-ancestors", "object-src base-uri frame-ancestors",
	}
	for _, v := range junk {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("panic on %q: %v", v, p)
				}
			}()
			gradeCSP(v)
			cspHasWildcardSource(strings.ToLower(v))
			gradeHSTS(v)
			hstsMaxAge(strings.ToLower(v))
			gradeFraming(v, v)
			findings.Clip(v)
			normalizeURL(v)
		}()
	}
}

// A strict CSP (nonce/hash based, no unsafe-*, real directives) grades OK
// even without X-Frame-Options at all: frame-ancestors is the header's
// modern, stronger replacement per OWASP's own clickjacking guidance.
func TestAuditFrameAncestorsCoversMissingXFrameOptions(t *testing.T) {
	srv := tlsServer(t, map[string]string{
		"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'",
	})
	if r := auditRows(t, srv)["x-frame-options"]; r[1] != "ok" {
		t.Fatalf("x-frame-options = %q, want ok (frame-ancestors covers it): %v", r[1], r)
	}
}

// Every Via hop starts with the HTTP version it arrived over, so a digit test
// on the whole value warned on every proxied site for disclosing "1.1" —
// which names no software. The version worth a warning is a product's.
func TestViaWarnsOnAProductVersionNotTheProtocol(t *testing.T) {
	for via, want := range map[string]string{
		"1.1 vegur":                                findings.Info,
		"2 heroku-router, 1.1 google":              findings.Info,
		"1.1 d1a2b3c4.cloudfront.net (CloudFront)": findings.Info,
		"HTTP/1.1 10.0.0.5:3128":                   findings.Info,
		"1.1 varnish (Varnish/6.0)":                findings.Warn,
		"1.0 fred, 1.1 p.example.net (Apache/1.1)": findings.Warn,
		"1.1 Squid/3.5.20":                         findings.Warn,
	} {
		r := &findings.Report{}
		auditExposure(r, http.Header{"Via": {via}})
		if len(r.Findings) != 1 || r.Findings[0].Status != want {
			t.Errorf("Via %q graded %+v, want %s", via, r.Findings, want)
		}
	}
	// The other banners keep the plain test: a digit in Server is a version.
	r := &findings.Report{}
	auditExposure(r, http.Header{"Server": {"nginx/1.25.3"}})
	if r.Findings[0].Status != findings.Warn {
		t.Errorf("Server with a version graded %s", r.Findings[0].Status)
	}
}

// RFC 6797 lets max-age be a quoted string and puts optional whitespace
// around the =, and a two-year policy written either way was called
// "disables HSTS, effectively missing". A value nobody can read is not one
// that disables anything: it says so.
func TestHSTSReadsEveryFormTheRFCAllows(t *testing.T) {
	for _, tc := range []struct {
		header, status, says string
	}{
		{`max-age="63072000"; includeSubDomains`, findings.OK, ""},
		{`max-age = 63072000 ; includeSubDomains`, findings.OK, ""},
		{`includeSubDomains; MAX-AGE=63072000; preload`, findings.OK, ""},
		{`max-age=0`, findings.Warn, "disables HSTS"},
		{`includeSubDomains`, findings.Warn, "no max-age"},
		{`max-age=two-years; includeSubDomains`, findings.Warn, "unreadable"},
		{`max-age=63072000; foo="includeSubDomains"`, findings.Warn, "no includeSubDomains"},
	} {
		status, detail := gradeHSTS(tc.header)
		if status != tc.status || !strings.Contains(detail, tc.says) {
			t.Errorf("%s: %s %q, want %s saying %q", tc.header, status, detail, tc.status, tc.says)
		}
	}
}

// The row answers whether any site can frame the page, so a header counts
// only when a browser enforces it and what it enforces keeps some site out.
// ALLOW-FROM is ignored by every current browser, ALLOWALL is no directive,
// and `frame-ancestors *` or a bare scheme lets anyone in — and a stated
// frame-ancestors makes a browser ignore X-Frame-Options beside it, so a
// DENY there cannot rescue it. Each of those read ok.
func TestFramingCountsOnlyAHeaderThatKeepsSomeSiteOut(t *testing.T) {
	for _, tc := range []struct {
		xfo, csp string
		want     string
	}{
		{"ALLOWALL", "", findings.Fail},
		{"ALLOW-FROM https://partner.example", "", findings.Fail},
		{"", "default-src 'self'; frame-ancestors *", findings.Fail},
		{"", "frame-ancestors https:", findings.Fail},
		{"", "frame-ancestors 'self' https://*", findings.Fail},
		{"DENY", "frame-ancestors *", findings.Fail},
		{"DENY", "", findings.OK},
		{" sameorigin ", "", findings.OK},
		{"", "frame-ancestors 'none'", findings.OK},
		{"", "frame-ancestors 'self' https://a.example", findings.OK},
		{"", "frame-ancestors https://*.example.com", findings.OK},
		{"ALLOWALL", "frame-ancestors 'self'", findings.OK},
		// Two policies are both enforced, so the stricter one holds.
		{"", "frame-ancestors *, frame-ancestors 'self'", findings.OK},
		// An empty source list matches nothing, which is 'none'.
		{"", "frame-ancestors; default-src 'self'", findings.OK},
	} {
		if got, detail := gradeFraming(tc.xfo, tc.csp); got != tc.want {
			t.Errorf("XFO %q, CSP %q: %s (%s), want %s", tc.xfo, tc.csp, got, detail, tc.want)
		}
	}
}

// A response may send a header twice, and a browser enforces every CSP it
// gets. Only the first was read: a second policy saying `frame-ancestors *`,
// beside a DENY a browser then ignores, read ok for a page any site frames.
func TestFramingReadsEveryLineOfBothHeaders(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Add("Content-Security-Policy", "default-src 'self'")
		w.Header().Add("Content-Security-Policy", "frame-ancestors *")
		w.Header().Set("X-Frame-Options", "DENY")
	}))
	t.Cleanup(srv.Close)
	if r := auditRows(t, srv)["x-frame-options"]; r[1] != findings.Fail {
		t.Errorf("x-frame-options = %v, want fail: the second policy lets any site frame the page", r)
	}
}

// The csp row read the first policy alone, as the framing row once did: a
// weakness stated in the second went unnamed, and directives the second
// states were named as missing. Every policy the response sends is read.
func TestCSPReadsEveryPolicyTheResponseSends(t *testing.T) {
	for _, tc := range []struct {
		name     string
		policies []string
		want     string
		named    []string
		unnamed  []string
	}{
		{
			name: "a weakness in the second policy",
			policies: []string{
				"object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
				"script-src 'self' 'unsafe-inline'; img-src *",
			},
			want:    findings.Warn,
			named:   []string{"'unsafe-inline'", "wildcard"},
			unnamed: []string{"no object-src", "no base-uri", "no frame-ancestors"},
		},
		{
			name: "the directives the first lacks, in the second",
			policies: []string{
				"default-src 'self'",
				"object-src 'none'; base-uri 'none'; frame-ancestors 'none'",
			},
			want: findings.OK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				for _, p := range tc.policies {
					w.Header().Add("Content-Security-Policy", p)
				}
			}))
			t.Cleanup(srv.Close)
			r := auditRows(t, srv)["csp"]
			if r[1] != tc.want {
				t.Fatalf("csp = %v, want %s", r, tc.want)
			}
			for _, s := range tc.named {
				if !strings.Contains(r[2], s) {
					t.Errorf("csp detail %q does not name %s", r[2], s)
				}
			}
			for _, s := range tc.unnamed {
				if strings.Contains(r[2], s) {
					t.Errorf("csp detail %q names %s, which the other policy states", r[2], s)
				}
			}
		})
	}
}

// A banner sent twice was read by its first line: the version the second
// discloses went unnamed and the row read info. Every line is read.
func TestExposureReadsEveryLineOfABanner(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("X-Powered-By", "Express")
		w.Header().Add("X-Powered-By", "PHP/8.1.0")
		w.Header().Add("Via", "1.1 vegur")
		w.Header().Add("Via", "1.1 cache (squid/3.5.20)")
		w.Header().Add("X-XSS-Protection", "")
		w.Header().Add("X-XSS-Protection", "1; mode=block")
	}))
	t.Cleanup(srv.Close)
	rows := auditRows(t, srv)
	if r := rows["x-powered-by"]; r[1] != findings.Warn || !strings.Contains(r[2], "Express") ||
		!strings.Contains(r[2], "PHP/8.1.0") {
		t.Errorf("x-powered-by = %v, want a warning naming both lines", r)
	}
	if r := rows["via"]; r[1] != findings.Warn || !strings.Contains(r[2], "squid/3.5.20") {
		t.Errorf("via = %v, want a warning naming the second hop's version", r)
	}
	if r, ok := rows["x-xss-protection"]; !ok || !strings.Contains(r[2], "mode=block") {
		t.Errorf("x-xss-protection = %v, want the line that has a value named", r)
	}
}

// Neither header at all is the actual clickjacking-vulnerable case, and must
// fail outright rather than warn: nothing stops this page being framed.
func TestAuditNoFramingDefenseAtAllFails(t *testing.T) {
	srv := tlsServer(t, nil)
	if r := auditRows(t, srv)["x-frame-options"]; r[1] != "fail" {
		t.Fatalf("x-frame-options = %q, want fail with no XFO and no frame-ancestors: %v", r[1], r)
	}
}

// The dangerous CORS shape: reflecting back an origin the server has never
// seen before, with credentials allowed — a from-a-single-request signal
// that the API can be called cross-origin on behalf of a logged-in victim.
func TestAuditCORSReflectsArbitraryOriginWithCredentials(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin")) // blind reflection
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := auditRows(t, srv)["cors"]
	if r[1] != "fail" {
		t.Fatalf("cors = %q, want fail for reflected-origin+credentials: %v", r[1], r)
	}
	if !strings.Contains(r[2], "arbitrary") {
		t.Errorf("cors detail should say the origin was arbitrary: %q", r[2])
	}
}

// A deliberately open, public API (wildcard, no credentials) is a real and
// common legitimate shape — must not be graded as a failure.
func TestAuditCORSWildcardWithoutCredentialsIsFine(t *testing.T) {
	srv := tlsServer(t, map[string]string{"Access-Control-Allow-Origin": "*"})
	if r := auditRows(t, srv)["cors"]; r[1] == "fail" || r[1] == "warn" {
		t.Fatalf("cors = %q, want ok/info for a public wildcard with no credentials: %v", r[1], r)
	}
}

// No CORS headers at all is the common case and not itself a finding.
func TestAuditNoCORSHeadersIsSilent(t *testing.T) {
	if _, ok := auditRows(t, tlsServer(t, nil))["cors"]; ok {
		t.Error("a response with no CORS headers should not produce a cors row")
	}
}

// HSTS depth: present but short-lived must warn, not pass — a header that
// expires before an attacker gives up is not meaningfully different from no
// header at all.
func TestAuditHSTSShortMaxAgeWarns(t *testing.T) {
	srv := tlsServer(t, map[string]string{"Strict-Transport-Security": "max-age=300"})
	if r := auditRows(t, srv)["hsts"]; r[1] != "warn" {
		t.Fatalf("hsts = %q, want warn for a 300s max-age: %v", r[1], r)
	}
}

// Long enough for preload eligibility but missing includeSubDomains still
// leaves sibling subdomains exposed — a real, separate weakness OWASP's own
// cheat sheet calls out, not folded silently into a pass.
func TestAuditHSTSMissingIncludeSubDomainsWarns(t *testing.T) {
	srv := tlsServer(t, map[string]string{"Strict-Transport-Security": "max-age=63072000"})
	r := auditRows(t, srv)["hsts"]
	if r[1] != "warn" || !strings.Contains(r[2], "includeSubDomains") {
		t.Fatalf("hsts = %v, want a warn naming includeSubDomains", r)
	}
}

// TLS 1.3 only offers AEAD suites, so a real server exercises the pass path
// for free; the point of this test is that the check exists and reports
// something rather than silently doing nothing.
func TestAuditCipherIsGraded(t *testing.T) {
	r := auditRows(t, tlsServer(t, nil))["tls-cipher"]
	if r == nil {
		t.Fatal("missing tls-cipher row")
	}
	if r[1] != "ok" {
		t.Errorf("tls-cipher = %q for a modern httptest server, want ok: %v", r[1], r)
	}
}

// Every finding row (everything but the headline "overall" summary) must
// carry a reference so a hardening report can be traced back to a named
// control, not just a locally-invented label.
func TestAuditEveryFindingCarriesAReference(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.18.0")
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "x"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for check, r := range auditRows(t, srv) {
		if check == "overall" {
			continue
		}
		if r[3] == "" {
			t.Errorf("check %q has no reference: %v", check, r)
		}
	}
}

// Every finding must also land in a declared group, or the detail page would
// silently drop it: the compact table would report a weakness the full
// report does not mention.
func TestEveryFindingLandsInADeclaredGroup(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.18.0")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "x"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	v, err := runWeb(t.Context(), req(map[string]any{"host": srv.URL, "timeout": 5}))
	if err != nil {
		t.Fatal(err)
	}
	compact := len(v.(view.Table).Rows) - 1 // minus the "overall" headline

	sections := detailSections(t, srv)
	grouped := 0
	for _, s := range sections.Items {
		if tbl, ok := s.View.(view.Table); ok && s.Title != "references" {
			grouped += len(tbl.Rows)
		}
	}
	if grouped != compact {
		t.Fatalf("detail page shows %d findings, compact table shows %d", grouped, compact)
	}
}

func detailSections(t *testing.T, srv *httptest.Server) view.Sections {
	t.Helper()
	v, err := runWeb(t.Context(), req(map[string]any{"host": srv.URL, "timeout": 5, "detail": true}))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detail view = %s, want Sections", view.TypeOf(v))
	}
	return s
}

func TestAuditDetailIsASectionedPage(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.18.0")
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "x"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Addressed by id rather than by heading: the heading is presentation
	// and free to be reworded, which is what view.Section's two fields are for.
	got := map[string]bool{}
	for _, s := range detailSections(t, srv).Items {
		got[s.Key()] = true
	}
	for _, want := range []string{"summary", grpTransport.ID, grpHeaders.ID, grpCookies.ID, grpExposure.ID, "references"} {
		if !got[want] {
			t.Errorf("detail page is missing the %q section: have %v", want, got)
		}
	}
	// Nothing set a CORS header, so that section has no subject at all — an
	// empty heading would read as a check that failed to run.
	if got[grpCORS.ID] {
		t.Error("cross-origin section rendered with no CORS findings")
	}
}

// The references section is what makes a finding actionable rather than
// merely named: one row per distinct control, with somewhere to read it up.
func TestReferenceTableIsDeduplicatedAndLinkable(t *testing.T) {
	srv := tlsServer(t, nil)
	var refs view.Table
	for _, s := range detailSections(t, srv).Items {
		if s.Key() == "references" {
			refs = s.View.(view.Table)
		}
	}
	if len(refs.Rows) == 0 {
		t.Fatal("no references cited")
	}
	seen := map[string]bool{}
	for _, r := range refs.Rows {
		if seen[r[0]] {
			t.Errorf("duplicate reference row: %v", r)
		}
		seen[r[0]] = true
		if r[1] == "" {
			t.Errorf("reference %q has no weakness title", r[0])
		}
		if !strings.HasPrefix(r[2], "https://cwe.mitre.org/") {
			t.Errorf("reference %q has no lookup URL: %q", r[0], r[2])
		}
	}
}

func TestReferenceURLPointsAtTheCitedCWE(t *testing.T) {
	if got := refClickjacking.URL(); got != "https://cwe.mitre.org/data/definitions/1021.html" {
		t.Errorf("url = %q", got)
	}
	if got := refCleartext.String(); got != "A04:2025 Cryptographic Failures · CWE-319" {
		t.Errorf("citation = %q", got)
	}
}

func TestAuditBadHostIsCoded(t *testing.T) {
	_, err := runWeb(t.Context(), req(map[string]any{"host": "://nope", "timeout": 2}))
	ve := view.AsError(err, "x")
	if ve.Code != "audit.web.badhost" && ve.Code != "audit.web.unreachable" {
		t.Errorf("want coded audit error, got %+v", ve)
	}
}

// A credential in front of the host makes the argument read as one host
// while the request goes to another: the grant, the consent prompt and the
// ledger quote staging.example.com, the request went to the address after
// the @, and it carried the prefix as a Basic credential. Refused before a
// byte is sent, bare or with a scheme.
func TestAHostWithCredentialsBeforeItIsRefused(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "https://")
	for _, host := range []string{
		"https://staging.example.com@" + addr + "/",
		"staging.example.com@" + addr,
		"https://user:pass@" + addr,
		// An empty credential is a credential, and the scheme is read however
		// it is spelled — or not spelled with its slashes at all.
		"https://@" + addr,
		"HTTPS://staging.example.com@" + addr,
		"https:staging.example.com@" + addr,
	} {
		_, err := runWeb(t.Context(), req(map[string]any{"host": host, "timeout": 5}))
		if ve := view.AsError(err, "x"); ve == nil || ve.Code != "audit.web.badhost" {
			t.Errorf("%s: want audit.web.badhost, got %+v", host, ve)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the refused hosts still sent %d requests", n)
	}
}

// A value with no host in it was requested as it stood: `//host` became a
// path under an empty host and came back "unreachable" with a hint to check
// the host was up, and a bare `:port` was dialled on this machine, a host
// the argument never names.
func TestAValueThatNamesNoHostIsRefused(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(srv.Close)
	_, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "https://"), ":")
	for _, host := range []string{"//127.0.0.1:" + port, ":" + port, "https://:" + port + "/", "https:///x"} {
		_, err := runWeb(t.Context(), req(map[string]any{"host": host, "timeout": 5}))
		if ve := view.AsError(err, "x"); ve == nil || ve.Code != "audit.web.badhost" {
			t.Errorf("%s: want audit.web.badhost, got %+v", host, ve)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("values naming no host still sent %d requests", n)
	}
}

func TestClipCollapsesAndTruncates(t *testing.T) {
	long := strings.Repeat("policy ", 40)
	got := findings.Clip(long)
	if utf8.RuneCountInString(got) > 96 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip did not truncate: runes=%d", utf8.RuneCountInString(got))
	}
	if findings.Clip("a\n  b\tc") != "a b c" {
		t.Errorf("clip did not collapse whitespace: %q", findings.Clip("a\n  b\tc"))
	}
}

// The cert-expiry window was a private 15 days here while `cert expiry`
// defaulted to 30, so a certificate 20 days from renewal was graded "ok" by
// `rta audit web` and "WARN <30d" by `rta cert expiry` — same host, same
// minute, two answers, and the quieter one wins the argument by default.
func TestCertExpiryWarnsOnTheSharedWindow(t *testing.T) {
	srv := expiringTLSServer(t, time.Now().Add(20*24*time.Hour))
	rows := auditRows(t, srv)
	r, ok := rows["cert-expiry"]
	if !ok {
		t.Fatal("no cert-expiry finding")
	}
	if r[1] != findings.Warn {
		t.Errorf("a certificate 20 days from expiry graded %q (%s), want %s", r[1], r[2], findings.Warn)
	}
	if !strings.Contains(r[2], fmt.Sprintf("<%dd", x509check.DefaultWarnDays)) {
		t.Errorf("detail %q does not name the shared %d-day window", r[2], x509check.DefaultWarnDays)
	}
}

// A certificate outside the window still has to grade clean, or the check
// above would pass just as well on a threshold that warns about everything.
func TestCertExpiryStaysQuietOutsideTheWindow(t *testing.T) {
	srv := expiringTLSServer(t, time.Now().Add(90*24*time.Hour))
	if r := auditRows(t, srv)["cert-expiry"]; r[1] != findings.OK {
		t.Errorf("a certificate 90 days from expiry graded %q (%s), want %s", r[1], r[2], findings.OK)
	}
}

// A certificate that is not valid yet fails every client as surely as one that
// has expired, and was graded ok for as long as its end date was far off.
func TestACertificateNotValidYetFailsTheExpiryCheck(t *testing.T) {
	srv := validBetweenTLSServer(t, time.Now().Add(48*time.Hour), time.Now().Add(120*24*time.Hour))
	r := auditRows(t, srv)["cert-expiry"]
	if r == nil || r[1] != findings.Fail || !strings.Contains(r[2], "not valid until") {
		t.Errorf("cert-expiry = %v, want a fail saying when the certificate becomes valid", r)
	}
}

// expiringTLSServer starts a TLS server presenting a self-signed certificate
// that expires at notAfter. httptest's own certificate is good until 2084,
// which is exactly the case an expiry check never has to think about.
func expiringTLSServer(t *testing.T, notAfter time.Time) *httptest.Server {
	t.Helper()
	return validBetweenTLSServer(t, time.Now().Add(-time.Hour), notAfter)
}

// validBetweenTLSServer is expiringTLSServer with the start of the
// certificate's validity under the test's control as well.
func validBetweenTLSServer(t *testing.T, notBefore, notAfter time.Time) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// legacyServer starts a TLS test server speaking only what cfg allows.
func legacyServer(t *testing.T, cfg *tls.Config) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// A host that speaks only a deprecated protocol is the finding the
// tls-version row exists for. The client's own floor of TLS 1.2 refused the
// handshake instead, and the audit answered that the host was unreachable,
// with a hint to check that it was — about a host that answered.
func TestADeprecatedProtocolIsGradedNotUnreachable(t *testing.T) {
	rows := auditRows(t, legacyServer(t, &tls.Config{
		MinVersion: tls.VersionTLS10, //nolint:gosec // the legacy host under audit
		MaxVersion: tls.VersionTLS11,
	}))
	if row := rows["tls-version"]; row == nil || row[1] != findings.Fail || !strings.Contains(row[2], "deprecated") {
		t.Errorf("tls-version = %v, want a fail naming the protocol deprecated", row)
	}
}

// Nor is a host that offers only a suite Go stopped proposing: 3DES and RSA
// key exchange left the client's defaults, and a host speaking nothing else
// read as unreachable rather than as a host using a broken cipher.
func TestABrokenCipherIsGradedNotUnreachable(t *testing.T) {
	rows := auditRows(t, legacyServer(t, &tls.Config{
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA},
	}))
	if row := rows["tls-cipher"]; row == nil || row[1] != findings.Fail {
		t.Errorf("tls-cipher = %v, want a fail for 3DES", row)
	}
}

func TestAuditIsReadIdempotent(t *testing.T) {
	for _, c := range Plugin(testCatalog, nil).Capabilities {
		if c.Safety != plugin.Read || !c.Idempotent {
			t.Errorf("%s must be read + idempotent — the audit toolbox only ever inspects", c.ID)
		}
	}
}

func TestWebIsRegistered(t *testing.T) {
	for _, c := range Plugin(testCatalog, nil).Capabilities {
		if c.ID == "audit.web" {
			return
		}
	}
	t.Fatal("audit.web not registered")
}

// **resp.Cookies() is what Go could parse, not what the server sent.**
//
// A Set-Cookie line Go's scanner cannot read is dropped without a word,
// while a browser's more forgiving parser still accepts and stores it. So
// grading only the parseable ones and calling the result "all N cookies
// set" made an exact-sounding claim — every cookie this site sets is
// hardened — about a set that was missing the one nobody could look at,
// which is the cookie most likely to be the odd one out.
func TestCookiesGoCouldNotParseAreNotGradedAsHardened(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "session=abc; Secure; HttpOnly; SameSite=Lax")
	resp.Header.Add("Set-Cookie", "inv@lid=x; Path=/")
	if got := len(resp.Cookies()); got != 1 {
		t.Fatalf("Go parsed %d of the 2 Set-Cookie lines, want 1 — the arrangement no longer reproduces", got)
	}

	r := &findings.Report{}
	auditCookies(r, resp)
	var unread *findings.Finding
	for i, f := range r.Findings {
		if f.Check == "cookie-unread" {
			unread = &r.Findings[i]
		}
		// The hardened rows must not claim to cover a cookie nobody read.
		if f.Status == findings.OK && strings.Contains(f.Detail, "all 2") {
			t.Errorf("%s claims to cover both cookies: %q", f.Check, f.Detail)
		}
	}
	if unread == nil {
		t.Fatalf("a Set-Cookie line nobody could parse left no trace: %+v", r.Findings)
	}
	if unread.Status != findings.Warn {
		t.Errorf("an unparsed Set-Cookie graded %q, want %q", unread.Status, findings.Warn)
	}
}

// A response whose cookies all parse says nothing extra, so the caveat
// stays worth reading.
func TestParseableCookiesRaiseNoCaveat(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "session=abc; Secure; HttpOnly; SameSite=Lax")
	r := &findings.Report{}
	auditCookies(r, resp)
	for _, f := range r.Findings {
		if f.Check == "cookie-unread" {
			t.Errorf("a well-formed response raised %q: %s", f.Check, f.Detail)
		}
	}
}

// The host audit.web requests is the record the gate judged. It was trimmed
// first, so a call on " staging.example.com" - its own record to the gate,
// which a grant on staging.example.com does not cover, and the one a person
// approving it read - audited staging.example.com. It is refused before
// anything is requested, as a kube audit refuses a padded namespace.
func TestAHostWithWhiteSpaceAroundItIsRefusedBeforeAnythingIsRequested(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	nbsp := string(rune(0xa0))
	for _, host := range []string{" " + srv.URL, srv.URL + " ", srv.URL + "\n", srv.URL + nbsp, "\t" + srv.URL, " ", "\t"} {
		_, err := runWeb(t.Context(), req(map[string]any{"host": host, "timeout": 5}))
		if ve := view.AsError(err, "x"); err == nil || ve.Code != "audit.web.badhost" {
			t.Errorf("host %q: err = %v, want audit.web.badhost", host, err)
		} else if !strings.Contains(ve.Message, "white space") {
			t.Errorf("host %q is refused without saying why: %s", host, ve.Message)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached the host", n)
	}
}

// A host holding a character that draws as nothing is refused as well, and
// for the same reason, since the request drops such a character before the
// name resolves: net/http maps a host that is not ASCII through IDNA, which
// removes a zero-width space, a soft hyphen, a word joiner or a byte order
// mark. A call on this server's address with one of them in front of it, its
// own record to the gate, reached the server.
func TestAHostHoldingACharacterThatDrawsAsNothingIsRefused(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "https://")
	for _, cp := range []rune{0x200b, 0xad, 0x2060, 0xfeff, 0x180e} {
		unseen := string(cp)
		for _, host := range []string{"https://" + unseen + addr, unseen + addr,
			"https://" + addr[:3] + unseen + addr[3:]} {
			_, err := runWeb(t.Context(), req(map[string]any{"host": host, "timeout": 5}))
			if ve := view.AsError(err, "x"); err == nil || ve.Code != "audit.web.badhost" {
				t.Errorf("host %q: err = %v, want audit.web.badhost", host, err)
			} else if !strings.Contains(ve.Message, "draws as nothing") {
				t.Errorf("host %q is refused without saying why: %s", host, ve.Message)
			}
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached the host", n)
	}
}

// Nor does a host the request would map to another name get through spelled
// some other way. IDNA drops what draws as nothing and folds a fullwidth
// digit into its digit and an ideographic full stop into a dot, and url.Parse
// decodes a percent-encoded host before any of it: a call on this server's
// address spelled either way, its own record to the gate, reached the server.
func TestAHostTheRequestWouldMapToAnotherNameIsRefused(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "https://")
	rest := strings.TrimPrefix(addr, "127")
	wide := string([]rune{0xff11, 0xff12, 0xff17})
	stop := string(rune(0x3002))
	for _, host := range []string{
		wide + rest,
		"https://" + wide + rest,
		"https://127" + stop + strings.TrimPrefix(rest, "."),
		"https://%E2%80%8B" + addr,
		"https://127%E3%80%82" + strings.TrimPrefix(rest, "."),
		"https://%EF%BC%91%EF%BC%92%EF%BC%97" + rest,
	} {
		_, err := runWeb(t.Context(), req(map[string]any{"host": host, "timeout": 5}))
		if ve := view.AsError(err, "x"); err == nil || ve.Code != "audit.web.badhost" {
			t.Errorf("host %q: err = %v, want audit.web.badhost", host, err)
		} else if !strings.Contains(ve.Message, "maps to others") || !strings.Contains(ve.Hint, "xn--") {
			t.Errorf("host %q is refused without saying why: %s (%s)", host, ve.Message, ve.Hint)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached the host", n)
	}
}
