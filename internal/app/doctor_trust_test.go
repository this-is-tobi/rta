package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/tlstrust"
)

// selfSigned is a certificate a server presents that nothing vouches for,
// ending at notAfter.
func selfSigned(t *testing.T, notAfter time.Time) [][]byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "vault"},
		NotBefore: notAfter.Add(-48 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{der}
}

// A machine that trusted nothing is told nothing about it; one that trusted
// servers is told so in a line, and one whose server waits, or presented
// another certificate, is warned, with the command that looks.
func TestDoctorSaysWhatIsTrustedAndWhatWaits(t *testing.T) {
	isolate(t)
	if _, ok := report(t)["trusted servers"]; ok {
		t.Error("a machine that trusted nothing has a trusted servers row")
	}
	later := time.Now().Add(24 * time.Hour)
	if _, _, verr := tlstrust.Record("vault", "kube:lab/vault/svc/vault:8200", "lab", selfSigned(t, later), true); verr != nil {
		t.Fatal(verr)
	}
	check(t, report(t), "trusted servers", "ok", "1 server trusted on first use")

	if _, _, verr := tlstrust.Record("vault", "kube:lab/vault/svc/vault:8200", "lab", selfSigned(t, later), true); verr != nil {
		t.Fatal(verr)
	}
	check(t, report(t), "trusted servers", "warn", "kube:lab/vault/svc/vault:8200 (vault)")
	check(t, report(t), "trusted servers", "warn", "other than the one trusted")
}

// A server waiting for the operator is a warning naming it, and a trusted
// certificate that has ended is said to be still reached.
func TestDoctorWarnsOfAServerWaitingAndNotesOneThatEnded(t *testing.T) {
	isolate(t)
	if _, _, verr := tlstrust.Record("keycloak", "sso.internal:8443", "", selfSigned(t, time.Now().Add(time.Hour)), false); verr != nil {
		t.Fatal(verr)
	}
	check(t, report(t), "trusted servers", "warn", "nothing here vouches for")
	check(t, report(t), "trusted servers", "warn", "`rta cert trust`")

	isolate(t)
	if _, _, verr := tlstrust.Record("vault", "kube:lab/vault/svc/vault:8200", "lab", selfSigned(t, time.Now().Add(-time.Hour)), true); verr != nil {
		t.Fatal(verr)
	}
	check(t, report(t), "trusted servers", "info", "has ended, and is still reached")
}
