package tlstrust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// isolated gives the test a data directory of its own and a clock it reads.
func isolated(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	at := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	previous := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = previous })
}

// chain is one self-signed certificate, DER, as a server presents it.
func chain(t *testing.T, name string) [][]byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{der}
}

func codeOf(verr *view.Error) string {
	if verr == nil {
		return ""
	}
	return verr.Code
}

// The server a call reaches is named the same however its inputs spell it:
// the forward's coordinate when there is one, host:port otherwise, the port a
// URL's scheme implies filled in and the host in one case.
func TestAServerIsNamedOneWayHoweverItIsWritten(t *testing.T) {
	url := []plugin.Field{{Name: "url", Endpoint: plugin.EndpointURL}}
	address := []plugin.Field{{Name: "address", Endpoint: plugin.EndpointAddress}}
	hostPort := []plugin.Field{{Name: "host", Endpoint: plugin.EndpointHost}, {Name: "port", Endpoint: plugin.EndpointPort}}
	asked := func(values map[string]any) plugin.Request { return plugin.NewRequest(values, false, false) }
	for _, tc := range []struct {
		what   string
		inputs []plugin.Field
		req    plugin.Request
		want   string
	}{
		{"an https URL", url, asked(map[string]any{"url": "https://Vault.Internal./v1"}), "vault.internal:443"},
		{"a URL with its port", url, asked(map[string]any{"url": "https://vault.internal:8200"}), "vault.internal:8200"},
		{"a plain URL", url, asked(map[string]any{"url": "http://vault.internal"}), "vault.internal:80"},
		{"a URL of no scheme rta knows", url, asked(map[string]any{"url": "ftp://vault.internal"}), ""},
		{"an address", address, asked(map[string]any{"address": "Cache.Internal:6380"}), "cache.internal:6380"},
		{"an address with no port", address, asked(map[string]any{"address": "cache.internal"}), ""},
		{"an IPv6 address", address, asked(map[string]any{"address": "[fd00::5]:6380"}), "[fd00::5]:6380"},
		{"a host and a port", hostPort, asked(map[string]any{"host": "db.internal", "port": 5432}), "db.internal:5432"},
		{"a host with no port", hostPort, asked(map[string]any{"host": "db.internal"}), ""},
		{"no endpoint declared", nil, asked(map[string]any{"url": "https://vault.internal"}), ""},
		{"through a forward", url, plugin.ResolveRequest(plugin.Capability{Inputs: url}, plugin.Inputs{
			Caller: map[string]any{"url": "https://127.0.0.1:5000"}, ProfileName: "lab", Tunnel: plugin.TunnelKube,
			Coordinate: "kube:lab/vault/svc/vault:8200"}, false, false), "kube:lab/vault/svc/vault:8200"},
	} {
		if got := ServerOf(tc.inputs, tc.req); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.what, got, tc.want)
		}
	}
}

// The first certificate a server presents waits for the operator, once: the
// same one presented again is the same pending one, first seen when it was
// first seen, and the store is not written for it.
func TestAFirstCertificateWaitsForTheOperator(t *testing.T) {
	isolated(t)
	presented := chain(t, "vault.internal")
	outcome, rec, verr := Record("vault", "vault.internal:8200", "prod", presented, false)
	if verr != nil || outcome != Waiting || rec.Trusted != nil || rec.Pending == nil || rec.Pending.Profile != "prod" {
		t.Fatalf("a first certificate: %v, %+v, %v", outcome, rec, verr)
	}
	if trusted, verr := Trusted("vault", "vault.internal:8200"); trusted != nil || verr != nil {
		t.Errorf("a pending certificate was handed out as trusted: %q, %v", trusted, verr)
	}
	before, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	now = func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }
	outcome, rec, verr = Record("vault", "vault.internal:8200", "prod", presented, false)
	after, _ := os.Stat(Path())
	if verr != nil || outcome != Waiting || rec.Pending.At.Day() != 13 || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the same certificate again: %v, %+v, %v, store rewritten %v", outcome, rec, verr,
			!after.ModTime().Equal(before.ModTime()))
	}
}

// Through a forward the first certificate is trusted as it arrives; any other
// after it waits, forward or not, and the one trusted is not replaced until
// the operator says so for the very one they were shown.
func TestAForwardTrustsTheFirstAndNothingAfterIt(t *testing.T) {
	isolated(t)
	first, second, third := chain(t, "vault"), chain(t, "vault"), chain(t, "vault")
	server := "kube:lab/vault/svc/vault:8200"
	outcome, rec, verr := Record("vault", server, "lab", first, true)
	if verr != nil || outcome != TrustedNow || rec.Trusted == nil || rec.Trusted.How != ByForward || rec.Pending != nil {
		t.Fatalf("a first certificate through a forward: %v, %+v, %v", outcome, rec, verr)
	}
	if outcome, _, _ := Record("vault", server, "lab", first, true); outcome != Known {
		t.Errorf("the trusted certificate reported again: %v", outcome)
	}
	outcome, rec, verr = Record("vault", server, "lab", second, true)
	if verr != nil || outcome != Waiting || Fingerprint(rec.Trusted.Chain) != Fingerprint(first) ||
		Fingerprint(rec.Pending.Chain) != Fingerprint(second) {
		t.Fatalf("another certificate through the forward: %v, %+v, %v", outcome, rec, verr)
	}
	// The server presents a third before the operator answers about the second.
	if _, _, verr := Record("vault", server, "lab", third, true); verr != nil {
		t.Fatal(verr)
	}
	if _, verr := Trust("vault", server, Fingerprint(second)); codeOf(verr) != "core.trust.moved" {
		t.Errorf("trusting the certificate shown after the server presented another: %+v", verr)
	}
	rec, verr = Trust("vault", server, Fingerprint(third))
	if verr != nil || rec.Trusted.How != ByOperator || Fingerprint(rec.Trusted.Chain) != Fingerprint(third) || rec.Pending != nil {
		t.Errorf("trusting the one shown: %+v, %v", rec, verr)
	}
	if trusted, _ := Trusted("vault", server); Fingerprint(trusted) != Fingerprint(third) {
		t.Errorf("handed out %q after the operator trusted the third", Fingerprint(trusted))
	}
	if _, verr := Trust("vault", server, Fingerprint(third)); codeOf(verr) != "core.trust.none" {
		t.Errorf("trusting with nothing waiting: %+v", verr)
	}
}

// What one plugin reported of a server is that plugin's: another reaching the
// same server is handed nothing of it.
func TestWhatOnePluginTrustedIsItsOwn(t *testing.T) {
	isolated(t)
	if _, _, verr := Record("vault", "kube:lab/vault/svc/vault:8200", "lab", chain(t, "vault"), true); verr != nil {
		t.Fatal(verr)
	}
	if trusted, verr := Trusted("keycloak", "kube:lab/vault/svc/vault:8200"); trusted != nil || verr != nil {
		t.Errorf("another plugin was handed %q, %v", trusted, verr)
	}
}

// A chain that is no certificate is recorded nowhere, whatever a plugin says
// a server presented.
func TestAChainThatIsNoCertificateIsRecordedNowhere(t *testing.T) {
	isolated(t)
	for _, bogus := range [][][]byte{nil, {}, {[]byte("not a certificate")}, {make([]byte, maxCert+1)}} {
		if outcome, _, verr := Record("vault", "vault.internal:8200", "", bogus, true); outcome != Known || verr != nil {
			t.Errorf("%d bytes: %v, %v", len(bogus), outcome, verr)
		}
	}
	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Errorf("a store was written for no certificate: %v", err)
	}
}

// A store full of servers keeps no more and says how one is taken back.
func TestAFullStoreKeepsNoMore(t *testing.T) {
	isolated(t)
	presented := chain(t, "db")
	for i := range maxServers {
		if _, _, verr := Record("pg", "db"+strconv.Itoa(i)+":5432", "", presented, false); verr != nil {
			t.Fatal(verr)
		}
	}
	_, _, verr := Record("pg", "one-more:5432", "", presented, false)
	if codeOf(verr) != "core.trust.full" || !strings.Contains(verr.Hint, "rta cert forget") {
		t.Errorf("one more server: %+v", verr)
	}
}

// Forgetting a server takes back what one plugin or every plugin remembered
// of it, and nothing else.
func TestForgettingAServerTakesBackOnlyIt(t *testing.T) {
	isolated(t)
	presented := chain(t, "vault")
	for _, r := range []struct{ ns, server string }{
		{"vault", "vault.internal:8200"}, {"keycloak", "vault.internal:8200"}, {"vault", "other:8200"},
	} {
		if _, _, verr := Record(r.ns, r.server, "", presented, true); verr != nil {
			t.Fatal(verr)
		}
	}
	if gone, verr := Forget("vault", "vault.internal:8200"); gone != 1 || verr != nil {
		t.Errorf("one plugin's: %d, %v", gone, verr)
	}
	if gone, verr := Forget("", "vault.internal:8200"); gone != 1 || verr != nil {
		t.Errorf("every plugin's: %d, %v", gone, verr)
	}
	servers, verr := List()
	if verr != nil || len(servers) != 1 || servers[0].Server != "other:8200" {
		t.Errorf("left: %+v, %v", servers, verr)
	}
}

// A store rta did not write is honoured in nothing and built upon by nothing.
func TestAStoreRtaDidNotWriteIsHonouredInNothing(t *testing.T) {
	isolated(t)
	if _, _, verr := Record("vault", "vault.internal:8200", "", chain(t, "vault"), true); verr != nil {
		t.Fatal(verr)
	}
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(data), `"forward"`, `"operator"`, 1)
	if err := os.WriteFile(Path(), []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	if trusted, verr := Trusted("vault", "vault.internal:8200"); trusted != nil || codeOf(verr) != "core.trust.forged" {
		t.Errorf("a forged store handed out %q, %+v", trusted, verr)
	}
	if _, _, verr := Record("vault", "other:8200", "", chain(t, "other"), true); codeOf(verr) != "core.trust.forged" {
		t.Errorf("a record over a forged store: %+v", verr)
	}
	if err := os.Remove(keyPath()); err != nil {
		t.Fatal(err)
	}
	if _, verr := Trusted("vault", "vault.internal:8200"); codeOf(verr) != "core.trust.unsealed" {
		t.Errorf("a store with no key beside it: %+v", verr)
	}
}
