package cli

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/yamlguard"
)

// A tab in a value used to disappear on the way out through `-o yaml`.
//
// Measured against the encoder rather than reasoned about: goccy/go-yaml
// decides whether a string needs quoting with token.IsNeedQuoted, which
// checks the reserved words, the numbers, the leading and trailing
// characters that would change a scalar's meaning — and not the tab. So
// `col<TAB>col` is written as a *plain* scalar, `v: col<TAB>col`, and a
// plain scalar cannot carry a tab: every parser folds it away. Round-tripped
// through the same library it comes back "colcol", and through yq it comes
// back whatever yq decides.
//
// That is the worst kind of bug a machine-readable format can have. It does
// not fail, it does not warn, and it does not corrupt the syntax — it hands
// the consumer a value that is *almost* the one rta had, with nothing
// anywhere reporting the difference.
//
// The other candidates are already handled and were checked at the same
// time. A newline becomes a block scalar and survives; a carriage return
// does not survive, and never reaches here because sanitize strips it along
// with the other control sequences (see sanitize.go, and the OSC 52 it
// records). The tab is the one character that is both legitimate data — it
// is *why* sanitize keeps it — and mishandled downstream.
//
// Fixed here rather than by cleaning the tab away, because the tab is the
// data. A value rta was given is a value rta prints back.

// yamlSafe returns v with every string the encoder would mishandle, and every
// number ToMap left as its text, replaced by one that emits itself correctly.
// Applied to the map ToMap already builds, so nothing else in the render path
// has to know about it.
func yamlSafe(v any) any {
	switch t := v.(type) {
	case string:
		if needsQuotedValue(t) {
			return quotedScalar(t)
		}
		return t
	case json.Number:
		return plainNumber(t)
	case map[string]any:
		quoted := make(map[any]any)
		out := make(map[string]any, len(t))
		for k, val := range t {
			if needsQuotedKey(k) {
				quoted[quotedScalar(k)] = yamlSafe(val)
			} else {
				out[k] = yamlSafe(val)
			}
		}
		if len(quoted) == 0 {
			return out
		}
		for k, val := range out {
			quoted[k] = val
		}
		return quoted
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = yamlSafe(val)
		}
		return out
	}
	return v
}

// needsQuotedValue is a string the encoder would write plain and every parser
// would read as another, or reject: one holding a tab (above), and one that
// begins `? `. The encoder writes that one bare too, and a parser reads it as
// the opening of a complex mapping key, so the whole document failed to parse
// for a cell, a value or a title that happened to begin with a question mark
// and a space. Found by adding it to the hostile strings every format is
// swept with; the encoder quotes the other indicators itself. The rule is
// yamlguard's, which the config and the manifests are written by too.
func needsQuotedValue(s string) bool { return yamlguard.NeedsQuotes(s) }

// needsQuotedKey is a map key the encoder would write as itself and every
// parser would read as another, or reject: what a value needs (above), and a
// newline.
//
// A key is where this stopped being only a value's problem. A table's records
// are keyed by its column names, and a name is whoever wrote the plugin's
// string: a SQL alias, a header read from a file. Measured the same way as the
// value above: a tab in a key is written plain and read back with the tab gone
// (`col<TAB>col` is `colcol`), so a record's field had a name nobody gave it,
// and a newline in one is written as a block scalar, `|-` on a line of its own,
// which is not a key at all and fails the whole document to parse — a table
// that encoded without trouble before it had records. A value that merely
// contains a newline survives as a block scalar, so the value case has never
// needed this half.
//
// One more is a key's alone, found the same way, by writing every hostile
// string as a column name and reading the record back: the encoder writes
// `<<` bare, which every parser takes for YAML's merge key, and the string
// after it failed the whole document. A SQL alias can be one. The indicators
// that mean something only alone or elsewhere in a line — `?`, `:`, `-`, `|`,
// a flow bracket — the encoder already quotes.
//
// The keys that do not need it are left as the strings they are, and a map
// without such a key is written exactly as it was: the encoder orders both
// kinds of map by the key's text, so quoting a key moves nothing.
func needsQuotedKey(k string) bool {
	return needsQuotedValue(k) || strings.ContainsRune(k, '\n') || k == "<<"
}

// quotedScalar is a string that writes itself as a double-quoted YAML scalar,
// which is the one style that can carry a tab.
//
// strconv.Quote is the escaping: Go's string syntax for the characters at
// issue here — \t, \n, \r, \\ and \" — is exactly YAML's double-quoted
// syntax for them, and both are the JSON escaping underneath.
type quotedScalar string

func (q quotedScalar) MarshalYAML() ([]byte, error) {
	return []byte(strconv.Quote(string(q))), nil
}

// plainNumber is a number that writes itself as the digits it was given: one
// no Go number holds exactly, which ToMap leaves as its json.Number. A
// json.Number is a string underneath, and the encoder quoted it, so
// 12345678901234567.89 came back a string.
type plainNumber json.Number

func (n plainNumber) MarshalYAML() ([]byte, error) {
	return []byte(n), nil
}
