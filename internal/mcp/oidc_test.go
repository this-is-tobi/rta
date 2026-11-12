package mcp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	idp := newFakeIdP(t)
	return idp.issuer, idp.key, idp.sign
}

// fakeCode is the one authorization code fakeIdP's token endpoint redeems.
const fakeCode = "fake-authorization-code"

// fakeIdP is fakeOIDCProvider with the rest of what an OAuth client asks of
// an authorization server: PKCE advertised, which the SDK's client refuses to
// proceed without, and a token endpoint. That endpoint redeems fakeCode for
// a signed access token naming testSubject, with the resource the client
// asked for (RFC 8707) as its audience, and records that resource. The
// authorization endpoint is never served: the test's code fetcher stands in
// for the browser, and for the person at it.
type fakeIdP struct {
	issuer string
	key    *rsa.PrivateKey
	sign   func(jwt.Claims) string

	mu        sync.Mutex
	resources []string
}

// requestedResources is the resource parameter of every token request so far.
func (p *fakeIdP) requestedResources() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.resources)
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: priv}
	// Not t.Fatal: the token endpoint signs on the server's goroutine.
	mint := func(claims jwt.Claims) (string, error) { return jwt.Signed(signer).Claims(claims).Serialize() }
	idp.sign = func(claims jwt.Claims) string {
		t.Helper()
		tok, err := mint(claims)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.issuer,
			"jwks_uri":                              idp.issuer + "/jwks",
			"authorization_endpoint":                idp.issuer + "/authorize",
			"token_endpoint":                        idp.issuer + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		jwk := jose.JSONWebKey{Key: &priv.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("code") != fakeCode || r.PostForm.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		resource := r.PostForm.Get("resource")
		idp.mu.Lock()
		idp.resources = append(idp.resources, resource)
		idp.mu.Unlock()
		now := time.Now()
		tok, err := mint(jwt.Claims{
			Issuer:   idp.issuer,
			Subject:  testSubject,
			Audience: jwt.Audience{resource},
			IssuedAt: jwt.NewNumericDate(now),
			Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": tok, "token_type": "Bearer", "expires_in": 3600,
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	idp.issuer = ts.URL
	return idp
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

// Discovery and the signing keys are fetched only over https once the issuer
// is. The issuer was the one address anything checked: go-oidc fetched the
// keys from wherever the discovery document named, plain http included, and
// followed a redirect off the issuer's https address to a plain one, and a key
// swapped in transit on either signs any token the server then accepts. The
// plain loopback exception stays a loopback one.
func TestOIDCNeverFetchesDiscoveryOrKeysInTheClear(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(plain.Close)
	document := func(issuer, keys string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "jwks_uri": keys,
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		}
	}
	for _, tc := range []struct {
		name      string
		tls       bool
		discovery func(issuer string) http.HandlerFunc
	}{
		{"an https issuer naming its keys at a plain address", true, func(issuer string) http.HandlerFunc {
			return document(issuer, plain.URL+"/jwks")
		}},
		{"an https issuer redirecting discovery to a plain address", true, func(string) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, plain.URL+"/.well-known/openid-configuration", http.StatusFound)
			}
		}},
		{"a plain loopback issuer naming its keys at a plain address elsewhere", false, func(issuer string) http.HandlerFunc {
			return document(issuer, "http://keys.example.invalid/jwks")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var issuer string
			mux := http.NewServeMux()
			mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
				tc.discovery(issuer)(w, r)
			})
			idp := httptest.NewUnstartedServer(mux)
			if tc.tls {
				idp.StartTLS()
			} else {
				idp.Start()
			}
			t.Cleanup(idp.Close)
			issuer = idp.URL
			saved := oidcTransport
			oidcTransport = func() http.RoundTripper { return idp.Client().Transport }
			t.Cleanup(func() { oidcTransport = saved })

			_, err := OIDCVerifier(context.Background(), issuer, testAudience, []string{testSubject}, nil)
			if err == nil {
				t.Fatal("the server started, trusting signing keys it would fetch in the clear")
			}
			if !strings.Contains(err.Error(), "only over https") {
				t.Errorf("err = %v, want it to say why", err)
			}
		})
	}
	if n := plainHits.Load(); n != 0 {
		t.Errorf("%d requests went to the plain address", n)
	}
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

	if _, err := v(context.Background(), sign(claims), nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("err = %v, want the bare auth.ErrInvalidToken sentinel", err)
	}
}

func TestOIDCVerifierRejectsWrongIssuer(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	claims := validClaims("https://not-the-configured-issuer.example")

	if _, err := v(context.Background(), sign(claims), nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("err = %v, want auth.ErrInvalidToken", err)
	}
}

func TestOIDCVerifierRejectsAnExpiredToken(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{testSubject})
	claims := validClaims(issuer)
	claims.Expiry = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	claims.IssuedAt = jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))

	if _, err := v(context.Background(), sign(claims), nil); !errors.Is(err, auth.ErrInvalidToken) {
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

	if _, err := v(context.Background(), tok, nil); !errors.Is(err, auth.ErrInvalidToken) {
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

	hmacSecret := key.N.Bytes()
	hmacSigner, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: hmacSecret}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(hmacSigner).Claims(validClaims(issuer)).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := v(context.Background(), tok, nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("an HS256 token signed with the RSA public key's modulus as the HMAC secret was accepted: %v", err)
	}
}

func TestOIDCVerifierRejectsASubjectOutsideTheAllowlist(t *testing.T) {
	issuer, _, sign := fakeOIDCProvider(t)
	v := newOIDCVerifier(t, issuer, []string{"somebody-else@example.com"})
	claims := validClaims(issuer) // subject is testSubject, not on the allowlist above

	if _, err := v(context.Background(), sign(claims), nil); !errors.Is(err, auth.ErrInvalidToken) {
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
