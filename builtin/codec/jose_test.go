package codec

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// seg is one base64url part, assembled from what it holds so a test says what
// each part *is* rather than leaving the reader to decode base64 in their head
// — and so no high-entropy literal sits beside the word "token" for a secret
// scanner to mistake for the real thing.
func seg(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func jose(t *testing.T, token string) view.Sections {
	t.Helper()
	v, err := runJWT(context.Background(), req(map[string]any{"token": token}))
	if err != nil {
		t.Fatalf("decoding %q: %v", token, err)
	}
	return v.(view.Sections)
}

func joseErr(t *testing.T, token string) *view.Error {
	t.Helper()
	_, err := runJWT(context.Background(), req(map[string]any{"token": token}))
	if err == nil {
		t.Fatalf("%q decoded; want a refusal", token)
	}
	return view.AsError(err, "test")
}

func section(t *testing.T, s view.Sections, id string) view.View {
	t.Helper()
	for _, item := range s.Items {
		if item.Key() == id {
			return item.View
		}
	}
	ids := make([]string, len(s.Items))
	for i, item := range s.Items {
		ids[i] = item.Key()
	}
	t.Fatalf("no %q section; have %v", id, ids)
	return nil
}

func verification(t *testing.T, s view.Sections) string {
	t.Helper()
	return section(t, s, "verification").(view.Text).Body
}

// An encrypted token is a JWT (RFC 7519 §3), and it used to be refused as
// "not a JWT". Its header travels in the clear and is what somebody debugging
// one needs: which key it is for, and which algorithms.
func TestAnEncryptedTokenShowsItsHeaderAndSaysWhatIsSealed(t *testing.T) {
	token := strings.Join([]string{
		seg(`{"alg":"RSA-OAEP-256","enc":"A256GCM","kid":"enc-1","cty":"JWT","zip":"DEF"}`),
		seg(strings.Repeat("k", 256)), seg("twelve bytes"), seg("sealed claims"), seg("sixteen-byte-tag"),
	}, ".")
	s := jose(t, token)
	header := section(t, s, "header").(view.KeyValue)
	if !pairsHave(header, "enc", "A256GCM") || !pairsHave(header, "kid", "enc-1") {
		t.Errorf("header = %+v", header.Pairs)
	}
	content := section(t, s, "content").(view.KeyValue)
	for name, want := range map[string]string{
		"encrypted key": "256 bytes", "initialization vector": "12 bytes",
		"ciphertext": "13 bytes", "authentication tag": "16 bytes",
	} {
		if got := pairValue(content, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !strings.HasPrefix(pairValue(content, "compression"), "DEF") {
		t.Errorf("compression = %q", pairValue(content, "compression"))
	}
	body := verification(t, s)
	for _, want := range []string{"ENCRYPTED", "recipient's private key (kid enc-1)", "itself a JWT"} {
		if !strings.Contains(body, want) {
			t.Errorf("verification = %q, want it to say %q", body, want)
		}
	}
}

// What decrypts a JWE depends on its key management algorithm, and naming a
// private key for a shared-key token sends somebody looking for a key pair
// that does not exist. An empty encrypted key is right for dir and wrong for
// a wrapping algorithm.
func TestAnEncryptedTokenNamesTheKeyItsAlgorithmNeeds(t *testing.T) {
	jwe := func(header, key string) string {
		return strings.Join([]string{seg(header), key, seg("iv-iv-iv-iv-"), seg("c"), seg("tag-tag-tag-tag-")}, ".")
	}
	s := jose(t, jwe(`{"alg":"dir","enc":"A128CBC-HS256"}`, ""))
	if body := verification(t, s); !strings.Contains(body, "the shared key it was encrypted with") {
		t.Errorf("dir: verification = %q", body)
	}
	if got := pairValue(section(t, s, "content").(view.KeyValue), "encrypted key"); !strings.Contains(got, "agrees the content key") {
		t.Errorf("dir: encrypted key = %q, want it to say why there is none", got)
	}
	pbes2 := jose(t, jwe(`{"alg":"PBES2-HS256+A128KW","enc":"A128GCM"}`, seg("wrapped")))
	if body := verification(t, pbes2); !strings.Contains(body, "takes that password") {
		t.Errorf("PBES2: verification = %q", body)
	}
	if body := verification(t, jose(t, jwe(`{"alg":"RSA-OAEP","enc":"A128GCM"}`, ""))); !strings.Contains(body, "incomplete") {
		t.Errorf("RSA-OAEP with no key: verification = %q, want the missing key named", body)
	}
	if body := verification(t, jose(t, jwe(`{"alg":"dir"}`, ""))); !strings.Contains(body, "names no enc") {
		t.Errorf("no enc: verification = %q", body)
	}
}

// The description is what an agent and `rta explain` read, and "the
// recipient's private key" said of every JWE is the phrase keyHeld exists to
// avoid: dir, the AES key wraps and PBES2 have no key pair to look for.
func TestTheDescriptionNamesEveryKindOfJWEKey(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID == "codec.jwt" && !strings.Contains(c.Description, "a private key, a shared key or a password") {
			t.Errorf("description = %q, want every kind of key a JWE may need named", c.Description)
		}
	}
}

// A JWS may carry anything; only a JWT's must be a claims set. RFC 8037's own
// Ed25519 example signs a sentence, and it was refused as a malformed token.
func TestASignedPayloadThatIsNotClaimsIsShownAsWhatItIs(t *testing.T) {
	rfc8037 := "eyJhbGciOiJFZERTQSJ9.RXhhbXBsZSBvZiBFZDI1NTE5IHNpZ25pbmc." +
		"hgyY0il_MGCjP0JzlnLWG1PPOt7-09PGcvMg3AIbQR6dWbhijcNR4ki4iylGjg5BhVsPt9g7sVvpAr_MuM0KAg"
	s := jose(t, rfc8037)
	if got := section(t, s, "payload").(view.Text).Body; got != "Example of Ed25519 signing" {
		t.Errorf("payload = %q", got)
	}
	if body := verification(t, s); !strings.Contains(body, "a JWS but not a JWT") {
		t.Errorf("verification = %q, want it to say why there are no claims", body)
	}

	binary := jose(t, seg(`{"alg":"HS256"}`)+"."+base64.RawURLEncoding.EncodeToString([]byte{0xff, 0xfe, 0x00, 0x01})+"."+seg("s"))
	if got := section(t, binary, "payload").(view.Text).Body; !strings.HasPrefix(got, "4 bytes, not plain text:\n00000000  ff fe 00 01") {
		t.Errorf("binary payload = %q", got)
	}
}

// A compact token's empty payload segment is detached or empty, and it
// cannot say which. The JSON form can: it detaches by leaving the member out
// (RFC 7515 Appendix F), so that is read rather than refused, and an empty
// one is the empty string every ACME POST-as-GET signs, which the page used
// to call detached.
func TestADetachedPayloadIsToldFromAnEmptyOne(t *testing.T) {
	s := jose(t, seg(`{"alg":"HS256"}`)+".."+seg("s"))
	if got := section(t, s, "payload").(view.Text).Body; !strings.Contains(got, "either the payload is detached") ||
		!strings.Contains(got, "or it is the empty string") {
		t.Errorf("compact: payload = %q, want both readings", got)
	}
	acme := seg(`{"alg":"ES256","kid":"https://acme.example/acct/1","nonce":"n","url":"https://acme.example/order/1"}`)
	for want, doc := range map[string]string{
		"Empty: an ACME POST-as-GET": fmt.Sprintf(`{"protected":%q,"payload":"","signature":%q}`, acme, seg("s")),
		"Empty: the payload is the empty string": fmt.Sprintf(`{"protected":%q,"payload":"","signature":%q}`,
			seg(`{"alg":"HS256"}`), seg("s")),
		"Detached: the payload travels separately": fmt.Sprintf(`{"protected":%q,"signature":%q}`, seg(`{"alg":"HS256"}`), seg("s")),
		"Detached: the payload travels": fmt.Sprintf(`{"signatures":[{"protected":%q,"signature":%q}]}`,
			seg(`{"alg":"HS256"}`), seg("s")),
	} {
		if got := section(t, jose(t, doc), "payload").(view.Text).Body; !strings.HasPrefix(got, want) {
			t.Errorf("%s: payload = %q, want it to start %q", doc, got, want)
		}
	}
}

// RFC 7797: with b64 false the payload is carried as it is, so decoding it as
// base64url would read garbage or fail.
func TestAnUnencodedPayloadIsReadAsItIs(t *testing.T) {
	s := jose(t, seg(`{"alg":"HS256","b64":false,"crit":["b64"]}`)+".$."+seg("s"))
	if got := section(t, s, "payload").(view.Text).Body; got != "$" {
		t.Errorf("payload = %q", got)
	}
}

// RFC 7797 §6 requires b64 in crit, because a parser that does not know the
// extension ignores it. Honoured without crit, the page showed the literal
// segment and "not a JWT" while such a library read {"sub":"admin"} under the
// same signature. Now the base64url reading is shown, and both are named.
func TestB64FalseWithoutCritIsReadBothWays(t *testing.T) {
	s := jose(t, seg(`{"alg":"HS256","b64":false}`)+"."+seg(`{"sub":"admin"}`)+"."+seg("s"))
	if got := pairValue(section(t, s, "claims").(view.KeyValue), "sub"); got != "admin" {
		t.Errorf("sub = %q, want the base64url reading", got)
	}
	if body := verification(t, s); !strings.Contains(body, "without listing b64 in crit") {
		t.Errorf("verification = %q, want the two readings named", body)
	}
	// Not base64url at all: the literal is the only reading there is.
	s = jose(t, seg(`{"alg":"HS256","b64":false}`)+".$."+seg("s"))
	if got := section(t, s, "payload").(view.Text).Body; got != "$" {
		t.Errorf("payload = %q", got)
	}
	if body := verification(t, s); !strings.Contains(body, "the payload is not base64url") {
		t.Errorf("verification = %q", body)
	}
}

// crit names what a verifier must understand, and nothing read it. An
// extension rta does not implement makes the JWS invalid to it (RFC 7515
// §4.1.11) whatever the signature says; so does a crit that is not a list of
// names, or one outside the protected header.
func TestWhatCritListsIsNamed(t *testing.T) {
	for want, token := range map[string]string{
		"lists \"x-unknown\", which rta does not implement": seg(`{"alg":"HS256","crit":["x-unknown"],"x-unknown":1}`) + "." + seg(`{}`) + "." + seg("s"),
		"is not a non-empty list of names":                  seg(`{"alg":"HS256","crit":"b64"}`) + "." + seg(`{}`) + "." + seg("s"),
		"The unprotected header carries crit": fmt.Sprintf(`{"payload":%q,"protected":%q,"header":{"crit":["b64"]},"signature":%q}`,
			seg(`{}`), seg(`{"alg":"HS256"}`), seg("s")),
	} {
		if body := verification(t, jose(t, token)); !strings.Contains(body, want) {
			t.Errorf("verification = %q, want %q", body, want)
		}
	}
	// b64 is the one extension rta implements.
	if body := verification(t, jose(t, seg(`{"alg":"HS256","b64":false,"crit":["b64"]}`)+".$."+seg("s"))); strings.Contains(body, "does not implement") {
		t.Errorf("b64 in crit was named as unknown: %q", body)
	}
}

// encoding/json hands every number back as float64, so a numeric sub past
// 2^53 was shown as a neighbouring number — a different user — and a nested
// one lost its last digits. HTML characters in a nested value came back as
// \u0026 and friends.
func TestNumbersAndNestedValuesAreShownExactlyAsTheTokenWritesThem(t *testing.T) {
	s := jose(t, buildJWT(t, `{"alg":"HS256"}`,
		`{"sub":9007199254740993,"org":{"id":12345678901234567890},"groups":["a&b<c>"],"ratio":0.1}`))
	claims := section(t, s, "claims").(view.KeyValue)
	for name, want := range map[string]string{
		"sub": "9007199254740993", "org": `{"id":12345678901234567890}`, "groups": `["a&b<c>"]`, "ratio": "0.1",
	} {
		if got := pairValue(claims, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// Every renderer strips terminal sequences, so a hostile claim cannot reach
// the terminal — but stripping cannot say it happened, and `x OSC 52 … y`
// displayed as `xy`. A decoder has to show what the token carries.
func TestControlAndInvisibleCharactersAreShownEscapedAndNamed(t *testing.T) {
	// The invisible characters are planted by code point rather than typed:
	// a source file holding a literal bidi override is the Trojan Source
	// shape, and an editor shows nothing where one sits.
	plant := strings.NewReplacer("<RLO>", string(rune(0x202e)), "<ZWSP>", string(rune(0x200b)))
	escaped := strings.NewReplacer("<RLO>", `\`+"u202e", "<ZWSP>", `\`+"u200b")
	s := jose(t, buildJWT(t, `{"alg":"HS256"}`,
		plant.Replace(`{"sub":"x\u001b]52;c;aGVsbG8=\u0007y","name":"ad<RLO>min","plain":"ok","deep":["<ZWSP>z"]}`)))
	claims := section(t, s, "claims").(view.KeyValue)
	for name, want := range map[string]string{
		"sub": `"x\x1b]52;c;aGVsbG8=\ay"`, "name": escaped.Replace(`"ad<RLO>min"`), "plain": "ok",
		"deep": escaped.Replace(`["<ZWSP>z"]`),
	} {
		if got := pairValue(claims, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	body := verification(t, s)
	if !strings.Contains(body, "shown quoted") || !strings.Contains(body, "deep, name, sub") {
		t.Errorf("verification = %q, want the escaped claims named", body)
	}
}

// A member named twice is how two JWT libraries come to read one token
// differently. RFC 7515 lets a parser keep the last, which is what is shown —
// and the page says so rather than choosing silently.
func TestAMemberNamedTwiceIsNamed(t *testing.T) {
	s := jose(t, seg(`{"alg":"RS256","alg":"none"}`)+"."+seg(`{"sub":"a","sub":"b"}`)+"."+seg("s"))
	if got := pairValue(section(t, s, "header").(view.KeyValue), "alg"); got != "none" {
		t.Errorf("alg = %q, want the last value", got)
	}
	body := verification(t, s)
	if !strings.Contains(body, "The header names alg more than once") ||
		!strings.Contains(body, "The claims set names sub more than once") {
		t.Errorf("verification = %q, want both duplicates named", body)
	}
}

// The JSON serialization keeps what matters in nested objects, and a repeat
// there was folded into the last value without a word: two protected members
// in one signature swap the signed header, and a repeated alg in a
// recipient's header chooses how its key is wrapped. Each is named by where
// it sits.
func TestAMemberNamedTwiceInsideTheJSONSerializationIsNamed(t *testing.T) {
	for want, doc := range map[string]string{
		"signatures[0].protected": fmt.Sprintf(`{"payload":%q,"signatures":[{"protected":%q,"protected":%q,"signature":%q}]}`,
			seg(`{"sub":"a"}`), seg(`{"alg":"none"}`), seg(`{"alg":"RS256"}`), seg("sig")),
		"header.kid": fmt.Sprintf(`{"payload":%q,"protected":%q,"header":{"kid":"a","kid":"b"},"signature":%q}`,
			seg(`{"sub":"a"}`), seg(`{"alg":"RS256"}`), seg("sig")),
		"recipients[0].header.alg": fmt.Sprintf(`{"protected":%q,"recipients":[{"header":{"alg":"RSA-OAEP","alg":"dir"},"encrypted_key":""}],"iv":%q,"ciphertext":%q,"tag":%q}`,
			seg(`{"enc":"A128GCM"}`), seg("iv"), seg("c"), seg("tag")),
	} {
		if body := verification(t, jose(t, doc)); !strings.Contains(body, "The JSON serialization names "+want+" more than once") {
			t.Errorf("%s: verification = %q, want the repeat named by its path", want, body)
		}
	}
}

// encoding/json reads a byte that is not UTF-8, and an escape of half a
// surrogate pair, as U+FFFD without an error: three different subjects showed
// as one, with nothing said, and two names differing in such a byte were
// reported as one name given twice.
func TestTextThatIsNotUTF8IsNamedRatherThanShownAsReplaced(t *testing.T) {
	replacement := string(rune(0xfffd))
	for want, claims := range map[string]string{
		"is not valid UTF-8":         `{"sub":"admin` + "\xff" + `"}`,
		"half of a UTF-16 surrogate": `{"sub":"admin` + `\` + `ud800"}`,
		"may be two different names": `{"sub` + "\xff" + `":1,"sub` + "\xfe" + `":2}`,
	} {
		body := verification(t, jose(t, buildJWT(t, `{"alg":"HS256"}`, claims)))
		if !strings.Contains(body, "The claims set ") || !strings.Contains(body, want) {
			t.Errorf("%q: verification = %q, want it to say the text %s", claims, body, want)
		}
	}
	// A pair is one character, an escaped backslash is not the start of an
	// escape, and the replacement character written as itself is text.
	for _, claims := range []string{
		`{"sub":"` + `\` + `ud83d` + `\` + `ude00"}`,
		`{"sub":"` + `\\` + `ud800"}`,
		`{"sub":"admin` + replacement + `"}`,
	} {
		if body := verification(t, jose(t, buildJWT(t, `{"alg":"HS256"}`, claims))); strings.Contains(body, "U+FFFD") {
			t.Errorf("%q: verification = %q, want no replacement named", claims, body)
		}
	}
}

// A padded or standard-alphabet segment is what gets a token refused by a
// strict library, and exactly what somebody pastes it here to find out.
func TestAForgivenDialectIsNamed(t *testing.T) {
	// 22 bytes, so its encoding needs padding: a length divisible by three
	// would encode identically either way and prove nothing.
	header := `{"alg":"HS256","n":12}`
	padded := base64.URLEncoding.EncodeToString([]byte(header)) + "." + seg(`{"a":1}`) + "." + seg("s")
	if !strings.Contains(padded, "=") {
		t.Fatal("the fixture needs a header whose encoding is padded")
	}
	s := jose(t, padded)
	if body := verification(t, s); !strings.Contains(body, "The header is padded base64") {
		t.Errorf("verification = %q", body)
	}
}

// A last character whose unused bits are not zero decodes, leniently, to the
// same bytes as the canonical one: "cx" and "cw" are both the byte 's'. A
// strict library refuses it, and a deny list keyed on the text sees another
// token, so it is named — and a canonical segment draws nothing.
func TestANonCanonicalSegmentIsNamed(t *testing.T) {
	body := verification(t, jose(t, seg(`{"alg":"HS256"}`)+"."+seg(`{"sub":"a"}`)+".cx"))
	if !strings.Contains(body, "The signature ends in a character whose unused bits are not zero") {
		t.Errorf("verification = %q, want the non-canonical signature named", body)
	}
	if body := verification(t, jose(t, seg(`{"alg":"HS256"}`)+"."+seg(`{"sub":"a"}`)+".cw")); strings.Contains(body, "unused bits") {
		t.Errorf("a canonical segment was named: %q", body)
	}
}

// What surrounds a token copied from where it was found: the header line, the
// scheme, the quotes of the JSON it sat in, a line break from a wrapped
// terminal.
func TestATokenIsFoundInsideWhatItWasCopiedWith(t *testing.T) {
	token := buildJWT(t, `{"alg":"HS256"}`, `{"sub":"found"}`)
	for _, wrapped := range []string{
		"Authorization: Bearer " + token,
		"authorization:bearer\t" + token,
		"BEARER " + token,
		"DPoP " + token,
		"DPoP: " + token,
		`"` + token + `"`,
		"'" + token + "'",
		`"Bearer ` + token + `"`,
		`Authorization: "Bearer ` + token + `"`,
		`"Authorization: Bearer ` + token + `"`,
		`Bearer '` + token + `'`,
		token[:20] + "\n  " + token[20:],
	} {
		s := jose(t, wrapped)
		if got := pairValue(section(t, s, "claims").(view.KeyValue), "sub"); got != "found" {
			t.Errorf("%q: sub = %q", wrapped, got)
		}
	}
}

func TestAWrongNumberOfPartsIsNamed(t *testing.T) {
	if verr := joseErr(t, "a.b.c.d"); !strings.Contains(verr.Message, "4 dot-separated parts") {
		t.Errorf("message = %q", verr.Message)
	}
	if verr := joseErr(t, "nodots"); !strings.Contains(verr.Message, "no dots") {
		t.Errorf("message = %q", verr.Message)
	}
}

// RFC 7515 Appendix A.7, the flattened JSON serialization: every ACME request
// is shaped like this, and it used to be "1 dot-separated part".
func TestAFlattenedJSONSignatureIsRead(t *testing.T) {
	s := jose(t, `{
	  "payload": "eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ",
	  "protected": "eyJhbGciOiJFUzI1NiJ9",
	  "header": {"kid": "2011-04-29"},
	  "signature": "DtEhU3ljbEg8L38VWAfUAqOyKAM6-Xx-F4GawxaepmXFCgfTjDxw5djxLa8ISlSApmWQxfKTUJqPP3-Kg6NU1Q"
	}`)
	if got := pairValue(section(t, s, "header").(view.KeyValue), "alg"); got != "ES256" {
		t.Errorf("protected alg = %q", got)
	}
	if got := pairValue(section(t, s, "unprotected").(view.KeyValue), "kid"); got != "2011-04-29" {
		t.Errorf("unprotected kid = %q", got)
	}
	if got := pairValue(section(t, s, "payload").(view.KeyValue), "iss"); got != "joe" {
		t.Errorf("iss = %q", got)
	}
	body := verification(t, s)
	if !strings.HasPrefix(body, "NOT VERIFIED") || !strings.Contains(body, "not covered by the signature") {
		t.Errorf("verification = %q", body)
	}
}

func TestAGeneralJSONSignatureListsEverySignature(t *testing.T) {
	s := jose(t, fmt.Sprintf(`{"payload":%q,"signatures":[
		{"protected":%q,"signature":%q},
		{"protected":%q,"header":{"kid":"b","alg":"x"},"signature":""}]}`,
		seg(`{"sub":"many"}`), seg(`{"alg":"RS256","kid":"a"}`), seg("sig"), seg(`{"alg":"ES256"}`)))
	first := section(t, s, "signature-1").(view.Sections)
	if got := pairValue(section(t, first, "header").(view.KeyValue), "kid"); got != "a" {
		t.Errorf("signature 1 kid = %q", got)
	}
	second := section(t, s, "signature-2").(view.Sections)
	if got := pairValue(section(t, second, "unprotected").(view.KeyValue), "kid"); got != "b" {
		t.Errorf("signature 2 kid = %q", got)
	}
	body := verification(t, s)
	for _, want := range []string{"none of its 2 signatures", "Signature 2: UNSIGNED", "RFC 7515 §7.2.1 forbids"} {
		if !strings.Contains(body, want) {
			t.Errorf("verification = %q, want %q", body, want)
		}
	}
}

func TestAJSONEncryptionIsRead(t *testing.T) {
	flattened := fmt.Sprintf(`{"protected":%q,"unprotected":{"jku":"https://example.com/keys"},
		"header":{"alg":"A128KW","kid":"7"},"encrypted_key":%q,"iv":%q,"ciphertext":%q,"tag":%q,"aad":%q}`,
		seg(`{"enc":"A128CBC-HS256"}`), seg("24-byte-wrapped-key-here"), seg("iv"), seg("secret"), seg("tag"), seg("aad"))
	s := jose(t, flattened)
	if got := pairValue(section(t, s, "recipient").(view.KeyValue), "alg"); got != "A128KW" {
		t.Errorf("recipient alg = %q", got)
	}
	if got := pairValue(section(t, s, "content").(view.KeyValue), "additional authenticated data"); got != "3 bytes" {
		t.Errorf("aad = %q", got)
	}
	if body := verification(t, s); !strings.Contains(body, "the shared key it was encrypted with (kid 7)") {
		t.Errorf("verification = %q", body)
	}

	general := fmt.Sprintf(`{"protected":%q,"recipients":[{"header":{"alg":"RSA1_5"},"encrypted_key":%q},
		{"header":{"alg":"A128KW"},"encrypted_key":%q}],"iv":%q,"ciphertext":%q,"tag":%q}`,
		seg(`{"enc":"A128CBC-HS256"}`), seg("one"), seg("two"), seg("iv"), seg("secret"), seg("tag"))
	s = jose(t, general)
	section(t, s, "recipient-2")
	if body := verification(t, s); !strings.Contains(body, "each of its 2 recipients") {
		t.Errorf("verification = %q", body)
	}
}

// RFC 7516 §7.2.1 makes a JWE's three header levels disjoint, and only a
// signer's own two were ever compared: alg in all three read as the
// protected one's with nothing said. And the unprotected-header note looked
// at the first recipient alone.
func TestAJSONEncryptionsHeaderLevelsAreCheckedForOverlap(t *testing.T) {
	three := fmt.Sprintf(`{"protected":%q,"unprotected":{"alg":"dir"},"recipients":[{"header":{"alg":"RSA-OAEP"},"encrypted_key":%q}],
		"iv":%q,"ciphertext":%q,"tag":%q}`, seg(`{"enc":"A128GCM","alg":"A128KW"}`), seg("k"), seg("iv"), seg("c"), seg("tag"))
	want := "The protected header, the shared unprotected header and the recipient's header all name alg, which RFC 7516 §7.2.1 forbids"
	if body := verification(t, jose(t, three)); !strings.Contains(body, want) {
		t.Errorf("verification = %q, want %q", body, want)
	}
	second := fmt.Sprintf(`{"protected":%q,"recipients":[{"encrypted_key":%q},{"header":{"alg":"A128KW"},"encrypted_key":%q}],
		"iv":%q,"ciphertext":%q,"tag":%q}`, seg(`{"enc":"A128GCM","alg":"RSA-OAEP"}`), seg("k"), seg("k"), seg("iv"), seg("c"), seg("tag"))
	body := verification(t, jose(t, second))
	for _, want := range []string{"not covered by the authentication tag", "The protected header and the header of recipient 2 both name alg"} {
		if !strings.Contains(body, want) {
			t.Errorf("verification = %q, want %q", body, want)
		}
	}
}

// The general and flattened syntaxes exclude each other (RFC 7515 §7.2.2,
// RFC 7516 §7.2.2), and a flattened alg-none signature beside a signatures
// list was dropped without a word: a parser reading the flattened members
// sees a header the page never showed.
func TestFlattenedMembersBesideAListAreNamed(t *testing.T) {
	jws := fmt.Sprintf(`{"payload":%q,"protected":%q,"signature":"","signatures":[{"protected":%q,"signature":%q}]}`,
		seg(`{"sub":"a"}`), seg(`{"alg":"none"}`), seg(`{"alg":"RS256"}`), seg("sig"))
	if body := verification(t, jose(t, jws)); !strings.Contains(body, "It carries a signatures list and, beside it, protected, signature") {
		t.Errorf("JWS: verification = %q", body)
	}
	jwe := fmt.Sprintf(`{"protected":%q,"encrypted_key":%q,"recipients":[{"header":{"alg":"RSA-OAEP"},"encrypted_key":%q}],
		"iv":%q,"ciphertext":%q,"tag":%q}`, seg(`{"enc":"A128GCM"}`), seg("k"), seg("k"), seg("iv"), seg("c"), seg("tag"))
	if body := verification(t, jose(t, jwe)); !strings.Contains(body, "It carries a recipients list and, beside it, encrypted_key") {
		t.Errorf("JWE: verification = %q", body)
	}
}

func TestAKeyHandedToTheTokenDecoderIsSentToTheKeyReader(t *testing.T) {
	verr := joseErr(t, `{"kty":"oct","k":"c2VjcmV0"}`)
	if verr.Code != "codec.jwt.notatoken" || !strings.Contains(verr.Hint, "rta codec jwk") {
		t.Errorf("got %s: %s (%s)", verr.Code, verr.Message, verr.Hint)
	}
}

// Sign, then sign again: the outer payload is the inner token, and cty says
// so. The inner one is decoded rather than shown as a string of base64.
func TestANestedTokenIsDecodedInPlace(t *testing.T) {
	inner := buildJWT(t, `{"alg":"HS256"}`, `{"sub":"inner"}`)
	// RFC 7515 §4.1.10: a cty with no slash is read as application/ plus
	// itself, and media types ignore case, so all three name a JWT.
	for _, cty := range []string{"JWT", "application/jwt", "Application/JWT"} {
		s := jose(t, seg(`{"alg":"RS256","cty":"`+cty+`"}`)+"."+seg(inner)+"."+seg("s"))
		nested, ok := findSection(s, "nested")
		if !ok {
			t.Errorf("cty %s: no nested token, verification = %q", cty, verification(t, s))
			continue
		}
		if got := pairValue(section(t, nested.(view.Sections), "claims").(view.KeyValue), "sub"); got != "inner" {
			t.Errorf("cty %s: inner sub = %q", cty, got)
		}
	}
	jwe := strings.Join([]string{seg(`{"alg":"dir","enc":"A128GCM","cty":"application/jwt"}`), "", seg("iv"), seg("c"), seg("tag")}, ".")
	if body := verification(t, jose(t, jwe)); !strings.Contains(body, "itself a JWT") {
		t.Errorf("JWE with cty application/jwt: verification = %q", body)
	}
}

// A chain of wrappers is followed only so far: past the bound, the payload is
// shown as the text it is instead of decoded again, and the page says why.
func TestNestingIsBounded(t *testing.T) {
	token := buildJWT(t, `{"alg":"HS256"}`, `{"sub":"bottom"}`)
	for range maxNesting + 2 {
		token = seg(`{"alg":"HS256","cty":"JWT"}`) + "." + seg(token) + "." + seg("s")
	}
	s := jose(t, token)
	depth := 0
	for {
		nested, ok := findSection(s, "nested")
		if !ok {
			break
		}
		depth++
		s = nested.(view.Sections)
	}
	if depth != maxNesting {
		t.Errorf("followed %d levels, want %d", depth, maxNesting)
	}
	// It is a token left undecoded by the bound, not a JWS that is not a JWT.
	body := verification(t, s)
	if !strings.Contains(body, fmt.Sprintf("follows only %d levels", maxNesting)) || strings.Contains(body, "not a JWT") {
		t.Errorf("innermost verification = %q, want the bound named and no claim that it is not a JWT", body)
	}
}

func findSection(s view.Sections, id string) (view.View, bool) {
	for _, item := range s.Items {
		if item.Key() == id {
			return item.View, true
		}
	}
	return nil, false
}

// The two dates a verifier refuses that are hardest to read off the numbers.
func TestAFutureIssueAndAnExpiryBeforeIssueAreNamed(t *testing.T) {
	now := time.Now()
	s := jose(t, buildJWT(t, `{"alg":"HS256"}`,
		fmt.Sprintf(`{"iat":%d,"exp":%d}`, now.Add(2*time.Hour).Unix(), now.Add(time.Hour).Unix())))
	body := verification(t, s)
	if !strings.Contains(body, "Its iat is in the future") || !strings.Contains(body, "expired before it was issued") {
		t.Errorf("verification = %q", body)
	}
}

// ACME and DPoP carry the signing key in the header, and what a person
// compares it against is its thumbprint — RFC 7638's own example key here.
func TestAKeyInTheHeaderIsNamedByItsThumbprint(t *testing.T) {
	s := jose(t, seg(`{"alg":"RS256","jwk":`+rfc7638Key+`}`)+"."+seg(`{"sub":"a"}`)+"."+seg("s"))
	if body := verification(t, s); !strings.Contains(body, "2048-bit RSA, thumbprint "+rfc7638Thumbprint) {
		t.Errorf("verification = %q", body)
	}
}

// With no argument the token comes from a pipe — on the CLI. Everywhere else
// stdin is not the call's to read, and the answer is a refusal, not a hang.
func TestNoTokenOffTheCLIIsRefusedRatherThanReadFromStdin(t *testing.T) {
	for _, s := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI} {
		_, err := runJWT(context.Background(), req(map[string]any{}).WithSurface(s))
		if verr := view.AsError(err, "test"); verr.Code != "codec.jwt.empty" {
			t.Errorf("%s: code = %q, want codec.jwt.empty", s, verr.Code)
		}
	}
}
