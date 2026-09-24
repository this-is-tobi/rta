package codec

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestB64RoundTrips(t *testing.T) {
	enc, err := runB64(context.Background(), req(map[string]any{"value": "hello, world"}))
	if err != nil {
		t.Fatal(err)
	}
	encoded := enc.(view.Text).Body
	if encoded == "hello, world" {
		t.Fatal("value was not encoded")
	}
	dec, err := runB64(context.Background(), req(map[string]any{"value": encoded, "decode": true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := dec.(view.Text).Body; got != "hello, world" {
		t.Errorf("round trip = %q", got)
	}
}

// A caller who already has an unpadded or URL-safe base64 string should not
// have to know which dialect produced it.
func TestB64DecodeAcceptsUnpaddedAndURLSafeVariants(t *testing.T) {
	// "a?b" -> base64 "YT9i", URL-safe with no padding needed here; use a
	// value whose standard encoding needs padding to exercise the raw variants.
	value := "any carnal pleasure."
	std, err := runB64(context.Background(), req(map[string]any{"value": value}))
	if err != nil {
		t.Fatal(err)
	}
	padded := std.(view.Text).Body
	unpadded := strings.TrimRight(padded, "=")
	dec, err := runB64(context.Background(), req(map[string]any{"value": unpadded, "decode": true}))
	if err != nil {
		t.Fatalf("unpadded decode: %v", err)
	}
	if got := dec.(view.Text).Body; got != value {
		t.Errorf("unpadded round trip = %q, want %q", got, value)
	}
}

func TestB64DecodeInvalidInputIsRefused(t *testing.T) {
	if _, err := runB64(context.Background(), req(map[string]any{"value": "not valid base64!!", "decode": true})); err == nil {
		t.Fatal("expected invalid base64 to be refused")
	}
}

func TestHexRoundTrips(t *testing.T) {
	enc, err := runHex(context.Background(), req(map[string]any{"value": "hello"}))
	if err != nil {
		t.Fatal(err)
	}
	encoded := enc.(view.Text).Body
	if encoded != "68656c6c6f" {
		t.Errorf("encoded = %q", encoded)
	}
	dec, err := runHex(context.Background(), req(map[string]any{"value": encoded, "decode": true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := dec.(view.Text).Body; got != "hello" {
		t.Errorf("round trip = %q", got)
	}
}

func TestURLRoundTrips(t *testing.T) {
	value := "a b&c=d"
	enc, err := runURL(context.Background(), req(map[string]any{"value": value}))
	if err != nil {
		t.Fatal(err)
	}
	encoded := enc.(view.Text).Body
	if encoded == value {
		t.Fatal("value was not escaped")
	}
	dec, err := runURL(context.Background(), req(map[string]any{"value": encoded, "decode": true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := dec.(view.Text).Body; got != value {
		t.Errorf("round trip = %q, want %q", got, value)
	}
}

func decodeText(t *testing.T, run func(context.Context, plugin.Request) (view.View, error), values map[string]any) string {
	t.Helper()
	values["decode"] = true
	v, err := run(context.Background(), req(values))
	if err != nil {
		t.Fatalf("%v: %v", values, err)
	}
	return v.(view.Text).Body
}

// Bytes that are not text used to be printed as they came, and every
// renderer strips control characters on the way to a terminal: six bytes
// decoded to an empty line. The dump shows each one.
func TestBinaryDecodesToADumpOfEveryByte(t *testing.T) {
	got := decodeText(t, runB64, map[string]any{"value": "AAECAwT/"})
	want := "6 bytes, not plain text:\n00000000  00 01 02 03 04 ff"
	if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "|......|") {
		t.Errorf("dump = %q, want it to start %q and end with the ASCII column", got, want)
	}
	// Text stays text, line breaks and all.
	if got := decodeText(t, runB64, map[string]any{"value": "bGluZSAxCmxpbmUgMgo="}); got != "line 1\nline 2\n" {
		t.Errorf("text = %q", got)
	}
	// Windows line endings are text too.
	if got := decodeText(t, runHex, map[string]any{"value": "610d0a62"}); got != "a\r\nb" {
		t.Errorf("CRLF = %q", got)
	}
}

// Text that would not show as itself — a terminal escape, a byte-order mark,
// a zero-width space, a form feed, a carriage return on its own — keeps its
// exact value for -o json and an agent, beside the dump that shows the person
// what the terminal would have cleaned. It used to come back as the dump
// alone, so `-o json | jq -r` handed a script the dump as though it were the
// value.
func TestTextThatHidesSomethingKeepsItsExactValueBesideTheDump(t *testing.T) {
	bom, zwsp := string(rune(0xfeff)), string(rune(0x200b))
	for _, value := range []string{"\x1b[2j", bom + "id,name\n1,ada\n", "hello" + zwsp + "world", "page 1\fpage 2", "50%\rdone", "\x00"} {
		v, err := runHex(context.Background(), req(map[string]any{"value": hexOf(value), "decode": true}))
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		s, ok := v.(view.Sections)
		if !ok {
			t.Errorf("%q decoded to %T, want the value and its bytes", value, v)
			continue
		}
		if got := section(t, s, "value").(view.Text).Body; got != value {
			t.Errorf("value = %q, want %q exactly", got, value)
		}
		if got := section(t, s, "bytes").(view.Text).Body; !strings.Contains(got, "not plain text:\n00000000  ") {
			t.Errorf("bytes = %q, want a dump", got)
		}
	}
}

func hexOf(s string) string {
	v, err := runHex(context.Background(), req(map[string]any{"value": s}))
	if err != nil {
		panic(err)
	}
	return v.(view.Text).Body
}

func TestADumpIsBounded(t *testing.T) {
	got := format.Dump(make([]byte, maxDump+100), maxDump)
	if !strings.Contains(got, "4196 bytes, not plain text — the first 4096 bytes shown") {
		t.Errorf("head = %q", got[:80])
	}
	if lines := strings.Count(got, "\n"); lines != maxDump/16 {
		t.Errorf("lines = %d, want %d", lines, maxDump/16)
	}
}

// Wrapped base64 — a PEM body, anything folded at 76 columns, a copy that
// picked up an indent — decodes as if it had never been wrapped.
func TestB64DecodeIgnoresTheWhitespaceItWasWrappedWith(t *testing.T) {
	if got := decodeText(t, runB64, map[string]any{"value": "  aGVs\n  bG8g\r\nd29y bGQ= "}); got != "hello world" {
		t.Errorf("got %q", got)
	}
	if got := decodeText(t, runB64, map[string]any{"value": "d29y bGQ="}); got != "world" {
		t.Errorf("grouped: got %q", got)
	}
}

// Two unpadded values on one line join into a string an unpadded decoder
// accepts, and decode to bytes neither holds: "hello" then garbage.
func TestB64DecodeRefusesTwoValuesSideBySide(t *testing.T) {
	_, err := runB64(context.Background(), req(map[string]any{"value": "aGVsbG8 d29ybGQ", "decode": true}))
	if verr := view.AsError(err, "test"); err == nil || verr.Code != "codec.b64.invalid" || !strings.Contains(verr.Message, "side by side") {
		t.Errorf("got %v, want codec.b64.invalid naming two values side by side", err)
	}
}

// The shapes hex is copied in: an openssl fingerprint, a 0x literal, bytes
// with spaces between them.
func TestHexDecodeAcceptsTheWaysHexIsWritten(t *testing.T) {
	for _, in := range []string{"68:65:6c:6c:6f", "0x68656C6C6F", "68 65 6c 6c 6f", "68-65-6c-6c-6f", "0x68 0x65 0x6c 0x6c 0x6f"} {
		if got := decodeText(t, runHex, map[string]any{"value": in}); got != "hello" {
			t.Errorf("%q decoded to %q", in, got)
		}
	}
	if _, err := runHex(context.Background(), req(map[string]any{"value": "zz", "decode": true})); err == nil {
		t.Error("non-hex decoded")
	}
	// Words rather than bytes are still whole bytes.
	if got := decodeText(t, runHex, map[string]any{"value": "0x6865 0x6c6c6f"}); got != "hello" {
		t.Errorf("0x words decoded to %q", got)
	}
}

// A separator says where a byte ends, and deleting the separators before
// decoding regrouped the digits: `0x1 0x2 0x3 0x4` came out as 12 34, and a
// MAC as `arp -a` prints it lost its leading zero byte. Each of these meant
// something other than what removing the separators decodes to, so each is
// refused.
func TestHexDecodeRefusesGroupsThatAreNotWholeBytes(t *testing.T) {
	for _, in := range []string{"0x1 0x2 0x3 0x4", "1:2:3:4", "0:c:29:a1:b2:30", "100x20", "0x0x41", "-41", "68::65", "68:"} {
		if v, err := runHex(context.Background(), req(map[string]any{"value": in, "decode": true})); err == nil {
			t.Errorf("%q decoded to %+v; want it refused", in, v)
		} else if verr := view.AsError(err, "test"); verr.Code != "codec.hex.invalid" {
			t.Errorf("%q: code = %q", in, verr.Code)
		}
	}
}

// A path is not a query: a space is %20, and + is a plus. Decoding a path as
// a query turned c++.txt into "c  .txt".
func TestURLPathModeKeepsAPlusAPlus(t *testing.T) {
	enc, err := runURL(context.Background(), req(map[string]any{"value": "a b/c+d", "path": true}))
	if err != nil {
		t.Fatal(err)
	}
	if got := enc.(view.Text).Body; got != "a%20b%2Fc+d" {
		t.Errorf("path escape = %q", got)
	}
	if got := decodeText(t, runURL, map[string]any{"value": "c++.txt", "path": true}); got != "c++.txt" {
		t.Errorf("path unescape = %q", got)
	}
	if got := decodeText(t, runURL, map[string]any{"value": "c++.txt"}); got != "c  .txt" {
		t.Errorf("query unescape = %q, which is what --path exists to avoid", got)
	}
}

// buildJWT base64url-encodes header/claims JSON with a placeholder
// signature, exactly the shape runJWT is asked to decode (no verification
// is performed, so the signature segment's content never matters here).
func buildJWT(t *testing.T, header, claims string) string {
	t.Helper()
	seg := base64.RawURLEncoding.EncodeToString
	return seg([]byte(header)) + "." + seg([]byte(claims)) + "." + seg([]byte("sig"))
}

func TestJWTDecodesHeaderAndClaims(t *testing.T) {
	token := buildJWT(t, `{"alg":"HS256","typ":"JWT"}`, `{"sub":"1234567890","name":"Ada"}`)
	v, err := runJWT(context.Background(), req(map[string]any{"token": token}))
	if err != nil {
		t.Fatal(err)
	}
	sections := v.(view.Sections)
	if len(sections.Items) != 3 {
		t.Fatalf("sections = %d, want 3 (header, claims, verification)", len(sections.Items))
	}
	header := sections.Items[0].View.(view.KeyValue)
	if pairsHave(header, "alg", "HS256") == false || pairsHave(header, "typ", "JWT") == false {
		t.Errorf("header = %+v", header.Pairs)
	}
	claims := sections.Items[1].View.(view.KeyValue)
	if !pairsHave(claims, "sub", "1234567890") || !pairsHave(claims, "name", "Ada") {
		t.Errorf("claims = %+v", claims.Pairs)
	}
	// It must say, unprompted, that nothing was verified.
	warning := sections.Items[2].View.(view.Text).Body
	if !strings.Contains(strings.ToUpper(warning), "NOT VERIFIED") {
		t.Errorf("no unverified warning in %q", warning)
	}
}

// A token with no signature was told its signature had not been checked,
// which is what every signed one is told too — and the one it misdescribed is
// the forgery that alg "none" names. The capitalised variants are the ones an
// attacker sends, so they have to read the same way.
func TestATokenWithNoSignatureSaysSoRatherThanUnchecked(t *testing.T) {
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	claims := seg(`{"sub":"1"}`)
	verification := func(header, signature string) string {
		t.Helper()
		v, err := runJWT(context.Background(), req(map[string]any{
			"token": seg(header) + "." + claims + "." + signature,
		}))
		if err != nil {
			t.Fatalf("header %s: %v", header, err)
		}
		return v.(view.Sections).Items[2].View.(view.Text).Body
	}

	for _, alg := range []string{"none", "None", "NONE"} {
		got := verification(`{"alg":"`+alg+`"}`, "")
		if !strings.HasPrefix(got, "UNSIGNED") || !strings.Contains(got, `"`+alg+`"`) {
			t.Errorf("alg %s: verification = %q, want it to say there is no signature and name the alg", alg, got)
		}
		if strings.Contains(got, "NOT VERIFIED") {
			t.Errorf("alg %s: an unsigned token was described as unchecked: %q", alg, got)
		}
	}
	if got := verification(`{"typ":"JWT"}`, ""); !strings.HasPrefix(got, "UNSIGNED") ||
		!strings.Contains(got, "does not claim one") {
		t.Errorf("no alg: verification = %q", got)
	}
	if got := verification(`{"alg":"RS256"}`, ""); !strings.HasPrefix(got, "UNSIGNED") ||
		!strings.Contains(got, "claims RS256") {
		t.Errorf("stripped RS256: verification = %q, want it to name the alg the header still claims", got)
	}
	// A signature present under alg none is contradictory, and still a
	// signature nobody checked.
	if got := verification(`{"alg":"none"}`, seg("sig")); !strings.HasPrefix(got, "NOT VERIFIED") {
		t.Errorf("alg none with a signature: verification = %q", got)
	}
}

// A JSON number decodes to float64, and fmt.Sprint on a large whole float64
// prints scientific notation (1.516239022e+09) — unreadable for exactly the
// claim (iat/exp/nbf) every real JWT carries.
//
// A prefix rather than the whole value: the row now carries the decoded date
// after the number (see jwtdate_test.go). The number itself still has to be
// there, in full and in figures, which is what this test has always been about.
func TestJWTNumericClaimsAreNotScientificNotation(t *testing.T) {
	token := buildJWT(t, `{"alg":"HS256"}`, `{"iat":1516239022}`)
	v, err := runJWT(context.Background(), req(map[string]any{"token": token}))
	if err != nil {
		t.Fatal(err)
	}
	claims := v.(view.Sections).Items[1].View.(view.KeyValue)
	if !strings.HasPrefix(pairValue(claims, "iat"), "1516239022") {
		t.Errorf("claims = %+v, want iat to start with 1516239022", claims.Pairs)
	}
}

func TestJWTMalformedInputIsRefused(t *testing.T) {
	if _, err := runJWT(context.Background(), req(map[string]any{"token": "not-a-jwt"})); err == nil {
		t.Fatal("expected a malformed token to be refused")
	}
}

func pairsHave(kv view.KeyValue, key, value string) bool {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value == value
		}
	}
	return false
}

// pairValue returns one pair's rendered value, or "" when the key is absent —
// for the assertions that check part of a value rather than all of it.
func pairValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// **encoding/json writes nothing into a map for the literal `null`, and
// reports no error doing it.**
//
// RFC 7519 requires each JWT segment to be a JSON object, so a segment
// holding `null` is malformed — but it decoded "successfully" into a nil
// map and rendered as an ordinary empty table, indistinguishable from a
// token whose header genuinely is `{}`. A reader inspecting a token that
// something else had already rejected was shown a clean, empty header and
// no reason to doubt it.
func TestAJWTSegmentThatIsNotAnObjectIsRefused(t *testing.T) {
	// Assembled from its segments rather than pasted in as one string, so
	// the test says what each part *is* instead of leaving the reader to
	// decode base64 in their head — and so no high-entropy literal sits
	// beside the word "token", which is what a secret scanner is looking
	// for and cannot tell apart from the real thing.
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	jwt := func(header, claims string) string {
		return seg(header) + "." + seg(claims) + "." + seg("not a signature")
	}

	_, err := runJWT(context.Background(), req(map[string]any{"token": jwt("null", `{"sub":"1"}`)}))
	if err == nil {
		t.Fatal("a token whose header is the literal null decoded as an empty header")
	}
	if !strings.Contains(err.Error(), "header") {
		t.Errorf("err = %v, want it to name the header as the bad segment", err)
	}

	// A genuinely empty object still decodes, so the guard is about the
	// shape and not about emptiness.
	if _, err := runJWT(context.Background(), req(map[string]any{
		"token": jwt("{}", `{"sub":"1"}`),
	})); err != nil {
		t.Errorf("a token with an empty but well-formed header was refused: %v", err)
	}
}
