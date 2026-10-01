package plugin

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"testing"
)

// A call is named again by where its reader reaches it: the address alone
// with no profile, beside the profile that filled it, and the profile alone
// through a forward the host opened, whose 127.0.0.1 end closed with the
// call. The arguments that reach it again follow the same rule.
func TestACallIsNamedAgainAsItsReaderReachesIt(t *testing.T) {
	address := Arg{Name: "address", Value: "https://vault.lab:8200"}
	for _, c := range []struct {
		name, profile string
		tunnel        Tunnel
		reached       string
		args          []Arg
	}{
		{"no profile", "", TunnelNone, "https://vault.lab:8200", []Arg{address}},
		{"a profile reached directly", "prod", TunnelNone, "https://vault.lab:8200 (profile prod)",
			[]Arg{{Name: "profile", Value: "prod"}, address}},
		{"a profile through a kube: forward", "prod", TunnelKube, "profile prod (through its kube: forward)",
			[]Arg{{Name: "profile", Value: "prod"}}},
		{"a profile through an ssh: forward", "lab", TunnelSSH, "profile lab (through its ssh: forward)",
			[]Arg{{Name: "profile", Value: "lab"}}},
	} {
		req := NewRequest(nil, false, false).WithProfile(c.profile, c.tunnel)
		if got := req.Reached("https://vault.lab:8200"); got != c.reached {
			t.Errorf("%s: Reached = %q, want %q", c.name, got, c.reached)
		}
		if got := req.ReachArgs(address); !slices.Equal(got, c.args) {
			t.Errorf("%s: ReachArgs = %v, want %v", c.name, got, c.args)
		}
	}
}

// A certificate refused for its name is said to be for the names a check
// reads, its DNS names and IP addresses, and not its common name, which Go's
// verifier ignores.
func TestCertNamesAreTheNamesACheckReads(t *testing.T) {
	for _, c := range []struct {
		cert *x509.Certificate
		want string
	}{
		{nil, "another name"},
		{&x509.Certificate{}, "no name a check reads"},
		{&x509.Certificate{DNSNames: []string{"db.internal"}, IPAddresses: []net.IP{net.ParseIP("10.0.0.5")}},
			"db.internal, 10.0.0.5"},
		{&x509.Certificate{DNSNames: []string{"a", "b", "c", "d", "e"}}, "a, b, c and 2 more"},
	} {
		if got := CertNames(c.cert); got != c.want {
			t.Errorf("CertNames = %q, want %q", got, c.want)
		}
	}
}

// Plain HTTP met by a server that speaks only TLS arrives in two shapes,
// and anything else is not it.
func TestTLSExpectedReadsBothShapesAPlainRequestToATLSPortArrivesIn(t *testing.T) {
	alert := fmt.Errorf("Get %q: net/http: HTTP/1.x transport connection broken: malformed HTTP response %s",
		"http://127.0.0.1:6333/collections", strconv.Quote(string([]byte{0x15, 0x03, 0x01, 0x00, 0x02, 0x02, 0x16})))
	listener := errors.New("400 Bad Request: Client sent an HTTP request to an HTTPS server.")
	for _, err := range []error{alert, listener} {
		if !TLSExpected(err) {
			t.Errorf("TLSExpected(%v) = false, want true", err)
		}
	}
	for _, err := range []error{nil, errors.New("malformed HTTP response \"HTTP/2.0\""),
		errors.New("dial tcp 127.0.0.1:6333: connect: connection refused")} {
		if TLSExpected(err) {
			t.Errorf("TLSExpected(%v) = true, want false", err)
		}
	}
}

// A word of a command line rta does not spell reads back as the value,
// whatever it holds: a space, a command substitution, a redirect's angle
// brackets, nothing at all.
func TestAShellWordReadsBackAsItsValue(t *testing.T) {
	for in, want := range map[string]string{
		"app":      "app",
		"app db":   "'app db'",
		"app$(id)": "'app$(id)'",
		"<x>":      "'<x>'",
		"":         "''",
		"it's":     `'it'"'"'s'`,
	} {
		if got := ShellWord(in); got != want {
			t.Errorf("ShellWord(%q) = %s, want %s", in, got, want)
		}
	}
}
