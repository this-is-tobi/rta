package audit

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// rows runs audit.web against a target and returns check -> row.
func rows(t *testing.T, target string) map[string][]string {
	t.Helper()
	v, err := runWeb(t.Context(), req(map[string]any{"host": target, "timeout": 5}))
	if err != nil {
		t.Fatalf("audit web %s: %v", target, err)
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

// detailOf is the Detail cell of one check, or "" when the check is absent.
func detailOf(rs map[string][]string, check string) string {
	if r, ok := rs[check]; ok {
		return r[2]
	}
	return ""
}

// hostPort rewrites a test server URL to use an equivalent hostname that is
// nevertheless a *different* hostname — 127.0.0.1 and localhost resolve to the
// same listener, so this produces a genuinely cross-host redirect that a test
// can still reach. Without it "another host" needs DNS.
func otherName(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:" + u.Port()
	return u.String()
}

// **A grant names one host, so the request stops at that host.**
//
// audit.web is NeedsGrant with Scope "host", and the MCP bridge authorizes
// the call once before the handler starts. A client that followed a 3xx
// wherever it pointed spent that authorization on a host the operator never
// named and no second check ever saw — the exact hole builtin/http refuses
// redirects outright to avoid, described in its own client comment as an
// authorized public endpoint that 302s to a cloud metadata service.
//
// It is not a theoretical reordering of who is at fault. The destination is
// chosen by the audited host, which is the party the audit exists to be
// suspicious of.
func TestARedirectToAnotherHostIsNotFollowed(t *testing.T) {
	var reached bool
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherName(t, elsewhere.URL), http.StatusFound)
	}))
	defer first.Close()

	rs := rows(t, first.URL)
	if reached {
		t.Error("the audit followed a redirect to a host the grant does not name")
	}

	// …and it says so, rather than reporting the redirect as if it were the
	// site. A report that quietly grades a 301 is worse than one that stops,
	// because it looks like an answer.
	redirect := detailOf(rs, "redirect")
	if redirect == "" {
		t.Fatalf("no redirect row — the report never says which response it graded:\n%v", rs)
	}
	if !strings.Contains(redirect, "not followed") {
		t.Errorf("the redirect row does not say it stopped: %q", redirect)
	}
}

// The other half of stopping: say what to run next. A refusal that leaves
// somebody to work out the follow-up by hand is a refusal they route around
// by pasting the URL into curl, which is neither audited nor graded.
func TestAStoppedRedirectHandsOverTheNextCommand(t *testing.T) {
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	target := otherName(t, elsewhere.URL)

	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}))
	defer first.Close()

	// The compact table clips at 96 characters, so the assertion is on the
	// unclipped finding: the destination has to survive the clip, the command
	// is allowed to live on the detail page.
	u, err := url.Parse(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	r := &report{}
	auditRedirect(r, u, fetch(t, u))
	if len(r.findings) != 1 {
		t.Fatalf("want one redirect finding, got %d", len(r.findings))
	}
	got := r.findings[0].detail
	if !strings.Contains(got, "rta audit web ") {
		t.Errorf("no follow-up command in the redirect finding: %q", got)
	}
	if !strings.Contains(got, target) {
		t.Errorf("the redirect finding does not name where it points: %q", got)
	}
	// Clipped, the destination must still be there — it is the one fact the
	// row exists to carry.
	if !strings.Contains(clip(got), target) {
		t.Errorf("the destination is lost to the compact clip: %q", clip(got))
	}
}

// **An http -> https upgrade is the behaviour being asked for, not a failure.**
//
// The transport grade used to be read off the URL that was typed while every
// other check read the response, so a host doing exactly the right thing was
// graded "plaintext HTTP — traffic is unencrypted" on the line directly above
// "tls-version ok TLS 1.3". A report that contradicts itself in adjacent rows
// trains the reader to skip it.
func TestAnUpgradeToHTTPSIsNotGradedAsPlaintext(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer secure.Close()

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL, http.StatusMovedPermanently)
	}))
	defer plain.Close()

	rs := rows(t, plain.URL)
	transport := rs["transport"]
	if transport == nil {
		t.Fatal("no transport row")
	}
	if transport[1] != stOK {
		t.Errorf("an http→https upgrade graded %q: %q", transport[1], transport[2])
	}
	if !strings.Contains(transport[2], "redirects here") {
		t.Errorf("the upgrade is not named as one: %q", transport[2])
	}
	// The same run must not still be claiming the traffic is unencrypted.
	if strings.Contains(transport[2], "unencrypted") {
		t.Errorf("still reported as unencrypted after upgrading: %q", transport[2])
	}
}

// The mirror image, which the old code could not report at all: an https URL
// that hands the client to plaintext. It used to reach the same row as a site
// that was never encrypted, and the two are different findings — one has no
// certificate, the other has one and sends you past it.
func TestADowngradeToPlaintextIsReported(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()

	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer secure.Close()

	rs := rows(t, secure.URL)
	transport := rs["transport"]
	if transport == nil {
		t.Fatal("no transport row")
	}
	if transport[1] != stFail {
		t.Errorf("a downgrade to plaintext graded %q: %q", transport[1], transport[2])
	}
	if !strings.Contains(transport[2], "downgraded") {
		t.Errorf("a downgrade is not named as one: %q", transport[2])
	}
}

// Plaintext that goes nowhere is still the plain failure, and may say so
// without hedging — the assertion is only safe because there is no redirect.
func TestPlaintextWithNoRedirectSaysNothingUpgradesIt(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()

	transport := rows(t, plain.URL)["transport"]
	if transport == nil || transport[1] != stFail {
		t.Fatalf("plaintext not graded as a failure: %v", transport)
	}
	if !strings.Contains(transport[2], "nothing redirects to HTTPS") {
		t.Errorf("want the absence stated: %q", transport[2])
	}
}

// **A redirect is not a document, so it is not graded as one.**
//
// Stopping at the apex's 301 is only useful if the row that matters is
// readable. Grading a 301 against a document's checklist adds seven invented
// warnings — no CSP, no framing policy, no referrer policy — around the one
// line saying where it points, and an operator who sees eight findings reads
// none of them.
func TestARedirectIsNotGradedAgainstADocumentChecklist(t *testing.T) {
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()

	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		http.Redirect(w, r, otherName(t, elsewhere.URL), http.StatusMovedPermanently)
	}))
	defer first.Close()

	rs := rows(t, first.URL)
	for _, invented := range []string{"csp", "x-frame-options", "referrer-policy", "coop", "coep", "corp"} {
		if _, ok := rs[invented]; ok {
			t.Errorf("%s graded on a 301 — a redirect renders no document", invented)
		}
	}
	// HSTS is the exception and it is the point of the exception: the promise
	// is about the origin, and the redirect an apex serves is exactly where a
	// site forgets to make it.
	if _, ok := rs["hsts"]; !ok {
		t.Error("hsts not graded on a redirect — that is the one header a redirect must still carry")
	}
}

// A same-host chain is followed, so the common canonicalisations still work
// without a second command — and the report says it moved.
func TestASameHostRedirectIsFollowedAndNamed(t *testing.T) {
	var landed bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			landed = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	rs := rows(t, srv.URL)
	if !landed {
		t.Error("a same-host redirect was not followed — the audit stopped inside the host it was given")
	}
	if d := detailOf(rs, "redirect"); !strings.Contains(d, "same host, followed") {
		t.Errorf("the hop is not reported: %q", d)
	}
}

// A loop on one host is inside the grant and would otherwise be followed
// forever — bounded, and the bound is stated rather than silently applied.
func TestASameHostLoopStopsAtTheHopBound(t *testing.T) {
	var hops int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		// A same-host path each time. Note "/"+r.URL.Path would be "//x" for
		// the root, which is a protocol-relative URL and a different host —
		// see TestAProtocolRelativeLocationIsAnotherHost.
		http.Redirect(w, r, "/"+strconv.Itoa(hops), http.StatusFound)
	}))
	defer srv.Close()

	rs := rows(t, srv.URL)
	if hops > maxRedirects+1 {
		t.Errorf("followed %d hops, bound is %d", hops, maxRedirects)
	}
	if d := detailOf(rs, "redirect"); !strings.Contains(d, "still redirecting") {
		t.Errorf("the bound is not reported: %q", d)
	}
}

// fetch makes the request the audit makes, under the audit's own redirect
// policy, so a test can assert on one check in isolation rather than through
// the whole table.
func fetch(t *testing.T, u *url.URL) *http.Response {
	t.Helper()
	client := &http.Client{
		CheckRedirect: followSameHost(u),
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec
	}
	resp, err := client.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// A protocol-relative Location — `//evil.example` — is a different host
// spelled so that it does not look like one. It reads as a path to anybody
// skimming, and net/url resolves it against the scheme rather than the path,
// so a check written against the literal string would follow it.
//
// The test exists because writing the loop test above produced exactly this
// by accident: "/" + "/x" is "//x", which resolved to the host "x". If a
// typo can reach another host, so can a hostile Location.
func TestAProtocolRelativeLocationIsAnotherHost(t *testing.T) {
	var reached bool
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	u, err := url.Parse(elsewhere.URL)
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "//localhost:"+u.Port()+"/", http.StatusFound)
	}))
	defer first.Close()

	rs := rows(t, first.URL)
	if reached {
		t.Error("a protocol-relative Location reached another host")
	}
	if d := detailOf(rs, "redirect"); !strings.Contains(d, "not followed") {
		t.Errorf("the hop is not reported as stopped: %q", d)
	}
}
