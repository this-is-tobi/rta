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
	"errors"
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
					"printed at the terminal, where they would show as nothing, and text holding a control " +
					"or invisible character comes back as its exact value beside the dump.",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{valueField, decodeField,
					{Name: "url", Type: plugin.Bool, Help: "use the URL-safe alphabet when encoding"}},
				Run: runB64,
			},
			{
				ID:      "codec.hex",
				Summary: "Hex encode or decode a value",
				Description: "Decoding accepts the spellings hex is copied in: a 0x prefix, and bytes " +
					"separated by colons, spaces or dashes, the way openssl prints a fingerprint. " +
					"Bytes that are not plain text are shown as a hex dump, beside the exact " +
					"value when they are text holding a control or invisible character.",
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
//
// A space inside a line is taken only where wrapped or grouped base64 puts
// one, after a whole group of four. Two unpadded values side by side on one
// line — `aGVsbG8 d29ybGQ`, "hello" and "world" — join into fourteen
// characters an unpadded decoder accepts, and decode to bytes neither value
// holds.
func decodeB64(value string) ([]byte, error) {
	for _, line := range strings.Split(value, "\n") {
		chunks := strings.Fields(line)
		for _, c := range chunks[:max(len(chunks)-1, 0)] {
			if len(c)%4 != 0 {
				return nil, fmt.Errorf("a space splits it after a piece of %s, where wrapped base64 breaks only "+
					"after whole groups of four: two values side by side decode to bytes neither of them holds",
					format.CountOf(len(c), "character"))
			}
		}
	}
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
		raw, err := decodeHex(value)
		if err != nil {
			return nil, view.Errorf("codec.hex.invalid", "not valid hex: %v", err).
				WithHint("pairs of 0-9 and a-f; a 0x prefix and colon, space or dash separators are taken as they are")
		}
		return decoded(raw), nil
	}
	return view.Text{Body: hex.EncodeToString([]byte(value))}, nil
}

// decodeHex reads hex as it is copied: a 0x prefix, and the colons, spaces
// and dashes that separate the bytes of a fingerprint (`openssl x509
// -fingerprint` prints AB:CD:…).
//
// Group by group, because a separator says where a byte ends. Deleting the
// separators and decoding what was left regrouped the digits: `0x1 0x2 0x3
// 0x4` came out as the two bytes 12 34, and the MAC `0:c:29:a1:b2:30` that
// macOS `arp -a` prints lost its leading zero byte. So every group has to be
// whole bytes, a 0x is taken off the front of a group and nowhere else (the
// 0 of an 0x in the middle of a run is a digit), and a separator with nothing
// on one side of it is refused rather than skipped.
func decodeHex(s string) ([]byte, error) {
	// A run of whitespace is one separator: a copy wrapped across lines or
	// indented is still one value.
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return []byte{}, nil
	}
	groups := splitHexGroups(s)
	var out []byte
	for _, g := range groups {
		if g == "" {
			return nil, errors.New("a separator with no digits on one side of it")
		}
		digits := g
		if len(digits) > 2 && (digits[:2] == "0x" || digits[:2] == "0X") {
			digits = digits[2:]
		}
		if len(groups) > 1 && len(digits)%2 != 0 {
			return nil, fmt.Errorf("%q is %s, which is not whole bytes", g, format.CountOf(len(digits), "digit"))
		}
		raw, err := hex.DecodeString(digits)
		if err != nil {
			return nil, err
		}
		out = append(out, raw...)
	}
	return out, nil
}

// splitHexGroups splits on each separator, keeping the empty group a doubled,
// leading or trailing one leaves so decodeHex can refuse it.
func splitHexGroups(s string) []string {
	var groups []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', ':', '-':
			groups = append(groups, s[start:i])
			start = i + 1
		}
	}
	return append(groups, s[start:])
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

// decoded is what a decode shows: the text when it shows as exactly itself,
// a dump of every byte when it is not text at all, and both when it is text
// that would show as something else. Printed as they came, such bytes showed
// as nothing: every renderer strips control characters on the way to a
// terminal, so `codec b64 --decode AAECAwT/` printed an empty line.
//
// Both, because the choice is made in the view and so applies to every
// surface. A dump in place of the value reached -o json, the one byte-exact
// channel, and an agent: a CSV with a byte-order mark, a string holding an
// ESC colour code or a zero-width space came back as the text "17 bytes, not
// plain text: …", cut at 4 KiB, and `-o json | jq -r` handed a script the
// dump as though it were the value. The value section carries the exact
// string for whoever reads the JSON; the terminal cleans what it prints of
// it, and the bytes section beside it shows the person what was cleaned.
// Invalid UTF-8 is the one case with no value section, since JSON cannot
// carry it exactly.
func decoded(raw []byte) view.View {
	switch {
	case !utf8.Valid(raw):
		return view.Text{Body: format.Dump(raw, maxDump)}
	case showsAsItIs(string(raw)):
		return view.Text{Body: string(raw)}
	}
	return view.Sections{Items: []view.Section{
		{ID: "value", Title: "value", View: view.Text{Body: string(raw)}},
		{ID: "bytes", Title: "bytes", View: view.Text{Body: format.Dump(raw, maxDump)}},
	}}
}

// showsAsItIs is the strict test a decoder needs: text holding nothing a
// renderer strips, spells out or cannot show — a control, an escape sequence,
// an invisible or bidi character, or a carriage return that is not the first
// half of a Windows line ending, which returns the cursor and lets the rest of
// the line print over the start of it. Line breaks and tabs are the layout of
// ordinary text.
//
// textclean.Deceives rather than format.PlainText, whose question is whether
// bytes are text at all: a response body or an object is shown as a page a
// reader skims, where a decoder's whole answer is the value, and a character
// that displays as anything but itself is a different answer.
func showsAsItIs(s string) bool {
	s = strings.ReplaceAll(s, "\r\n", "")
	return !textclean.Deceives(strings.NewReplacer("\n", "", "\t", "").Replace(s))
}

// maxDump bounds a dump. Past it the bytes are a file somebody wants on disk,
// not lines to read at a terminal, and `base64 -d` is the tool for that.
const maxDump = 4 << 10
