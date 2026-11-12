package plugin

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// redirecting is a server that answers /from with status to location, and
// /to with what it was sent, counting the requests that reached /to.
func redirecting(t *testing.T, status int, location func(self string) string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var reached atomic.Int32
	var self string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/from":
			http.Redirect(w, r, location(self), status)
		default:
			reached.Add(1)
			body, _ := io.ReadAll(r.Body)
			_, _ = io.WriteString(w, r.Method+" "+string(body)+" "+r.Header.Get("X-Token"))
		}
	}))
	self = srv.URL
	t.Cleanup(srv.Close)
	return srv, &reached
}

// call sends method with body and a credential header to url through a client
// that follows p, and answers what came back or the refusal.
func call(t *testing.T, p RedirectPolicy, method, url, body string) (string, *RedirectRefusal) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Token", "s3cret")
	// Bounded, so a policy that stopped refusing a loop fails here rather
	// than following it for as long as the test may run: a client with a
	// CheckRedirect of its own has no cap but the one that function keeps.
	resp, err := (&http.Client{CheckRedirect: p.CheckRedirect, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		var refusal *RedirectRefusal
		if !errors.As(err, &refusal) {
			t.Fatalf("not a refusal: %v", err)
		}
		return "", refusal
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	return string(got), nil
}

// The same origin is followed, a GET as a base path or trailing slash behind a
// proxy sends one, and a write a 307 sends on with its method and body.
func TestARedirectWithinTheOriginIsFollowed(t *testing.T) {
	srv, _ := redirecting(t, http.StatusMovedPermanently, func(self string) string { return self + "/to" })
	if got, refused := call(t, RedirectSameOrigin, http.MethodGet, srv.URL+"/from", ""); refused != nil || got != "GET  s3cret" {
		t.Fatalf("got %q, refused %v", got, refused)
	}
	srv, _ = redirecting(t, http.StatusTemporaryRedirect, func(self string) string { return self + "/to" })
	if got, refused := call(t, RedirectSameOrigin, http.MethodPut, srv.URL+"/from", "the write"); refused != nil ||
		got != "PUT the write s3cret" {
		t.Fatalf("got %q, refused %v", got, refused)
	}
}

// A write answered 301, 302 or 303 would go out again as a GET without its
// body and report success; it is refused before it is sent.
func TestAWriteARedirectWouldTurnIntoAReadIsRefused(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther} {
		srv, reached := redirecting(t, status, func(self string) string { return self + "/to" })
		_, refused := call(t, RedirectSameOrigin, http.MethodPut, srv.URL+"/from", "the write")
		if refused == nil || refused.Reason != RedirectRewrite || refused.Status != status || refused.Method != http.MethodPut {
			t.Fatalf("%d: refused %+v", status, refused)
		}
		if reached.Load() != 0 {
			t.Errorf("%d: the rewritten request went out", status)
		}
	}
}

// Another address is refused, so the credential never reaches it; the path
// and query of where it pointed are not kept, since a sign-in redirect's can
// carry a token.
func TestARedirectToAnotherAddressIsRefusedAndKeepsNoPath(t *testing.T) {
	other, reached := redirecting(t, http.StatusFound, nil)
	srv, _ := redirecting(t, http.StatusFound, func(string) string { return other.URL + "/to?code=abc" })
	_, refused := call(t, RedirectSameOrigin, http.MethodGet, srv.URL+"/from", "")
	if refused == nil || refused.Reason != RedirectElsewhere {
		t.Fatalf("refused %+v", refused)
	}
	if reached.Load() != 0 {
		t.Error("the other address was reached, with the credential")
	}
	if refused.To.String() != other.URL || strings.Contains(refused.Error(), "code=abc") {
		t.Errorf("the refusal kept more than the origin: %v", refused)
	}
}

// http to https on the same host is an upgrade, refused since the call already
// crossed in the clear; https to http is refused always; a loop after five; and
// a client that follows none, any redirect.
func TestEveryOtherRedirectIsRefusedForItsReason(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/to", http.StatusFound)
	}))
	t.Cleanup(tlsSrv.Close)
	upgrade, _ := redirecting(t, http.StatusMovedPermanently, func(string) string { return tlsSrv.URL + "/to" })
	loop, _ := redirecting(t, http.StatusFound, func(self string) string { return self + "/from" })
	plain, _ := redirecting(t, http.StatusFound, func(self string) string { return self + "/to" })

	if _, refused := call(t, RedirectSameOrigin, http.MethodGet, upgrade.URL+"/from", ""); refused == nil || refused.Reason != RedirectUpgrade {
		t.Errorf("upgrade: %+v", refused)
	}
	client := tlsSrv.Client()
	client.CheckRedirect = RedirectSameOrigin.CheckRedirect
	if _, err := client.Get(tlsSrv.URL + "/from"); !isReason(err, RedirectDowngrade) {
		t.Errorf("downgrade: %v", err)
	}
	if _, refused := call(t, RedirectSameOrigin, http.MethodGet, loop.URL+"/from", ""); refused == nil || refused.Reason != RedirectLoop {
		t.Errorf("loop: %+v", refused)
	}
	if _, refused := call(t, RedirectNever, http.MethodGet, plain.URL+"/from", ""); refused == nil || refused.Reason != RedirectPolicyNever {
		t.Errorf("never: %+v", refused)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func isReason(err error, reason RedirectReason) bool {
	var refusal *RedirectRefusal
	return errors.As(err, &refusal) && refusal.Reason == reason
}

// A refusal names the setting the way its reader changes it, gives the
// product's own sentence for another address, and says the forward is why
// when the call went through one.
func TestARefusalSaysWhatToChangeWhereTheReaderChangesIt(t *testing.T) {
	refusal := &RedirectRefusal{Status: 307, Method: "GET", Reason: RedirectElsewhere,
		From: mustURL(t, "https://vault-0.internal:8200"), To: mustURL(t, "https://vault-1.internal:8200")}
	cli := NewRequest(nil, false, false).WithSurface(SurfaceCLI)
	verr := refusal.Refusal(cli, "vault", "url", "")
	if verr.Code != "vault.redirect.refused" ||
		verr.Message != "https://vault-0.internal:8200 answered with a redirect to https://vault-1.internal:8200, another address, which rta does not follow" ||
		!strings.HasPrefix(verr.Hint, "if the service answers at https://vault-1.internal:8200, set --url https://vault-1.internal:8200;") {
		t.Errorf("direct: %+v", verr)
	}
	if verr := refusal.Refusal(cli, "vault", "url", "a standby names the active node this way"); !strings.HasPrefix(verr.Hint, "a standby names the active node this way — ") {
		t.Errorf("with the product's hint: %q", verr.Hint)
	}
	forward := cli.WithProfile("prod", TunnelKube)
	if verr := refusal.Refusal(forward, "vault", "url", "a standby names the active node this way"); !strings.Contains(verr.Hint, "outside the forward the profile opened") ||
		!strings.Contains(verr.Message, "profile prod") {
		t.Errorf("through a forward: %+v", verr)
	}
	mcp := NewRequest(nil, false, false).WithSurface(SurfaceMCP)
	upgrade := &RedirectRefusal{Status: 301, Method: "GET", Reason: RedirectUpgrade,
		From: mustURL(t, "http://sso.internal"), To: mustURL(t, "https://sso.internal")}
	if verr := upgrade.Refusal(mcp, "keycloak", "url", ""); !strings.HasPrefix(verr.Hint, "the operator's `url` set to https://sso.internal reaches it directly") {
		t.Errorf("to an agent: %q", verr.Hint)
	}
}
