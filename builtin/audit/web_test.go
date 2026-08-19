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
	"testing"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/x509check"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
			clip(v)
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

	got := map[string]bool{}
	for _, s := range detailSections(t, srv).Items {
		got[s.Title] = true
	}
	for _, want := range []string{"summary", grpTransport, grpHeaders, grpCookies, grpExposure, "references"} {
		if !got[want] {
			t.Errorf("detail page is missing the %q section: have %v", want, got)
		}
	}
	// Nothing set a CORS header, so that section has no subject at all — an
	// empty heading would read as a check that failed to run.
	if got[grpCORS] {
		t.Error("cross-origin section rendered with no CORS findings")
	}
}

// The references section is what makes a finding actionable rather than
// merely named: one row per distinct control, with somewhere to read it up.
func TestReferenceTableIsDeduplicatedAndLinkable(t *testing.T) {
	srv := tlsServer(t, nil)
	var refs view.Table
	for _, s := range detailSections(t, srv).Items {
		if s.Title == "references" {
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
	if got := refClickjacking.url(); got != "https://cwe.mitre.org/data/definitions/1021.html" {
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

func TestClipCollapsesAndTruncates(t *testing.T) {
	long := strings.Repeat("policy ", 40)
	got := clip(long)
	if utf8.RuneCountInString(got) > 96 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip did not truncate: runes=%d", utf8.RuneCountInString(got))
	}
	if clip("a\n  b\tc") != "a b c" {
		t.Errorf("clip did not collapse whitespace: %q", clip("a\n  b\tc"))
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
	if r[1] != stWarn {
		t.Errorf("a certificate 20 days from expiry graded %q (%s), want %s", r[1], r[2], stWarn)
	}
	if !strings.Contains(r[2], fmt.Sprintf("<%dd", x509check.DefaultWarnDays)) {
		t.Errorf("detail %q does not name the shared %d-day window", r[2], x509check.DefaultWarnDays)
	}
}

// A certificate outside the window still has to grade clean, or the check
// above would pass just as well on a threshold that warns about everything.
func TestCertExpiryStaysQuietOutsideTheWindow(t *testing.T) {
	srv := expiringTLSServer(t, time.Now().Add(90*24*time.Hour))
	if r := auditRows(t, srv)["cert-expiry"]; r[1] != stOK {
		t.Errorf("a certificate 90 days from expiry graded %q (%s), want %s", r[1], r[2], stOK)
	}
}

// expiringTLSServer starts a TLS server presenting a self-signed certificate
// that expires at notAfter. httptest's own certificate is good until 2084,
// which is exactly the case an expiry check never has to think about.
func expiringTLSServer(t *testing.T, notAfter time.Time) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
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

func TestAuditIsReadIdempotent(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Read || !c.Idempotent {
			t.Errorf("%s must be read + idempotent — the audit toolbox only ever inspects", c.ID)
		}
	}
}

func TestWebIsRegistered(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID == "audit.web" {
			return
		}
	}
	t.Fatal("audit.web not registered")
}
