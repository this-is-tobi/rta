package plugin

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// defaultResponseTimeout is how long a request waits for its response's
// headers when the Service names no other wait.
const defaultResponseTimeout = 30 * time.Second

// proxyFromEnvironment is the proxy a request goes through, as HTTP(S)_PROXY
// and NO_PROXY say: the host hands both to every plugin. A variable so a test
// can stand in for net/http's, which reads the environment once per process.
var proxyFromEnvironment = http.ProxyFromEnvironment

// HTTPClient is the client an HTTP API's calls go through, for one call:
// build it in the handler and let it go with the call.
//
// **Never keep one past the call** — in a package variable, a cache, a
// session that outlives it — as the obvious optimisation would. One plugin
// process serves every profile and every caller, and a kept client pools
// connections across them: one opened with one profile's client certificate
// would carry the next profile's request.
//
// Its transport states every setting a call relies on: the proxy, HTTP/2, and
// the dial, handshake and idle timeouts http.DefaultTransport has. Stated, not
// cloned from that global, which any dependency may have replaced or changed;
// and never a bare http.Transport, which has none of them — two plugins built
// one the moment a CA was named, and lost the operator's proxy with it. Its
// ResponseHeaderTimeout (Service.ResponseTimeout, 30 seconds by default)
// bounds a server that takes a request and never answers, which nothing else
// would: the host bounds no call. It bounds the wait for the headers alone,
// so a long download still streams. No overall Timeout, for that download,
// and no cookie jar, so nothing one call was told rides on the next.
//
// CheckRedirect is s.Redirects. A request body must be one Go can send again,
// a bytes or strings reader, or a 307 comes back as a response rather than
// being followed.
//
// Refused for a Service reached by no URL, and for an https:// proxy beside
// ca-file or tls-server-name (<ns>.proxy.tls): Go checks the proxy's own
// certificate with the TLS settings meant for the server behind it, and every
// call would fail at the proxy, for a reason the handshake's words would put
// on the server.
func (s Service) HTTPClient(req Request) (*http.Client, *view.Error) {
	if s.Reach != EndpointURL {
		return nil, view.Errorf(s.Namespace+".conn.invalid", "%s is not reached over HTTP, so it has no HTTP client", s.Product).
			WithHint("Service.HTTPClient is for a Service whose Reach is EndpointURL")
	}
	target, verr := s.Target(req)
	if verr != nil {
		return nil, verr
	}
	plan, verr := s.TLS(req)
	if verr != nil {
		return nil, verr
	}
	if verr := s.proxyTLS(req, target, plan); verr != nil {
		return nil, verr
	}
	return &http.Client{Transport: s.transport(plan.Config), CheckRedirect: s.Redirects.CheckRedirect}, nil
}

// transport is the transport an HTTP API's calls go through, with cfg as its
// TLS settings.
func (s Service) transport(cfg *tls.Config) *http.Transport {
	wait := s.ResponseTimeout
	if wait == 0 {
		wait = defaultResponseTimeout
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 proxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: wait,
		TLSClientConfig:       cfg,
	}
}

// proxyTLS refuses an https:// proxy for a call whose TLS settings would
// check the proxy too: Go handshakes such a proxy with the transport's own
// TLS settings, so ca-file's CA replaces the roots the proxy's certificate
// is checked against, and tls-server-name is the name it is checked for.
// An http:// proxy carries the call as a tunnel, its TLS end to end, and a
// call naming neither setting checks the proxy against the system's roots.
func (s Service) proxyTLS(req Request, target *url.URL, plan *TLSPlan) *view.Error {
	if plan.Config == nil || (plan.CAFile == "" && plan.ServerName == "") {
		return nil
	}
	proxy := httpsProxy(target)
	if proxy == nil {
		return nil
	}
	var named []string
	if plan.CAFile != "" {
		named = append(named, "ca-file")
	}
	if plan.ServerName != "" {
		named = append(named, "tls-server-name")
	}
	return view.Errorf(s.Namespace+".proxy.tls", "the proxy %s names, %s, is reached over TLS, and %s would check its "+
		"certificate as well as %s's", proxyVariable(target), proxy.Host, req.Surface().SettingName(named...), s.Product).
		WithHint("Go checks an https:// proxy with the TLS settings meant for the server behind it: an http:// proxy " +
			"carries the call with its TLS still end to end, and NO_PROXY naming " + target.Hostname() +
			" reaches it directly")
}

// httpsProxy is the https:// proxy a call to target goes through, or nil. A
// proxy setting that does not parse is the transport's to refuse, in its own
// words, when the call is made.
func httpsProxy(target *url.URL) *url.URL {
	proxy, err := proxyFromEnvironment(&http.Request{URL: target})
	if err != nil || proxy == nil || proxy.Scheme != "https" {
		return nil
	}
	return proxy
}

// proxied is the proxy an HTTP API's call goes through and the URL it carries
// the call to, or nil for a call that goes direct, as every other service's
// does.
func (s Service) proxied(req Request) (proxy, target *url.URL) {
	if s.Reach != EndpointURL {
		return nil, nil
	}
	target, verr := s.Target(req)
	if verr != nil {
		return nil, nil
	}
	proxy, err := proxyFromEnvironment(&http.Request{URL: target})
	if err != nil || proxy == nil {
		return nil, nil
	}
	return proxy, target
}

// proxyVariable is the variable that names the proxy a call to target goes
// through.
func proxyVariable(target *url.URL) string {
	if target.Scheme == "https" {
		return "HTTPS_PROXY"
	}
	return "HTTP_PROXY"
}
