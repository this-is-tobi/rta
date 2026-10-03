package app

import (
	"bytes"
	"encoding/csv"
	"strings"

	"github.com/spf13/pflag"
)

// listFlag is how a list input is given on the command line: repeat the flag,
// or put several in one separated by commas, as pflag's own StringSlice takes
// it — except that an argument which is not valid CSV is one value, not an
// error.
//
// pflag reads a flag's argument as a line of CSV, and a line of CSV that has a
// quote in the middle of a field is malformed. `--header 'If-None-Match:
// "33a64df5"'`, `--header 'Link: <https://a>; rel="next"'` and `--tag 'a"b'`
// were each refused with `parse error on line 1, column 16: bare " in
// non-quoted-field` — the name of an encoding the person had not been told they
// were writing in — and the headers that carry a quote are the ones an HTTP
// client exists to send. A bare quote cannot have been meant as CSV, so the
// argument is taken whole. Quoting a field on purpose (`'"Accept: a, b"'`) still
// keeps its comma.
type listFlag struct {
	values  []string
	changed bool
}

func newListFlag(def []string) *listFlag { return &listFlag{values: append([]string(nil), def...)} }

// splitList is the elements of one argument.
func splitList(raw string) []string {
	if raw == "" {
		return []string{}
	}
	fields, err := csv.NewReader(strings.NewReader(raw)).Read()
	if err != nil {
		return []string{raw}
	}
	return fields
}

// Set takes one occurrence of the flag. The first replaces the declared
// default and the rest add to it, as StringSlice's do.
func (l *listFlag) Set(raw string) error {
	parts := splitList(raw)
	if !l.changed {
		l.values, l.changed = parts, true
		return nil
	}
	l.values = append(l.values, parts...)
	return nil
}

// Type is StringSlice's, so a help page and a completion read the flag as
// they read one.
func (l *listFlag) Type() string { return "stringSlice" }

// String renders the list the way pflag renders StringSlice's: bracketed CSV,
// which is what a default is compared against to decide it is empty.
func (l *listFlag) String() string {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write(l.values)
	w.Flush()
	return "[" + strings.TrimSuffix(b.String(), "\n") + "]"
}

// Append, Replace and GetSlice make this a pflag.SliceValue, which is what code
// that handles every slice flag alike looks for.
func (l *listFlag) Append(v string) error { l.values = append(l.values, v); return nil }

func (l *listFlag) Replace(v []string) error { l.values = append([]string(nil), v...); return nil }

func (l *listFlag) GetSlice() []string { return append([]string(nil), l.values...) }

var _ pflag.SliceValue = (*listFlag)(nil)
