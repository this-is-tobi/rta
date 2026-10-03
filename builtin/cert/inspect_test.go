package cert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// selfIssued builds a certificate from tmpl, signed by its own fresh key, with
// the serial and the validity window a test did not care to choose.
func selfIssued(t *testing.T, tmpl *x509.Certificate) (der []byte, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.SerialNumber == nil {
		tmpl.SerialNumber = big.NewInt(42)
	}
	if tmpl.NotAfter.IsZero() {
		tmpl.NotBefore, tmpl.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour)
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, key
}

// inspectFile writes a certificate built from tmpl to a PEM file, runs
// cert.inspect on it, and returns the rows by key.
func inspectFile(t *testing.T, tmpl *x509.Certificate) map[string]string {
	t.Helper()
	der, _ := selfIssued(t, tmpl)
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := runInspect(context.Background(), req(map[string]any{"target": path}))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, p := range v.(view.KeyValue).Pairs {
		rows[p.Key] = p.Value
	}
	return rows
}

// A certificate lists every name it answers to. Only the DNS names were: one
// made for an address, which is what a service reached by IP, a kubelet or an
// etcd member carries, printed no names at all and read as valid for nothing.
func TestInspectListsEveryNameACertificateAnswersTo(t *testing.T) {
	rows := inspectFile(t, &x509.Certificate{
		Subject:        pkix.Name{CommonName: "svc"},
		DNSNames:       []string{"svc.internal", "*.svc.internal"},
		IPAddresses:    []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("::1")},
		EmailAddresses: []string{"ops@example.org"},
		URIs:           []*url.URL{{Scheme: "spiffe", Host: "example.org", Path: "/ns/prod/sa/api"}},
	})
	for key, want := range map[string]string{
		"dns-names":    "svc.internal, *.svc.internal",
		"ip-addresses": "10.0.0.5, ::1",
		"emails":       "ops@example.org",
		"uris":         "spiffe://example.org/ns/prod/sa/api",
	} {
		if rows[key] != want {
			t.Errorf("%s = %q, want %q", key, rows[key], want)
		}
	}

	rows = inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "bare"}})
	for _, key := range []string{"dns-names", "ip-addresses", "emails", "uris"} {
		if v, ok := rows[key]; ok {
			t.Errorf("a certificate with no %s has a row for them: %q", key, v)
		}
	}
}

// serveCert answers TLS on a loopback port with tmpl's certificate and returns
// the address.
func serveCert(t *testing.T, tmpl *x509.Certificate) string {
	t.Helper()
	der, key := selfIssued(t, tmpl)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				_ = conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// A certificate that is not valid yet fails every client's check as surely as
// an expired one, and read as a sound one for as long as its end date was far
// off: expires-in counted down to an end it was nowhere near, and the table
// said ok.
func TestACertificateNotValidYetIsSaidSo(t *testing.T) {
	future := &x509.Certificate{
		Subject:   pkix.Name{CommonName: "soon"},
		NotBefore: time.Now().Add(48 * time.Hour), NotAfter: time.Now().Add(100 * 24 * time.Hour),
	}
	rows := inspectFile(t, future)
	if got := rows["validity"]; !strings.HasPrefix(got, "not yet valid") || !strings.Contains(got, "begins in 1d") {
		t.Errorf("validity = %q, want it to say the certificate is not valid yet and for how long", got)
	}
	if v, ok := inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "now"}})["validity"]; ok {
		t.Errorf("a certificate valid now has a validity row: %q", v)
	}

	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets": []string{serveCert(t, future), serveCert(t, &x509.Certificate{
			Subject:   pkix.Name{CommonName: "ok"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(200 * 24 * time.Hour),
		})},
		"warn-days": 30, "timeout": 5,
	}))
	if err != nil {
		t.Fatal(err)
	}
	rowsOut := v.(view.Table).Rows
	if got := rowsOut[0][3]; !strings.HasPrefix(got, "INVALID") {
		t.Errorf("status of a certificate not valid yet = %q, want INVALID", got)
	}
	if got := rowsOut[1][3]; got != "ok" {
		t.Errorf("status of a sound certificate = %q, want ok", got)
	}
}

// codeOf is the code of err, or "" for no error.
func codeOf(err error) string {
	if err == nil {
		return ""
	}
	return view.AsError(err, "test").Code
}

// A path that names no file is refused as a path. It was dialled as a host,
// and a mistyped certificate path was answered "dial tcp: lookup certs/tls.crt:
// no such host", which sends the reader to their network. Over MCP the target
// reaches the handler rewritten to an absolute path by the path gate, and the
// agent was handed that path inside a failed DNS lookup of it.
func TestAPathThatNamesNoFileIsNotDialledAsAHost(t *testing.T) {
	for _, target := range []string{"certs/tls.crt", "./nope.pem", "leaf.pem", `C:\certs\a.cer`} {
		_, err := runInspect(context.Background(), req(map[string]any{"target": target, "timeout": 2}))
		if code := codeOf(err); code != "cert.file.notfound" || !strings.Contains(err.Error(), "no certificate file at "+target) {
			t.Errorf("%s: %v, want it said that there is no such file, and not dialled", target, err)
		}
	}

	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets": []string{"/etc/ssl/leaf.pem"}, "warn-days": 30, "timeout": 2,
	}).WithSurface(plugin.SurfaceMCP))
	if err != nil {
		t.Fatal(err)
	}
	if status := v.(view.Table).Rows[0][3]; !strings.Contains(status, "file path") || strings.Contains(status, "lookup") {
		t.Errorf("status = %q, want the file path named as what it is", status)
	}
	// At a terminal expiry reads files too, so the same mistyped path is a
	// missing file there, not a host and not a lookup.
	v, err = runExpiry(context.Background(), req(map[string]any{
		"targets": []string{"/etc/ssl/leaf.pem"}, "warn-days": 30, "timeout": 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if status := v.(view.Table).Rows[0][3]; !strings.Contains(status, "no certificate file at") || strings.Contains(status, "lookup") {
		t.Errorf("status = %q, want it said that there is no such file", status)
	}

	addr, _ := startTLS(t)
	mcp := req(map[string]any{"target": addr, "timeout": 2}).WithSurface(plugin.SurfaceMCP)
	_, err = runInspect(context.Background(), mcp)
	if codeOf(err) != "cert.file.notfound" {
		t.Errorf("over MCP, a target that is no file: %v, want cert.file.notfound and no dial", err)
	}
}

// A certificate file the server may not open is not a missing one: over MCP
// both were "no certificate file at" the path, and the caller went looking for
// a mistake in a path that was right.
func TestOverMCPAnUnreadableCertificateFileIsNotSaidToBeMissing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot search")
	}
	dir := t.TempDir()
	sealed := filepath.Join(dir, "sealed")
	if err := os.Mkdir(sealed, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sealed, "leaf.pem")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealed, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	_, err := runInspect(context.Background(), req(map[string]any{"target": target}).WithSurface(plugin.SurfaceMCP))
	if code := codeOf(err); code != "cert.file.unreadable" {
		t.Errorf("over MCP, a certificate file in a directory that cannot be searched: %v, want cert.file.unreadable", err)
	}
}

// The address of a site, pasted, is the host it names. `cert inspect
// https://host:port` was refused as a missing certificate file for its
// slashes, and `cert expiry` called it "too many colons in address". A URL of
// another scheme is not a TLS host, and is refused as what it is.
func TestAnHTTPSURLIsReadAsTheHostItNames(t *testing.T) {
	addr, _ := startTLS(t)
	for _, target := range []string{"https://" + addr, "https://" + addr + "/", "HTTPS://" + addr + "/a/b?c=d"} {
		v, err := runInspect(context.Background(), req(map[string]any{"target": target, "timeout": 5}))
		if err != nil {
			t.Errorf("inspect %s: %v, want the certificate of %s", target, err, addr)
			continue
		}
		if kv := v.(view.KeyValue); len(kv.Pairs) == 0 {
			t.Errorf("inspect %s answered no rows", target)
		}
		if _, err := runChain(context.Background(), req(map[string]any{"target": target, "timeout": 5})); err != nil {
			t.Errorf("chain %s: %v", target, err)
		}
	}

	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets": []string{"https://" + addr}, "warn-days": 30, "timeout": 5,
	}))
	if err != nil {
		t.Fatal(err)
	}
	row := v.(view.Table).Rows[0]
	if row[0] != "https://"+addr || row[3] != "ok" {
		t.Errorf("expiry row = %q, want the target as typed and the host's status", row)
	}

	for _, target := range []string{"ftp://" + addr, "http://" + addr, "https://", "ssh://"} {
		_, err := runInspect(context.Background(), req(map[string]any{"target": target, "timeout": 2}))
		if codeOf(err) != "cert.target.invalid" {
			t.Errorf("%s: %v, want cert.target.invalid", target, err)
		}
	}
}

// The common name is optional, and a chain drew a certificate without one as a
// branch with no name: an intermediate known by its organisation, a leaf with
// an empty subject and its names in the SAN.
func TestChainNamesACertificateThatHasNoCommonName(t *testing.T) {
	for _, c := range []struct {
		tmpl *x509.Certificate
		want string
	}{
		{&x509.Certificate{Subject: pkix.Name{CommonName: "plain"}}, "plain"},
		{&x509.Certificate{Subject: pkix.Name{Organization: []string{"NoCN Inc"}, OrganizationalUnit: []string{"Unit"}}}, "OU=Unit,O=NoCN Inc"},
		{&x509.Certificate{DNSNames: []string{"only-san.example", "b.example"}}, "only-san.example"},
		{&x509.Certificate{IPAddresses: []net.IP{net.ParseIP("10.0.0.5")}}, "10.0.0.5"},
		{&x509.Certificate{EmailAddresses: []string{"ops@example.org"}}, "ops@example.org"},
		{&x509.Certificate{SerialNumber: big.NewInt(0x1234)}, "serial 1234"},
	} {
		der, _ := selfIssued(t, c.tmpl)
		path := filepath.Join(t.TempDir(), "c.pem")
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		v, err := runChain(context.Background(), req(map[string]any{"target": path}))
		if err != nil {
			t.Fatal(err)
		}
		if got := v.(view.Tree).Roots[0].Label; got != c.want {
			t.Errorf("label = %q, want %q", got, c.want)
		}
	}
	if got := inspectFile(t, &x509.Certificate{DNSNames: []string{"x.example"}})["subject"]; got != "(empty)" {
		t.Errorf("subject of a certificate with none = %q, want (empty)", got)
	}
}

// A DER file is a certificate file: what a Windows export names .cer, what an
// AIA caIssuers URL serves, and what Java and most appliances write. It holds
// no PEM block, so it was "no CERTIFICATE blocks found", the words for a file
// that is not a certificate at all.
func TestADERCertificateIsRead(t *testing.T) {
	a, _ := selfIssued(t, &x509.Certificate{Subject: pkix.Name{CommonName: "first"}})
	b, _ := selfIssued(t, &x509.Certificate{Subject: pkix.Name{CommonName: "second"}})
	dir := t.TempDir()
	one, two := filepath.Join(dir, "one.cer"), filepath.Join(dir, "two.cer")
	if err := os.WriteFile(one, a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two, append(append([]byte{}, a...), b...), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := runInspect(context.Background(), req(map[string]any{"target": one}))
	if err != nil {
		t.Fatalf("a DER certificate: %v", err)
	}
	if got := v.(view.KeyValue).Pairs[0].Value; got != "CN=first" {
		t.Errorf("subject = %q, want CN=first", got)
	}
	chain, err := runChain(context.Background(), req(map[string]any{"target": two}))
	if err != nil {
		t.Fatal(err)
	}
	if root := chain.(view.Tree).Roots[0]; root.Label != "first" || len(root.Children) != 1 || root.Children[0].Label != "second" {
		t.Errorf("concatenated DER read as %+v, want a chain of first then second", root)
	}

	junk := filepath.Join(dir, "junk.cer")
	if err := os.WriteFile(junk, []byte{0x30, 0x03, 0x02, 0x01, 0x01}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = runInspect(context.Background(), req(map[string]any{"target": junk}))
	if verr := view.AsError(err, "test"); err == nil || verr.Code != "cert.file.empty" || !strings.Contains(verr.Hint, "DER") {
		t.Errorf("DER that is no certificate: %v, want cert.file.empty saying what is read", err)
	}
}

// The key is named by type and size. The signature algorithm beside it is the
// issuer's, so a certificate on a 1024-bit RSA key read as sound.
func TestInspectNamesTheKeyByTypeAndSize(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key  crypto.Signer
		want string
	}{{rsaKey, "RSA 2048"}, {edKey, "Ed25519"}, {p384, "ECDSA P-384"}} {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "k"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, c.key.Public(), c.key)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		if got := publicKeyOf(parsed); got != c.want {
			t.Errorf("publicKeyOf = %q, want %q", got, c.want)
		}
	}
	if got := inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "k"}})["public-key"]; got != "ECDSA P-256" {
		t.Errorf("public-key row = %q, want ECDSA P-256", got)
	}
}

// The serial is hexadecimal, a whole number of bytes: the spelling `openssl x509
// -serial`, a CRL, a CT log and a browser use. A 160-bit serial in decimal is
// 48 digits that match none of them.
func TestInspectSpellsTheSerialInHex(t *testing.T) {
	serial, _ := new(big.Int).SetString("637cb9bee3d623e2cfad17ec4e0680e8282c0c", 16)
	for _, c := range []struct {
		serial *big.Int
		want   string
	}{
		{serial, "637cb9bee3d623e2cfad17ec4e0680e8282c0c"},
		{big.NewInt(255), "ff"},
		{big.NewInt(15), "0f"},
		{big.NewInt(0x1234), "1234"},
	} {
		rows := inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "s"}, SerialNumber: c.serial})
		if rows["serial"] != c.want {
			t.Errorf("serial %v = %q, want %q", c.serial, rows["serial"], c.want)
		}
	}
}

// What a certificate is for is listed: whether it is a CA and how far below it
// may go, and the uses its key usage and extended key usage allow. A client
// that answers "unsupported certificate purpose" is refusing a leaf with no
// server auth, and nothing in the output said which uses it had.
func TestInspectSaysWhatACertificateIsFor(t *testing.T) {
	for _, c := range []struct {
		name string
		tmpl x509.Certificate
		want map[string]string
	}{
		{"a server leaf", x509.Certificate{
			KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		}, map[string]string{
			"key-usage": "digital signature, key encipherment", "ext-key-usage": "server auth, client auth",
		}},
		{"an issuing CA with a path length of zero", x509.Certificate{
			IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		}, map[string]string{
			"ca": "yes, path length 0: no intermediate below it", "key-usage": "certificate signing, CRL signing",
		}},
		{"a root with no path length", x509.Certificate{
			IsCA: true, BasicConstraintsValid: true, MaxPathLen: -1,
		}, map[string]string{"ca": "yes"}},
		{"a CA with a path length of two", x509.Certificate{
			IsCA: true, BasicConstraintsValid: true, MaxPathLen: 2,
		}, map[string]string{"ca": "yes, path length 2"}},
		{"a vendor use no table names", x509.Certificate{
			UnknownExtKeyUsage: []asn1.ObjectIdentifier{{1, 3, 6, 1, 4, 1, 311, 10, 3, 4}},
			ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		}, map[string]string{"ext-key-usage": "code signing, 1.3.6.1.4.1.311.10.3.4"}},
		{"a certificate that sets none", x509.Certificate{}, map[string]string{}},
	} {
		tmpl := c.tmpl
		tmpl.Subject = pkix.Name{CommonName: "u"}
		rows := inspectFile(t, &tmpl)
		for _, key := range []string{"ca", "key-usage", "ext-key-usage"} {
			if rows[key] != c.want[key] {
				t.Errorf("%s: %s = %q, want %q", c.name, key, rows[key], c.want[key])
			}
		}
	}
}
