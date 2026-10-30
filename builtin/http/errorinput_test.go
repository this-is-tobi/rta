package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	stdnet "net"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func capabilityOf(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("http declares no %s", id)
	return plugin.Capability{}
}

// checkInput fails the test when err is not code naming input, or names an
// input capability id does not declare — which every surface ignores, so a
// misspelt name would pass for no name at all.
func checkInput(t *testing.T, id string, err error, code, input string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: accepted, want %s", id, code)
		return
	}
	ve := view.AsError(err, "x")
	if ve.Code != code {
		t.Errorf("%s: %s (%s), want %s", id, ve.Code, ve.Message, code)
		return
	}
	if ve.Input != input {
		t.Errorf("%s: %s names input %q, want %q: %s", id, code, ve.Input, input, ve.Message)
	}
	if _, ok := capabilityOf(t, id).ErrorInput(ve); ve.Input != "" && !ok {
		t.Errorf("%s: %s names %q, which it does not declare", id, code, ve.Input)
	}
}

// A refusal of one input's value names that input, so a form opens again on
// the box that holds the fix and an agent knows which argument to send again.
// Two inputs that answer one question, and a connection that failed, name
// none: no one box holds the fix for either.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}))
	t.Cleanup(srv.Close)
	absent := filepath.Join(t.TempDir(), "absent")
	empty := credentialFile(t, "\n")
	tooLarge := credentialFile(t, strings.Repeat("x", maxCredential+1))
	payload := bodyFile(t, `{"a":1}`)

	for _, c := range []struct {
		why    string
		method string
		values map[string]any
		code   string
		input  string
	}{
		{"a URL no parser takes", "GET",
			map[string]any{"url": "http://exa mple.invalid/"}, "http.request.invalid", "url"},
		{"a scheme the client does not speak", "GET",
			map[string]any{"url": "ftp://127.0.0.1:1/x"}, "http.request.invalid", "url"},
		// Refused before the address guard looks the name up: a lookup that
		// failed would have answered http.request.failed.
		{"a scheme the client does not speak, at a name", "GET",
			map[string]any{"url": "ftp://name-that-is-never-looked-up.invalid/x"}, "http.request.invalid", "url"},
		{"https to a server that answers in plain http", "GET",
			map[string]any{"url": "https://" + strings.TrimPrefix(srv.URL, "http://")}, "http.request.failed", "url"},
		{"a connection the port refused", "GET",
			map[string]any{"url": "http://127.0.0.1:1/"}, "http.request.failed", ""},
		{"a header with no colon", "GET",
			map[string]any{"url": srv.URL, "header": []string{"no-colon"}}, "http.header.invalid", "header"},
		{"a token file that is not there", "GET",
			map[string]any{"url": srv.URL, "bearer-file": absent}, "http.auth.file", "bearer-file"},
		{"a token file past the cap", "HEAD",
			map[string]any{"url": srv.URL, "bearer-file": tooLarge}, "http.auth.file", "bearer-file"},
		{"an empty user:password file", "DELETE",
			map[string]any{"url": srv.URL, "basic-file": empty}, "http.auth.file", "basic-file"},
		{"curl's dash for a pipe", "GET",
			map[string]any{"url": srv.URL, "basic-file": "-"}, "http.auth.file", "basic-file"},
		{"a token as a value and as a file", "GET",
			map[string]any{"url": srv.URL, "bearer": "a", "bearer-file": empty}, "http.auth.twice", ""},
		{"a body file that is not there", "POST",
			map[string]any{"url": srv.URL, "data-file": absent}, "http.data.file", "data-file"},
		{"curl's dash for a pipe", "PUT",
			map[string]any{"url": srv.URL, "data-file": "-"}, "http.data.file", "data-file"},
		{"a body as a value and as a file", "POST",
			map[string]any{"url": srv.URL, "data": "a", "data-file": payload}, "http.data.twice", ""},
		{"curl's way of naming a file", "PUT",
			map[string]any{"url": srv.URL, "data": "@" + payload}, "http.data.atfile", "data"},
	} {
		t.Run(c.method+" "+c.why, func(t *testing.T) {
			_, err := doRequest(context.Background(), c.method, req(c.values))
			checkInput(t, "http."+strings.ToLower(c.method), err, c.code, c.input)
		})
	}

	for code, want := range map[string]string{
		"teapot":  "http.status.badcode",
		"9xx":     "http.status.badcode",
		"599-500": "http.status.badcode",
		"600-699": "http.status.unknown",
		"299":     "http.status.unknown",
	} {
		_, err := runStatus(context.Background(), plugin.NewRequest(map[string]any{"code": code}, false, false))
		checkInput(t, "http.status", err, want, "code")
	}
}

// Of the ways a request can fail, the URL is the input to change for a scheme
// the client does not speak, a server that answered in plain http, and a host
// the resolver says does not exist. A resolver that did not answer, a name
// that is not the URL's (the proxy the environment names), a certificate, a
// refusal and a deadline are failures the URL as typed did not cause.
// What net/http itself will not send is refused before anything leaves, dry
// run or not, naming the input: it failed inside the transport, after a lookup
// of the host, as a request to a host that does not answer.
func TestWhatNetHTTPWouldRefuseIsRefusedBeforeTheRequestLeaves(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		values      map[string]any
	}{
		{"a URL with no host", "url", map[string]any{"url": "http:///orders/7"}},
		{"a header name with a space", "header", map[string]any{"url": "https://rta-dry-run.invalid/x", "header": []string{"Bad Key: v"}}},
		{"a header value on two lines", "header", map[string]any{"url": "https://rta-dry-run.invalid/x", "header": []string{"X-Note: a\nb"}}},
	} {
		tc.values["timeout"] = "5s"
		for _, dry := range []bool{true, false} {
			_, err := doRequest(context.Background(), "GET", plugin.NewRequest(tc.values, dry, true))
			var verr *view.Error
			if !errors.As(err, &verr) || verr.Input != tc.input ||
				(verr.Code != "http.request.invalid" && verr.Code != "http.header.invalid") {
				t.Errorf("%s (dry run %v) = %v, want a refusal naming %s", tc.name, dry, err, tc.input)
			}
		}
	}
	// An empty value is one net/http sends, and how a default header is taken away.
	if _, err := doRequest(context.Background(), "GET", plugin.NewRequest(map[string]any{
		"url": "https://rta-dry-run.invalid/x", "header": []string{"User-Agent:"}, "timeout": "5s"}, true, true)); err != nil {
		t.Errorf("an empty header value was refused: %v", err)
	}
}

func TestOnlyAFailureOfTheURLAsTypedNamesIt(t *testing.T) {
	const target = "https://nope.example:8443/x"
	failed := func(err error) error { return &url.Error{Op: "Get", URL: target, Err: err} }
	notFound := func(name string) error {
		return &stdnet.OpError{Op: "dial", Net: "tcp", Err: &stdnet.DNSError{Err: "no such host", Name: name, IsNotFound: true}}
	}
	for why, c := range map[string]struct {
		err   error
		input string
	}{
		"a plain http answer":                {failed(stdhttp.ErrSchemeMismatch), "url"},
		"a host that does not exist":         {failed(notFound("nope.example")), "url"},
		"the same host, written in capitals": {failed(notFound("NOPE.example")), "url"},
		"a proxy that does not exist":        {failed(&stdnet.OpError{Op: "proxyconnect", Net: "tcp", Err: notFound("proxy.invalid")}), ""},
		"a resolver that did not answer":     {failed(&stdnet.DNSError{Err: "i/o timeout", Name: "nope.example", IsTimeout: true}), ""},
		"a certificate that did not verify":  {failed(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), ""},
		"a connection the port refused":      {failed(errors.New("dial tcp 192.0.2.1:8443: connect: connection refused")), ""},
		"a deadline":                         {failed(context.DeadlineExceeded), ""},
	} {
		if got := requestFailed(plugin.SurfaceCLI, "GET", target, c.err).Input; got != c.input {
			t.Errorf("%s: names input %q, want %q", why, got, c.input)
		}
	}

	// The address guard never names the URL: what the host is, not how the URL
	// was typed, is refused. It names local-network where that switch is what
	// lets the call through, and nothing over MCP or for an address no switch
	// opens.
	for _, c := range []struct {
		ip    string
		sf    plugin.Surface
		input string
	}{
		{"10.0.0.1", plugin.SurfaceCLI, "local-network"},
		{"10.0.0.1", plugin.SurfaceMCP, ""},
		{"169.254.169.254", plugin.SurfaceCLI, ""},
	} {
		blocked := &blockedAddrError{host: "nope.example", ip: stdnet.ParseIP(c.ip)}
		if got := blockedRefusal(c.sf, "GET", target, blocked).Input; got != c.input {
			t.Errorf("the address guard on %s, %v, names input %q, want %q", c.ip, c.sf, got, c.input)
		}
	}
}

// A credential or a body file that is there and that this process may not read
// names no input: the path is right, and the fix is a chmod, not the box.
func TestAFileThatCannotBeReadNamesNoInput(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode bits do not stop this reader")
	}
	shut := credentialFile(t, "tok\n")
	if err := os.Chmod(shut, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, verr := credential(plugin.NewRequest(map[string]any{"bearer-file": shut}, false, false), "bearer"); verr == nil ||
		verr.Code != "http.auth.file" || verr.Input != "" {
		t.Errorf("the bearer file: %+v, want http.auth.file naming none", verr)
	}
	if _, verr := requestBody(plugin.NewRequest(map[string]any{"data-file": shut}, false, false)); verr == nil ||
		verr.Code != "http.data.file" || verr.Input != "" {
		t.Errorf("the body file: %+v, want http.data.file naming none", verr)
	}
}

// A dry run of a URL this client cannot send refuses it as the run would,
// rather than previewing a request that could never leave.
func TestADryRunOfASchemeTheClientDoesNotSpeakIsRefused(t *testing.T) {
	c := capabilityOf(t, "http.get")
	_, err := c.Run(context.Background(), plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{
		Caller: map[string]any{"url": "ftp://example.invalid/x"}}), true, false).WithSurface(plugin.SurfaceCLI))
	if verr := view.AsError(err, "x"); verr == nil || verr.Code != "http.request.invalid" || verr.Input != "url" {
		t.Errorf("a dry run of an ftp URL = %+v, want http.request.invalid naming url", verr)
	}
}
