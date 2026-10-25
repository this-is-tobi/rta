package codec

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refusalOf runs the capability id names on values and returns its refusal,
// beside the capability, so the input the refusal names can be held to the
// inputs that capability declares.
func refusalOf(t *testing.T, id string, values map[string]any) (plugin.Capability, *view.Error) {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID != id {
			continue
		}
		_, err := c.Run(context.Background(), req(values))
		if err == nil {
			t.Fatalf("%s ran; want a refusal", id)
		}
		return c, view.AsError(err, "test")
	}
	t.Fatalf("no capability %s", id)
	return plugin.Capability{}, nil
}

// A refusal of what one input was given names that input (view.Error.Input):
// the TUI opens the form again on the box that holds it, and an agent is told
// which argument to send again. codec.jwt has three boxes, and a check that
// cannot be made names the one whose value is wrong: a token that does not
// decode, a key that is not one or does not hold the token's kid, a secret
// file that holds no secret, or none at all for a token whose alg needs one.
// A refusal no one box answers names none: a signature that does not match,
// a token that cannot be checked at all, two signatures each refused for its
// own reason.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	signing, x, y := ecKey(t)
	other, ox, oy := ecKey(t)
	ecJWK := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	set := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","kid":"old","x":%q,"y":%q},`+
		`{"kty":"EC","crv":"P-256","kid":"current","x":%q,"y":%q}]}`, ox, oy, x, y)
	offCurve := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, x)
	offCurveUnderKid := fmt.Sprintf(`{"keys":[{"kty":"EC","crv":"P-256","kid":"current","x":%q,"y":%q}]}`, x, x)
	oct := `{"kty":"oct","k":"c2VjcmV0"}`

	es := es256(t, signing, `{"alg":"ES256"}`)
	hs := sign(`{"alg":"HS256"}`, `{"sub":"a"}`, func([]byte) []byte { return []byte("x") })
	rs := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func(in []byte) []byte {
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey(), crypto.SHA256, sha256Of(in))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	})
	zeroed := sign(`{"alg":"RS256"}`, `{"sub":"a"}`, func([]byte) []byte { return make([]byte, 256) })

	pub := publicPEM(t, &rsaKey().PublicKey)
	spki, err := x509.MarshalPKIXPublicKey(&rsaKey().PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	hugeExponent, err := x509.MarshalPKIXPublicKey(&rsa.PublicKey{N: rsaKey().N, E: 1<<31 + 1})
	if err != nil {
		t.Fatal(err)
	}
	sealedPEM := pemOf(t, "ENCRYPTED PRIVATE KEY", []byte(strings.Repeat("sealed", 20)))
	anonymous := strings.Replace(strings.Replace(rfc7638Key, `,"kid":"2011-04-29"`, "", 1), `,"alg":"RS256"`, "", 1)
	tooManyKeys := `{"keys":[` + strings.TrimSuffix(strings.Repeat(anonymous+",", maxKeyChecks+1), ",") + `]}`

	payload := seg(`{"sub":"a"}`)
	jwe := seg(`{"alg":"dir","enc":"A128GCM"}`)
	jweParts := fmt.Sprintf(`"iv":%q,"ciphertext":%q,"tag":%q`, seg("iv"), seg("c"), seg("tag"))
	twoSignatures := func(first, firstSig, second, secondSig string) string {
		return fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"signature":%q},{"protected":%q,"signature":%q}]}`,
			payload, first, firstSig, second, secondSig)
	}
	mismatched := strings.Split(es256(t, other, `{"alg":"ES256"}`), ".")
	detached := strings.Split(es, ".")
	sigs := make([]string, maxSignatures+1)
	for i := range sigs {
		sigs[i] = fmt.Sprintf(`{"protected":%q,"signature":%q}`, seg(`{"alg":"RS256"}`), seg("s"))
	}
	tooManySignatures := fmt.Sprintf(`{"payload":%q,"signatures":[%s]}`, payload, strings.Join(sigs, ","))

	jwt := func(tok string) map[string]any { return map[string]any{"token": tok} }
	checked := func(tok, key string) map[string]any { return map[string]any{"token": tok, "key": key} }
	secretIn := func(tok, path string) map[string]any { return map[string]any{"token": tok, "secret-file": path} }
	secret := func(contents string) string { return secretFile(t, contents) }

	for name, tc := range map[string]struct {
		id     string
		values map[string]any
		code   string
		input  string
	}{
		"base64 that does not decode": {"codec.b64", map[string]any{"value": "not valid base64!!", "decode": true}, "codec.b64.invalid", "value"},
		"hex that does not decode":    {"codec.hex", map[string]any{"value": "0xzz", "decode": true}, "codec.hex.invalid", "value"},
		"an escape that is not one":   {"codec.url", map[string]any{"value": "%zz", "decode": true}, "codec.url.invalid", "value"},

		"a blank token":                                   {"codec.jwt", jwt(" \n "), "codec.jwt.empty", "token"},
		"a token with no dots":                            {"codec.jwt", jwt("not-a-jwt"), "codec.jwt.invalid", "token"},
		"a header that is not base64url":                  {"codec.jwt", jwt("e3!.e30.x"), "codec.jwt.invalid", "token"},
		"a header that is not an object":                  {"codec.jwt", jwt("W10.e30.x"), "codec.jwt.invalid", "token"},
		"a header cut short":                              {"codec.jwt", jwt("eyJhIjox.e30."), "codec.jwt.invalid", "token"},
		"a payload that is not base64url":                 {"codec.jwt", jwt("e30.e30!.x"), "codec.jwt.invalid", "token"},
		"an encrypted part that is not base64url":         {"codec.jwt", jwt(jwe + "..!!." + seg("c") + "." + seg("tag")), "codec.jwt.invalid", "token"},
		"JSON that does not parse":                        {"codec.jwt", jwt(`{"payload":`), "codec.jwt.invalid", "token"},
		"JSON that is neither a JWS nor a JWE":            {"codec.jwt", jwt(`{"iss":"a"}`), "codec.jwt.invalid", "token"},
		"a key where the token goes":                      {"codec.jwt", jwt(oct), "codec.jwt.notatoken", "token"},
		"a payload member that is not a string":           {"codec.jwt", jwt(`{"payload":1,"signature":"c2ln"}`), "codec.jwt.invalid", "token"},
		"a protected header that is not a string":         {"codec.jwt", jwt(`{"payload":"e30","protected":1,"signature":"c2ln"}`), "codec.jwt.invalid", "token"},
		"an unprotected header that is not an object":     {"codec.jwt", jwt(`{"payload":"e30","header":1,"signature":"c2ln"}`), "codec.jwt.invalid", "token"},
		"a signature that is not an object":               {"codec.jwt", jwt(`{"payload":"e30","signatures":[1]}`), "codec.jwt.invalid", "token"},
		"an empty signatures list":                        {"codec.jwt", jwt(`{"payload":"e30","signatures":[]}`), "codec.jwt.invalid", "token"},
		"signatures that disagree about b64":              {"codec.jwt", jwt(twoSignatures(seg(`{"alg":"HS256"}`), "c2ln", seg(`{"alg":"HS256","b64":false,"crit":["b64"]}`), "c2ln")), "codec.jwt.invalid", "token"},
		"a shared unprotected header that is not one":     {"codec.jwt", jwt(fmt.Sprintf(`{"protected":%q,"unprotected":1,%s}`, jwe, jweParts)), "codec.jwt.invalid", "token"},
		"a recipient that is not an object":               {"codec.jwt", jwt(fmt.Sprintf(`{"protected":%q,"recipients":[1],%s}`, jwe, jweParts)), "codec.jwt.invalid", "token"},
		"an empty recipients list":                        {"codec.jwt", jwt(fmt.Sprintf(`{"protected":%q,"recipients":[],%s}`, jwe, jweParts)), "codec.jwt.invalid", "token"},
		"a signature that does not decode, checked":       {"codec.jwt", checked(seg(`{"alg":"ES256"}`)+"."+payload+".!!", ecJWK), "codec.jwt.invalid", "token"},
		"a key that is none of the forms read":            {"codec.jwt", checked(es, "not a key at all"), "codec.jwt.key", "key"},
		"a key that is not JSON":                          {"codec.jwt", checked(es, "{"), "codec.jwt.key", "key"},
		"a public key without its PEM armour":             {"codec.jwt", checked(rs, base64.StdEncoding.EncodeToString(spki)), "codec.jwt.key", "key"},
		"a shared secret given as the key":                {"codec.jwt", checked(hs, oct), "codec.jwt.key", "key"},
		"a key set of shared secrets":                     {"codec.jwt", checked(hs, `{"keys":[`+oct+`]}`), "codec.jwt.key", "key"},
		"an empty key set":                                {"codec.jwt", checked(es, `{"keys":[]}`), "codec.jwt.key", "key"},
		"a key off its curve":                             {"codec.jwt", checked(es, offCurve), "codec.jwt.key", "key"},
		"more private keys than one call reads":           {"codec.jwt", checked(rs, strings.Repeat(sealedPEM, maxKeyChecks+1)), "codec.jwt.key", "key"},
		"a PEM block that does not parse":                 {"codec.jwt", checked(rs, pemOf(t, "PUBLIC KEY", []byte("garbage"))), "codec.jwt.key", "key"},
		"a sealed private key and nothing else":           {"codec.jwt", checked(rs, sealedPEM), "codec.jwt.key", "key"},
		"PEM holding no key":                              {"codec.jwt", checked(es, "-----BEGIN EC PARAMETERS-----\nBggqhkjOPQMBBw==\n-----END EC PARAMETERS-----\n"), "codec.jwt.key", "key"},
		"more key checks than one call makes":             {"codec.jwt", checked(zeroed, tooManyKeys), "codec.jwt.key", "key"},
		"a key crypto/rsa refuses":                        {"codec.jwt", checked(rs, pemOf(t, "PUBLIC KEY", hugeExponent)), "codec.jwt.key", "key"},
		"the key under the kid off its curve":             {"codec.jwt", checked(es256(t, signing, `{"alg":"ES256","kid":"current"}`), offCurveUnderKid), "codec.jwt.key", "key"},
		"a kid the key set does not hold":                 {"codec.jwt", checked(es256(t, signing, `{"alg":"ES256","kid":"gone"}`), set), "codec.jwt.nokey", "key"},
		"two signatures under kids the set does not hold": {"codec.jwt", checked(twoSignatures(seg(`{"alg":"ES256","kid":"gone"}`), seg("s"), seg(`{"alg":"ES256","kid":"lost"}`), seg("s")), set), "codec.jwt.nokey", "key"},
		// Signature 1 wants another key and signature 2 a secret file, and
		// either one verified is enough: no one box is the fix.
		"two signatures each refused over its own box": {"codec.jwt", checked(twoSignatures(seg(`{"alg":"ES256","kid":"gone"}`), seg("s"), seg(`{"alg":"HS256"}`), seg("s")), set), "codec.jwt.nokey", ""},
		"a secret file that is not there":              {"codec.jwt", secretIn(hs, filepath.Join(t.TempDir(), "nothing-here")), "codec.jwt.secret", "secret-file"},
		"a directory for a secret file":                {"codec.jwt", secretIn(hs, t.TempDir()), "codec.jwt.secret", "secret-file"},
		"a secret file too large to be a secret":       {"codec.jwt", secretIn(hs, secret(strings.Repeat("s", maxSecretFile+1))), "codec.jwt.secret", "secret-file"},
		"an empty secret file":                         {"codec.jwt", secretIn(hs, secret("")), "codec.jwt.secret", "secret-file"},
		"a secret file holding a line break":           {"codec.jwt", secretIn(hs, secret("\n")), "codec.jwt.secret", "secret-file"},
		"an oct key whose k does not decode":           {"codec.jwt", secretIn(hs, secret(`{"kty":"oct","k":"!!"}`)), "codec.jwt.secret", "secret-file"},
		"a key set in the secret file":                 {"codec.jwt", secretIn(hs, secret(`{"keys":[`+oct+`]}`)), "codec.jwt.secret", "secret-file"},
		"a public key in the secret file":              {"codec.jwt", secretIn(hs, secret(pub)), "codec.jwt.alg", "secret-file"},
		"an HMAC token checked with a public key only": {"codec.jwt", checked(hs, pub), "codec.jwt.alg", "secret-file"},

		"a signature that does not match":      {"codec.jwt", checked(es256(t, other, `{"alg":"ES256"}`), ecJWK), "codec.jwt.signature", ""},
		"an unsigned token checked":            {"codec.jwt", secretIn(seg(`{"alg":"none"}`)+"."+payload+".", secret("s3cret")), "codec.jwt.unsigned", ""},
		"an encrypted token checked":           {"codec.jwt", secretIn(jwe+".."+seg("iv")+"."+seg("c")+"."+seg("tag"), secret("s3cret")), "codec.jwt.encrypted", ""},
		"an alg rta cannot check":              {"codec.jwt", secretIn(sign(`{"alg":"ES256K"}`, `{}`, func([]byte) []byte { return []byte("x") }), secret("s3cret")), "codec.jwt.alg", ""},
		"a detached payload checked":           {"codec.jwt", checked(detached[0]+".."+detached[2], ecJWK), "codec.jwt.detached", ""},
		"more signatures than one call checks": {"codec.jwt", checked(tooManySignatures, rfc7638Key), "codec.jwt.invalid", ""},
		"two signatures, neither checkable":    {"codec.jwt", checked(twoSignatures(seg(`{"alg":"ES256K"}`), seg("s"), mismatched[0], mismatched[2]), ecJWK), "codec.jwt.signature", ""},
		// One code, two boxes: an RS256 token checked with a secret alone
		// wants a key, and an HS256 one whose secret declares itself for
		// another alg wants another secret file. Which is the token's alg to
		// say, not the refusal's.
		"an RS256 token checked with a secret alone":     {"codec.jwt", secretIn(rs, secret("s3cret")), "codec.jwt.nokey", ""},
		"a secret file declaring itself for another alg": {"codec.jwt", secretIn(hs, secret(`{"kty":"oct","k":"c2VjcmV0","alg":"HS512"}`)), "codec.jwt.nokey", ""},

		"a blank key":              {"codec.jwk", map[string]any{"key": "  "}, "codec.jwk.empty", "key"},
		"a key that is not a JWK":  {"codec.jwk", map[string]any{"key": "not json"}, "codec.jwk.invalid", "key"},
		"a key set with no list":   {"codec.jwk", map[string]any{"key": `{"keys":{}}`}, "codec.jwk.invalid", "key"},
		"an object that is no key": {"codec.jwk", map[string]any{"key": `{"iss":"not a key"}`}, "codec.jwk.invalid", "key"},
	} {
		t.Run(name, func(t *testing.T) {
			c, verr := refusalOf(t, tc.id, tc.values)
			if verr.Code != tc.code || verr.Input != tc.input {
				t.Fatalf("got %s naming %q (%s); want %s naming %q", verr.Code, verr.Input, verr.Message, tc.code, tc.input)
			}
			if f, ok := c.ErrorInput(verr); tc.input != "" && (!ok || f.Name != tc.input) {
				t.Errorf("%s names %q, which %s does not declare", verr.Code, verr.Input, c.ID)
			}
		})
	}
}

// A secret file that is there and that this process may not read names no
// input: the path is right, and the fix is a chmod, not the box.
func TestASecretFileThatCannotBeReadNamesNoInput(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode bits do not stop this reader")
	}
	shut := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(shut, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shut, 0o000); err != nil {
		t.Fatal(err)
	}
	_, verr := verifierFrom(context.Background(), plugin.NewRequest(map[string]any{"secret-file": shut}, false, false))
	if verr == nil || verr.Code != "codec.jwt.secret" || verr.Input != "" {
		t.Errorf("the secret file: %+v, want codec.jwt.secret naming none", verr)
	}
}
