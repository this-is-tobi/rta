package codec

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
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
	"unicode/utf16"

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

// An oct JWK in the secret file is a key, and what it declares about itself
// is held to as --key holds a key to it: a strict library refuses an AES key
// wrap key, or one declared for HS512, for an HS256 token, and here both gave
// a bare VERIFIED. key_ops ["sign"] still admits the check, as it does for a
// private key given to --key.
func TestAnOctKeyInTheSecretFileIsHeldToWhatItDeclares(t *testing.T) {
	k := []byte(strings.Repeat("k", 32))
	token := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, k)
		mac.Write(in)
		return mac.Sum(nil)
	})
	oct := func(extra string) string {
		return fmt.Sprintf(`{"kty":"oct",%s"k":%q}`, extra, base64.RawURLEncoding.EncodeToString(k))
	}
	for extra, want := range map[string]string{
		`"use":"enc",`:                     "is for encryption (use enc)",
		`"alg":"A256KW",`:                  "is declared for A256KW",
		`"alg":"HS512",`:                   "is declared for HS512",
		`"key_ops":["encrypt","decrypt"],`: "is limited by key_ops to encrypt, decrypt",
	} {
		mustRefuse(t, token, "", oct(extra), "codec.jwt.nokey", "the oct key in "+`"`)
		mustRefuse(t, token, "", oct(extra), "codec.jwt.nokey", want)
	}
	for _, extra := range []string{`"alg":"HS256",`, `"use":"sig",`, `"key_ops":["sign"],`, `"key_ops":["verify"],`} {
		mustVerify(t, token, "", oct(extra), "HS256 signature matches the oct key in")
	}
}

// A key set of shared secrets in the secret file was HMAC'd as its JSON text,
// so a token made with the set's own k was told it did not match, blamed on
// the token being changed — and --key sends a set of oct keys there. The set
// is refused, saying what the file takes, and --key says so up front.
func TestAKeySetInTheSecretFileIsRefusedForTheOneKey(t *testing.T) {
	k := []byte(strings.Repeat("k", 32))
	token := sign(`{"alg":"HS256","kid":"a"}`, `{"sub":"a"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, k)
		mac.Write(in)
		return mac.Sum(nil)
	})
	set := fmt.Sprintf(`{"keys":[{"kty":"oct","kid":"a","k":%q}]}`, base64.RawURLEncoding.EncodeToString(k))
	mustRefuse(t, token, "", set, "codec.jwt.secret", "holds a key set")
	mustRefuse(t, token, "", set, "codec.jwt.secret", `{"kty":"oct","k":…}`)
	mustRefuse(t, token, set, "", "codec.jwt.key", "not the set, with --secret-file")
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

// RFC 7518 §3.2 requires an HMAC key at least as long as its hash. A
// one-byte secret still matches, and the verdict now says what a strict
// library makes of it; a long enough one draws nothing.
func TestAShortHMACSecretIsNamedBesideItsVerdict(t *testing.T) {
	for secret, short := range map[string]bool{"a": true, strings.Repeat("k", 32): false} {
		token := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(in)
			return mac.Sum(nil)
		})
		body, verr := verifyWith(t, token, "", secret)
		if verr != nil {
			t.Fatalf("refused: %s", verr.Message)
		}
		if named := strings.Contains(body, "The secret is 1 byte, under the 32 RFC 7518 §3.2 requires"); named != short {
			t.Errorf("%d-byte secret: verification = %q", len(secret), body)
		}
	}
}

// A secret file that is not there, is empty, or is not a secret at all.
//
// Whitespace alone is empty too. `echo "$SECRET" > f` with SECRET unset
// writes a line break and nothing else, and that byte was the HMAC key: a
// token made with it VERIFIED, and a real one was told it had been changed
// after it was signed.
func TestASecretFileThatCannotBeReadIsRefused(t *testing.T) {
	token := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func([]byte) []byte { return []byte("x") })
	for name, path := range map[string]string{
		"missing":                    filepath.Join(t.TempDir(), "nothing-here"),
		"empty":                      secretFile(t, ""),
		"huge":                       secretFile(t, strings.Repeat("s", maxSecretFile+1)),
		"a line break":               secretFile(t, "\n"),
		"CRLF":                       secretFile(t, "\r\n"),
		"spaces":                     secretFile(t, "   "),
		"a byte-order mark and a LF": secretFile(t, byteOrderMark+"\n"),
	} {
		_, err := runJWT(context.Background(), req(map[string]any{"token": token, "secret-file": path}))
		if verr := view.AsError(err, "test"); err == nil || verr.Code != "codec.jwt.secret" {
			t.Errorf("%s: got %v, want codec.jwt.secret", name, err)
		}
	}
	for _, blank := range []string{"\n", "\r\n", "   "} {
		mac := hmac.New(sha256.New, []byte(blank))
		made := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func(in []byte) []byte { mac.Write(in); return mac.Sum(nil) })
		mustRefuse(t, made, "", blank, "codec.jwt.secret", "holds only whitespace")
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

	// A set of nothing but shared secrets, and a set of nothing at all, were
	// "no usable key in what was given: " with nothing after the colon, and
	// the oct keys' note, which says where a secret goes, was dropped for a
	// hint to run codec.jwk. Beside a key its members do not make, the oct
	// key was left out of the list of what could not be used.
	for name, tc := range map[string]struct{ set, want, hint string }{
		"only oct": {`{"keys":[{"kty":"oct","kid":"hmac","k":"c2VjcmV0"}]}`,
			"the key set holds only shared secrets (kty oct), and --key takes only public keys", "--secret-file"},
		"empty": {`{"keys":[]}`, "the key set holds no keys", ""},
		"oct beside a broken key": {fmt.Sprintf(`{"keys":[{"kty":"oct","kid":"hmac","k":"c2VjcmV0"},{"kty":"EC","crv":"P-256","kid":"ec","x":%q,"y":%q}]}`, x, x),
			`no usable key in what was given: EC P-256, kid "ec" cannot be used: its x and y are not a point on P-256, ` +
				`so no signature can verify against it; 48-bit shared secret, kid "hmac" is a shared secret (kty oct), which --key does not take`,
			"rta codec jwk"},
	} {
		_, verr := verifyWith(t, hs, tc.set, "")
		if verr == nil || verr.Code != "codec.jwt.key" || verr.Message != tc.want || !strings.Contains(verr.Hint, tc.hint) {
			t.Errorf("%s: got %+v, want codec.jwt.key %q with a hint naming %q", name, verr, tc.want, tc.hint)
		}
	}
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
	set := `{"keys":[` + rfc8037Public + `]}`
	for name, secret := range map[string]string{
		"PEM":              publicPEM(t, &key.PublicKey),
		"bare base64 SPKI": bare,
		"DER":              string(der),
		"PKCS#1 DER":       string(x509.MarshalPKCS1PublicKey(&key.PublicKey)),
		"certificate":      base64.StdEncoding.EncodeToString(certDER),
		// Every reading of the file is a secret it may be taken as, and the
		// one without a final line break was never looked at: DER with a
		// newline after it parses as nothing, and without one as the key.
		"DER and a line break": string(der) + "\n",
		"RSA JWK":              rfc7638Key,
		"key set":              set,
		// What an editor or a shell saves a key as: a byte-order mark in
		// front (older Notepad), UTF-16 (PowerShell 5.1's `>`), and more
		// than one line break or a space after DER, which x509 refuses as
		// trailing data. Each was taken as HMAC bytes, and VERIFIED a
		// token forged with them.
		"JWK after a byte-order mark":            byteOrderMark + rfc7638Key,
		"key set after a byte-order mark, CRLF":  byteOrderMark + set + "\r\n",
		"base64 DER after a byte-order mark":     byteOrderMark + bare,
		"JWK in UTF-16, little-endian, CRLF":     utf16Of(rfc7638Key+"\r\n", false),
		"PEM in UTF-16, little-endian":           utf16Of(publicPEM(t, &key.PublicKey), false),
		"PEM in UTF-16, big-endian":              utf16Of(publicPEM(t, &key.PublicKey), true),
		"DER and two line breaks":                string(der) + "\n\n",
		"DER and a space":                        string(der) + " ",
		"certificate DER and a CRLF line break":  string(certDER) + "\r\n",
		"key set with a byte-order mark, spaced": byteOrderMark + "  " + set,
		// A second mark: the text had only the first taken off, the base64
		// reading choked on the other, and the raw bytes were the HMAC key.
		"base64 DER after two byte-order marks": byteOrderMark + byteOrderMark + bare,
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
	// A key set saved with a byte-order mark was "not a JWK, a key set or
	// PEM" in --key, with a hint to pass the file as --secret-file — where it
	// verified the forgery. It is read as the key set it is.
	a4 := "eyJhbGciOiJFZERTQSJ9.RXhhbXBsZSBvZiBFZDI1NTE5IHNpZ25pbmc." +
		"hgyY0il_MGCjP0JzlnLWG1PPOt7-09PGcvMg3AIbQR6dWbhijcNR4ki4iylGjg5BhVsPt9g7sVvpAr_MuM0KAg"
	mustVerify(t, a4, byteOrderMark+set, "", "EdDSA signature matches Ed25519")
}

// utf16Of is s as a UTF-16 file, the byte-order mark first.
func utf16Of(s string, bigEndian bool) string {
	var b []byte
	for _, u := range utf16.Encode([]rune(byteOrderMark + s)) {
		if bigEndian {
			b = append(b, byte(u>>8), byte(u))
		} else {
			b = append(b, byte(u), byte(u>>8))
		}
	}
	return string(b)
}

// A raw public key in bare base64, an Ed25519 x or an EC point, is nothing
// publicKeyIn can tell from a random secret of the same length, so the hint
// is the only guard there is. The refusal sent it to the secret file, where a
// token HMAC'd with the key's bytes then verified: the forgery, reached
// through the refusal's own pointer. Base64 is told how a raw key goes in
// instead, on every surface; only text that is no base64 at all is still told
// where a secret goes.
func TestARawKeyInBase64IsNotSentToTheSecret(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := sign(`{"alg":"HS256"}`, `{"sub":"admin"}`, func(in []byte) []byte {
		mac := hmac.New(sha256.New, pub)
		mac.Write(in)
		return mac.Sum(nil)
	})
	for _, surface := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI, plugin.SurfaceMCP} {
		_, err := runJWT(context.Background(), req(map[string]any{"token": forged, "key": base64.StdEncoding.EncodeToString(pub)}).
			WithSurface(surface))
		verr := view.AsError(err, "test")
		if err == nil || verr.Code != "codec.jwt.key" || strings.Contains(verr.Hint, "secret-file") ||
			!strings.Contains(verr.Hint, `{"kty":"OKP","crv":"Ed25519","x":…}`) {
			t.Errorf("%s: got %v (hint %q), want a raw key told to go in as a JWK and not sent to the secret", surface, err, verr.Hint)
		}
	}
	mustRefuse(t, forged, "hunter2!", "", "codec.jwt.key", "--secret-file")
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

// repairPEM looked for the footer from the start of the text rather than
// after the header, so a one-line paste whose END marker comes first sliced
// backwards and crashed the CLI and the TUI: an empty block, whose footer
// starts inside the header's own closing dashes, and a paste that begins
// mid-chain at the previous block's END. The empty block is refused as
// holding no key, and the key after a stray END is read.
func TestAOneLinePEMWhoseFooterComesFirstIsReadRatherThanCrashing(t *testing.T) {
	key := rsaKey()
	rs := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	for _, material := range []string{
		"-----BEGIN PUBLIC KEY-----END PUBLIC KEY-----",
		"-----BEGIN CERTIFICATE-----END CERTIFICATE-----",
		"-----END PUBLIC KEY----- -----BEGIN PUBLIC KEY----- AAAA",
	} {
		mustRefuse(t, rs, material, "", "codec.jwt.key", "no public key, private key or certificate in the PEM given")
	}
	midChain := "AB -----END PUBLIC KEY----- " + strings.ReplaceAll(publicPEM(t, &key.PublicKey), "\n", " ")
	mustVerify(t, rs, midChain, "", "2048-bit RSA from PEM")
}

// crypto/rsa refuses a key under 1024 bits and an even modulus inside the
// check, and the refusal came back as a plain mismatch: a correct signature
// from a 512-bit key read as a token changed after it was signed. Both are
// named instead, by the verifier and by codec.jwk.
func TestAnRSAKeyTooSmallOrEvenIsNamedAsSuch(t *testing.T) {
	p, err := rand.Prime(rand.Reader, 256)
	if err != nil {
		t.Fatal(err)
	}
	q, err := rand.Prime(rand.Reader, 256)
	if err != nil {
		t.Fatal(err)
	}
	b := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	small := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, b(new(big.Int).Mul(p, q)))
	evenN := new(big.Int).Lsh(big.NewInt(1), 2047)
	even := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, b(evenN))
	token := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func([]byte) []byte { return make([]byte, 64) })
	mustRefuse(t, token, small, "", "codec.jwt.nokey", "under the 1024 bits")
	mustRefuse(t, token, even, "", "codec.jwt.nokey", "even modulus")
	if n := notes(jwk(t, small)); !strings.Contains(n, "can be factored") {
		t.Errorf("512-bit notes = %q", n)
	}
	if n := notes(jwk(t, even)); !strings.Contains(n, "Its n is even") {
		t.Errorf("even notes = %q", n)
	}
	// Whatever else crypto/rsa refuses a key for is passed on, not read as
	// a mismatch.
	if ok, why := checkPKCS1(&rsa.PublicKey{N: evenN, E: 65537}, nil, []byte("x"), make([]byte, 256), crypto.SHA256); ok ||
		!strings.Contains(why, "public modulus is even") {
		t.Errorf("checkPKCS1 = %v, %q, want crypto/rsa's reason", ok, why)
	}
}

// RFC 7518 §3.3 requires 2048 bits, and a strict library refuses less. The
// same 1024-bit key was qualified as a JWK and a bare VERIFIED as PEM, since
// only readJWK said anything about its size.
func TestAnRSAKeyUnder2048BitsIsNamedFromPEMAsFromAJWK(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	token := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	b := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	const want = "About that key: its modulus is 1024 bits, under the 2048 RFC 7518 §3.3 requires."
	mustVerify(t, token, publicPEM(t, &key.PublicKey), "", want)
	mustVerify(t, token, fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, b(key.N)), "", want)
}

// Checking an RSA signature costs the square of the modulus, and crypto/rsa
// sets no ceiling on it: one free call with a key of a million bits held a
// CPU core for twenty seconds. A key over the ceiling is refused by name
// before any check, as a JWK — which codec.jwk names too — and as PEM.
func TestAnRSAKeyOverTheCeilingIsRefusedBeforeAnyCheck(t *testing.T) {
	n := new(big.Int).Lsh(big.NewInt(1), 19999)
	n.SetBit(n, 0, 1)
	b := base64.RawURLEncoding.EncodeToString
	key := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":%q}`, b(n.Bytes()), b(big.NewInt(1<<31-1).Bytes()))
	token := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func([]byte) []byte { return make([]byte, 2500) })
	mustRefuse(t, token, key, "", "codec.jwt.key", "its modulus is 20000 bits, over the 16384 a verifier accepts")
	if got := notes(jwk(t, key)); !strings.Contains(got, "over the 16384 a verifier accepts") {
		t.Errorf("codec.jwk notes = %q, want the size named", got)
	}
	der, err := x509.MarshalPKIXPublicKey(&rsa.PublicKey{N: n, E: 65537})
	if err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, token, pemOf(t, "PUBLIC KEY", der), "", "codec.jwt.nokey",
		"20000-bit RSA from PEM is over the 16384 bits a verifier accepts")
}

// Every key that fits is tried against every signature, so the cost of one
// call is their product whatever the size of the keys: 200 keys without a
// kid against a JSON JWS of 200 signatures took thirteen seconds. The
// signatures a checked JWS may carry and the key checks one call makes are
// both bounded, and a call whose caller has gone stops between keys.
func TestTheWorkOneCheckDoesIsBounded(t *testing.T) {
	payload := seg(`{"sub":"a"}`)
	sigs := make([]string, maxSignatures+1)
	for i := range sigs {
		sigs[i] = fmt.Sprintf(`{"protected":%q,"signature":%q}`, seg(`{"alg":"RS256"}`), seg("s"))
	}
	many := fmt.Sprintf(`{"payload":%q,"signatures":[%s]}`, payload, strings.Join(sigs, ","))
	mustRefuse(t, many, rfc7638Key, "", "codec.jwt.invalid", fmt.Sprintf("checks at most %d", maxSignatures))
	// Decoded without a key, a signature costs no more than its header.
	if body := verification(t, jose(t, many)); !strings.Contains(body, fmt.Sprintf("none of its %d signatures", maxSignatures+1)) {
		t.Errorf("decoded without a key: verification = %q", body)
	}

	anonymous := strings.Replace(strings.Replace(rfc7638Key, `,"kid":"2011-04-29"`, "", 1), `,"alg":"RS256"`, "", 1)
	keys := make([]string, maxKeyChecks+1)
	for i := range keys {
		keys[i] = anonymous
	}
	set := `{"keys":[` + strings.Join(keys, ",") + `]}`
	token := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func([]byte) []byte { return make([]byte, 256) })
	mustRefuse(t, token, set, "", "codec.jwt.key", fmt.Sprintf("more than the %d key checks one call makes", maxKeyChecks))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runJWT(ctx, req(map[string]any{"token": token, "key": rfc7638Key}))
	if verr := view.AsError(err, "test"); err == nil || verr.Code != "codec.jwt.cancelled" {
		t.Errorf("a cancelled call: got %v, want codec.jwt.cancelled", err)
	}
}

// pkcs1Key is RFC 8017's RSAPrivateKey, spelled out so a test can put in it
// numbers x509 would never marshal.
type pkcs1Key struct {
	Version               int
	N                     *big.Int
	E                     int
	D, P, Q, Dp, Dq, Qinv *big.Int
	AdditionalPrimes      []pkcs1Prime `asn1:"optional,omitempty"`
}

type pkcs1Prime struct{ Prime, Exp, Coeff *big.Int }

// x509 checks a private key's numbers against each other as it parses it,
// at the square of their size and more, and before fits sees the modulus: a
// PKCS #1 key of 132 KB, a small modulus beside a prime of a million bits,
// held a CPU core for five seconds before it was refused as an invalid prime,
// from a free call over MCP. A number over the ceiling, or more DER than a
// key under it takes, is refused before the parse, and a key at the ceiling
// is not.
func TestAPrivateKeyIsMeasuredBeforeItIsParsed(t *testing.T) {
	key := rsaKey()
	token := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func([]byte) []byte { return make([]byte, 256) })
	marshal := func(k pkcs1Key) []byte {
		der, err := asn1.Marshal(k)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	pkcs8 := func(inner []byte) []byte {
		der, err := asn1.Marshal(struct {
			Version    int
			Algo       pkix.AlgorithmIdentifier
			PrivateKey []byte
		}{0, pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}, Parameters: asn1.NullRawValue}, inner})
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	whole := pkcs1Key{0, key.N, key.E, key.D, key.Primes[0], key.Primes[1],
		key.Precomputed.Dp, key.Precomputed.Dq, key.Precomputed.Qinv, nil}
	hugePrime := whole
	hugePrime.P = new(big.Int).SetBit(new(big.Int).Lsh(big.NewInt(1), 19999), 0, 1)
	manyPrimes := whole
	manyPrimes.Version = 1
	for range 40 {
		manyPrimes.AdditionalPrimes = append(manyPrimes.AdditionalPrimes, pkcs1Prime{key.Primes[0], key.Primes[0], key.Primes[0]})
	}
	const prime = "holds a number of 20000 bits, over the 16384 a verifier accepts"
	for name, tc := range map[string]struct{ material, want string }{
		"PKCS #1, a prime over the ceiling":   {pemOf(t, "RSA PRIVATE KEY", marshal(hugePrime)), prime},
		"PKCS #8, a prime over the ceiling":   {pemOf(t, "PRIVATE KEY", pkcs8(marshal(hugePrime))), prime},
		"PKCS #1, more than a key could take": {pemOf(t, "RSA PRIVATE KEY", marshal(manyPrimes)), "more than a key of 16384 bits takes"},
	} {
		t.Run(name, func(t *testing.T) {
			mustRefuse(t, token, tc.material, "", "codec.jwt.key", tc.want)
		})
	}

	// A key at the ceiling: its modulus and private exponent whole, and the
	// five numbers of half their size, each with its top bit set.
	full := func(bits int) *big.Int {
		return new(big.Int).SetBit(new(big.Int).Lsh(big.NewInt(1), uint(bits-1)), 0, 1)
	}
	half := full(maxRSABits / 2)
	ceiling := marshal(pkcs1Key{0, full(maxRSABits), 65537, full(maxRSABits), half, half, half, half, half, nil})
	for _, der := range [][]byte{ceiling, pkcs8(ceiling)} {
		if why := oversizedPrivateKey(der); why != "" {
			t.Errorf("a key of %d bits, %d bytes of DER: %s", maxRSABits, len(der), why)
		}
	}

	// Reading one costs about what a check does, so a call reads as many as
	// it makes checks: 330 of the largest took two seconds.
	signed := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	one := pemOf(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	mustVerify(t, signed, strings.Repeat(one, maxKeyChecks), "", "2048-bit RSA from PEM")
	mustRefuse(t, token, strings.Repeat(one, maxKeyChecks+1), "", "codec.jwt.key",
		fmt.Sprintf("more than %d private keys, the most one call reads", maxKeyChecks))
}

// A key crypto/rsa refuses was reported as a signature that does not match
// it, with the hint that the token had been changed, and codec.jwk, which the
// refusal points to, found nothing wrong with an even exponent. The exponents
// no RSA key has are named by the verifier and by codec.jwk, and whatever
// else crypto/rsa refuses a key for is a refusal of the key, not of the token.
func TestAnRSAKeyCryptoRSARefusesIsNotATamperedToken(t *testing.T) {
	key := rsaKey()
	token := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	b := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	even := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":%q}`, b(key.N), b(big.NewInt(65536)))
	if n := notes(jwk(t, even)); !strings.Contains(n, "Its exponent is even") {
		t.Errorf("codec.jwk notes = %q, want the even exponent named", n)
	}
	one := pemOf(t, "RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&rsa.PublicKey{N: key.N, E: 1}))
	evenPEM := pemOf(t, "RSA PUBLIC KEY", x509.MarshalPKCS1PublicKey(&rsa.PublicKey{N: key.N, E: 65536}))
	large, err := x509.MarshalPKIXPublicKey(&rsa.PublicKey{N: key.N, E: 1<<31 + 1})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ key, code, want string }{
		"even exponent, JWK":       {even, "codec.jwt.key", "its exponent is even"},
		"exponent 1, PEM":          {one, "codec.jwt.nokey", "has an exponent of 1"},
		"even exponent, PEM":       {evenPEM, "codec.jwt.nokey", "has an even exponent"},
		"exponent over 2^31, SPKI": {pemOf(t, "PUBLIC KEY", large), "codec.jwt.key", "crypto/rsa refuses 2048-bit RSA from PEM (public exponent too large)"},
	} {
		_, verr := verifyWith(t, token, tc.key, "")
		if verr == nil || verr.Code != tc.code || !strings.Contains(verr.Message, tc.want) ||
			strings.Contains(verr.Hint, "changed after it was signed") {
			t.Errorf("%s: got %+v, want %s naming %q and no word of tampering", name, verr, tc.code, tc.want)
		}
	}
}

// procTypeEncrypted is a legacy encrypted PEM key, the Proc-Type header form,
// around bytes that are not a key. These tests build their private-key blocks
// with pem.EncodeToMemory rather than spelling them out, because a private-key
// header written in a source file is what a secret scanner exists to find, and
// the blocks here are placeholders with nothing inside them.
func procTypeEncrypted(t *testing.T, body []byte) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: map[string]string{
		"Proc-Type": "4,ENCRYPTED",
		"DEK-Info":  "AES-128-CBC,00112233445566778899AABBCCDDEEFF",
	}, Bytes: body}))
}

// A key this cannot open is named as such: an encrypted or OpenSSH private
// key was "no private key in the PEM", with no hint. And a key no JWS
// algorithm here checks is named by what it is, not by its Go type.
func TestAPEMKeyThatCannotBeUsedIsNamed(t *testing.T) {
	token := sign(`{"alg":"EdDSA"}`, `{"sub":"a"}`, func([]byte) []byte { return make([]byte, 64) })
	sealed := []byte(strings.Repeat("sealed", 20))
	for want, key := range map[string]string{
		"it is encrypted":      pemOf(t, "ENCRYPTED PRIVATE KEY", sealed),
		"OpenSSH's own format": pemOf(t, "OPENSSH PRIVATE KEY", sealed),
		"blocks read are":      "-----BEGIN EC PARAMETERS-----\nBggqhkjOPQMBBw==\n-----END EC PARAMETERS-----\n",
		"encrypted: decrypt":   procTypeEncrypted(t, sealed),
	} {
		mustRefuse(t, token, key, "", "codec.jwt.key", want)
	}
	x25519, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, verr := verifyWith(t, token, publicPEM(t, x25519.PublicKey()), "")
	if verr == nil || !strings.Contains(verr.Message, "X25519 from PEM is not an Ed25519 key") || strings.Contains(verr.Message, "*ecdh") {
		t.Errorf("got %+v, want the X25519 key named as that", verr)
	}
}

// A server.pem is usually a certificate beside its encrypted private key, and
// a key this cannot open used to refuse the whole bundle, in either order,
// although the certificate beside it verifies. The key is passed over when
// something else in the PEM can be used, and still named when nothing can.
func TestACertificateBesideAKeyThatCannotBeOpenedVerifies(t *testing.T) {
	key := rsaKey()
	rs := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(11), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pemOf(t, "CERTIFICATE", certDER)
	sealed := []byte(strings.Repeat("sealed", 20))
	for name, sealed := range map[string]string{
		"PKCS#8 encrypted":    pemOf(t, "ENCRYPTED PRIVATE KEY", sealed),
		"Proc-Type encrypted": procTypeEncrypted(t, sealed),
		"OpenSSH":             pemOf(t, "OPENSSH PRIVATE KEY", sealed),
	} {
		t.Run(name, func(t *testing.T) {
			mustVerify(t, rs, cert+sealed, "", "2048-bit RSA from PEM")
			mustVerify(t, rs, sealed+cert, "", "2048-bit RSA from PEM")
		})
	}
	encrypted := pemOf(t, "ENCRYPTED PRIVATE KEY", sealed)
	mustRefuse(t, rs, encrypted, "", "codec.jwt.key", "it is encrypted: decrypt it first")
	mustRefuse(t, rs, encrypted, "", "codec.jwt.key", "openssl pkey -in <key> -pubout")
}

// What codec.jwk says about a key used to stay behind when codec.jwt used
// it: an x5c holding another key gave a bare VERIFIED, and key_ops limiting a
// key to encryption was ignored where use enc was not.
func TestWhatIsWrongWithAKeyTravelsWithItsVerdict(t *testing.T) {
	signing, x, y := ecKey(t)
	other, _, _ := ecKey(t)
	token := es256(t, signing, `{"alg":"ES256"}`)
	mismatched := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q,"x5c":[%q]}`,
		x, y, base64.StdEncoding.EncodeToString(certFor(t, other)))
	mustVerify(t, token, mismatched, "", "About that key: the first certificate in its x5c holds a different key")
	mustRefuse(t, token, fmt.Sprintf(`{"kty":"EC","crv":"P-256","key_ops":["encrypt"],"x":%q,"y":%q}`, x, y), "",
		"codec.jwt.nokey", "limited by key_ops to encrypt")
	body, verr := verifyWith(t, token, fmt.Sprintf(`{"kty":"EC","crv":"P-256","key_ops":["verify"],"x":%q,"y":%q}`, x, y), "")
	if verr != nil || strings.Contains(body, "About that key") {
		t.Errorf("a key with nothing wrong: %q, %v", body, verr)
	}
	// A private key allowed to sign checks what it signs: WebCrypto exports
	// every ECDSA and RSA signing key with key_ops ["sign"], and only its
	// public half is used. A public key claiming to sign is still refused.
	scalar, err := signing.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d := base64.RawURLEncoding.EncodeToString(scalar)
	private := fmt.Sprintf(`{"kty":"EC","crv":"P-256","key_ops":["sign"],"x":%q,"y":%q,"d":%q}`, x, y, d)
	mustVerify(t, token, private, "", "ES256 signature matches EC P-256")
	mustVerify(t, token, `{"keys":[`+private+`]}`, "", "ES256 signature matches EC P-256")
	mustRefuse(t, token, fmt.Sprintf(`{"kty":"EC","crv":"P-256","key_ops":["sign"],"x":%q,"y":%q}`, x, y), "",
		"codec.jwt.nokey", "limited by key_ops to sign")
}

// A mismatch is worded with the key that was tried. It used to name the
// first key the kid filter let through, often one skipped for its type, and
// then gave the skipped key's reason as though it explained the mismatch.
func TestAMismatchNamesTheKeyThatWasTried(t *testing.T) {
	key := rsaKey()
	rs := sign(`{"alg":"RS256","kid":"k1"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	parts := strings.Split(rs, ".")
	parts[1] = seg(`{"sub":"admin"}`)
	tampered := strings.Join(parts, ".")
	b := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	rsaJWK := func(extra string) string {
		return fmt.Sprintf(`{"kty":"RSA","kid":"k1",%s"n":%q,"e":"AQAB"}`, extra, b(key.N))
	}
	_, x, y := ecKey(t)
	for name, tc := range map[string]struct{ key, secret, want, not string }{
		"secret beside the key": {rsaJWK(""), "s3cret", `does not match 2048-bit RSA, kid "k1" (not tried: the secret in`, "does not match the secret"},
		"another type first": {fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","x":%q,"y":%q},{"kty":"RSA","n":%q,"e":"AQAB"}]}`, x, y, b(key.N)),
			"", "does not match 2048-bit RSA, key 2 of the set (not tried: EC P-256, key 1 of the set is not an RSA key)", "does not match EC"},
		"an encryption key first": {`{"keys":[` + rsaJWK(`"use":"enc",`) + `,` + rsaJWK("") + `]}`,
			"", `(not tried: 2048-bit RSA, kid "k1" is for encryption (use enc))`, "match 2048-bit RSA, kid \"k1\": "},
	} {
		_, verr := verifyWith(t, tampered, tc.key, tc.secret)
		if verr == nil || verr.Code != "codec.jwt.signature" || !strings.Contains(verr.Message, tc.want) || strings.Contains(verr.Message, tc.not) {
			t.Errorf("%s: got %+v, want codec.jwt.signature saying %q", name, verr, tc.want)
		}
	}
}

// The no-usable-key refusal read "an PS256 signature", and its hint always
// said the algorithm needs an RSA key of 2048 bits or more, even when the key
// skipped was one, refused for what it declares about itself: the reader was
// sent to its size when the cause was the key set's declaration.
func TestAKeySkippedForItsDeclarationIsNotBlamedOnItsSize(t *testing.T) {
	key := rsaKey()
	ps := sign(`{"alg":"PS256","kid":"r1"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, sha256Of(in), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	b := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	for extra, want := range map[string]string{
		`"alg":"RS384",`:         `is declared for RS384`,
		`"use":"enc",`:           `is for encryption (use enc)`,
		`"key_ops":["encrypt"],`: `is limited by key_ops to encrypt`,
	} {
		set := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"r1",%s"n":%q,"e":"AQAB"}]}`, extra, b(key.N))
		_, verr := verifyWith(t, ps, set, "")
		if verr == nil || verr.Code != "codec.jwt.nokey" || !strings.Contains(verr.Message, "no key given can check a PS256 signature: ") ||
			!strings.Contains(verr.Message, want) || strings.Contains(verr.Hint, "2048") || !strings.Contains(verr.Hint, "PS256") {
			t.Errorf("%s: got %+v, want a PS256 refusal naming %q with a hint about the declaration, not the size", extra, verr, want)
		}
	}
	// A key refused for its type is still told what the algorithm needs.
	mustRefuse(t, ps, rfc8037Public, "", "codec.jwt.nokey", "a PS256 signature needs an RSA key of 2048 bits or more")
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

// A key under the token's own kid that its members do not make was dropped,
// and the refusal said no key had that kid and that the issuer may have
// rotated its keys. It is in the set, and what is wrong with it is named.
func TestAnUnusableKeyUnderTheKidIsNamedRatherThanMissing(t *testing.T) {
	signing, x, y := ecKey(t)
	_, ox, oy := ecKey(t)
	set := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","kid":"other","x":%q,"y":%q},`+
		`{"kty":"EC","crv":"P-256","kid":"current","x":%q,"y":%q}]}`, ox, oy, x, x)
	mustRefuse(t, es256(t, signing, `{"alg":"ES256","kid":"current"}`), set, "", "codec.jwt.key", "not a point on P-256")
	_, verr := verifyWith(t, es256(t, signing, `{"alg":"ES256","kid":"current"}`), set, "")
	if verr != nil && strings.Contains(verr.Hint, "rotated") {
		t.Errorf("hint = %q, sent looking for a rotation", verr.Hint)
	}
	// A set with nothing usable says what is wrong with each key.
	lone := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, x)
	mustRefuse(t, es256(t, signing, `{"alg":"ES256"}`), lone, "", "codec.jwt.key", "no usable key in what was given: EC P-256 cannot be used: its x and y are not a point")
	// The usable key beside it still verifies a token that names it.
	mustVerify(t, es256(t, signing, `{"alg":"ES256","kid":"mine"}`),
		fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","kid":"mine","x":%q,"y":%q},{"kty":"EC","crv":"P-256","kid":"current","x":%q,"y":%q}]}`, x, y, x, x),
		"", `kid "mine"`)
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

// An encrypted token checked with a secret file alone was told that without
// --key it shows the header, and dropping --key changes nothing when the
// secret file is what asked for the check. The hint names what was given.
func TestAnEncryptedTokenIsToldWhatToLeaveOut(t *testing.T) {
	protected := seg(`{"alg":"dir","enc":"A128GCM"}`)
	compact := strings.Join([]string{protected, "", seg("iv"), seg("c"), seg("tag")}, ".")
	jsonForm := fmt.Sprintf(`{"protected":%q,"iv":%q,"ciphertext":%q,"tag":%q}`, protected, seg("iv"), seg("c"), seg("tag"))
	_, x, y := ecKey(t)
	key := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	for _, token := range []string{compact, jsonForm} {
		for want, in := range map[string]struct{ key, secret string }{
			"without --secret-file it shows the header":           {"", "s3cret"},
			"without --key it shows the header":                   {key, ""},
			"without --key and --secret-file it shows the header": {key, "s3cret"},
		} {
			_, verr := verifyWith(t, token, in.key, in.secret)
			if verr == nil || verr.Code != "codec.jwt.encrypted" || !strings.HasPrefix(verr.Hint, want) {
				t.Errorf("key %t, secret %t: got %+v, want a hint starting %q", in.key != "", in.secret != "", verr, want)
			}
		}
	}
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

// Every signature of a general JSON JWS is checked and reported. The first
// match used to be the whole answer, beside the header of a signature that
// had not verified; and when none matched, signature 1's error came alone,
// unnumbered, hiding signature 2's mismatch behind an alg nothing checks.
func TestEverySignatureOfAGeneralJSONSignatureIsReported(t *testing.T) {
	given, x, y := ecKey(t)
	other, _, _ := ecKey(t)
	key := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	payload := seg(`{"sub":"a"}`)
	signature := func(priv *ecdsa.PrivateKey, protected string) string {
		r, s, err := ecdsa.Sign(rand.Reader, priv, sha256Of([]byte(protected+"."+payload)))
		if err != nil {
			t.Fatal(err)
		}
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return base64.RawURLEncoding.EncodeToString(sig)
	}
	admin, plain := seg(`{"alg":"ES256","kid":"a","role":"admin"}`), seg(`{"alg":"ES256"}`)
	doc := fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"signature":%q},{"protected":%q,"signature":%q}]}`,
		payload, admin, signature(other, admin), plain, signature(given, plain))
	body, verr := verifyWith(t, doc, key, "")
	if verr != nil {
		t.Fatalf("refused: %s", verr.Message)
	}
	if !strings.HasPrefix(body, "Signature 2 of 2: VERIFIED") || !strings.Contains(body, "Signature 1 of 2 was not verified: the ES256 signature does not match") {
		t.Errorf("verification = %q, want signature 2 verified and signature 1 named as not", body)
	}

	es256k := seg(`{"alg":"ES256K"}`)
	failing := fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"signature":%q},{"protected":%q,"signature":%q}]}`,
		payload, es256k, seg("s"), plain, signature(other, plain))
	_, verr = verifyWith(t, failing, key, "")
	if verr == nil || verr.Code != "codec.jwt.signature" || !strings.Contains(verr.Message, "signature 1 of 2: rta cannot check") ||
		!strings.Contains(verr.Message, "signature 2 of 2: the ES256 signature does not match") {
		t.Errorf("got %+v, want the mismatch's code and every signature named", verr)
	}
}

// RFC 7797 §3 has every signature of a JWS agree about b64, and the page
// read the payload the way the first one says. A garbage signature without
// b64 in front of a good one with b64 false showed claims decoded from
// base64url under "Signature 2 of 2: VERIFIED", a reading that signature never
// signed. A conforming verifier refuses the document, and so does this, with
// a key or without.
func TestSignaturesThatDisagreeAboutB64AreRefused(t *testing.T) {
	payload := seg(`{"sub":"admin"}`)
	first, second := seg(`{"alg":"HS256"}`), seg(`{"alg":"HS256","b64":false,"crit":["b64"]}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(second + "." + payload))
	doc := fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"signature":%q},{"protected":%q,"signature":%q}]}`,
		payload, first, seg("garbage"), second, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	want := "signature 1 reads the payload as base64url, and signature 2 reads it as it is"
	mustRefuse(t, doc, "", "s3cret", "codec.jwt.invalid", want)
	if verr := joseErr(t, doc); verr.Code != "codec.jwt.invalid" || !strings.Contains(verr.Message, want) {
		t.Errorf("decoded without a key: got %s: %s", verr.Code, verr.Message)
	}
	// b64 false without crit is a third reading, and disagrees with both.
	loose := fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"signature":"c2ln"},{"protected":%q,"signature":"c2ln"}]}`,
		payload, second, seg(`{"alg":"HS256","b64":false}`))
	if verr := joseErr(t, loose); verr.Code != "codec.jwt.invalid" || !strings.Contains(verr.Message, "signature 2 sets b64 to false without listing it in crit") {
		t.Errorf("b64 false with and without crit: got %s: %s", verr.Code, verr.Message)
	}
	// Signatures that agree are read as before.
	agree := fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"signature":"c2ln"},{"protected":%q,"signature":"c2ln"}]}`,
		payload, first, seg(`{"alg":"HS256","b64":true}`))
	if got := pairValue(section(t, jose(t, agree), "payload").(view.KeyValue), "sub"); got != "admin" {
		t.Errorf("signatures that agree: sub = %q", got)
	}
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
