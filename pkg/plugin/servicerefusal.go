package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Refusal words a failed connection or request for req's reader, under the
// codes every connection plugin shares, or answers nil for a failure none of
// them names, which the plugin words itself, as <ns>.conn.failed by
// convention. A *view.Error comes back as it is.
//
// **Ask the driver's own errors first.** A server's coded answer, a status a
// driver typed, a reply it quotes because it could not parse it: whatever a
// server said is read by what it is before this is asked, since a server's
// words relayed with nothing typed under them can spell a dial's failure as a
// dial does (netdiag.go). A plugin may replace a hint for its product, never
// the code.
//
// In the order the plugins settled on, each reading only what it names: a
// refused redirect; a deadline; a name that did not resolve; a certificate
// other than the one trusted for the server on first use, one nothing here
// trusts, one for another name — through a forward, ForwardNameRefusal — and
// any other verdict, each carrying the chain the server presented when no CA
// the operator named was the judge (presenting); the proxy the call goes through
// not taking it; no route; nothing listening; a timeout; plain HTTP to a
// server that speaks only TLS. The certificate is asked before the dial,
// since the dial's questions read words where no errno is, and a
// certificate's names are the server's to choose.
func (s Service) Refusal(req Request, err error) *view.Error {
	if err == nil {
		return nil
	}
	var already *view.Error
	if errors.As(err, &already) {
		return already
	}
	sf := req.Surface()
	setting, endpoint := s.setting(), s.endpoint(req)
	proxy, target := s.proxied(req)
	named := strings.TrimSpace(req.String("tls-server-name")) != ""

	var redirect *RedirectRefusal
	if errors.As(err, &redirect) {
		return redirect.Refusal(req, s.Namespace, setting, s.RedirectHint)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return s.timedOut(endpoint, proxy, target)
	}
	// The name first: a dial that could not resolve its host fails with a
	// *net.OpError around the *net.DNSError, and read the other way round,
	// every name nothing resolves was a port nothing listens on. The name the
	// resolver was asked, which through a proxy is the proxy's: the server's
	// own name is the proxy's to resolve.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		name := dnsErr.Name
		if name == "" {
			name = HostOf(endpoint)
		}
		return view.Errorf(s.Namespace+".host.unknown", "no address for %q", name).
			WithHint(sf.DNSHint(name) + through(proxy, target))
	}
	// The handshake's verdict, when there is one, carries what the server
	// presented, which each refusal of a certificate below hands the host
	// (presenting). A server that presented none of the certificates trusted
	// for it is that before it is anything else: Go's own verdict on it is no
	// news, since what was trusted stood in for exactly that.
	var verifyErr *tls.CertificateVerificationError
	errors.As(err, &verifyErr)
	if verifyErr != nil && s.trusts(req) {
		return s.changed(req, endpoint, verifyErr)
	}
	// The CA named as where it belongs, not as something to pass: over MCP
	// ca-file is the operator's setting, and an agent told to pass it has no
	// such argument to give. Only for a verdict that means an issuer nothing
	// here vouches for (CertUntrusted), never a revoked certificate, which a
	// CA file would get past by checking less.
	// A copy of the CA that signed the server's certificate, expired in the CA
	// the operator named, whatever Go called it: an expired root is reported
	// as the certificate that expired, which read as the server's own.
	ca := s.tlsSettings(req).ca
	if ca != "" {
		if expired, ok := expiredSigner(ca, verifyErr); ok {
			return view.Errorf(s.Namespace+".tls.ca.expired", "the CA in %s that signed the certificate behind %s expired on %s",
				sf.SettingName("ca-file"), req.Reached(endpoint), expired.UTC().Format(time.DateOnly)).
				WithHint(staleCopy(sf, "ca-file")).WithInput("ca-file")
		}
	}
	if CertUntrusted(err) {
		if ca != "" {
			return s.unvouched(req, endpoint)
		}
		hint := "a " + s.Product + " behind an internal CA wants that CA rather than verification turned off: " +
			sf.CAHint("ca-file")
		if proxy != nil {
			hint += "; the call goes through the proxy at " + proxy.Host + ", and a proxy that inspects TLS presents " +
				"a certificate of its own"
		}
		return s.presenting(req, view.Errorf(s.Namespace+".tls.untrusted", "%s presented a certificate nothing here trusts",
			req.Reached(endpoint)).WithHint(hint), verifyErr)
	}
	// A certificate that is not for the end of a forward is no fault of the
	// server's: the host fills the endpoint with 127.0.0.1, which a server's
	// certificate names only by luck. A name given and not matched is the
	// certificate's to explain, forward or not.
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		if req.Tunnel() != TunnelNone && !named {
			return s.presenting(req, ForwardNameRefusal(req, s.Namespace+".tls.forward", endpoint, "server", hostErr),
				verifyErr)
		}
		hint := sf.SettingName("tls-server-name") + " checks it for one of those names in place of " + hostErr.Host +
			", as strictly; or reach the server by one of them"
		if named {
			hint = "the certificate is checked for the name in " + sf.SettingName("tls-server-name") +
				": name one it is for, or have it reissued for " + hostErr.Host
		}
		return s.presenting(req, view.Errorf(s.Namespace+".tls.name", "the certificate behind %s is for %s, not for %s",
			req.Reached(endpoint), CertNames(hostErr.Certificate), hostErr.Host).WithHint(hint), verifyErr)
	}
	// Every other verdict is its own reason, in the verifier's words, and
	// never "could not reach": the server was reached, and answered.
	if verifyErr != nil {
		rejected := view.Errorf(s.Namespace+".tls.rejected", "%s presented a certificate that does not verify: %v",
			req.Reached(endpoint), verifyErr.Err)
		if hint := CertPolicyHint(err); hint != "" {
			return s.presenting(req, rejected.WithHint(hint), verifyErr)
		}
		checked := "the host in " + sf.SettingName(setting)
		if named {
			checked = "the name in " + sf.SettingName("tls-server-name")
		}
		return s.presenting(req, rejected.WithHint("a certificate is checked for "+checked+", its dates and the use "+
			"it was issued for, as well as for who issued it"), verifyErr)
	}
	unroutable, refused := DialUnroutable(err), DialRefused(err)
	// Through a proxy, a dial that failed is the proxy's: the server is the
	// proxy's to reach, and "nothing is listening" about the server sent its
	// reader to check a server the call never got near.
	if proxy != nil && atProxy(err) && (unroutable || refused) {
		code := s.Namespace + ".conn.refused"
		if unroutable {
			code = s.Namespace + ".conn.unreachable"
		}
		return view.Errorf(code, "the proxy %s names, %s, did not take the call: %v", proxyVariable(target),
			proxy.Host, dialReason(err)).
			WithHint("is the proxy up? NO_PROXY naming " + target.Hostname() + " reaches " + s.Product + " without it")
	}
	// Short of the host, before the port: a dial that found no way there
	// reached nothing that could refuse it, and read as refused, a server
	// behind a VPN that is down was "nothing is listening".
	if unroutable {
		return view.Errorf(s.Namespace+".conn.unreachable", "%s cannot be reached from this machine: %v",
			endpoint, dialReason(err)).
			WithHint("no route leads there from here — a VPN or tunnel the server sits behind that is down looks " +
				"exactly like this, and so does " + sf.SettingName(setting) + " naming an address on a network this " +
				"machine is not on")
	}
	if refused {
		hint := "is the server up, and is " + sf.SettingName(setting) + " right?"
		if s.Port != 0 {
			hint += " " + s.Product + " listens on " + strconv.Itoa(s.Port) + " by default"
		}
		return view.Errorf(s.Namespace+".conn.refused", "nothing is listening on %s", endpoint).WithHint(hint)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return s.timedOut(endpoint, proxy, target)
	}
	if TLSExpected(err) && s.plain(req) {
		return view.Errorf(s.Namespace+".tls.expected", "this call reached %s in the clear, and it speaks only TLS",
			req.Reached(endpoint)).WithHint(s.toTLS(req))
	}
	return nil
}

// presenting is verr carrying the chain the server presented (view.Error.
// Presented), for the host to remember so the operator can trust that server
// on first use. Not when a CA the operator named was the judge: that CA is the
// only issuer they vouch for on the connection, and a server it does not vouch
// for is theirs to fix rather than to trust past it.
func (s Service) presenting(req Request, verr *view.Error, verifyErr *tls.CertificateVerificationError) *view.Error {
	if verifyErr == nil || len(verifyErr.UnverifiedCertificates) == 0 || s.tlsSettings(req).ca != "" {
		return verr
	}
	chain := make([][]byte, 0, len(verifyErr.UnverifiedCertificates))
	for _, c := range verifyErr.UnverifiedCertificates {
		chain = append(chain, c.Raw)
	}
	return verr.WithPresented(chain)
}

// unvouched is the refusal of a server the CA the operator named does not
// vouch for. It said "a certificate nothing here trusts" and told them to
// name the CA in ca-file, which they had: what the server presents was issued
// by another CA, or by the same one renewed with a key of its own. (A copy
// that expired is said as that before this is asked, by expiredSigner, and
// CAPool refuses a bundle whose every certificate has expired before anything
// is dialled.) Never with the server's chain to trust on first use: the named
// CA is the only issuer the operator vouches for on the connection
// (presenting).
func (s Service) unvouched(req Request, endpoint string) *view.Error {
	sf := req.Surface()
	return view.Errorf(s.Namespace+".tls.untrusted", "%s presented a certificate the CA in %s does not vouch for",
		req.Reached(endpoint), sf.SettingName("ca-file")).
		WithHint("another CA issued it, or the one named renewed with a key of its own and left this copy behind: " +
			"export the CA the server's certificate is issued by now").WithInput("ca-file")
}

// expiredSigner is the end date of an expired certificate in the bundle at
// path that signed the certificate the server presented first, when there is
// one.
func expiredSigner(path string, verifyErr *tls.CertificateVerificationError) (time.Time, bool) {
	if verifyErr == nil || len(verifyErr.UnverifiedCertificates) == 0 {
		return time.Time{}, false
	}
	bundle, err := readTLSFile(path)
	if err != nil {
		return time.Time{}, false
	}
	leaf := verifyErr.UnverifiedCertificates[0]
	for _, c := range certificatesIn(bundle) {
		if time.Now().After(c.NotAfter) && leaf.CheckSignatureFrom(c) == nil {
			return c.NotAfter, true
		}
	}
	return time.Time{}, false
}

// changed is the refusal of a server that presented a certificate other than
// the ones trusted for it on first use, with what it presented: a renewal by
// another CA, a new self-signed certificate and another server answering at
// that address are one answer from here, and only somebody who knows the
// server tells them apart. The host says when what it replaces was trusted,
// and how the operator looks at both.
func (s Service) changed(req Request, endpoint string, verifyErr *tls.CertificateVerificationError) *view.Error {
	return s.presenting(req, view.Errorf(s.Namespace+".tls.changed",
		"%s presented a certificate other than the one trusted for it", req.Reached(endpoint)).
		WithHint("a renewal by another CA, a new self-signed certificate and another server answering there all look "+
			"like this, and only somebody who knows the server can tell them apart"), verifyErr)
}

func (s Service) timedOut(endpoint string, proxy, target *url.URL) *view.Error {
	return view.Errorf(s.Namespace+".timeout", "%s did not answer in time", endpoint).
		WithHint("a firewall that drops rather than refuses looks exactly like this, and so does a server that took " +
			"the request and never answered" + through(proxy, target))
}

// plain reports whether the call went out in the clear.
func (s Service) plain(req Request) bool {
	mode, verr := s.mode(req, s.tlsSettings(req))
	return verr == nil && mode == TLSDisable
}

// toTLS is where a call that went out in the clear to a server that speaks
// only TLS is changed: through a forward the profile's connection, which the
// host fills, and otherwise the setting that switches TLS.
func (s Service) toTLS(req Request) string {
	sf := req.Surface()
	forward := req.Tunnel() != TunnelNone
	switch {
	case s.Switch == TLSByScheme && forward:
		return "a forward carries plain http:// unless the profile's connection says its far end speaks TLS: " +
			"`tunnel-tls: true` on that connection"
	case s.Switch == TLSByScheme:
		return "an https:// URL is what makes the call TLS: " + sf.SettingName("url") + " names the scheme"
	case forward:
		return "a forward turns TLS off, and either of " + sf.SettingName("ca-file", "tls-server-name") +
			" on that connection turns it back on, the server's certificate checked"
	case s.Switch == TLSOnOff:
		return sf.SettingTo("tls", true) + " connects over TLS"
	}
	return sf.SettingTo("tls", TLSVerifyFull) + " connects over TLS and checks the server's certificate"
}

// atProxy reports whether err is a failure to reach the proxy a request went
// through, which Go's transport says as a proxyconnect *net.OpError.
func atProxy(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "proxyconnect"
}

// dialReason is what the operating system said of a failed dial, without the
// addresses net.OpError puts around it.
func dialReason(err error) error {
	var opErr *net.OpError
	for errors.As(err, &opErr) && opErr.Err != nil {
		err = opErr.Err
	}
	return err
}

// through is the clause a refusal ends with when the call went through a
// proxy, which may be what answered: NO_PROXY is how the operator reaches the
// server without it.
func through(proxy, target *url.URL) string {
	if proxy == nil {
		return ""
	}
	return "; the call goes through the proxy at " + proxy.Host + " that " + proxyVariable(target) +
		" names, and NO_PROXY naming " + target.Hostname() + " reaches it directly"
}
