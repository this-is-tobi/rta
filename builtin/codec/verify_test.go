package codec

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// verifyWith runs codec.jwt with a key and/or a secret and returns the
// verification section, or the refusal.
func verifyWith(t *testing.T, token, key, secret string) (string, *view.Error) {
	t.Helper()
	values := map[string]any{"token": token}
	if key != "" {
		values["key"] = key
	}
	if secret != "" {
		values["secret"] = secret
	}
	v, err := runJWT(context.Background(), req(values))
	if err != nil {
		return "", view.AsError(err, "test")
	}
	return verification(t, v.(view.Sections)), nil
}

func mustVerify(t *testing.T, token, key, secret, want string) {
	t.Helper()
	body, verr := verifyWith(t, token, key, secret)
	if verr != nil {
		t.Fatalf("refused: %s: %s", verr.Code, verr.Message)
	}
	if !strings.HasPrefix(body, "VERIFIED") || !strings.Contains(body, want) {
		t.Errorf("verification = %q, want VERIFIED naming %q", body, want)
	}
}

func mustRefuse(t *testing.T, token, key, secret, code, want string) {
	t.Helper()
	_, verr := verifyWith(t, token, key, secret)
	if verr == nil {
		t.Fatalf("verified; want %s", code)
	}
	if verr.Code != code || !strings.Contains(verr.Message+" "+verr.Hint, want) {
		t.Errorf("got %s: %s (%s); want %s naming %q", verr.Code, verr.Message, verr.Hint, code, want)
	}
}

// sign builds a compact JWS over header and claims with sign.
func sign(header, claims string, signer func(input []byte) []byte) string {
	input := seg(header) + "." + seg(claims)
	return input + "." + base64.RawURLEncoding.EncodeToString(signer([]byte(input)))
}

var rsaKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

func pemOf(t *testing.T, kind string, der []byte) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

func publicPEM(t *testing.T, pub crypto.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pemOf(t, "PUBLIC KEY", der)
}

func sha256Of(input []byte) []byte {
	sum := sha256.Sum256(input)
	return sum[:]
}

// RFC 7515 Appendix A.1, HMAC with the RFC's own key, and RFC 8037 A.4,
// Ed25519 with the RFC's own key: answers computed by somebody else, which is
// what keeps a self-consistent mistake in signing and checking from passing.
func TestTheRFCExamplesVerify(t *testing.T) {
	a1 := "eyJ0eXAiOiJKV1QiLA0KICJhbGciOiJIUzI1NiJ9." +
		"eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ." +
		"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	mustVerify(t, a1, `{"kty":"oct","k":"AyM1SysPpbyDfgZld3umj1qzKObwVMkoqQ-EstJQLr_T-1qS0gZH75aKtMN3Yj0iPS4hcgUuTwjAzZr1Z9CAow"}`,
		"", "HS256 signature matches")

	a4 := "eyJhbGciOiJFZERTQSJ9.RXhhbXBsZSBvZiBFZDI1NTE5IHNpZ25pbmc." +
		"hgyY0il_MGCjP0JzlnLWG1PPOt7-09PGcvMg3AIbQR6dWbhijcNR4ki4iylGjg5BhVsPt9g7sVvpAr_MuM0KAg"
	mustVerify(t, a4, rfc8037Public, "", "EdDSA signature matches Ed25519")
	// Only the public half is used, so the private JWK checks it too.
	mustVerify(t, a4, rfc8037Private, "", "Ed25519")
}

// A shared secret typed at the terminal, and the most common way to get it
// wrong: handing over its base64 rather than its bytes.
func TestASecretVerifiesAsGivenOrAsBase64(t *testing.T) {
	secret := []byte("correct horse battery staple")
	token := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, secret)
		mac.Write(in)
		return mac.Sum(nil)
	})
	mustVerify(t, token, "", string(secret), "the secret given")
	mustVerify(t, token, "", base64.StdEncoding.EncodeToString(secret), "read as base64")
	mustRefuse(t, token, "", "wrong", "codec.jwt.signature", "does not match")
}

// The attack: an HS256 token "signed" with the bytes of an RSA public key,
// which anyone has. A verifier that lets the header pick the algorithm and
// feeds it the configured key accepts it. Here the key's type decides, and the
// refusal says what was attempted.
func TestAPublicKeyNeverChecksAnHMACSignature(t *testing.T) {
	pubPEM := publicPEM(t, &rsaKey().PublicKey)
	forged := sign(`{"alg":"HS256"}`, `{"sub":"admin"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, []byte(pubPEM))
		mac.Write(in)
		return mac.Sum(nil)
	})
	mustRefuse(t, forged, pubPEM, "", "codec.jwt.alg", "algorithm-confusion")

	// And the reverse: a shared secret never checks an RSA signature — and
	// the refusal says that, not that a kid is missing, which a secret has
	// no way of carrying.
	rs := sign(`{"alg":"RS256","kid":"k1"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey(), crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	mustRefuse(t, rs, `{"kty":"oct","k":"c2VjcmV0"}`, "", "codec.jwt.nokey", "is a shared secret")
	mustRefuse(t, rs, "", "s3cret", "codec.jwt.nokey", "needs an RSA key")
}

func TestRSAVerifiesFromEveryPEMShape(t *testing.T) {
	key := rsaKey()
	rs := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	ps := sign(`{"alg":"PS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, sha256Of(in),
			&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "issuer"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pub := publicPEM(t, &key.PublicKey)
	for name, material := range map[string]string{
		"PKIX public key":  pub,
		"PKCS#1 public":    pemOf(t, "RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&key.PublicKey)),
		"certificate":      pemOf(t, "CERTIFICATE", certDER),
		"PKCS#8 private":   pemOf(t, "PRIVATE KEY", pkcs8),
		"one line, joined": strings.ReplaceAll(pub, "\n", " "),
	} {
		t.Run(name, func(t *testing.T) {
			mustVerify(t, rs, material, "", "2048-bit RSA")
			mustVerify(t, ps, material, "", "PS256 signature matches")
		})
	}
	tampered := strings.Split(rs, ".")
	tampered[1] = seg(`{"sub":"admin"}`)
	mustRefuse(t, strings.Join(tampered, "."), pub, "", "codec.jwt.signature", "changed after it was signed")
}

func es256(t *testing.T, priv *ecdsa.PrivateKey, header string) string {
	t.Helper()
	return sign(header, `{"sub":"a"}`, func(in []byte) []byte {
		r, s, err := ecdsa.Sign(rand.Reader, priv, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return out
	})
}

// The key set is chosen from by kid, a key for encryption is never used to
// verify, and a rotated-away kid is named as what it is.
func TestAKeySetIsChosenFromByKid(t *testing.T) {
	signing, x, y := ecKey(t)
	_, ox, oy := ecKey(t)
	set := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","kid":"old","x":%q,"y":%q},`+
		`{"kty":"EC","crv":"P-256","kid":"current","x":%q,"y":%q},`+
		`{"kty":"EC","crv":"P-256","kid":"enc","use":"enc","x":%q,"y":%q}]}`, ox, oy, x, y, x, y)
	mustVerify(t, es256(t, signing, `{"alg":"ES256","kid":"current"}`), set, "", `kid "current"`)
	mustRefuse(t, es256(t, signing, `{"alg":"ES256","kid":"gone"}`), set, "", "codec.jwt.nokey", "rotated")
	mustRefuse(t, es256(t, signing, `{"alg":"ES256","kid":"enc"}`), set, "", "codec.jwt.nokey", "use enc")
	// No kid in the header: every key that fits is tried.
	mustVerify(t, es256(t, signing, `{"alg":"ES256"}`), set, "", `kid "current"`)
}

// The ES256 mistake worth naming: a DER signature, which other ECDSA APIs
// produce, where JOSE wants R and S side by side.
func TestAnECDSASignatureInDERIsNamedAsSuch(t *testing.T) {
	priv, x, y := ecKey(t)
	der := sign(`{"alg":"ES256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := ecdsa.SignASN1(rand.Reader, priv, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	key := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	mustRefuse(t, der, key, "", "codec.jwt.signature", "ASN.1 DER")

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, es256(t, priv, `{"alg":"ES256"}`), publicPEM(t, &p384.PublicKey), "", "codec.jwt.nokey", "not a P-256 key")
}

// RFC 9864's fully-specified name for the same check.
func TestEd25519VerifiesUnderEitherName(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf(`{"kty":"OKP","crv":"Ed25519","x":%q}`, base64.RawURLEncoding.EncodeToString(pub))
	for _, alg := range []string{"EdDSA", "Ed25519"} {
		token := sign(`{"alg":"`+alg+`"}`, `{"sub":"a"}`, func(in []byte) []byte { return ed25519.Sign(priv, in) })
		mustVerify(t, token, key, "", alg+" signature matches")
	}
}

func TestWhatCannotBeVerifiedIsRefusedByName(t *testing.T) {
	secret := "s3cret"
	mustRefuse(t, seg(`{"alg":"none"}`)+"."+seg(`{"sub":"a"}`)+".", "", secret, "codec.jwt.unsigned", `"none"`)
	// alg none with a signature beside it is still nothing to check.
	mustRefuse(t, seg(`{"alg":"none"}`)+"."+seg(`{"sub":"a"}`)+"."+seg("sig"), "", secret, "codec.jwt.unsigned", `"none"`)
	jwe := strings.Join([]string{seg(`{"alg":"dir","enc":"A128GCM"}`), "", seg("iv"), seg("c"), seg("tag")}, ".")
	mustRefuse(t, jwe, "", secret, "codec.jwt.encrypted", "encrypted, not signed")
	mustRefuse(t, sign(`{"alg":"ES256K"}`, `{}`, func([]byte) []byte { return []byte("x") }), "", secret,
		"codec.jwt.alg", "cannot check")
	mustRefuse(t, buildJWT(t, `{"alg":"HS256"}`, `{}`), "not a key at all", "", "codec.jwt.key", "not a JWK")
}

// Every ACME request is a flattened JWS; its signature covers the protected
// header as carried and the payload as carried.
func TestAJSONSerializationVerifies(t *testing.T) {
	priv, x, y := ecKey(t)
	protected, payload := seg(`{"alg":"ES256","nonce":"n","url":"https://acme.example/new-order"}`), seg(`{"ids":[]}`)
	r, s, err := ecdsa.Sign(rand.Reader, priv, sha256Of([]byte(protected+"."+payload)))
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	token := fmt.Sprintf(`{"protected":%q,"payload":%q,"signature":%q}`,
		protected, payload, base64.RawURLEncoding.EncodeToString(sig))
	mustVerify(t, token, fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y), "", "ES256 signature matches")
}

// The secret is a credential, and an agent must never be invited to supply
// one: the host drops a Local input from the MCP schema and from every MCP
// call, and this is the declaration that tells it to.
func TestTheSecretIsNeverAnAgentInput(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID != "codec.jwt" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "secret" && (!f.Local || f.Type != plugin.Secret) {
				t.Errorf("codec.jwt's secret is %+v, want a Local secret", f)
			}
		}
	}
}
