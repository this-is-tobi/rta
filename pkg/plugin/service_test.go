package plugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

var (
	vaultService = Service{Namespace: "vault", Product: "Vault", Reach: EndpointURL, Default: "http://127.0.0.1:8200",
		Port: 8200, Switch: TLSByScheme}
	redisService = Service{Namespace: "redis", Product: "Redis", Reach: EndpointAddress, Default: "127.0.0.1:6379",
		Port: 6379, Switch: TLSOnOff, ClientCert: true}
	pgService = Service{Namespace: "pg", Product: "PostgreSQL", Reach: EndpointHost, Default: "localhost",
		Port: 5432, Switch: TLSModes, ClientCert: true}
)

// asked is a request with values, on the CLI.
func asked(values map[string]any) Request {
	return NewRequest(values, false, false).WithSurface(SurfaceCLI)
}

// forwarded is asked through a profile's kube: forward.
func forwarded(values map[string]any) Request {
	return asked(values).WithProfile("lab", TunnelKube)
}

// writePEM writes der as a PEM block of typ to a file of its own, and returns
// its path.
func writePEM(t *testing.T, typ string, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// keyPEM writes key as a PEM file, and returns its path.
func keyPEM(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return writePEM(t, "EC PRIVATE KEY", der)
}

// useProxy stands in for the environment's proxy, raw for every request.
func useProxy(t *testing.T, raw string) {
	t.Helper()
	proxy, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	previous := proxyFromEnvironment
	proxyFromEnvironment = func(*http.Request) (*url.URL, error) { return proxy, nil }
	t.Cleanup(func() { proxyFromEnvironment = previous })
}

func codeOf(verr *view.Error) string {
	if verr == nil {
		return ""
	}
	return verr.Code
}

func names(fields []Field) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Name
	}
	return out
}

// Each kind of Service declares the settings the plugins converged on, every
// one Local and keyed by its own name, and a plugin declaring them beside its
// credential is one the SDK takes: the tls switch is the one a forward turns
// off, in the word the host fills.
func TestAServiceDeclaresTheSettingsEveryPluginSpellsAlike(t *testing.T) {
	for _, tc := range []struct {
		svc  Service
		want []string
		off  any
	}{
		{vaultService, []string{"url", "ca-file", "tls-server-name"}, nil},
		{redisService, []string{"address", "tls", "ca-file", "tls-server-name", "cert-file", "key-file"}, false},
		{pgService, []string{"host", "port", "tls", "ca-file", "tls-server-name", "cert-file", "key-file"}, TLSDisable},
	} {
		fields := tc.svc.Fields()
		if got := names(fields); !slices.Equal(got, tc.want) {
			t.Errorf("%s declares %v, want %v", tc.svc.Namespace, got, tc.want)
		}
		for _, f := range fields {
			if !f.Local || f.Config != f.Name || f.Group == "" || f.Help == "" {
				t.Errorf("%s's %s: Local %v, Config %q, Group %q, Help %q", tc.svc.Namespace, f.Name, f.Local, f.Config, f.Group, f.Help)
			}
			if f.Name == "tls" && (f.Endpoint != EndpointTLS || TLSOffValue(f) != tc.off) {
				t.Errorf("%s's tls: role %q, off %v", tc.svc.Namespace, f.Endpoint, TLSOffValue(f))
			}
		}
		p := Plugin{Name: tc.svc.Namespace, Summary: "s", Version: "1.0.0", Capabilities: []Capability{{
			ID: tc.svc.Namespace + ".status", Summary: "status", Safety: Read,
			Inputs: append(fields, Field{Name: "password", Type: Secret, Local: true, EnvFallback: true, Help: "password"}),
			Run:    func(context.Context, Request) (view.View, error) { return view.Text{Body: "up"}, nil },
		}}}
		if err := p.Validate(); err != nil {
			t.Errorf("%s: a plugin declaring its Service's fields is refused: %v", tc.svc.Namespace, err)
		}
	}
}

// A Service that cannot exist is a declaration's mistake, met the first time
// the plugin starts rather than on the first call that reads it.
func TestAServiceThatCannotExistPanicsWhereItIsDeclared(t *testing.T) {
	for name, svc := range map[string]Service{
		"no namespace":         {Product: "X", Reach: EndpointURL},
		"a URL with a switch":  {Namespace: "x", Product: "X", Reach: EndpointURL, Switch: TLSModes},
		"a scheme and no URL":  {Namespace: "x", Product: "X", Reach: EndpointAddress, Port: 1, Switch: TLSByScheme},
		"an address, no port":  {Namespace: "x", Product: "X", Reach: EndpointAddress, Switch: TLSOnOff},
		"a role it cannot be":  {Namespace: "x", Product: "X", Reach: EndpointPort, Port: 1, Switch: TLSOnOff},
		"a switch it is not":   {Namespace: "x", Product: "X", Reach: EndpointAddress, Port: 1, Switch: TLSSwitch(9)},
		"a port past the last": {Namespace: "x", Product: "X", Reach: EndpointHost, Port: 70000, Switch: TLSModes},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.HasPrefix(fmt.Sprint(r), "plugin: Service ") {
					t.Errorf("recovered %v", r)
				}
			}()
			svc.Fields()
		})
	}
}

// Target is where the call goes, each form checked before anything is
// dialled; a URL holding a credential is refused without repeating it.
func TestTargetIsWhereTheCallGoes(t *testing.T) {
	for _, tc := range []struct {
		svc    Service
		values map[string]any
		want   string
	}{
		{vaultService, map[string]any{"url": "https://vault.internal:8200/"}, "https://vault.internal:8200"},
		{vaultService, map[string]any{"url": " https://sso.example/auth/ "}, "https://sso.example/auth"},
		{redisService, map[string]any{"address": "db.internal"}, "//db.internal:6379"},
		{redisService, map[string]any{"address": "db.internal:7000"}, "//db.internal:7000"},
		{redisService, map[string]any{"address": "[::1]"}, "//[::1]:6379"},
		{redisService, map[string]any{"address": "::1"}, "//[::1]:6379"},
		{pgService, map[string]any{"host": "db.internal"}, "//db.internal:5432"},
		{pgService, map[string]any{"host": "db.internal", "port": 6543}, "//db.internal:6543"},
	} {
		got, verr := tc.svc.Target(asked(tc.values))
		if verr != nil || got.String() != tc.want {
			t.Errorf("%s %v: %v, %v; want %s", tc.svc.Namespace, tc.values, got, verr, tc.want)
		}
	}
	for _, tc := range []struct {
		svc    Service
		values map[string]any
		code   string
	}{
		{vaultService, map[string]any{"url": "ftp://vault.internal"}, "vault.url.invalid"},
		{vaultService, map[string]any{"url": "vault.internal:8200"}, "vault.url.invalid"},
		{vaultService, map[string]any{"url": "https://"}, "vault.url.invalid"},
		{vaultService, map[string]any{"url": "https://vault.internal/?x=1"}, "vault.url.invalid"},
		{redisService, map[string]any{"address": "rediss://db.internal:6380"}, "redis.address.invalid"},
		{redisService, map[string]any{"address": ":6379"}, "redis.address.invalid"},
		{redisService, map[string]any{"address": "db.internal:0"}, "redis.address.invalid"},
		{redisService, map[string]any{"address": "db.internal:redis"}, "redis.address.invalid"},
		{redisService, map[string]any{"address": "a:b:c"}, "redis.address.invalid"},
		{pgService, map[string]any{"host": " "}, "pg.host.invalid"},
	} {
		if _, verr := tc.svc.Target(asked(tc.values)); codeOf(verr) != tc.code || verr.Input == "" {
			t.Errorf("%v: %+v, want %s naming its input", tc.values, verr, tc.code)
		}
	}
	if _, verr := redisService.Target(asked(map[string]any{"address": "rediss://db.internal:6380"})); verr == nil ||
		!strings.Contains(verr.Message, "names no scheme") {
		t.Errorf("an address with a scheme: %+v", verr)
	}
	_, verr := vaultService.Target(asked(map[string]any{"url": "https://ops:s3cret@vault.internal"}))
	if codeOf(verr) != "vault.url.invalid" || strings.Contains(verr.Message, "s3cret") {
		t.Errorf("a URL holding a password: %+v", verr)
	}
}

// Each switch reads the settings into the mode they ask for, and a setting
// the mode would not read is refused rather than taken in silence; a key
// given alone asks for TLS and is refused as half a pair, where one plugin
// sent the call, password and all, in the clear.
func TestTLSReadsTheModeTheSettingsAskFor(t *testing.T) {
	ca, _ := issue(t, nil, nil, time.Hour, true)
	caFile := writePEM(t, "CERTIFICATE", ca.Raw)
	for _, tc := range []struct {
		name   string
		svc    Service
		req    Request
		mode   string
		code   string
		inHint string
	}{
		{"on/off, nothing", redisService, asked(nil), TLSDisable, "", ""},
		{"on/off, tls", redisService, asked(map[string]any{"tls": true}), TLSVerifyFull, "", ""},
		{"on/off, a CA", redisService, asked(map[string]any{"ca-file": caFile}), TLSVerifyFull, "", ""},
		{"on/off, a name", redisService, asked(map[string]any{"tls-server-name": "db.internal"}), TLSVerifyFull, "", ""},
		{"on/off, a key alone", redisService, asked(map[string]any{"key-file": "/k.pem"}), "", "redis.tls.cert.missing", "a pair"},
		{"on/off, a certificate alone", redisService, asked(map[string]any{"cert-file": "/c.pem"}), "", "redis.tls.key.missing", "a pair"},
		{"scheme, https", vaultService, asked(map[string]any{"url": "https://v"}), TLSVerifyFull, "", ""},
		{"scheme, http", vaultService, asked(map[string]any{"url": "http://v"}), TLSDisable, "", ""},
		{"scheme, a CA beside http", vaultService, asked(map[string]any{"url": "http://v", "ca-file": caFile}),
			"", "vault.tls.ca.unused", "to call a plain server, clear --ca-file"},
		{"scheme, a name beside http", vaultService, asked(map[string]any{"url": "http://v", "tls-server-name": "v"}),
			"", "vault.tls.name.unused", "an https:// URL"},
		{"scheme, a CA through a plain forward", vaultService,
			forwarded(map[string]any{"url": "http://127.0.0.1:5000", "ca-file": caFile}),
			"", "vault.tls.ca.unused", "`tunnel-tls: true` on that connection"},
		{"modes, nothing", pgService, asked(map[string]any{"host": "db"}), TLSPrefer, "", ""},
		{"modes, a CA beside prefer", pgService, asked(map[string]any{"host": "db", "tls": "prefer", "ca-file": caFile}),
			"", "pg.tls.ca.unused", "--tls verify-full"},
		{"modes, a name beside require", pgService, asked(map[string]any{"host": "db", "tls": "require", "tls-server-name": "db"}),
			"", "pg.tls.name.unused", "to keep require, clear --tls-server-name"},
		{"modes, a name beside verify-ca", pgService, asked(map[string]any{"host": "db", "tls": "verify-ca", "tls-server-name": "db"}),
			"", "pg.tls.name.unused", "checks the name too"},
		{"modes, a client pair beside disable", pgService, asked(map[string]any{"host": "db", "tls": "disable", "cert-file": "/c"}),
			"", "pg.tls.cert.unused", "any other mode"},
		{"modes, a CA beside disable", pgService, asked(map[string]any{"host": "db", "tls": "disable", "ca-file": caFile}),
			"", "pg.tls.ca.unused", "--tls verify-full checks the server's certificate; to keep disable, clear --ca-file"},
		{"modes, a CA through a forward", pgService, forwarded(map[string]any{"host": "127.0.0.1", "tls": "disable", "ca-file": caFile}),
			TLSVerifyFull, "", ""},
		{"modes, through a forward alone", pgService, forwarded(map[string]any{"host": "127.0.0.1", "tls": "disable"}),
			TLSDisable, "", ""},
		{"modes, verify-ca and a CA", pgService, asked(map[string]any{"host": "db", "tls": "verify-ca", "ca-file": caFile}),
			TLSVerifyCA, "", ""},
	} {
		plan, verr := tc.svc.TLS(tc.req)
		switch {
		case tc.code != "":
			if codeOf(verr) != tc.code || !strings.Contains(verr.Hint, tc.inHint) || verr.Input == "" {
				t.Errorf("%s: %+v, want %s with %q in its hint", tc.name, verr, tc.code, tc.inHint)
			}
		case verr != nil:
			t.Errorf("%s: refused: %v", tc.name, verr)
		case plan.Mode != tc.mode || (plan.Config == nil) != (tc.mode == TLSDisable):
			t.Errorf("%s: mode %q, config %v, want %q", tc.name, plan.Mode, plan.Config != nil, tc.mode)
		case plan.Config != nil && plan.Config.MinVersion != tls.VersionTLS12:
			t.Errorf("%s: MinVersion %x", tc.name, plan.Config.MinVersion)
		}
	}
}

// A plan connects the way its mode says, over a real handshake: verify-full
// checks the chain and the name, verify-ca the chain alone, against the CA
// named or, with none, the system's roots, and require nothing at all.
func TestATLSPlanChecksWhatItsModeSays(t *testing.T) {
	cert, key := issue(t, nil, nil, time.Hour, false)
	caFile := writePEM(t, "CERTIFICATE", cert.Raw)
	plan := func(svc Service, values map[string]any) *tls.Config {
		t.Helper()
		p, verr := svc.TLS(asked(values))
		if verr != nil {
			t.Fatal(verr)
		}
		return p.Config.Clone()
	}
	full := plan(redisService, map[string]any{"ca-file": caFile})
	if err := handshake(t, cert, key, full); err != nil {
		t.Errorf("verify-full against the CA it names: %v", err)
	}
	full.ServerName = "other.internal"
	if err := handshake(t, cert, key, full); codeOf(redisService.Refusal(asked(nil), err)) != "redis.tls.name" {
		t.Errorf("verify-full for another name: %v", err)
	}
	chain := plan(pgService, map[string]any{"host": "db", "tls": "verify-ca", "ca-file": caFile})
	chain.ServerName = "other.internal"
	if err := handshake(t, cert, key, chain); err != nil {
		t.Errorf("verify-ca checked the name: %v", err)
	}
	system := plan(pgService, map[string]any{"host": "db", "tls": "verify-ca"})
	if err := handshake(t, cert, key, system); codeOf(pgService.Refusal(asked(nil), err)) != "pg.tls.untrusted" {
		t.Errorf("verify-ca against the system's roots: %v", err)
	}
	if err := handshake(t, cert, key, plan(pgService, map[string]any{"host": "db", "tls": "require"})); err != nil {
		t.Errorf("require checked the certificate: %v", err)
	}
}

// A client pair is read whole, both halves through the capped reader, and a
// pair that does not match is refused without a byte of either file.
func TestAClientPairIsReadAsAPair(t *testing.T) {
	cert, key := issue(t, nil, nil, time.Hour, false)
	_, other := issue(t, nil, nil, time.Hour, false)
	certFile, keyFile, otherKey := writePEM(t, "CERTIFICATE", cert.Raw), keyPEM(t, key), keyPEM(t, other)
	p, verr := redisService.TLS(asked(map[string]any{"cert-file": certFile, "key-file": keyFile}))
	if verr != nil || p.Mode != TLSVerifyFull || len(p.Config.Certificates) != 1 || p.CertFile != certFile {
		t.Fatalf("a pair: %+v, %v", p, verr)
	}
	_, verr = redisService.TLS(asked(map[string]any{"cert-file": certFile, "key-file": otherKey}))
	if codeOf(verr) != "redis.tls.pair.invalid" || strings.Contains(verr.Message, "PRIVATE KEY") {
		t.Errorf("a mismatched pair: %+v", verr)
	}
	_, verr = redisService.TLS(asked(map[string]any{"cert-file": filepath.Join(t.TempDir(), "absent"), "key-file": keyFile}))
	if codeOf(verr) != "redis.tls.cert.unreadable" || verr.Input != "cert-file" {
		t.Errorf("an unreadable certificate: %+v", verr)
	}
	_, verr = redisService.TLS(asked(map[string]any{"cert-file": certFile, "key-file": t.TempDir()}))
	if codeOf(verr) != "redis.tls.key.unreadable" || verr.Input != "key-file" {
		t.Errorf("a directory for a key: %+v", verr)
	}
}

// An HTTP API's client reaches the server its CA names, follows the policy's
// redirects, keeps the proxy, holds no jar and no overall timeout, and is
// refused for a service that is no HTTP API.
func TestTheHTTPClientIsTheOneEveryHTTPPluginWrote(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(other.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/away" {
			http.Redirect(w, r, other.URL, http.StatusFound)
		}
	}))
	t.Cleanup(srv.Close)
	req := asked(map[string]any{"url": srv.URL, "ca-file": writePEM(t, "CERTIFICATE", srv.Certificate().Raw)})
	client, verr := vaultService.HTTPClient(req)
	if verr != nil {
		t.Fatal(verr)
	}
	transport := client.Transport.(*http.Transport)
	if client.Jar != nil || client.Timeout != 0 || transport.Proxy == nil || transport.ResponseHeaderTimeout != 30*time.Second ||
		!transport.ForceAttemptHTTP2 || transport.TLSClientConfig.RootCAs == nil {
		t.Errorf("client %+v, transport %+v", client, transport)
	}
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("a GET to the server the CA names: %v", err)
	}
	_ = resp.Body.Close()
	resp, err = client.Get(srv.URL + "/away")
	if err == nil {
		_ = resp.Body.Close()
	}
	if codeOf(vaultService.Refusal(req, err)) != "vault.redirect.refused" {
		t.Errorf("a redirect to another address: %v", err)
	}
	if _, verr := redisService.HTTPClient(asked(nil)); codeOf(verr) != "redis.conn.invalid" {
		t.Errorf("a client for a service that is no HTTP API: %v", verr)
	}
}

// A server that takes a request and never answers is bounded by the
// response timeout, which the host's calls have no other bound for.
func TestAServerThatNeverAnswersIsATimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	svc := vaultService
	svc.ResponseTimeout = 50 * time.Millisecond
	req := asked(map[string]any{"url": srv.URL})
	client, verr := svc.HTTPClient(req)
	if verr != nil {
		t.Fatal(verr)
	}
	// A bound of the test's own, so a client the response timeout no longer
	// bounds fails rather than hangs. By the time taken, since net/http's
	// timeout matches context.DeadlineExceeded as the context's own does.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	get, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := client.Do(get)
	if err == nil {
		_ = resp.Body.Close()
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the response timeout did not bound the wait: %v", took)
	}
	if verr := svc.Refusal(req, err); codeOf(verr) != "vault.timeout" {
		t.Errorf("a server that never answers: %v, %+v", err, verr)
	}
}

// An https:// proxy beside a CA or a server name is refused: Go would check
// the proxy's certificate with them. An http:// proxy carries the call, and
// one beside neither setting is checked against the system's roots.
func TestAnHTTPSProxyIsRefusedBesideTheSettingsThatWouldCheckIt(t *testing.T) {
	ca, _ := issue(t, nil, nil, time.Hour, true)
	caFile := writePEM(t, "CERTIFICATE", ca.Raw)
	useProxy(t, "https://proxy.corp:3128")
	for _, tc := range []struct {
		values map[string]any
		code   string
	}{
		{map[string]any{"url": "https://vault.internal", "ca-file": caFile}, "vault.proxy.tls"},
		{map[string]any{"url": "https://vault.internal", "tls-server-name": "vault.lan"}, "vault.proxy.tls"},
		{map[string]any{"url": "https://vault.internal"}, ""},
		{map[string]any{"url": "http://vault.internal"}, ""},
	} {
		_, verr := vaultService.HTTPClient(asked(tc.values))
		if codeOf(verr) != tc.code || (tc.code != "" && !strings.Contains(verr.Hint, "NO_PROXY naming vault.internal")) {
			t.Errorf("%v: %+v, want %q", tc.values, verr, tc.code)
		}
	}
	useProxy(t, "http://proxy.corp:3128")
	if _, verr := vaultService.HTTPClient(asked(map[string]any{"url": "https://vault.internal", "ca-file": caFile})); verr != nil {
		t.Errorf("an http:// proxy beside a CA: %v", verr)
	}
}

// stalled is a network error that timed out.
type stalled struct{}

func (stalled) Error() string   { return "i/o timeout" }
func (stalled) Timeout() bool   { return true }
func (stalled) Temporary() bool { return true }

// Every failure the plugins shared is worded under its code, in the order
// they settled on; anything else is the plugin's to word.
func TestARefusalWordsEachFailureUnderItsCode(t *testing.T) {
	dial := func(errno syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
	}
	cert, _ := issue(t, nil, nil, time.Hour, false)
	https := map[string]any{"url": "https://vault.internal:8200"}
	plain := map[string]any{"url": "http://vault.internal:8200"}
	for _, tc := range []struct {
		name string
		req  Request
		err  error
		code string
		says string
	}{
		{"a redirect", asked(https), &RedirectRefusal{Status: 302, Method: "GET", Reason: RedirectDowngrade,
			From: mustURL(t, "https://vault.internal:8200"), To: mustURL(t, "http://vault.internal")}, "vault.redirect.refused", "plain HTTP"},
		{"a deadline", asked(https), fmt.Errorf("get: %w", context.DeadlineExceeded), "vault.timeout", "in time"},
		{"a name", asked(https), &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "vault.internal"}},
			"vault.host.unknown", `"vault.internal"`},
		{"an issuer nothing trusts", asked(https), &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
			"vault.tls.untrusted", "nothing here trusts"},
		{"another name", asked(https), &tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: cert, Host: "vault.internal"}},
			"vault.tls.name", "is for db.internal, 127.0.0.1, not for vault.internal"},
		{"another name through a forward", forwarded(map[string]any{"url": "https://127.0.0.1:5000"}),
			&tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: cert, Host: "127.0.0.1"}}, "vault.tls.forward", "where the forward ends"},
		{"a named name through a forward", forwarded(map[string]any{"url": "https://127.0.0.1:5000", "tls-server-name": "vault.lan"}),
			&tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: cert, Host: "vault.lan"}}, "vault.tls.name", "not for vault.lan"},
		{"an expired certificate", asked(https), &tls.CertificateVerificationError{Err: x509.CertificateInvalidError{Reason: x509.Expired}},
			"vault.tls.rejected", "does not verify"},
		{"no route", asked(https), dial(syscall.ENETUNREACH), "vault.conn.unreachable", "network is unreachable"},
		{"nothing listening", asked(https), dial(syscall.ECONNREFUSED), "vault.conn.refused", "nothing is listening on https://vault.internal:8200"},
		{"a timeout", asked(https), &url.Error{Op: "Get", URL: "https://vault.internal:8200", Err: stalled{}}, "vault.timeout", "in time"},
		{"plain HTTP to a TLS port", asked(plain), errors.New("400: Client sent an HTTP request to an HTTPS server"),
			"vault.tls.expected", "speaks only TLS"},
		{"the same words over TLS", asked(https), errors.New("400: Client sent an HTTP request to an HTTPS server"), "", ""},
		{"anything else", asked(https), errors.New("unexpected EOF"), "", ""},
		{"a refusal already worded", asked(https), view.Errorf("vault.denied", "no"), "vault.denied", "no"},
	} {
		verr := vaultService.Refusal(tc.req, tc.err)
		if codeOf(verr) != tc.code || (verr != nil && !strings.Contains(verr.Message, tc.says)) {
			t.Errorf("%s: %+v, want %q saying %q", tc.name, verr, tc.code, tc.says)
		}
	}
}

// Through a proxy, a dial that failed is the proxy's, and every refusal that
// may be the proxy's answer names it and NO_PROXY.
func TestARefusalNamesTheProxyTheCallWentThrough(t *testing.T) {
	useProxy(t, "http://proxy.corp:3128")
	req := asked(map[string]any{"url": "https://vault.internal:8200"})
	atProxy := &url.Error{Op: "Get", URL: "https://vault.internal:8200", Err: &net.OpError{Op: "proxyconnect", Net: "tcp",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}}}
	verr := vaultService.Refusal(req, atProxy)
	if codeOf(verr) != "vault.conn.refused" ||
		verr.Message != "the proxy HTTPS_PROXY names, proxy.corp:3128, did not take the call: connect: connection refused" ||
		!strings.Contains(verr.Hint, "NO_PROXY naming vault.internal") {
		t.Errorf("a proxy that refused: %+v", verr)
	}
	for name, err := range map[string]error{
		"untrusted": &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		"timeout":   stalled{},
	} {
		if verr := vaultService.Refusal(req, err); verr == nil || !strings.Contains(verr.Hint, "proxy.corp:3128") {
			t.Errorf("%s through a proxy: %+v", name, verr)
		}
	}
}
