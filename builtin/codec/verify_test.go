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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// verifyWith runs codec.jwt with a key and/or a secret, written to the file
// --secret-file names, and returns the verification section, or the refusal.
func verifyWith(t *testing.T, token, key, secret string) (string, *view.Error) {
	t.Helper()
	values := map[string]any{"token": token}
	if key != "" {
		values["key"] = key
	}
	if secret != "" {
		values["secret-file"] = secretFile(t, secret)
	}
	v, err := runJWT(context.Background(), req(values))
	if err != nil {
		return "", view.AsError(err, "test")
	}
	return verification(t, v.(view.Sections)), nil
}

func secretFile(t *testing.T, secret string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
	// The RFC gives its key as an oct JWK, which a secret file may hold.
	mustVerify(t, a1, "", `{"kty":"oct","k":"AyM1SysPpbyDfgZld3umj1qzKObwVMkoqQ-EstJQLr_T-1qS0gZH75aKtMN3Yj0iPS4hcgUuTwjAzZr1Z9CAow"}`,
		"HS256 signature matches the oct key in")

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
	mustVerify(t, token, "", string(secret), "the secret in")
	mustVerify(t, token, "", string(secret)+"\n", "without its final line break")
	mustVerify(t, token, "", base64.StdEncoding.EncodeToString(secret)+"\n", "read as base64")
	// A secret has no kid, and the hint for a key sent somebody looking for
	// one.
	mustRefuse(t, token, "", "wrong", "codec.jwt.signature", "check the secret file")
}

// A signature with its last letter changed where only unused bits live still
// matches under a lenient decoder, and it used to be a bare VERIFIED. It still
// matches, since the bytes are the same, and the page now says the text is
// not the canonical spelling of them.
func TestAVerifiedSignatureThatIsNotCanonicalIsNamed(t *testing.T) {
	token := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(in)
		return mac.Sum(nil)
	})
	// A 32-byte MAC leaves two bits of its last character unused; flipping
	// the lowest changes the text and not the bytes.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, token[len(token)-1])
	loose := token[:len(token)-1] + string(alphabet[last^1])
	body, verr := verifyWith(t, loose, "", "s3cret")
	if verr != nil {
		t.Fatalf("refused: %s", verr.Message)
	}
	if !strings.HasPrefix(body, "VERIFIED") || !strings.Contains(body, "unused bits are not zero") {
		t.Errorf("verification = %q, want VERIFIED and the non-canonical signature named", body)
	}
}

// A matching signature on a token whose crit lists an extension rta does not
// implement was a bare VERIFIED. The signature is still checked, and the
// verdict is followed by what the RFC makes of such a token.
func TestAVerifiedTokenWithAnUnknownCritSaysSo(t *testing.T) {
	token := sign(`{"alg":"HS256","crit":["http://example.com/must-understand"],"http://example.com/must-understand":1}`, `{"sub":"a"}`,
		func(in []byte) []byte {
			mac := hmac.New(sha256.New, []byte("s3cret"))
			mac.Write(in)
			return mac.Sum(nil)
		})
	body, verr := verifyWith(t, token, "", "s3cret")
	if verr != nil {
		t.Fatalf("refused: %s", verr.Message)
	}
	if !strings.HasPrefix(body, "VERIFIED") || !strings.Contains(body, "which rta does not implement") {
		t.Errorf("verification = %q, want VERIFIED qualified by the crit it cannot honour", body)
	}
}

// An unencoded payload (RFC 7797) is carried as it is, and §5.2 lets it hold
// a space. Whitespace used to come out of the whole token before it was split,
// so "hello world" showed as "helloworld" and its valid signature was reported
// as the token changed after signing.
func TestAnUnencodedPayloadKeepsItsSpaces(t *testing.T) {
	header := seg(`{"alg":"HS256","b64":false,"crit":["b64"]}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(header + ".hello world"))
	token := header + ".hello world." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if got := section(t, jose(t, token), "payload").(view.Text).Body; got != "hello world" {
		t.Errorf("payload = %q", got)
	}
	mustVerify(t, token, "", "s3cret", "HS256 signature matches")
}

// A detached payload was signed with content that is not in the token, so
// the check over the empty string fails however good the signature is, and
// the refusal blamed tampering or the key. It now says the payload is not
// here. A signature that really is over the empty string still verifies,
// and a JSON form's empty payload member is the empty string, not detached.
func TestADetachedPayloadIsNamedWhenItsSignatureCannotBeChecked(t *testing.T) {
	hs := func(input string) string {
		mac := hmac.New(sha256.New, []byte("k"))
		mac.Write([]byte(input))
		return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	h := seg(`{"alg":"HS256"}`)
	mustRefuse(t, h+".."+hs(h+"."+seg(`{"sub":"x"}`)), "", "k", "codec.jwt.detached", "either the payload is detached")
	mustVerify(t, h+".."+hs(h+"."), "", "k", "HS256 signature matches")

	signing, x, y := ecKey(t)
	key := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	parts := strings.Split(es256(t, signing, `{"alg":"ES256"}`), ".")
	mustRefuse(t, parts[0]+".."+parts[2], key, "", "codec.jwt.detached", "not here")
	mustRefuse(t, fmt.Sprintf(`{"protected":%q,"signature":%q}`, parts[0], parts[2]), key, "",
		"codec.jwt.detached", "the payload is detached")
	mustRefuse(t, fmt.Sprintf(`{"protected":%q,"payload":"","signature":%q}`, parts[0], parts[2]), key, "",
		"codec.jwt.signature", "does not match")
}

// A secret file that is not there, is empty, or is not a secret at all.
func TestASecretFileThatCannotBeReadIsRefused(t *testing.T) {
	token := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func([]byte) []byte { return []byte("x") })
	for name, path := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "nothing-here"),
		"empty":   secretFile(t, ""),
		"huge":    secretFile(t, strings.Repeat("s", maxSecretFile+1)),
	} {
		_, err := runJWT(context.Background(), req(map[string]any{"token": token, "secret-file": path}))
		if verr := view.AsError(err, "test"); err == nil || verr.Code != "codec.jwt.secret" {
			t.Errorf("%s: got %v, want codec.jwt.secret", name, err)
		}
	}
}

// An ambient secret used to make every decode a verification, from every
// surface: an RS256 or unsigned token stopped decoding, and an agent calling
// a free Read got VERIFIED or not against the operator's secret for any token
// it signed with a guess. Nothing in the environment reaches the handler now,
// under either name the variable could have.
func TestNoSecretIsTakenFromTheEnvironment(t *testing.T) {
	t.Setenv("RTA_CODEC_SECRET", "hunter2")
	t.Setenv("RTA_CODEC_SECRET_FILE", secretFile(t, "hunter2"))
	var jwt plugin.Capability
	for _, c := range Plugin().Capabilities {
		if c.ID == "codec.jwt" {
			jwt = c
		}
	}
	token := sign(`{"alg":"HS256"}`, `{"sub":"admin"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, []byte("hunter2"))
		mac.Write(in)
		return mac.Sum(nil)
	})
	for _, surface := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceMCP} {
		values := plugin.Resolve(jwt, plugin.Inputs{Caller: map[string]any{"token": token}})
		v, err := runJWT(context.Background(), plugin.NewRequest(values, false, false).WithSurface(surface))
		if err != nil {
			t.Fatalf("%s: %v", surface, err)
		}
		if body := verification(t, v.(view.Sections)); !strings.HasPrefix(body, "NOT VERIFIED") {
			t.Errorf("%s: verification = %q, want nothing checked", surface, body)
		}
	}
	if plugin.Profilable(jwt) {
		t.Error("codec.jwt is profilable, so a profile's secrets mapping could fill a secret")
	}
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
	mustRefuse(t, rs, "", "s3cret", "codec.jwt.nokey", "needs an RSA key")
}

// --key is an input an agent may fill, and --secret-file is Local so that an
// agent is never invited to supply a shared secret. An oct JWK in --key was
// that secret one JSON wrapper away, so it is refused, and one in a set is
// skipped with a note. Where a secret goes is said the way the surface
// asking can act on: an agent has no --secret-file.
func TestASharedSecretIsNeverTakenAsAKey(t *testing.T) {
	hs := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, []byte("secret"))
		mac.Write(in)
		return mac.Sum(nil)
	})
	oct := `{"kty":"oct","k":"c2VjcmV0"}`
	mustRefuse(t, hs, oct, "", "codec.jwt.key", "--secret-file")

	for surface, want := range map[plugin.Surface]string{
		plugin.SurfaceCLI: "--secret-file", plugin.SurfaceTUI: "secret-file box", plugin.SurfaceMCP: "never from an agent",
	} {
		for name, key := range map[string]string{"oct JWK": oct, "public key": publicPEM(t, &rsaKey().PublicKey)} {
			_, err := runJWT(context.Background(), req(map[string]any{"token": hs, "key": key}).WithSurface(surface))
			verr := view.AsError(err, "test")
			if err == nil || !strings.Contains(verr.Hint, want) {
				t.Errorf("%s, %s: got %v (hint %q), want a hint naming %q", surface, name, err, verr.Hint, want)
			}
			if surface == plugin.SurfaceMCP && strings.Contains(verr.Hint, "--secret-file") {
				t.Errorf("%s: an agent was told to pass a flag it has no way to: %q", name, verr.Hint)
			}
		}
	}

	signing, x, y := ecKey(t)
	set := fmt.Sprintf(`{"keys":[{"kty":"oct","kid":"hmac","k":"c2VjcmV0"},{"kty":"EC","crv":"P-256","kid":"ec","x":%q,"y":%q}]}`, x, y)
	body, verr := verifyWith(t, es256(t, signing, `{"alg":"ES256","kid":"ec"}`), set, "")
	if verr != nil {
		t.Fatalf("refused: %s", verr.Message)
	}
	if !strings.HasPrefix(body, "VERIFIED") || !strings.Contains(body, `kid "hmac" is a shared secret (kty oct), which --key does not take`) {
		t.Errorf("verification = %q, want VERIFIED and the oct key named as skipped", body)
	}
	mustRefuse(t, hs, set, "", "codec.jwt.alg", "only a public key was given")
}

// The same attack through the other door: the public key handed over as the
// secret. The guard used to look only at which input the material came in, so
// the text of a PEM, or the bare base64 SPKI Keycloak's console shows a realm
// key as, verified an HS256 token forged with it.
func TestAPublicKeyGivenAsTheSecretIsRefused(t *testing.T) {
	key := rsaKey()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(9), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	bare := base64.StdEncoding.EncodeToString(der)
	for name, secret := range map[string]string{
		"PEM":              publicPEM(t, &key.PublicKey),
		"bare base64 SPKI": bare,
		"DER":              string(der),
		"PKCS#1 DER":       string(x509.MarshalPKCS1PublicKey(&key.PublicKey)),
		"certificate":      base64.StdEncoding.EncodeToString(certDER),
		"RSA JWK":          rfc7638Key,
		"key set":          `{"keys":[` + rfc8037Public + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			forged := sign(`{"alg":"HS256"}`, `{"sub":"admin"}`, func(in []byte) []byte {
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write(in)
				return mac.Sum(nil)
			})
			mustRefuse(t, forged, "", secret, "codec.jwt.alg", "algorithm-confusion")
		})
	}
	// And the hint that led there: bare base64 in --key is told it needs its
	// armour, not sent to the secret.
	_, verr := verifyWith(t, sign(`{"alg":"RS256"}`, `{}`, func([]byte) []byte { return []byte("x") }), bare, "")
	if verr == nil || verr.Code != "codec.jwt.key" || !strings.Contains(verr.Hint, "BEGIN PUBLIC KEY") ||
		strings.Contains(verr.Message+verr.Hint, "secret") {
		t.Errorf("bare base64 key: got %+v, want a hint naming PEM armour and not the secret", verr)
	}
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
// call, and this is the declaration that tells it to. No EnvFallback, which
// would fill it on every surface from the environment.
func TestTheSecretIsNeverAnAgentInput(t *testing.T) {
	found := false
	for _, c := range Plugin().Capabilities {
		if c.ID != "codec.jwt" {
			continue
		}
		for _, f := range c.Inputs {
			switch f.Name {
			case "secret-file":
				found = true
				if !f.Local || f.EnvFallback || f.Type != plugin.Path {
					t.Errorf("codec.jwt's secret-file is %+v, want a Local path with no environment fallback", f)
				}
			case "secret":
				t.Error("codec.jwt takes the secret itself again, which puts it in argv")
			}
		}
	}
	if !found {
		t.Error("codec.jwt has no secret-file input")
	}
}
