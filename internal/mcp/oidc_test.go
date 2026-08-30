package mcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	testAudience = "rta-remote"
	testSubject  = "alice@example.com"
)

// fakeOIDCProvider serves a minimal, correct discovery document and JWKS
// endpoint over httptest, and returns a signer for minting tokens against
// the same key the JWKS publishes — real discovery, real JWKS fetch, real
// signature verification, exactly the path a production --oidc-issuer takes.
func fakeOIDCProvider(t *testing.T) (issuer string, key *rsa.PrivateKey, sign func(jwt.Claims) string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	var tsURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                tsURL,
			"jwks_uri":                              tsURL + "/jwks",
			"authorization_endpoint":                tsURL + "/authorize",
			"token_endpoint":                        tsURL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		jwk := jose.JSONWebKey{Key: &priv.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	tsURL = ts.URL

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	sign = func(claims jwt.Claims) string {
		t.Helper()
		tok, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	return tsURL, priv, sign
}

func validClaims(issuer string) jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		Issuer:   issuer,
		Subject:  testSubject,
		Audience: jwt.Audience{testAudience},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
	}
}

func newOIDCVerifier(t *testing.T, issuer string, subjects []string) auth.TokenVerifier {
	t.Helper()
	var stderr strings.Builder
	v, err := OIDCVerifier(context.Background(), issuer, testAudience, subjects, &stderr)
	if err != nil {
		t.Fatalf("OIDCVerifier setup: %v", err)
	}
	return v
}

func TestOIDCVerifierAcceptsAValidToken(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	claims := validClaims(issuer)

	info, err := v(context.Background(), sign(claims), nil)
	if err != nil {
		t.Fatalf("valid token refused: %v", err)
	}
	if info.UserID != testSubject {
		t.Errorf("UserID = %q, want %q", info.UserID, testSubject)
	}
	if !info.Expiration.Equal(claims.Expiry.Time()) {
		t.Errorf("Expiration = %v, want %v", info.Expiration, claims.Expiry.Time())
	}
}

func TestOIDCVerifierRejectsWrongAudience(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	claims := validClaims(issuer)
	claims.Audience = jwt.Audience{"somebody-elses-app"}

	if _, err := v(context.Background(), sign(claims), nil); err != auth.ErrInvalidToken {
		t.Errorf("err = %v, want the bare auth.ErrInvalidToken sentinel", err)
	}
}

func TestOIDCVerifierRejectsWrongIssuer(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	claims := validClaims("https://not-the-configured-issuer.example")

	if _, err := v(context.Background(), sign(claims), nil); err != auth.ErrInvalidToken {
		t.Errorf("err = %v, want auth.ErrInvalidToken", err)
	}
}

func TestOIDCVerifierRejectsAnExpiredToken(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	claims := validClaims(issuer)
	claims.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	claims.IssuedAt = jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))

	if _, err := v(context.Background(), sign(claims), nil); err != auth.ErrInvalidToken {
		t.Errorf("err = %v, want auth.ErrInvalidToken", err)
	}
}

// A token signed by a key the published JWKS never listed — the ordinary
// forged-signature case, distinct from the algorithm-confusion one below.
func TestOIDCVerifierRejectsABadSignature(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	_ = sign // the provider's own signer is not used for this token

	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rogueSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: other},
		(&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(rogueSigner).Claims(validClaims(issuer)).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := v(context.Background(), tok, nil); err != auth.ErrInvalidToken {
		t.Errorf("err = %v, want auth.ErrInvalidToken", err)
	}
}

// The classic RS256-to-HS256 attack: sign with a symmetric algorithm using
// bytes derived from the RSA *public* key as the HMAC secret — something an
// attacker who only knows the public key (which is, by definition, public)
// can always do. If a verifier trusted the token's own "alg" header to pick
// the verification method, this would pass; go-oidc's provider.Verifier
// instead only ever accepts algorithms the provider's discovery document
// advertised, so this must fail regardless of how the token asserts itself.
func TestOIDCVerifierRejectsAlgorithmConfusion(t *testing.T) {
	issuer, key, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	_ = sign

	hmacSecret := key.PublicKey.N.Bytes()
	hmacSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: hmacSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(hmacSigner).Claims(validClaims(issuer)).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := v(context.Background(), tok, nil); err != auth.ErrInvalidToken {
		t.Errorf("an HS256 token signed with the RSA public key's modulus as the HMAC secret was accepted: %v", err)
	}
}

func TestOIDCVerifierRejectsASubjectOutsideTheAllowlist(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{"somebody-else@example.com"})
	claims := validClaims(issuer) // subject is testSubject, not on the allowlist above

	if _, err := v(context.Background(), sign(claims), nil); err != auth.ErrInvalidToken {
		t.Errorf("err = %v, want auth.ErrInvalidToken", err)
	}
}

func TestOIDCVerifierRequiresAtLeastOneSubject(t *testing.T) {
	issuer, _, _ := fakeOIDCProvider(t)
	if _, err := OIDCVerifier(context.Background(), issuer, testAudience, nil, nil); err == nil {
		t.Fatal("OIDCVerifier accepted an empty subject allowlist")
	}
}

func TestOIDCVerifierFailsSetupOnBadDiscovery(t *testing.T) {
	// Nothing listens here; discovery must fail at construction time rather
	// than lazily on the first call.
	if _, err := OIDCVerifier(context.Background(), "http://127.0.0.1:1", testAudience,
		[]string{testSubject}, nil); err == nil {
		t.Fatal("OIDCVerifier succeeded against an issuer with no discovery document")
	}
}
