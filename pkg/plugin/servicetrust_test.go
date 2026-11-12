package plugin

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// presentingChain is handshake for a server presenting a chain rather than a
// certificate alone: chain[0] is its own, signed by key.
func presentingChain(t *testing.T, chain []*x509.Certificate, key *ecdsa.PrivateKey, cfg *tls.Config) error {
	t.Helper()
	der := make([][]byte, len(chain))
	for i, c := range chain {
		der[i] = c.Raw
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: der, PrivateKey: key}}})
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

// redisAt is a call to a Redis over TLS at 127.0.0.1, which every certificate
// issue makes names, trusting what is given.
func redisAt(trusted ...*x509.Certificate) Request {
	req := asked(map[string]any{"address": "127.0.0.1:6379", "tls": true})
	return req.WithTrusted(raw(trusted...))
}

func raw(certs ...*x509.Certificate) [][]byte {
	if len(certs) == 0 {
		return nil
	}
	out := make([][]byte, len(certs))
	for i, c := range certs {
		out[i] = c.Raw
	}
	return out
}

func planned(t *testing.T, svc Service, req Request) *tls.Config {
	t.Helper()
	p, verr := svc.TLS(req)
	if verr != nil {
		t.Fatal(verr)
	}
	return p.Config.Clone()
}

func samePresented(got [][]byte, want ...*x509.Certificate) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i].Raw) {
			return false
		}
	}
	return true
}

// A server nothing here vouches for is refused with what it presented, for
// the host to remember; once that certificate is trusted, it is reached, and
// a server presenting another is refused as changed, with the other.
func TestATrustedCertificateStandsInWhereNothingElseVouches(t *testing.T) {
	cert, key := issue(t, nil, nil, time.Hour, false)
	other, _ := issue(t, nil, nil, time.Hour, false)

	first := redisAt()
	err := handshake(t, cert, key, planned(t, redisService, first))
	verr := redisService.Refusal(first, err)
	if verr == nil || !strings.HasPrefix(verr.Code, "redis.tls.") || verr.Code == "redis.tls.changed" ||
		!samePresented(verr.Presented, cert) {
		t.Fatalf("a server nothing vouches for: %+v (%v), want a redis.tls refusal presenting its certificate", verr, err)
	}

	trusted := redisAt(cert)
	if err := handshake(t, cert, key, planned(t, redisService, trusted)); err != nil {
		t.Errorf("the server presenting the certificate trusted for it: %v", err)
	}

	elsewhere := redisAt(other)
	err = handshake(t, cert, key, planned(t, redisService, elsewhere))
	verr = redisService.Refusal(elsewhere, err)
	if codeOf(verr) != "redis.tls.changed" || !samePresented(verr.Presented, cert) ||
		!strings.Contains(verr.Message, "other than the one trusted for it") {
		t.Errorf("a server presenting another certificate than the one trusted: %+v (%v)", verr, err)
	}
}

// The very certificate trusted is taken whatever its dates say — the operator
// trusted those bytes, not an issuer — where nothing else would take it.
func TestATrustedCertificateIsTakenWhenItHasExpired(t *testing.T) {
	expired, key := issue(t, nil, nil, -30*time.Minute, false)
	if err := handshake(t, expired, key, planned(t, redisService, redisAt())); err == nil {
		t.Fatal("an expired self-signed certificate verified with nothing trusted")
	}
	if err := handshake(t, expired, key, planned(t, redisService, redisAt(expired))); err != nil {
		t.Errorf("the expired certificate trusted: %v", err)
	}
}

// A server renewed by the CA it presented when it was trusted is the server
// it was, held to its dates and to the name the call checks; one from another
// CA has changed, and so has a renewal for a name the call does not ask for.
func TestARenewalByTheTrustedCAIsTheSameServer(t *testing.T) {
	ca, caKey := issue(t, nil, nil, 10*365*24*time.Hour, true)
	first, _ := issue(t, ca, caKey, 90*24*time.Hour, false)
	renewed, renewedKey := issue(t, ca, caKey, 90*24*time.Hour, false)
	otherCA, otherKey := issue(t, nil, nil, 10*365*24*time.Hour, true)
	stranger, strangerKey := issue(t, otherCA, otherKey, 90*24*time.Hour, false)

	// Trusted as the first server presented itself: its certificate and its CA.
	req := redisAt(first, ca)
	if err := presentingChain(t, []*x509.Certificate{renewed, ca}, renewedKey, planned(t, redisService, req)); err != nil {
		t.Errorf("a renewal by the trusted CA: %v", err)
	}
	if err := presentingChain(t, []*x509.Certificate{renewed}, renewedKey, planned(t, redisService, req)); err != nil {
		t.Errorf("a renewal by the trusted CA, sent without it: %v", err)
	}
	err := presentingChain(t, []*x509.Certificate{stranger, otherCA}, strangerKey, planned(t, redisService, req))
	if verr := redisService.Refusal(req, err); codeOf(verr) != "redis.tls.changed" || !samePresented(verr.Presented, stranger, otherCA) {
		t.Errorf("a certificate from another CA: %+v (%v)", verr, err)
	}
	named := asked(map[string]any{"address": "127.0.0.1:6379", "tls": true, "tls-server-name": "cache.internal"}).
		WithTrusted(raw(first, ca))
	err = presentingChain(t, []*x509.Certificate{renewed, ca}, renewedKey, planned(t, redisService, named))
	if verr := redisService.Refusal(named, err); codeOf(verr) != "redis.tls.changed" {
		t.Errorf("a renewal for a name the call does not check: %+v (%v)", verr, err)
	}
}

// A renewal through a forward is held to tls-server-name when there is one,
// and to no name when there is none: the host there is 127.0.0.1, and which
// server answers is the coordinate's to say. Go's own check keeps the name.
func TestARenewalThroughAForwardIsHeldToTheNameStated(t *testing.T) {
	for _, tc := range []struct {
		what          string
		req           Request
		name, renewal string
	}{
		{"direct", asked(map[string]any{"address": "db.internal:6379", "tls": true}), "db.internal", "db.internal"},
		{"direct, named", asked(map[string]any{"address": "10.0.0.5:6379", "tls": true, "tls-server-name": "db.internal"}),
			"db.internal", "db.internal"},
		{"through a forward", forwarded(map[string]any{"address": "127.0.0.1:5000", "tls": true}), "127.0.0.1", ""},
		{"through a forward, named", forwarded(map[string]any{"address": "127.0.0.1:5000", "tls": true,
			"tls-server-name": "db.internal"}), "db.internal", "db.internal"},
	} {
		set := redisService.tlsSettings(tc.req)
		name, renewal, verr := redisService.trustNames(tc.req, TLSVerifyFull, set)
		if verr != nil || name != tc.name || renewal != tc.renewal {
			t.Errorf("%s: name %q, renewal %q, %v; want %q and %q", tc.what, name, renewal, verr, tc.name, tc.renewal)
		}
	}
	if name, renewal, _ := pgService.trustNames(asked(map[string]any{"host": "db"}), TLSVerifyCA, tlsSettings{}); name != "" || renewal != "" {
		t.Errorf("verify-ca checked a name: %q, %q", name, renewal)
	}
}

// A CA the operator named is the only issuer they vouch for on that
// connection: nothing trusted stands in beside it, and a server it does not
// vouch for is refused as ever, presenting nothing to trust past it.
func TestACANamedLeavesNothingForATrustedCertificate(t *testing.T) {
	cert, key := issue(t, nil, nil, time.Hour, false)
	otherCA, _ := issue(t, nil, nil, time.Hour, true)
	req := asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": writePEM(t, "CERTIFICATE", otherCA.Raw)}).
		WithTrusted(raw(cert))
	err := handshake(t, cert, key, planned(t, redisService, req))
	verr := redisService.Refusal(req, err)
	if verr == nil || verr.Code == "redis.tls.changed" || verr.Presented != nil {
		t.Errorf("a server the named CA does not vouch for: %+v (%v), want refused presenting nothing", verr, err)
	}
}

// A mode that checks nothing has nothing for a trusted certificate to stand
// in for, and the plan is the one it was.
func TestAModeThatChecksNothingIgnoresWhatWasTrusted(t *testing.T) {
	cert, _ := issue(t, nil, nil, time.Hour, false)
	for _, mode := range []string{TLSPrefer, TLSRequire} {
		p, verr := pgService.TLS(asked(map[string]any{"host": "db", "tls": mode}).WithTrusted(raw(cert)))
		if verr != nil || p.Config.VerifyConnection != nil || !p.Config.InsecureSkipVerify {
			t.Errorf("%s beside a trusted certificate: %+v, %v", mode, p, verr)
		}
	}
	p, verr := pgService.TLS(asked(map[string]any{"host": "db", "tls": TLSDisable}).WithTrusted(raw(cert)))
	if verr != nil || p.Config != nil {
		t.Errorf("disable beside a trusted certificate: %+v, %v", p, verr)
	}
}

// What a server presented is the host's to read and nobody's to be shown: no
// output an error is written in carries it.
func TestWhatAServerPresentedIsInNoOutput(t *testing.T) {
	cert, _ := issue(t, nil, nil, time.Hour, false)
	verr := view.Errorf("redis.tls.untrusted", "nothing trusts it").WithPresented(raw(cert))
	out, err := json.Marshal(verr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(out)), "presented") || len(out) > 200 {
		t.Errorf("an error with what a server presented marshals as %s", out)
	}
}
