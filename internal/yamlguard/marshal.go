package yamlguard

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// The other half: a YAML document rta writes has to read back as what was
// written.
//
// goccy/go-yaml decides whether a string needs quoting with token.IsNeedQuoted,
// and two kinds of string it writes as plain scalars that every parser reads as
// something else. A tab cannot live in a plain scalar, so `col<TAB>col` came back
// "colcol". And a string beginning `? ` is the indicator of a complex mapping
// key: `note: ? why this exists` is not a value at all, and the whole document
// fails to parse. That second one was not a format detail. `rta profile set
// staging --note "? why this exists"` wrote exactly that line into config.yaml,
// and every rta command after it refused to start until somebody edited the file
// by hand. The renderer had quoted the tab for a long time (internal/render/cli);
// the config, the dashboard and the index manifests were written straight
// through the encoder.

// NeedsQuotes reports a string the encoder would write plain and a parser would
// read back as another, or refuse: one holding a tab, or one beginning `? `.
// Every other indicator — a leading `-` or `:` before a space, `#`, `!`, `&`,
// `*`, a flow bracket, a reserved word, a number — the encoder already quotes.
func NeedsQuotes(s string) bool {
	return strings.ContainsRune(s, '\t') || strings.HasPrefix(s, "? ")
}

// Marshal is yaml.MarshalWithOptions writing every string so that it reads back
// as itself, and refusing to hand back a document that does not parse.
//
// A value with no string that NeedsQuotes — every config file anybody has
// written so far — is written exactly as the encoder writes it, byte for byte,
// its block scalars and comments where they were. A value with one is written
// through a hook on strings that quotes it. The hook has a cost that is why it
// is not always on: what it returns, the encoder parses and places without
// re-indenting, so a multi-line string it handed back as a block scalar landed
// at the wrong depth under a nested key and broke the document. In that pass
// every multi-line string is written double-quoted instead, the one form that
// carries its line breaks on one line, so a file that holds such a string trades
// its block scalars' look for being readable at all.
func Marshal(v any, opts ...yaml.EncodeOption) ([]byte, error) {
	if !holdsString(reflect.ValueOf(v), NeedsQuotes) {
		return yaml.MarshalWithOptions(v, opts...)
	}
	out, err := yaml.MarshalWithOptions(v, append(opts, yaml.CustomMarshaler(quotedWhereNeeded))...)
	if err != nil {
		return nil, err
	}
	var back any
	if err := yaml.Unmarshal(out, &back); err != nil {
		return nil, fmt.Errorf("a value that cannot be written as YAML: %w", err)
	}
	return out, nil
}

func quotedWhereNeeded(s string) ([]byte, error) {
	if NeedsQuotes(s) || strings.ContainsAny(s, "\n\r") {
		// Go's escapes for the characters at issue — \t, \n, \\, \" — are
		// YAML's double-quoted ones, and both are JSON's underneath.
		return []byte(strconv.Quote(s)), nil
	}
	return yaml.Marshal(s)
}

// holdsString reports whether any string reachable from v, a map key included,
// satisfies match.
func holdsString(v reflect.Value, match func(string) bool) bool {
	switch v.Kind() {
	case reflect.String:
		return match(v.String())
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && holdsString(v.Elem(), match)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() && holdsString(v.Field(i), match) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if holdsString(v.Index(i), match) {
				return true
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if holdsString(iter.Key(), match) || holdsString(iter.Value(), match) {
				return true
			}
		}
	}
	return false
}
