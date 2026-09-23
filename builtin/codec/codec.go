// Package codec is the built-in encode/decode plugin: base64, hex, URL
// escaping, and unverified inspection of the JOSE family — tokens and keys.
// Stdlib only, no network, no state.
//
// Every capability stays Read even though codec.jwt and the *.decode
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
	"net/url"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Plugin returns the codec plugin declaration.
func Plugin() plugin.Plugin {
	valueField := plugin.Field{Name: "value", Type: plugin.String, Positional: true, Required: true, Help: "the text to transform"}
	decodeField := plugin.Field{Name: "decode", Type: plugin.Bool, Help: "decode instead of encode"}

	return plugin.Plugin{
		Name:    "codec",
		Summary: "Mechanical encode/decode: base64, hex, URL escaping, JWT and JWK inspection",
		Capabilities: []plugin.Capability{
			{
				ID:      "codec.b64",
				Summary: "Base64 encode or decode a value",
				Description: "Decoding accepts standard, URL-safe, and unpadded variants without being " +
					"told which — the caller already has the encoded value, so being forgiving about " +
					"which base64 dialect produced it costs nothing.",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField,
					{Name: "url", Type: plugin.Bool, Help: "use the URL-safe alphabet when encoding"}},
				Run: runB64,
			},
			{
				ID:      "codec.hex",
				Summary: "Hex encode or decode a value",
				Safety:  plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField},
				Run:    runHex,
			},
			{
				ID:      "codec.url",
				Summary: "Escape a value for a URL, or unescape one",
				Description: "Query-component escaping (spaces become +), the form almost everyone " +
					"means by \"URL encode this\" — the value for a query string or form body.",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField},
				Run:    runURL,
			},
			{
				ID:      "codec.jwt",
				Summary: "Decode a JWT, JWS or JWE for inspection: headers, claims, dates",
				Description: "Reads every serialization the JOSE family defines — a signed token (JWS), an " +
					"encrypted one (JWE), and the JSON form of either — with the headers and claims decoded, " +
					"the dates read, and anything a strict parser would refuse named: a padded segment, a " +
					"member given twice, an empty signature. A JWE's header is read and its content is not, " +
					"because decrypting takes the recipient's private key. Unverified and labeled as such: " +
					"this is for reading a token while debugging, not for authenticating one — anyone can " +
					"hand you a token with any claims at all. A pasted `Authorization: Bearer` line works. " +
					"Given no argument, reads the token from standard input, which keeps a live one out of " +
					"shell history and out of the process list.",
				Safety: plugin.Read, Idempotent: true,
				// Positional but not Required, because a pipe can supply it —
				// so without this the dashboard's automatic set (every Read
				// that needs no input) would call it unasked, on a timer, with
				// nothing to decode. debug.ansi is the same shape for the same
				// reason.
				NoPreview: true,
				// Secret: a JWT handed to `codec jwt` is a live bearer token far
				// more often than it is a specimen, and a String here reached
				// both the completion shortlist and the agent log intact.
				Inputs: []plugin.Field{{Name: "token", Type: plugin.Secret, Positional: true, Help: "the token to decode"}},
				Run:    runJWT,
			},
			{
				ID:      "codec.jwk",
				Summary: "Read a JSON Web Key or key set: type, size, thumbprint, and whether it is private",
				Description: "Takes one JWK or a whole key set — an issuer's jwks_uri answer — and says what " +
					"each key is: its type and size, whether its point is on its curve, its kid, use and alg, " +
					"and the RFC 7638 thumbprint a DPoP cnf.jkt or a pinned key is compared against. Names a " +
					"key holding private material, which a published set never should, and two keys sharing " +
					"a kid. A certificate chain in x5c is read and checked against the key beside it. Private " +
					"members are never printed. Given no argument, reads the key from standard input.",
				Safety: plugin.Read, Idempotent: true,
				NoPreview: true,
				// Secret for the reason codec.jwt's token is: a private JWK is
				// exactly what somebody pastes here to find out whether it is
				// one, and it must not reach the agent log or a completion.
				Inputs: []plugin.Field{{Name: "key", Type: plugin.Secret, Positional: true, Help: "the JWK or key set"}},
				Run:    runJWK,
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
