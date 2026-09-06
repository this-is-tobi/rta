// Package codec is the built-in encode/decode plugin: base64, hex, URL
// escaping, and unverified JWT inspection. Stdlib only, no network, no
// state.
//
// All four capabilities stay Read even though jwt.decode and the *.decode
// direction of the others reveal a value in a new form — unlike kv.get, the
// caller already possesses the encoded input; decoding it does not hand them
// anything they did not already have. codec.jwt makes no
// claim about the token's authenticity: it decodes and prints the claims for
// inspection, nothing more, and says so in its own output.
package codec

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/timefmt"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Plugin returns the codec plugin declaration.
func Plugin() plugin.Plugin {
	valueField := plugin.Field{Name: "value", Type: plugin.String, Positional: true, Required: true, Help: "the text to transform"}
	decodeField := plugin.Field{Name: "decode", Type: plugin.Bool, Help: "decode instead of encode"}

	return plugin.Plugin{
		Name:    "codec",
		Summary: "Mechanical encode/decode: base64, hex, URL escaping, JWT inspection",
		Capabilities: []plugin.Capability{
			{
				ID:      "codec.b64",
				Summary: "Base64 encode or decode a value",
				Description: "Decoding accepts standard, URL-safe, and unpadded variants without being " +
					"told which — the caller already has the encoded value, so being forgiving about " +
					"which base64 dialect produced it costs nothing.",
				Safety: plugin.Read,
				Inputs: []plugin.Field{valueField, decodeField,
					{Name: "url", Type: plugin.Bool, Help: "use the URL-safe alphabet when encoding"}},
				Run: runB64,
			},
			{
				ID:      "codec.hex",
				Summary: "Hex encode or decode a value",
				Safety:  plugin.Read,
				Inputs:  []plugin.Field{valueField, decodeField},
				Run:     runHex,
			},
			{
				ID:      "codec.url",
				Summary: "Escape a value for a URL, or unescape one",
				Description: "Query-component escaping (spaces become +), the form almost everyone " +
					"means by \"URL encode this\" — the value for a query string or form body.",
				Safety: plugin.Read,
				Inputs: []plugin.Field{valueField, decodeField},
				Run:    runURL,
			},
			{
				ID:      "codec.jwt",
				Summary: "Decode a JWT's header and claims for inspection",
				Description: "Unverified by default and clearly labeled as such: this is for reading a " +
					"token while debugging, not for authenticating one. No signature check is performed " +
					"— anyone can hand you a token with any claims at all.",
				Safety: plugin.Read,
				// Secret: a JWT handed to `codec jwt` is a live bearer token far
				// more often than it is a specimen, and a String here reached
				// both the completion shortlist and the agent log intact.
				Inputs: []plugin.Field{{Name: "token", Type: plugin.Secret, Positional: true, Required: true, Help: "the JWT to decode"}},
				Run:    runJWT,
			},
		},
	}
}

func runB64(_ context.Context, req plugin.Request) (view.View, error) {
	value := req.String("value")
	if req.Bool("decode") {
		decoded, err := decodeB64(value)
		if err != nil {
			return nil, view.Errorf("codec.b64.invalid", "not valid base64: %v", err)
		}
		return view.Text{Body: string(decoded)}, nil
	}
	enc := base64.StdEncoding
	if req.Bool("url") {
		enc = base64.URLEncoding
	}
	return view.Text{Body: enc.EncodeToString([]byte(value))}, nil
}

// decodeB64 tries the dialects a caller is actually likely to hand us —
// standard and URL-safe, each padded and unpadded — before giving up.
func decodeB64(value string) ([]byte, error) {
	var lastErr error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		decoded, err := enc.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func runHex(_ context.Context, req plugin.Request) (view.View, error) {
	value := req.String("value")
	if req.Bool("decode") {
		decoded, err := hex.DecodeString(value)
		if err != nil {
			return nil, view.Errorf("codec.hex.invalid", "not valid hex: %v", err)
		}
		return view.Text{Body: string(decoded)}, nil
	}
	return view.Text{Body: hex.EncodeToString([]byte(value))}, nil
}

func runURL(_ context.Context, req plugin.Request) (view.View, error) {
	value := req.String("value")
	if req.Bool("decode") {
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			return nil, view.Errorf("codec.url.invalid", "not a valid URL-escaped value: %v", err)
		}
		return view.Text{Body: decoded}, nil
	}
	return view.Text{Body: url.QueryEscape(value)}, nil
}

func runJWT(_ context.Context, req plugin.Request) (view.View, error) {
	token := strings.TrimSpace(req.String("token"))
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, view.Errorf("codec.jwt.invalid", "not a JWT: expected 3 dot-separated parts, got %d", len(parts)).
			WithHint("a JWT looks like header.payload.signature")
	}
	header, err := decodeJSONSegment(parts[0])
	if err != nil {
		return nil, view.Errorf("codec.jwt.invalid", "decoding header: %v", err)
	}
	claims, err := decodeJSONSegment(parts[1])
	if err != nil {
		return nil, view.Errorf("codec.jwt.invalid", "decoding claims: %v", err)
	}
	body := "NOT VERIFIED — the signature was not checked. This is a debugging view of what the " +
		"token claims, not proof of who issued it."
	if w := window(claims); w != "" {
		body += "\n\n" + w
	}
	return view.Sections{Items: []view.Section{
		{ID: "header", Title: "header", View: keyValueOf(header)},
		{ID: "claims", Title: "claims", View: keyValueOf(claims)},
		{ID: "verification", Title: "verification", View: view.Text{Body: body}},
	}}, nil
}

// window states what the token's own dates say about whether it is live right
// now. That is the question somebody decoding a token in a hurry actually has,
// and a column of ten-digit integers answers it worse than anything else on
// the screen — the reader has to know today's epoch to subtract from.
//
// Phrased throughout as what the token says rather than what is so. These
// dates are exactly as unverified as the rest of it: anybody can mint a token
// claiming to be valid until 2099, and this sentence renders directly beneath
// the line saying nobody checked. A reader who takes "still valid" as
// authentication has been told otherwise twice in the same paragraph.
func window(claims map[string]any) string {
	exp, hasExp := numericDate(claims, "exp")
	nbf, hasNbf := numericDate(claims, "nbf")
	now := time.Now()
	switch {
	// RFC 7519 §4.1.4: the current time must be *before* exp, so an instant
	// equal to it is already too late.
	case hasExp && !now.Before(exp):
		return "Its own dates say it is expired: exp is " + timefmt.Stamp(exp) + "."
	case hasNbf && now.Before(nbf):
		return "Its own dates say it is not usable yet: nbf is " + timefmt.Stamp(nbf) + "."
	case hasExp:
		return "Its own dates say it is unexpired: exp is " + timefmt.Stamp(exp) + "."
	}
	return ""
}

// numericDate reads one claim as an instant, or reports that it is not one —
// missing, the wrong JSON type, or a number too large to be a date.
func numericDate(claims map[string]any, key string) (time.Time, bool) {
	v, ok := claims[key].(float64)
	if !ok {
		return time.Time{}, false
	}
	return timefmt.FromSeconds(v)
}

// decodeJSONSegment reads one base64url-encoded, unpadded JWT segment (per
// RFC 7519) as a JSON object.
func decodeJSONSegment(segment string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// keyValueOf renders a decoded JWT segment as a stable, sorted KeyValue —
// map iteration order is not, and a claims table that reshuffles between
// two identical calls would be a strange thing to script against.
func keyValueOf(m map[string]any) view.KeyValue {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := view.KeyValue{}
	for _, k := range keys {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: k, Value: formatClaim(k, m[k])})
	}
	return kv
}

// numericDateClaims are the claims RFC 7519 §4.1 defines as a NumericDate —
// seconds since the epoch — and so the only ones whose units the specification
// settles rather than the issuer. A number under any other name could be a
// version, a tenant, a key id or a sequence number, and rendering one of those
// as a date would invent a fact rather than surface one.
//
// Deliberately not auth_time or updated_at: both are NumericDate too, but in
// OpenID Connect rather than here, and a JWT is not necessarily an ID token.
// Adding either is one line the day something needs it.
var numericDateClaims = map[string]bool{"exp": true, "nbf": true, "iat": true}

// formatClaim renders one decoded JSON value for display. json.Unmarshal
// hands every number back as float64, and fmt.Sprint on that prints large
// whole numbers — exactly what iat/exp/nbf timestamps are — in scientific
// notation (1.516239022e+09), which is worse than useless for a claim
// that's supposed to be read as a Unix time. FormatFloat with 'f' avoids it
// for numbers of any size without rounding a genuine fraction.
//
// Which left the claim readable and still unread: `exp 1516242622` is a
// correctly printed integer that nobody can date without arithmetic. A claim
// the specification defines as a time gets rendered as one, beside the raw
// number rather than instead of it — the number is what the token contains and
// what a bug report has to quote.
func formatClaim(key string, v any) string {
	switch t := v.(type) {
	case float64:
		raw := strconv.FormatFloat(t, 'f', -1, 64)
		if !numericDateClaims[key] {
			return raw
		}
		at, ok := timefmt.FromSeconds(t)
		if !ok {
			return raw
		}
		return raw + "  " + timefmt.Stamp(at)
	case string, bool, nil:
		return fmt.Sprint(t)
	default:
		// A nested object or array: still worth showing, not worth losing to
		// Go's map/slice formatting. If it somehow fails to marshal (it came
		// from json.Unmarshal, so it always will), fall back rather than panic.
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
		return fmt.Sprint(t)
	}
}
