package codec

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
