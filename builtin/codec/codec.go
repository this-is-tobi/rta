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
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
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
				Description: "Decoding accepts standard, URL-safe, and unpadded variants, and the line " +
					"breaks and spaces wrapped base64 arrives with, without being told which — the caller " +
					"already has the encoded value, so being forgiving about which dialect produced it " +
					"costs nothing. Bytes that are not plain text are shown as a hex dump rather than " +
					"printed at the terminal, where they would show as nothing.",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField,
					{Name: "url", Type: plugin.Bool, Help: "use the URL-safe alphabet when encoding"}},
				Run: runB64,
			},
			{
				ID:      "codec.hex",
				Summary: "Hex encode or decode a value",
				Description: "Decoding accepts the spellings hex is copied in: a 0x prefix, and bytes " +
					"separated by colons, spaces or dashes, the way openssl prints a fingerprint. Bytes " +
					"that are not plain text are shown as a hex dump.",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField},
				Run:    runHex,
			},
			{
				ID:      "codec.url",
				Summary: "Escape a value for a URL, or unescape one",
				Description: "Query-component escaping by default (spaces become +), the form almost " +
					"everyone means by \"URL encode this\" — the value for a query string or form body. " +
					"--path escapes a path segment instead, where a space is %20 and + is a literal plus: " +
					"decoding a path as a query turns c++.txt into \"c  .txt\".",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField,
					{Name: "path", Type: plugin.Bool, Help: "escape or unescape a path segment rather than a query value"}},
				Run: runURL,
			},
			{
				ID:      "codec.jwt",
				Summary: "Decode a JWT, JWS or JWE for inspection: headers, claims, dates",
				Description: "Reads every serialization the JOSE family defines — a signed token (JWS), an " +
					"encrypted one (JWE), and the JSON form of either — with the headers and claims decoded, " +
					"the dates read, and anything a strict parser would refuse named: a padded segment, a " +
					"member given twice, an empty signature. A JWE's header is read and its content is not, " +
					"because decrypting takes the recipient's private key. Unverified unless --key or --secret " +
					"is given, and labeled as such: anyone can hand you a token with any claims at all. With " +
					"--key — a public key, certificate or the issuer's key set, fetched by you, since a capability " +
					"that fetched a URL its caller names would not be a free read — the signature is checked, the " +
					"algorithm is decided by the key and never by the token, and a mismatch is an error naming " +
					"why. An HMAC signature takes --secret instead, never a public key. A pasted " +
					"`Authorization: Bearer` line works. Given no argument, reads the token from standard input, " +
					"which keeps a live one out of shell history and out of the process list.",
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
				Inputs: []plugin.Field{
					{Name: "token", Type: plugin.Secret, Positional: true, Help: "the token to decode"},
					// Secret although a public key is not one: what somebody
					// pastes here is as often a private JWK as a public one,
					// and only the public half is ever used.
					{Name: "key", Type: plugin.Secret, Help: "the public key, certificate or key set to verify the signature with"},
					// Local: an HMAC secret is a credential, and an agent
					// must never be invited to supply one. EnvFallback keeps
					// it off argv for the person at the terminal.
					{Name: "secret", Type: plugin.Secret, Local: true, EnvFallback: true,
						Help: "the shared secret an HS256, HS384 or HS512 signature is made with"},
				},
				Run: runJWT,
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
		raw, err := decodeB64(value)
		if err != nil {
			return nil, view.Errorf("codec.b64.invalid", "not valid base64: %v", err)
		}
		return decoded(raw), nil
	}
	enc := base64.StdEncoding
	if req.Bool("url") {
		enc = base64.URLEncoding
	}
	return view.Text{Body: enc.EncodeToString([]byte(value))}, nil
}

// decodeB64 tries the dialects a caller is actually likely to hand us —
// standard and URL-safe, each padded and unpadded — before giving up, with
// the whitespace removed first: base64 wrapped at 64 or 76 columns, a PEM
// body, a value copied across a terminal's line break. The decoder skips a
// line break on its own and refuses a space, so a copy that picked up an
// indent failed while the same text without it decoded.
func decodeB64(value string) ([]byte, error) {
	value = strings.Join(strings.Fields(value), "")
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
		raw, err := hex.DecodeString(hexDigits(value))
		if err != nil {
			return nil, view.Errorf("codec.hex.invalid", "not valid hex: %v", err).
				WithHint("pairs of 0-9 and a-f; a 0x prefix and colon, space or dash separators are taken as they are")
		}
		return decoded(raw), nil
	}
	return view.Text{Body: hex.EncodeToString([]byte(value))}, nil
}

// hexDigits strips what hex is copied with: 0x prefixes, and the colons,
// spaces and dashes that separate the bytes of a fingerprint (`openssl x509
// -fingerprint` prints AB:CD:…). None of them is a hex digit, so removing
// them cannot change what the digits say.
func hexDigits(s string) string {
	s = strings.NewReplacer("0x", "", "0X", "").Replace(s)
	return strings.Map(func(r rune) rune {
		switch r {
		case ':', ' ', '-', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}

func runURL(_ context.Context, req plugin.Request) (view.View, error) {
	value, path := req.String("value"), req.Bool("path")
	if req.Bool("decode") {
		unescape := url.QueryUnescape
		if path {
			unescape = url.PathUnescape
		}
		out, err := unescape(value)
		if err != nil {
			return nil, view.Errorf("codec.url.invalid", "not a valid URL-escaped value: %v", err)
		}
		return decoded([]byte(out)), nil
	}
	if path {
		return view.Text{Body: url.PathEscape(value)}, nil
	}
	return view.Text{Body: url.QueryEscape(value)}, nil
}

// decoded is what a decode shows: the text when it is plain text, a hex dump
// when it is not.
//
// Printing decoded bytes as they came was a decoder that answered with
// nothing. Every renderer strips control characters on the way to a terminal,
// so an escape sequence cannot act there — and so `codec b64 --decode
// AAECAwT/` printed an empty line, and -o json turned its 0xff into U+FFFD. A
// dump shows every byte, in the layout `hexdump -C` made familiar.
func decoded(raw []byte) view.View {
	if plainText(raw) {
		return view.Text{Body: string(raw)}
	}
	return view.Text{Body: dump(raw)}
}

// plainText reports whether raw is text a terminal shows exactly as it is:
// valid UTF-8 holding nothing a renderer strips or a reader cannot see, the
// line breaks and tabs of ordinary text aside.
func plainText(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for _, r := range string(raw) {
		if r != '\n' && r != '\t' && r != '\r' && textclean.Deceives(string(r)) {
			return false
		}
	}
	return true
}

// maxDump bounds a dump. Past it the bytes are a file somebody wants on disk,
// not lines to read at a terminal, and `base64 -d` is the tool for that.
const maxDump = 4 << 10

func dump(raw []byte) string {
	shown := raw
	var b strings.Builder
	b.WriteString(format.CountOf(len(raw), "byte") + ", not plain text")
	if len(raw) > maxDump {
		shown = raw[:maxDump]
		b.WriteString(" — the first " + format.Bytes(maxDump) + " shown")
	}
	b.WriteString(":")
	for off := 0; off < len(shown); off += 16 {
		line := shown[off:min(off+16, len(shown))]
		fmt.Fprintf(&b, "\n%08x  ", off)
		for i := range 16 {
			if i < len(line) {
				fmt.Fprintf(&b, "%02x ", line[i])
			} else {
				b.WriteString("   ")
			}
			if i == 7 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(" |")
		for _, c := range line {
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|")
	}
	return b.String()
}
