package plugin

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// TLSPlan is what a call's settings ask of transport security, read and
// checked: what the connection is made with.
type TLSPlan struct {
	// Mode is the mode in force: under TLSModes the one stated, or verify-full
	// where a forward turned it off and a TLS setting turned it back on;
	// otherwise TLSVerifyFull or TLSDisable.
	Mode string
	// Config is nil for a plain connection. Otherwise TLS 1.2 at least, the
	// ServerName tls-server-name gives, the RootCAs ca-file holds — in place
	// of the system's roots, as Surface.CAHint tells the operator — and the
	// client pair. verify-ca checks the chain in VerifyConnection, without
	// the name; require and prefer check nothing, which only a mode the
	// operator stated asks for.
	Config *tls.Config
	// The files as named, ~ expanded and each read once already, and the name,
	// for a driver that takes paths rather than a tls.Config.
	CAFile, CertFile, KeyFile, ServerName string
}

// TLS reads the call's TLS settings into a plan, every file they name read
// and checked before anything is dialled, the dry run included: a refusal
// then comes before the server is reached, rather than as a handshake that
// failed for a reason it cannot say. <ns>.tls.ca.unreadable and .ca.invalid,
// .cert.missing, .key.missing, .cert.unreadable, .key.unreadable and
// .pair.invalid; and .ca.unused, .name.unused and .cert.unused for a setting
// the mode would not read.
func (s Service) TLS(req Request) (*TLSPlan, *view.Error) {
	set := s.tlsSettings(req)
	mode, verr := s.mode(req, set)
	if verr != nil {
		return nil, verr
	}
	if verr := s.unread(req, mode, set); verr != nil {
		return nil, verr
	}
	plan := &TLSPlan{Mode: mode, CAFile: set.ca, CertFile: set.cert, KeyFile: set.key, ServerName: set.name}
	if mode == TLSDisable {
		return plan, nil
	}
	roots, verr := CAPool(req, s.Namespace, "ca-file")
	if verr != nil {
		return nil, verr
	}
	pair, verr := s.clientPair(req, set)
	if verr != nil {
		return nil, verr
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: set.name, RootCAs: roots}
	if pair != nil {
		cfg.Certificates = []tls.Certificate{*pair}
	}
	switch mode {
	case TLSVerifyCA:
		cfg.InsecureSkipVerify = true //nolint:gosec // VerifyConnection checks the chain, which is all verify-ca asks
		cfg.VerifyConnection = verifyChain(roots)
	case TLSRequire, TLSPrefer:
		cfg.InsecureSkipVerify = true //nolint:gosec // checking nothing is what require and prefer say, and only the operator states either
	}
	plan.Config = cfg
	return plan, nil
}

// tlsSettings are the TLS settings a call names, paths ~ expanded.
type tlsSettings struct{ ca, name, cert, key string }

func (s Service) tlsSettings(req Request) tlsSettings {
	set := tlsSettings{ca: ExpandHome(req.String("ca-file")), name: strings.TrimSpace(req.String("tls-server-name"))}
	if s.ClientCert {
		set.cert, set.key = ExpandHome(req.String("cert-file")), ExpandHome(req.String("key-file"))
	}
	return set
}

// any reports whether the call names any TLS setting: each means something
// only over TLS.
func (t tlsSettings) any() bool { return t.ca != "" || t.name != "" || t.cert != "" || t.key != "" }

// mode is the TLS mode the call's settings ask for, before a file is read.
func (s Service) mode(req Request, set tlsSettings) (string, *view.Error) {
	switch s.Switch {
	case TLSByScheme:
		target, verr := s.Target(req)
		if verr != nil {
			return "", verr
		}
		if target.Scheme == "https" {
			return TLSVerifyFull, nil
		}
		return TLSDisable, nil
	case TLSOnOff:
		if req.Bool("tls") || set.any() {
			return TLSVerifyFull, nil
		}
		return TLSDisable, nil
	}
	mode := req.String("tls")
	if mode == "" {
		mode = TLSPrefer
	}
	if !slices.Contains(tlsModes, mode) {
		return "", view.Errorf(s.Namespace+".tls.invalid", "%q is not a TLS mode", mode).
			WithHint(req.Surface().SettingName("tls") + " is one of " + strings.Join(tlsModes, ", ")).WithInput("tls")
	}
	// Through a forward the host has turned the mode off (EndpointTLS), since
	// a database's own negotiation over a forward breaks it. A TLS setting
	// beside it is the operator saying the far end is checked, and the forward
	// carries TLS end to end when asked: at verify-full, the one mode that
	// checks what the setting names, never a mode that checks less.
	if mode == TLSDisable && req.Tunnel() != TunnelNone && set.any() {
		return TLSVerifyFull, nil
	}
	return mode, nil
}

// unread refuses a TLS setting the call's mode would not read.
//
// An operator who names a CA, a name or a client certificate expects it
// checked or sent. Taken in silence, the call read as checked while it went
// out in the clear, or went unverified: a CA written once for the https://
// address is still there the day url is pointed at a plain one, and a CA
// beside require checks nothing. Plugins refused this for themselves, under
// four codes, and took it in silence beside some modes.
func (s Service) unread(req Request, mode string, set tlsSettings) *view.Error {
	var setting, code, what string
	switch {
	case set.ca != "" && mode != TLSVerifyCA && mode != TLSVerifyFull:
		setting, code, what = "ca-file", "ca", "names a CA"
	case set.name != "" && mode != TLSVerifyFull:
		setting, code, what = "tls-server-name", "name", "names a certificate to check"
	case set.cert != "" && mode == TLSDisable:
		setting, code, what = "cert-file", "cert", "names a client certificate"
	case set.key != "" && mode == TLSDisable:
		setting, code, what = "key-file", "cert", "names a client key"
	default:
		return nil
	}
	sf := req.Surface()
	reached := req.Reached(s.endpoint(req))
	checked := sf.SettingTo("tls", TLSVerifyFull)
	var because, fix string
	switch {
	case s.Switch == TLSByScheme:
		because = "this call would reach " + reached + " over plain HTTP"
		fix = "an https:// URL is what makes the call TLS"
		if req.Tunnel() != TunnelNone {
			fix = "a forward carries plain http:// unless the profile's connection says its far end speaks TLS: " +
				"`tunnel-tls: true` on that connection, for a " + s.Product + " serving HTTPS on the port it forwards"
		}
	case mode == TLSDisable && code == "cert":
		because = sf.SettingTo("tls", TLSDisable) + " reaches " + reached + " in the clear"
		fix = "any other mode presents it to a server that asks for one"
	case mode == TLSDisable:
		because = sf.SettingTo("tls", TLSDisable) + " reaches " + reached + " in the clear"
		fix = checked + " checks the server's certificate"
	case mode == TLSVerifyCA:
		because = "verify-ca checks the chain and not the name"
		fix = checked + " checks the name too"
	case code == "ca":
		because = sf.SettingTo("tls", mode) + " checks no certificate"
		fix = checked + " checks the certificate against it, and verify-ca its chain alone"
	default:
		because = sf.SettingTo("tls", mode) + " checks no certificate"
		fix = checked + " checks the certificate for it"
	}
	return view.Errorf(s.Namespace+".tls."+code+".unused", "%s %s, and %s", sf.SettingName(setting), what, because).
		WithHint(fix + s.clearInstead(req, mode, setting)).WithInput(setting)
}

// clearInstead is the second way out of a refusal of a TLS setting the mode
// would not read: the setting cleared.
//
// **A refusal that names only the way to TLS reads as the only way.** A CA is
// the setting an operator writes once, among their global settings, for the
// servers that have one; the day one development server answers in the
// clear is the day it is refused, and told only how to get TLS on a server
// that has none, they bend the server to the setting when the setting is what
// should move. Through a forward the way is the profile's own set:, whose
// value beats the global one, so an empty value there clears it for that
// connection alone.
func (s Service) clearInstead(req Request, mode, setting string) string {
	switch {
	case req.Tunnel() != TunnelNone:
		return "; for a far end that is plain, give that profile's set: an empty " + setting + " instead"
	case s.Switch == TLSByScheme:
		return "; to call a plain server, clear " + req.Surface().SettingName(setting)
	}
	return "; to keep " + mode + ", clear " + req.Surface().SettingName(setting)
}

// clientPair reads the client certificate and its key, or nil when neither is
// named. Both or neither: a half pair is refused rather than dialled, since a
// handshake without the certificate is answered by a server that does not
// say why, and a key named alone once left a call in the clear.
func (s Service) clientPair(req Request, set tlsSettings) (*tls.Certificate, *view.Error) {
	if set.cert == "" && set.key == "" {
		return nil, nil
	}
	sf := req.Surface()
	pair := sf.SettingName("cert-file", "key-file") + " are a pair: the client certificate and its private key, both PEM"
	switch {
	case set.cert == "":
		return nil, view.Errorf(s.Namespace+".tls.cert.missing", "%s is given without %s",
			sf.SettingName("key-file"), sf.SettingName("cert-file")).WithHint(pair).WithInput("cert-file")
	case set.key == "":
		return nil, view.Errorf(s.Namespace+".tls.key.missing", "%s is given without %s",
			sf.SettingName("cert-file"), sf.SettingName("key-file")).WithHint(pair).WithInput("key-file")
	}
	certPEM, err := readTLSFile(set.cert)
	if err != nil {
		return nil, view.Errorf(s.Namespace+".tls.cert.unreadable", "%v", err).
			WithHint(sf.SettingName("cert-file") + " is a path on this machine, read here rather than by the server").
			WithInput(PathInput("cert-file", err))
	}
	keyPEM, err := readTLSFile(set.key)
	if err != nil {
		return nil, view.Errorf(s.Namespace+".tls.key.unreadable", "%v", err).
			WithHint(sf.SettingName("key-file") + " is a path on this machine, read here rather than by the server").
			WithInput(PathInput("key-file", err))
	}
	// Go's words say what is wrong with the pair, and never quote its bytes.
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, view.Errorf(s.Namespace+".tls.pair.invalid", "%s and %s are not a certificate and its key: %v",
			set.cert, set.key, err).WithHint(pair + ", the key unencrypted").WithInput("cert-file")
	}
	return &certificate, nil
}

// verifyChain is the check verify-ca makes in place of Go's: the server's
// chain against roots, the system's when nil, and not its name. In
// VerifyConnection rather than VerifyPeerCertificate, which Go skips for a
// resumed session.
func verifyChain(roots *x509.CertPool) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("tls: the server sent no certificate")
		}
		opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
		for _, c := range cs.PeerCertificates[1:] {
			opts.Intermediates.AddCert(c)
		}
		if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
			return &tls.CertificateVerificationError{UnverifiedCertificates: cs.PeerCertificates, Err: err}
		}
		return nil
	}
}
