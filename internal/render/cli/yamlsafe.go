package cli

import (
	"strconv"
	"strings"
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

// yamlSafe returns v with every string the encoder would mishandle replaced
// by one that emits itself correctly. Applied to the map ToMap already
// builds, so nothing else in the render path has to know about it.
func yamlSafe(v any) any {
	switch t := v.(type) {
	case string:
		if strings.ContainsRune(t, '\t') {
			return quotedScalar(t)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = yamlSafe(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = yamlSafe(val)
		}
		return out
	}
	return v
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
