package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/tlstrust"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// presentedChain is one self-signed certificate for name, as a server
// presents it.
func presentedChain(t *testing.T, name string) [][]byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{der}
}

func capabilityOf(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %s", id)
	return plugin.Capability{}
}

// run calls capability id with values, at a terminal answering with answer
// when it is given one.
func run(t *testing.T, id string, values map[string]any, dryRun bool, answer func(card string) bool) (view.View, error) {
	t.Helper()
	c := capabilityOf(t, id)
	req := plugin.ResolveRequest(c, plugin.Inputs{Caller: values}, dryRun, false).WithSurface(plugin.SurfaceCLI)
	if answer != nil {
		req = req.WithConfirmer(answer)
	}
	return c.Run(context.Background(), req)
}

// Trusting, listing and forgetting a server's certificate are a person's
// alone: none is a tool an agent is offered.
func TestTrustIsNeverATool(t *testing.T) {
	for _, id := range []string{"cert.trust", "cert.trusted", "cert.forget"} {
		if c := capabilityOf(t, id); !c.HumanOnly {
			t.Errorf("%s is offered over MCP", id)
		}
	}
}

// A certificate waiting is trusted once the person has read it and said yes,
// its SHA-256 whole on the card they read; a no trusts nothing and ends as a
// declined prompt does; and with nothing waiting, nothing is pretended.
func TestACertificateIsTrustedOnceSomebodyHasReadIt(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, verr := run(t, "cert.trust", nil, false, nil); codeOf(verr) != "core.trust.none" {
		t.Errorf("with nothing waiting: %+v", verr)
	}
	presented := presentedChain(t, "vault.internal")
	if _, _, verr := tlstrust.Record("vault", "vault.internal:8200", "prod", presented, false); verr != nil {
		t.Fatal(verr)
	}
	var shown string
	_, verr := run(t, "cert.trust", map[string]any{"server": "vault.internal:8200"}, false,
		func(card string) bool { shown = card; return false })
	if codeOf(verr) != "core.confirm.required" {
		t.Errorf("a no: %+v", verr)
	}
	for _, want := range []string{"vault.internal:8200 presented to vault through profile prod", "CN=vault.internal (self-signed)",
		tlstrust.Fingerprint(presented), "Trust it for vault's calls to vault.internal:8200?"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the card does not say %q:\n%s", want, shown)
		}
	}
	if trusted, _ := tlstrust.Trusted("vault", "vault.internal:8200"); trusted != nil {
		t.Fatal("a no trusted it")
	}
	v, verr := run(t, "cert.trust", map[string]any{"server": "vault.internal:8200"}, false, func(string) bool { return true })
	if verr != nil {
		t.Fatal(verr)
	}
	if table, ok := v.(view.Table); !ok || len(table.Rows) != 1 || table.Rows[0][2] != "yes" {
		t.Errorf("a yes: %+v", v)
	}
	if trusted, _ := tlstrust.Trusted("vault", "vault.internal:8200"); tlstrust.Fingerprint(trusted) != tlstrust.Fingerprint(presented) {
		t.Error("a yes trusted nothing")
	}
}

// Named, a server's certificate is trusted where nobody can be asked: typing
// its name is the word. Unnamed, every one waiting is not a thing a script may
// trust, and a dry run shows each and trusts none.
func TestEveryCertificateWaitingIsTrustedOnlyAtATerminal(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	for _, server := range []string{"a.internal:443", "b.internal:443"} {
		if _, _, verr := tlstrust.Record("vault", server, "", presentedChain(t, server), false); verr != nil {
			t.Fatal(verr)
		}
	}
	if _, verr := run(t, "cert.trust", nil, false, nil); codeOf(verr) != "core.trust.unnamed" {
		t.Errorf("every one, with nobody to ask: %+v", verr)
	}
	v, verr := run(t, "cert.trust", nil, true, nil)
	if text, ok := v.(view.Text); verr != nil || !ok || strings.Count(text.Body, "would trust") != 2 {
		t.Errorf("a dry run: %+v, %+v", v, verr)
	}
	if trusted, _ := tlstrust.Trusted("vault", "a.internal:443"); trusted != nil {
		t.Error("a dry run trusted a certificate")
	}
	if _, verr := run(t, "cert.trust", map[string]any{"server": "a.internal:443"}, false, nil); verr != nil {
		t.Errorf("one named, with nobody to ask: %+v", verr)
	}
	if trusted, _ := tlstrust.Trusted("vault", "a.internal:443"); trusted == nil {
		t.Error("the one named was not trusted")
	}
}

// A certificate other than the one trusted is shown beside the one it would
// replace, and listed as changed, with what to do about it.
func TestAChangedCertificateIsShownBesideTheOneItReplaces(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	server := "kube:lab/vault/svc/vault:8200"
	first, second := presentedChain(t, "vault"), presentedChain(t, "vault")
	if _, _, verr := tlstrust.Record("vault", server, "lab", first, true); verr != nil {
		t.Fatal(verr)
	}
	if _, _, verr := tlstrust.Record("vault", server, "lab", second, true); verr != nil {
		t.Fatal(verr)
	}
	v, verr := run(t, "cert.trusted", nil, false, nil)
	table, ok := v.(view.Table)
	if verr != nil || !ok || len(table.Rows) != 1 || table.Rows[0][2] != "WARN changed" || len(table.Warnings) != 1 ||
		!strings.Contains(table.Warnings[0].Hint, "rta cert trust "+server) {
		t.Fatalf("the list: %+v, %+v", v, verr)
	}
	var shown string
	if _, verr := run(t, "cert.trust", map[string]any{"server": server}, false,
		func(card string) bool { shown = card; return true }); verr != nil {
		t.Fatal(verr)
	}
	if !strings.Contains(shown, "in place of the one trusted") || !strings.Contains(shown, "as it arrived through a forward") ||
		!strings.Contains(shown, tlstrust.Fingerprint(first)) || !strings.Contains(shown, tlstrust.Fingerprint(second)) {
		t.Errorf("the card:\n%s", shown)
	}
	v, _ = run(t, "cert.trusted", nil, false, nil)
	if table := v.(view.Table); len(table.Rows) != 1 || table.Rows[0][2] != "OK trusted" || !strings.Contains(table.Rows[0][4], "on your word") {
		t.Errorf("listed after it was trusted: %+v", table.Rows)
	}
}

// Forgetting takes back what was remembered, and forgetting what is not is
// said to be nothing.
func TestForgettingAServerIsSaidAsDone(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, _, verr := tlstrust.Record("vault", "vault.internal:8200", "", presentedChain(t, "vault"), true); verr != nil {
		t.Fatal(verr)
	}
	if v, verr := run(t, "cert.forget", map[string]any{"server": "vault.internal:8200"}, true, nil); verr != nil ||
		!strings.Contains(v.(view.Text).Body, "would forget") {
		t.Errorf("a dry run: %+v, %+v", v, verr)
	}
	if _, verr := run(t, "cert.forget", map[string]any{"server": "vault.internal:8200"}, false, nil); verr != nil {
		t.Fatal(verr)
	}
	if trusted, _ := tlstrust.Trusted("vault", "vault.internal:8200"); trusted != nil {
		t.Error("forgotten and still trusted")
	}
	if _, verr := run(t, "cert.forget", map[string]any{"server": "vault.internal:8200"}, false, nil); codeOf(verr) != "core.trust.none" {
		t.Errorf("forgetting it again: %v", verr)
	}
}
