package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

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
//     of a parser-differential attack — so a duplicate is noticed and named,
//     at whatever depth it sits.
//   - Marshalling a nested value escapes <, > and & as \u003c, \u003e and
//     \u0026, so `["a&b"]` was shown as `["a\u0026b"]`.

// object is one decoded JSON object: the value of every member, the last one
// where a name repeats, and the names that did.
type object struct {
	values map[string]any
	dupes  []string
	// replaced says why some of the text was replaced, or is "". encoding/json
	// reads a byte that is not UTF-8, and an escape of half a UTF-16
	// surrogate pair, as U+FFFD without an error, so `admin` followed by
	// 0xff, by a lone high surrogate, or by U+FFFD itself are three subjects
	// to a parser that keeps bytes and one on the page — and two names that
	// differ in such a byte read as one name given twice.
	replaced string
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
	w := walker{dec: dec}
	values, err := w.object(1)
	if err != nil {
		return object{}, err
	}
	// json.Unmarshal refuses trailing data after the value, and a Decoder
	// reading a stream does not; the segment is one value or it is malformed.
	if _, err := dec.Token(); err != io.EOF {
		return object{}, errors.New("more after the JSON object")
	}
	sort.Strings(w.dupes)
	obj := object{values: values, dupes: w.dupes}
	switch {
	case !utf8.Valid(raw):
		obj.replaced = "is not valid UTF-8"
	case loneSurrogate(raw):
		obj.replaced = "escapes half of a UTF-16 surrogate pair, which is no character"
	}
	return obj, nil
}

// loneSurrogate reports whether raw, already read as valid JSON, holds a
// backslash-u escape of a surrogate (U+D800 to U+DFFF) that is not one half
// of a high-then-low pair. Valid JSON has a backslash only inside a string,
// so each one starts an escape, and skipping the character after it keeps an
// escaped backslash from being read as the start of another.
func loneSurrogate(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		switch r := hex4(raw, i+1); {
		case r >= 0xd800 && r < 0xdc00:
			if i+10 < len(raw) && raw[i+5] == '\\' && raw[i+6] == 'u' {
				if low := hex4(raw, i+7); low >= 0xdc00 && low < 0xe000 {
					i += 10
					continue
				}
			}
			return true
		case r >= 0xdc00 && r < 0xe000:
			return true
		}
		i += 4
	}
	return false
}

// hex4 reads the four hex digits at raw[i:], or returns -1.
func hex4(raw []byte, i int) int {
	if i+4 > len(raw) {
		return -1
	}
	n, err := strconv.ParseUint(string(raw[i:i+4]), 16, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

// maxDepth is how deeply decodeObject follows nested values: the bound
// encoding/json enforces itself, which held while every member was decoded
// by it. The walker recurses once per level, and a megabyte of `[` would
// otherwise ask for more stack than a goroutine may have, which is not an
// error but the end of the process — `rta mcp serve` included.
const maxDepth = 10000

// maxPath bounds how much of the path above a repeated name the note gives:
// enough to place it in any token a person reads, and a bound, since a path
// is otherwise as long as every name above it together.
const maxPath = 128

// walker reads a JSON value token by token rather than handing each member to
// encoding/json, which is what lets it see a name repeated at any depth.
// Handed to the decoder, a nested object folded its repeats into a map
// silently, and nested objects are where the JSON serialization keeps what
// matters: two protected members inside signatures[0] swapped the signed
// header, a kid given twice in an unprotected header chose the key, and a
// key set's kty lives in keys[i]. A repeat is recorded under its path —
// signatures[0].protected, keys[1].kty — so the note can say where it is.
type walker struct {
	dec   *json.Decoder
	dupes []string
	// at is the way down to the value being read, a step a level, and the
	// path is written from it only for a repeat. Written at every level as it
	// went, each level held its own copy of every name above it: 410 KB of
	// long names nested 4000 deep asked for 790 MB, and the 4 MiB an MCP
	// request may carry for tens of gigabytes, from a free Read.
	at []step
}

// step is one level of the way down: a member's name, or an element's index
// when index is not -1.
type step struct {
	name  string
	index int
}

func (s step) String() string {
	if s.index >= 0 {
		return "[" + strconv.Itoa(s.index) + "]"
	}
	return s.name
}

// path is where a repeat of name in the object being read sits, as the note
// gives it. Only the last maxPath bytes of the way down are written, after an
// ellipsis: whole, a path is as long as every name above it together, and a
// document of repeats deep under long names cost what writing every path did.
func (w *walker) path(name string) string {
	start, size := len(w.at), 0
	for start > 0 && size+len(w.at[start-1].String())+1 <= maxPath {
		start--
		size += len(w.at[start].String()) + 1
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	for i, s := range append(w.at[start:len(w.at):len(w.at)], step{name: name, index: -1}) {
		if s.index < 0 && i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.String())
	}
	return b.String()
}

func (w *walker) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nested more than %d levels deep", maxDepth)
	}
	tok, err := w.dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		m, err := w.object(depth)
		return m, err
	case json.Delim('['):
		list, err := w.array(depth)
		return list, err
	}
	return tok, nil
}

// object reads the members of an object whose opening brace has been read,
// and its closing brace.
func (w *walker) object(depth int) (map[string]any, error) {
	m := map[string]any{}
	// A count rather than a search of the dupes found so far: an object of
	// distinct names each given twice made that search quadratic, and a
	// megabyte of them took seconds on a free Read an agent can call.
	seen := map[string]int{}
	for w.dec.More() {
		tok, err := w.dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := tok.(string) // inside an object the decoder yields only string names
		w.at = append(w.at, step{name: name, index: -1})
		v, err := w.value(depth + 1)
		w.at = w.at[:len(w.at)-1]
		if err != nil {
			return nil, err
		}
		if seen[name]++; seen[name] == 2 {
			w.dupes = append(w.dupes, w.path(name))
		}
		m[name] = v
	}
	_, err := w.dec.Token()
	return m, err
}

// array reads the elements of an array whose opening bracket has been read.
// Never nil, even empty: a nil slice renders as null, and the token says [].
func (w *walker) array(depth int) ([]any, error) {
	list := []any{}
	for i := 0; w.dec.More(); i++ {
		w.at = append(w.at, step{index: i})
		v, err := w.value(depth + 1)
		w.at = w.at[:len(w.at)-1]
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	_, err := w.dec.Token()
	return list, err
}

// asObject wraps a nested object — an unprotected header, a key inside a key
// set — that arrived through decodeObject and so already holds json.Number.
// A name repeated inside it is not in its own dupes: it was recorded, by
// path, against the document it was decoded from.
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

// notNumbers lists the NumericDate claims o holds as something other than a
// JSON number, which RFC 7519 §2 requires. Libraries split on it: golang-jwt
// and jsonwebtoken refuse a string exp, and PyJWT converts it and enforces
// it. Shown as a bare value with no date and nothing said, a string exp left
// somebody debugging an "invalid exp" rejection no nearer to why.
func notNumbers(o object) []string {
	var out []string
	for _, name := range []string{"exp", "nbf", "iat"} {
		if _, number := o.values[name].(json.Number); o.has(name) && !number {
			out = append(out, name)
		}
	}
	return out
}

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
