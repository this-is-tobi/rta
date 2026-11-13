package plugin

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Service declares, once, the connection a plugin opens to the one service it
// fronts: the settings that say where a call goes and how its server is
// checked, and what a handler reads back from them — Target for where, TLS for
// how, HTTPClient for an HTTP API's client, and Refusal for the words of a
// connection that failed.
//
// **Every connection plugin wrote these for itself, and the copies drifted.**
// Eight read a CA file for themselves. Six decided whether the settings
// asked for TLS, and one of them forgot key-file, so a client key given alone
// left the call, password and all, in the clear. Two HTTP clients dropped the
// operator's proxy the moment a CA was named, and three plugins refused every
// redirect, under three codes. A plugin builds one Service, puts Fields in
// each capability's Inputs beside the credential and the inputs that are the
// product's own, and calls the methods from its handlers. Nothing here is a
// first-party privilege: a third-party plugin imports the same package.
type Service struct {
	// Namespace prefixes every code a refusal carries: "vault" gives
	// vault.tls.ca.unreadable.
	Namespace string
	// Product names the service in a sentence: "Vault", "Redis".
	Product string
	// Reach is the setting that says where a call goes, by the role a forward
	// fills it in: EndpointURL declares `url`, EndpointAddress `address`, and
	// EndpointHost `host` beside `port`.
	Reach EndpointRole
	// Default is where a call goes when nothing says, in Reach's form: a URL,
	// host[:port], or a host.
	Default string
	// Port is the service's own port: what an address naming none is given,
	// `port`'s default, and what a refusal of a port nothing listens on names.
	// A URL's scheme says its own, so a URL service may leave it 0.
	Port int
	// Switch is how the settings switch transport security: TLSByScheme for
	// an HTTP API, whose URL's scheme is the switch; TLSOnOff; or TLSModes.
	// Not a field called TLS, which is the method a handler reads the plan by.
	Switch TLSSwitch
	// ClientCert offers cert-file and key-file, for a service that
	// authenticates a client by its certificate.
	ClientCert bool
	// Redirects is an HTTP API's redirect policy; the zero value is
	// RedirectSameOrigin.
	Redirects RedirectPolicy
	// RedirectHint, when set, is said beside a refused redirect to another
	// address: what in this product sends one, and what to point url at
	// instead.
	RedirectHint string
	// ResponseTimeout is how long HTTPClient's requests wait for a response's
	// headers, 30 seconds when zero. An API that answers one request only once
	// long work is done — a snapshot made before a byte of it is sent — sets
	// it to what that work takes; a request's context still ends it sooner.
	ResponseTimeout time.Duration
}

// TLSSwitch is how a Service's settings switch transport security.
type TLSSwitch int

const (
	// TLSByScheme: no switch of its own; https:// in `url` is TLS, checked as
	// verify-full. A CA, a server name or a client certificate beside http://
	// is refused (<ns>.tls.ca.unused and its siblings), never ignored: it is
	// the setting an operator wrote for the https:// address, still there the
	// day url is pointed at a plain one.
	TLSByScheme TLSSwitch = iota
	// TLSOnOff: `tls`, a Bool, off by default. A CA, a server name or either
	// half of a client pair turns it on, at verify-full: each means something
	// only over TLS, and the alternative is a value that does nothing in
	// silence until --tls is typed beside it.
	TLSOnOff
	// TLSModes: `tls`, one of the five modes, for a database that offers TLS
	// on its plain port. A file or a name beside a mode that does not read it
	// is refused. Through a forward, where the host turns the mode off, any of
	// them turns it back on, at verify-full.
	TLSModes
)

// The five modes TLSModes offers, as libpq spells them and MySQL's
// --ssl-mode orders them.
const (
	TLSDisable    = "disable"
	TLSPrefer     = "prefer"
	TLSRequire    = "require"
	TLSVerifyCA   = "verify-ca"
	TLSVerifyFull = "verify-full"
)

var tlsModes = []string{TLSDisable, TLSPrefer, TLSRequire, TLSVerifyCA, TLSVerifyFull}

// Fields are the Service's settings: the endpoint, then tls, ca-file,
// tls-server-name, and cert-file and key-file, as Switch and ClientCert say.
// Append the credential and the inputs that are the product's own. A plugin
// may add a clause to a returned Help for what its protocol adds, and changes
// nothing else: the names, the keys and the roles are what the host, a
// profile and every other plugin read the same way.
//
// **Every one is Local, keyed by its own name.** Where a call goes and how
// its server is checked are the operator's to say: a caller that could choose
// them would choose where the credential goes and which certificate is good
// enough, and a path read off this machine's disk is a file-read primitive
// the moment a caller can name it.
//
// It panics for a Service that cannot exist — a URL with a TLS switch of its
// own, an address with no port to give one that names none — which is a
// declaration's mistake, met the first time the plugin starts.
func (s Service) Fields() []Field {
	if problem := s.problem(); problem != "" {
		panic("plugin: Service " + strconv.Quote(s.Namespace) + " " + problem)
	}
	var fields []Field
	switch s.Reach {
	case EndpointURL:
		fields = append(fields, Field{Name: "url", Type: String, Default: s.Default, Config: "url", Local: true,
			Endpoint: EndpointURL, Group: "network", Help: s.Product + " URL, https:// for TLS"})
	case EndpointAddress:
		fields = append(fields, Field{Name: "address", Type: String, Default: s.Default, Config: "address",
			Local: true, Endpoint: EndpointAddress, Group: "network",
			Help: fmt.Sprintf("%s address, host[:port] — port %d when it names none", s.Product, s.Port)})
	default:
		fields = append(fields,
			Field{Name: "host", Type: String, Default: s.Default, Config: "host", Local: true,
				Endpoint: EndpointHost, Group: "network", Help: s.Product + " host"},
			Field{Name: "port", Type: Int, Default: s.Port, Min: 1, Max: 65535, Config: "port", Local: true,
				Endpoint: EndpointPort, Group: "network", Help: s.Product + " port"})
	}
	// What turns TLS on, or which mode reads the setting, said where it is
	// set: the same file means three things under the three switches.
	var caClause, nameClause, certClause string
	switch s.Switch {
	case TLSByScheme:
		caClause, nameClause, certClause = " — an https:// URL only", " — an https:// URL only", " — an https:// URL only"
	case TLSOnOff:
		on := "connect over TLS — also on when ca-file, tls-server-name"
		if s.ClientCert {
			on += ", cert-file or key-file"
		}
		fields = append(fields, Field{Name: "tls", Type: Bool, Default: false, Config: "tls", Local: true,
			Endpoint: EndpointTLS, Group: "tls", Help: on + " is set"})
		caClause, nameClause, certClause = " — turns TLS on", " — turns TLS on", " — turns TLS on"
	case TLSModes:
		fields = append(fields, Field{Name: "tls", Type: String, Default: TLSPrefer, Options: slices.Clone(tlsModes),
			Config: "tls", Local: true, Endpoint: EndpointTLS, Group: "tls",
			Help: "TLS mode: disable, prefer (TLS when the server offers it, unverified), require (TLS, unverified), " +
				"verify-ca (the chain checked) or verify-full (the chain and the name checked)"})
		forward := "; through a kube: or ssh: forward it turns TLS on, at verify-full"
		caClause = " — read by verify-ca and verify-full" + forward
		nameClause = " — read by verify-full" + forward
		certClause = " — sent under any mode but disable" + forward
	}
	fields = append(fields,
		Field{Name: "ca-file", Type: String, Default: "", Config: "ca-file", Local: true, Group: "tls",
			File: FileCertificates, Suggest: SuggestPath("ca-file"),
			Help: "PEM bundle of the CA the server's certificate is checked against, in place of this machine's own " +
				"trust store" + caClause},
		Field{Name: "tls-server-name", Type: String, Default: "", Config: "tls-server-name", Local: true, Group: "tls",
			Help: "name the server's certificate is checked for, in place of the host the call reaches" + nameClause})
	if s.ClientCert {
		fields = append(fields,
			Field{Name: "cert-file", Type: String, Default: "", Config: "cert-file", Local: true, Group: "tls",
				Suggest: SuggestPath("cert-file"),
				Help:    "client certificate, PEM, for a server that asks for one" + certClause},
			Field{Name: "key-file", Type: String, Default: "", Config: "key-file", Local: true, Group: "tls",
				Suggest: SuggestPath("key-file"), Help: "private key for cert-file, PEM, unencrypted"})
	}
	return fields
}

// problem is what makes s a Service that cannot exist, or "".
func (s Service) problem() string {
	switch {
	case s.Namespace == "" || s.Product == "":
		return "names no Namespace or no Product, and every refusal is worded with both"
	case s.Reach != EndpointURL && s.Reach != EndpointAddress && s.Reach != EndpointHost:
		return fmt.Sprintf("has Reach %q: a Service is reached by a URL, an address, or a host and its port", s.Reach)
	case s.Switch < TLSByScheme || s.Switch > TLSModes:
		return fmt.Sprintf("has Switch %d, which is no TLSSwitch", s.Switch)
	case s.Reach == EndpointURL && s.Switch != TLSByScheme:
		return "is reached by URL, whose scheme is its TLS switch: a second switch beside it would one day say the opposite"
	case s.Reach != EndpointURL && s.Switch == TLSByScheme:
		return "switches TLS by a URL's scheme and is reached by no URL"
	case s.Reach != EndpointURL && (s.Port < 1 || s.Port > 65535):
		return fmt.Sprintf("has Port %d: an address or a host is given the service's own port, 1 to 65535", s.Port)
	}
	return ""
}

// Target is where the call goes, read from its settings and checked before
// anything is dialled. For EndpointURL an http:// or https:// URL with a host
// and neither a user nor a password, its path's trailing slash cut so a path
// joins it whole; otherwise a URL holding only Host, host:port, with Port
// given to an address that names none. A setting that is none of those is
// refused as <ns>.<setting>.invalid, named the way the reader changes it.
func (s Service) Target(req Request) (*url.URL, *view.Error) {
	switch s.Reach {
	case EndpointURL:
		return s.targetURL(req)
	case EndpointAddress:
		return s.targetAddress(req)
	}
	host := strings.TrimSpace(req.String("host"))
	if host == "" {
		return nil, view.Errorf(s.Namespace+".host.invalid", "no %s host named", s.Product).
			WithHint(req.Surface().SettingName("host") + " names the server").WithInput("host")
	}
	return &url.URL{Host: net.JoinHostPort(host, strconv.Itoa(s.port(req)))}, nil
}

func (s Service) targetURL(req Request) (*url.URL, *view.Error) {
	raw := strings.TrimSpace(req.String("url"))
	sf := req.Surface()
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "":
		return nil, view.Errorf(s.Namespace+".url.invalid", "%q is not a %s URL", raw, s.Product).
			WithHint(sf.SettingName("url") + " is http:// or https://, a host, and its port when it is not the " +
				"scheme's" + s.byDefault()).WithInput("url")
	// A user or a password in the URL is a credential in the one setting every
	// refusal, receipt and record names: the credential has its own input,
	// which nothing prints.
	case u.User != nil:
		return nil, view.Errorf(s.Namespace+".url.invalid", "%s holds a user or a password", u.Redacted()).
			WithHint(sf.SettingName("url") + " names where the call goes, and every refusal and record repeats it: " +
				"the credential is a setting of its own").WithInput("url")
	case u.RawQuery != "" || u.Fragment != "":
		return nil, view.Errorf(s.Namespace+".url.invalid", "%q carries a query or a fragment", raw).
			WithHint(sf.SettingName("url") + " is the base every request's path is joined to, which ends before ? and #").
			WithInput("url")
	}
	u.Path, u.RawPath = strings.TrimRight(u.Path, "/"), strings.TrimRight(u.RawPath, "/")
	return u, nil
}

func (s Service) targetAddress(req Request) (*url.URL, *view.Error) {
	raw := strings.TrimSpace(req.String("address"))
	invalid := func(why string) *view.Error {
		return view.Errorf(s.Namespace+".address.invalid", "%q is not a %s address: %s", raw, s.Product, why).
			WithHint(req.Surface().SettingName("address") + " is host[:port]" + s.byDefault()).
			WithInput("address")
	}
	if strings.Contains(raw, "://") {
		return nil, invalid("an address names no scheme, and TLS is the tls setting's to switch")
	}
	host, p, err := net.SplitHostPort(raw)
	if err != nil {
		// No port, or an IPv6 address without one, bracketed or bare.
		host, p = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), strconv.Itoa(s.Port)
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return nil, invalid("it is neither a host nor a host and a port")
		}
	}
	n, err := strconv.Atoi(p)
	switch {
	case host == "":
		return nil, invalid("it names no host")
	case err != nil || n < 1 || n > 65535:
		return nil, invalid("its port is not a number from 1 to 65535")
	}
	return &url.URL{Host: net.JoinHostPort(host, p)}, nil
}

// byDefault is the clause naming where a call goes when nothing says, or "".
func (s Service) byDefault() string {
	if s.Default == "" {
		return ""
	}
	return ", " + s.Default + " by default"
}

// port is the port a host and port service's call goes to.
func (s Service) port(req Request) int {
	if n := req.Int("port"); n != 0 {
		return n
	}
	return s.Port
}

// setting is the input that says where a call goes.
func (s Service) setting() string {
	switch s.Reach {
	case EndpointURL:
		return "url"
	case EndpointAddress:
		return "address"
	}
	return "host"
}

// endpoint is where a call goes as its settings say it, for a sentence.
func (s Service) endpoint(req Request) string {
	if s.Reach == EndpointHost {
		return net.JoinHostPort(strings.TrimSpace(req.String("host")), strconv.Itoa(s.port(req)))
	}
	return strings.TrimSpace(req.String(s.setting()))
}
