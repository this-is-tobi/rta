package plugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"
)

// dialErr is a failed dial as net.Dial returns it on this machine: the
// operating system's error inside the *os.SyscallError and *net.OpError
// every dial wraps it in.
func dialErr(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(10, 9, 8, 7), Port: 5432},
		Err: os.NewSyscallError("connect", errno)}
}

// A refused port is a refusal, and a dial that found no way to the host is
// not one: a *net.OpError is every failed dial, and read as "nothing is
// listening" a server behind a VPN that was down was told to check a port
// no packet reached. A name that did not resolve, a reset and a timeout are
// neither of the two.
func TestADialIsReadByTheErrorTheSystemGaveIt(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := closed.Addr().String()
	_ = closed.Close()
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, refused := d.DialContext(context.Background(), "tcp", addr)
	if refused == nil {
		_ = conn.Close()
		t.Skip("something is listening on the port just closed")
	}
	if !DialRefused(refused) || DialUnroutable(refused) {
		t.Errorf("a dial to a closed port (%v): refused %v, unroutable %v, want true and false",
			refused, DialRefused(refused), DialUnroutable(refused))
	}

	for _, errno := range unroutableErrnos {
		got := dialErr(errno)
		if !DialUnroutable(got) || DialRefused(got) {
			t.Errorf("%v: unroutable %v, refused %v, want true and false", got, DialUnroutable(got), DialRefused(got))
		}
	}
	for _, other := range []error{
		&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "db.internal", IsNotFound: true}},
		dialErr(syscall.ECONNRESET),
		dialErr(syscall.ETIMEDOUT),
		context.DeadlineExceeded,
		nil,
	} {
		if DialRefused(other) || DialUnroutable(other) {
			t.Errorf("%v: refused %v, unroutable %v, want neither", other, DialRefused(other), DialUnroutable(other))
		}
	}
}

// A driver that flattens a dial's error into its own message — gRPC's
// status carries the text alone — is read by the words that error has on
// this machine. A chain that names an error of its own is read by it, and
// not by whatever words sit beside it.
func TestAFlattenedDialIsReadByItsWords(t *testing.T) {
	flatten := func(err error) error {
		return fmt.Errorf("rpc error: code = Unavailable desc = connection error: desc = %q",
			"transport: Error while dialing: "+err.Error())
	}
	if got := flatten(dialErr(refusedErrnos[0])); !DialRefused(got) || DialUnroutable(got) {
		t.Errorf("%v: refused %v, unroutable %v", got, DialRefused(got), DialUnroutable(got))
	}
	if got := flatten(dialErr(unroutableErrnos[0])); !DialUnroutable(got) || DialRefused(got) {
		t.Errorf("%v: unroutable %v, refused %v", got, DialUnroutable(got), DialRefused(got))
	}
	named := fmt.Errorf("the server said %s: %w", refusedErrnos[0].Error(), os.NewSyscallError("read", syscall.ECONNRESET))
	if DialRefused(named) {
		t.Errorf("%v is read as refused by its words, over the error it names", named)
	}
}

// A host reached on one of its addresses was reached: a driver that dials
// each joins their answers, and an IPv6 address with no route beside an
// IPv4 one that refused is a port nothing listens on. And a name that did
// not resolve is neither, though the resolver kept the words of its own
// failed exchange with the DNS server.
func TestADialIsReadWhoeverElseAnsweredBesideIt(t *testing.T) {
	joined := errors.Join(dialErr(unroutableErrnos[0]), dialErr(refusedErrnos[0]))
	if !DialRefused(joined) || DialUnroutable(joined) {
		t.Errorf("%v: refused %v, unroutable %v, want true and false", joined, DialRefused(joined), DialUnroutable(joined))
	}
	for _, errno := range append(slices.Clone(unroutableErrnos), refusedErrnos...) {
		exchange := &net.OpError{Op: "read", Net: "udp", Err: os.NewSyscallError("read", errno)}
		dns := &net.OpError{Op: "dial", Net: "tcp",
			Err: &net.DNSError{Err: exchange.Error(), Name: "db.internal", Server: "10.9.8.53:53"}}
		if DialRefused(dns) || DialUnroutable(dns) {
			t.Errorf("%v: refused %v, unroutable %v, want neither", dns, DialRefused(dns), DialUnroutable(dns))
		}
	}
}

// issue is a certificate for db.internal and 127.0.0.1, valid from an hour
// ago for valid, signed by parent's key — or by its own when parent is nil,
// and then a CA of its own, as a self-signed server certificate is.
func issue(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, valid time.Duration, ca bool) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "db.internal"},
		DNSNames: []string{"db.internal"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(valid),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
		IsCA: ca || parent == nil, BasicConstraintsValid: true}
	if tmpl.IsCA {
		tmpl.KeyUsage |= x509.KeyUsageCertSign
	}
	signer := key
	if parent == nil {
		parent = tmpl
	} else {
		signer = parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// handshake is the error a client given cfg gets from a server presenting
// cert, signed by key.
func handshake(t *testing.T, cert *x509.Certificate, key *ecdsa.PrivateKey, cfg *tls.Config) error {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		if c, err := ln.Accept(); err == nil {
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	conn, err := tls.Dial("tcp", ln.Addr().String(), cfg)
	if err == nil {
		_ = conn.Close()
	}
	return err
}

// A certificate nothing here vouches for is untrusted, as whichever verifier
// ran says so. With no CA given, macOS asks its own verifier, which answered
// a leaf from a private CA sent without its CA "certificate is not trusted",
// untyped — read as a generic failure while Go's verifier elsewhere says
// unknown authority, and the CA cures it. A self-signed certificate valid
// for years the system answers "not standards compliant", which is no
// untrusted issuer's verdict alone, and it is left to the system's words.
func TestACertificateNothingVouchesForIsUntrustedOnEverySystem(t *testing.T) {
	ca, caKey := issue(t, nil, nil, 10*365*24*time.Hour, true)
	leaf, leafKey := issue(t, ca, caKey, 90*24*time.Hour, false)
	longSelf, longKey := issue(t, nil, nil, 10*365*24*time.Hour, false)
	shortSelf, shortKey := issue(t, nil, nil, 90*24*time.Hour, false)
	system := &tls.Config{ServerName: "db.internal"}
	for _, c := range []struct {
		what string
		cert *x509.Certificate
		key  *ecdsa.PrivateKey
	}{
		{"a leaf from a private CA", leaf, leafKey},
		{"a self-signed certificate valid for ninety days", shortSelf, shortKey},
	} {
		err := handshake(t, c.cert, c.key, system)
		if err == nil || !CertUntrusted(err) {
			t.Errorf("%s, against the system's roots (%v), is not read as untrusted", c.what, err)
		}
	}
	err := handshake(t, longSelf, longKey, system)
	if darwin := runtime.GOOS == "darwin" || runtime.GOOS == "ios"; err == nil || CertUntrusted(err) == darwin {
		t.Errorf("a self-signed certificate valid for ten years, against the system's roots (%v), is read as untrusted: %v",
			err, CertUntrusted(err))
	}

	// Given the CA, Go's verifier runs, and a host the certificate is not
	// for is that reason and not an untrusted one.
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	err = handshake(t, leaf, leafKey, &tls.Config{RootCAs: pool, ServerName: "other.internal"})
	var host x509.HostnameError
	if !errors.As(err, &host) || CertUntrusted(err) {
		t.Errorf("a certificate for another host (%v) is read as untrusted", err)
	}
}

// Of the verdicts macOS's verifier answers untyped, only the one that says
// no anchor vouches for the chain is read as untrusted. Every other is a
// reason a CA file would go around rather than cure — naming one runs Go's
// verifier, which checks no revocation — so a revoked certificate read as
// untrusted handed its reader the one setting that let them connect to it.
// Each shape here is the one Go's darwin verifier builds: "x509: " and the
// system's description, the certificate's name in curly quotes.
func TestOnlyTheSystemsNotTrustedVerdictIsReadAsUntrusted(t *testing.T) {
	open, closing := string(rune(0x201c)), string(rune(0x201d))
	system := func(name, verdict string) error {
		return &tls.CertificateVerificationError{Err: fmt.Errorf("x509: %s", open+name+closing+" "+verdict)}
	}
	notTrusted := system("db.internal", "certificate is not trusted")
	for _, c := range []struct {
		goos string
		err  error
		want bool
	}{
		{"darwin", notTrusted, true},
		{"ios", notTrusted, true},
		{"linux", notTrusted, false},
		{"windows", notTrusted, false},
		{"darwin", fmt.Errorf("pg: %w", notTrusted), true},
		{"darwin", system("db.internal", "certificate is revoked"), false},
		{"darwin", system("db.internal", "certificate is not standards compliant"), false},
		{"darwin", system("db.internal", "certificate is blocked"), false},
		{"darwin", system("db.internal", "certificate is using a broken signature algorithm"), false},
		{"darwin", system("db.internal", "certificate is not permitted for this usage"), false},
		{"darwin", &tls.CertificateVerificationError{Err: fmt.Errorf("x509: Unknown trust error for %s certificate",
			open+"db.internal"+closing)}, false},
		{"darwin", &tls.CertificateVerificationError{Err: fmt.Errorf("x509: User or administrator set %s certificate as distrusted",
			open+"db.internal"+closing)}, false},
		// A name is the server's to choose, and cannot make a verdict read as
		// another: the words after it are the system's.
		{"darwin", system("x"+closing+" certificate is not trusted", "certificate is revoked"), false},
		// A verdict in another language is left to the system's words.
		{"darwin", &tls.CertificateVerificationError{Err: fmt.Errorf("x509: Zertifikat %s wird nicht vertraut",
			string(rune(0x201e))+"db.internal"+open)}, false},
		// Go's own words for a reason it types, and a verdict flattened out
		// of the handshake's error, are not the system's verdict.
		{"darwin", &tls.CertificateVerificationError{Err: x509.CertificateInvalidError{Reason: x509.Expired,
			Detail: open + "db.internal" + closing + " certificate is expired"}}, false},
		{"darwin", errors.New("x509: " + open + "db.internal" + closing + " certificate is not trusted"), false},
		{"linux", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, true},
		{"darwin", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, true},
		{"linux", x509.SystemRootsError{}, true},
		{"darwin", &tls.CertificateVerificationError{Err: x509.CertificateInvalidError{Reason: x509.Expired}}, false},
		{"darwin", &tls.CertificateVerificationError{Err: x509.InsecureAlgorithmError(x509.SHA1WithRSA)}, false},
		{"darwin", &tls.CertificateVerificationError{Err: x509.HostnameError{Host: "db"}}, false},
		{"darwin", &tls.CertificateVerificationError{}, false},
		{"darwin", errors.New("tls: handshake failure"), false},
		{"darwin", nil, false},
	} {
		if got := certUntrusted(c.goos, c.err); got != c.want {
			t.Errorf("on %s, %v: untrusted %v, want %v", c.goos, c.err, got, c.want)
		}
	}
}
