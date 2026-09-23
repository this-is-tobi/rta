package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/this-is-tobi/rta/builtin/internal/timefmt"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A JOSE header, a claims set or a key, read the way a debugging view needs it
// read rather than the way a program consuming it would.
//
// Three things encoding/json does by default are wrong for that, and each one
// put something on the screen that the token does not say:
//
//   - Numbers arrive as float64, so 9007199254740993 — a numeric sub, a
//     snowflake id — was shown as 9007199254740992, a different user. The
//     literal is kept instead, and converted only where a claim is dated.
//   - A member named twice keeps its last value without a word. RFC 7515 §4
//     and RFC 7519 §4 let a parser either refuse that or keep the last, which
//     means two libraries can disagree about what one token says — the shape
//     of a parser-differential attack — so a duplicate is noticed and named.
//   - Marshalling a nested value escapes <, > and & as \u003c, \u003e and
//     \u0026, so `["a&b"]` was shown as `["a\u0026b"]`.

// object is one decoded JSON object: the value of every member, the last one
// where a name repeats, and the names that did.
type object struct {
	values map[string]any
	dupes  []string
}

var errNotObject = errors.New("not a JSON object")

// decodeObject reads raw as exactly one JSON object.
//
// **encoding/json writes nothing into a map for the literal `null`, and
// reports no error doing it.** RFC 7519 requires every segment to be a JSON
// object, so `null` is malformed — but it decoded "successfully" into a nil
// map and rendered as an ordinary empty table, which is also exactly what a
// token whose header genuinely is `{}` renders as. Reading the first token and
// requiring a brace closes that for `null` and for every other non-object
// value at once.
func decodeObject(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return object{}, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return object{}, errNotObject
	}
	obj := object{values: map[string]any{}}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return object{}, err
		}
		name, _ := tok.(string) // inside an object the decoder yields only string names
		var v any
		if err := dec.Decode(&v); err != nil {
			return object{}, err
		}
		if seen[name] && !slices.Contains(obj.dupes, name) {
			obj.dupes = append(obj.dupes, name)
		}
		seen[name] = true
		obj.values[name] = v
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return object{}, err
	}
	// json.Unmarshal refuses trailing data after the value, and a Decoder
	// reading a stream does not; the segment is one value or it is malformed.
	if _, err := dec.Token(); err != io.EOF {
		return object{}, errors.New("more after the JSON object")
	}
	sort.Strings(obj.dupes)
	return obj, nil
}

// asObject wraps a nested object — an unprotected header, a key inside a key
// set — that arrived through decodeObject and so already holds json.Number.
// A name repeated inside it cannot be seen any more; only the top level of
// each decoded document is checked, which is where alg, kid and kty live.
func asObject(v any) (object, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return object{}, false
	}
	return object{values: m}, true
}

func (o object) has(name string) bool {
	_, ok := o.values[name]
	return ok
}

// str is a string member, or "" when it is absent or of another type.
func (o object) str(name string) string {
	s, _ := o.values[name].(string)
	return s
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

// numericDate reads one member as an instant, or reports that it is not one —
// missing, the wrong JSON type, or a number too large to be a date.
func numericDate(o object, name string) (time.Time, bool) {
	n, ok := o.values[name].(json.Number)
	if !ok {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil {
		return time.Time{}, false
	}
	return timefmt.FromSeconds(f)
}

// rendered is a KeyValue plus the names whose values had to be escaped to be
// shown at all, so the page can say that it escaped them.
type rendered struct {
	kv      view.KeyValue
	escaped []string
}

// keyValueOf renders an object as a stable, sorted KeyValue — map iteration
// order is not, and a claims table that reshuffles between two identical calls
// would be a strange thing to script against.
func keyValueOf(o object) rendered {
	names := make([]string, 0, len(o.values))
	for name := range o.values {
		names = append(names, name)
	}
	sort.Strings(names)
	var r rendered
	for _, name := range names {
		key := visible(name)
		value, hidden := formatValue(o, name)
		if hidden || key != name {
			r.escaped = append(r.escaped, key)
		}
		r.kv.Pairs = append(r.kv.Pairs, view.Pair{Key: key, Value: value})
	}
	return r
}

// formatValue renders one member for display, and reports whether anything
// in it had to be escaped to be seen.
//
// A claim the specification defines as a time is rendered as one, beside the
// literal rather than instead of it — the number is what the token contains
// and what a bug report has to quote, and `exp 1516242622` is a correctly
// printed integer that nobody can date without arithmetic.
func formatValue(o object, name string) (string, bool) {
	switch t := o.values[name].(type) {
	case json.Number:
		raw := t.String()
		if !numericDateClaims[name] {
			return raw, false
		}
		at, ok := numericDate(o, name)
		if !ok {
			return raw, false
		}
		return raw + "  " + timefmt.Stamp(at), false
	case string:
		out := visible(t)
		return out, out != t
	case bool, nil:
		return fmt.Sprint(t), false
	default:
		raw := compactJSON(t)
		out := escapeHidden(raw)
		return out, out != raw
	}
}

// compactJSON renders a nested object or array on one line, without the HTML
// escaping encoding/json applies by default and with every number exactly as
// the token wrote it.
func compactJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// It came from the decoder, so it always encodes; fall back rather
		// than panic if that ever stops being true.
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// visible returns s as it is, or Go-quoted when it holds anything that would
// display as something other than what it is: a control or escape sequence, a
// line break, an invisible or bidi character.
//
// Every renderer already strips terminal sequences from what it prints, so a
// hostile claim cannot write to the clipboard or retitle the window. What
// stripping cannot do is say it happened: `x ESC ] 52 ; c ; … BEL y` printed as
// `xy`, and a decoder whose purpose is showing what a token carries hid the
// most interesting thing in it. Quoting shows it, and a quoted value is
// unambiguous where an inline escape beside a literal backslash would not be.
func visible(s string) string {
	if !textclean.Deceives(s) {
		return s
	}
	return strconv.QuoteToGraphic(s)
}

// quote is visible for a value quoted inside a sentence: quoted once either
// way, so escaping never leaves it wrapped in two pairs of quotes.
func quote(s string) string {
	if v := visible(s); v != s {
		return v
	}
	return `"` + s + `"`
}

// escapeHidden is visible for text that is already JSON: encoding/json
// escapes C0 controls itself but passes DEL, the C1 block and the invisible
// characters straight through, so those are written as \u escapes — which
// keep the text valid JSON — rather than quoted a second time.
func escapeHidden(s string) string {
	if !textclean.Deceives(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if !textclean.Deceives(string(r)) {
			b.WriteRune(r)
			continue
		}
		if r > 0xffff {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
			continue
		}
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}
