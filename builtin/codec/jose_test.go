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
	if got := section(t, binary, "payload").(view.Text).Body; got != "4 bytes of binary data: fffe0001" {
		t.Errorf("binary payload = %q", got)
	}
}

func TestADetachedPayloadIsNamedRatherThanRefused(t *testing.T) {
	s := jose(t, seg(`{"alg":"HS256"}`)+".."+seg("s"))
	if got := section(t, s, "payload").(view.Text).Body; !strings.Contains(got, "Detached") {
		t.Errorf("payload = %q", got)
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

// encoding/json hands every number back as float64, so a numeric sub past
// 2^53 was shown as a neighbouring number — a different user — and a nested
// one lost its last digits. HTML characters in a nested value came back as
// & and friends.
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
	s := jose(t, seg(`{"alg":"RS256","cty":"JWT"}`)+"."+seg(inner)+"."+seg("s"))
	nested := section(t, s, "nested").(view.Sections)
	if got := pairValue(section(t, nested, "claims").(view.KeyValue), "sub"); got != "inner" {
		t.Errorf("inner sub = %q", got)
	}
}

// A chain of wrappers is followed only so far: past the bound, the payload is
// shown as the text it is instead of decoded again.
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
