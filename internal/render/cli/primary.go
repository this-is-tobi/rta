package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// PrintPrimary writes the one value a capability's answer is — its declared
// Primary: the password a generator made, the response body, the token — and
// nothing around it, when the answer is going to a pipe or a file. It reports
// whether it did, so the caller renders the whole view otherwise.
//
// Pretty is the one format allowed to change with where it is going, and the
// reason it is here at all: `rta gen password | pbcopy` copied the box drawn
// around the password, and `rta http get URL | jq .` was handed a table. The
// structured formats are untouched, and a terminal sees the whole view.
//
// What the view says besides the value goes to the notes stream, which the
// command line wires to stderr, marked "#" as csv's are: the status of a
// response a script is about to parse, an entropy, a page that is one of
// several. Stdout stays the value and nothing else, one per row, each ending
// in a newline.
//
// It declines, and the view is drawn as it always was, for a view with no such
// column or key, which is a HEAD's response and a dry run's preview; for a
// value the view marks Redacted, which would print as its mask and so is
// never what a pipe was asking for; and for a shape that is neither a table
// nor a key/value list.
func PrintPrimary(w io.Writer, v view.View, name string, opts Options) bool {
	if name == "" || v == nil || opts.Format != Pretty || opts.Screen {
		return false
	}
	v = view.Redact(sanitize(v))
	var values []string
	var notes []string
	switch t := v.(type) {
	case view.Table:
		col := columnNamed(t.Columns, name)
		if col < 0 || len(t.Rows) == 0 {
			return false
		}
		for _, row := range t.Rows {
			if col < len(row) {
				values = append(values, row[col])
			}
		}
		if len(t.Rows) == 1 {
			for i, c := range t.Columns {
				if i != col && i < len(t.Rows[0]) && t.Rows[0][i] != "" {
					notes = append(notes, c.Name+": "+t.Rows[0][i])
				}
			}
		}
		if t.Total > len(t.Rows) {
			notes = append(notes, fmt.Sprintf("%d of %s", len(t.Rows), format.CountOf(t.Total, "row")))
		}
		for _, e := range t.Warnings {
			notes = append(notes, e.Code+" "+e.Message)
		}
	case view.KeyValue:
		found := false
		for _, p := range t.Pairs {
			switch {
			case p.Key == name:
				values = append(values, p.Value)
				found = true
			case strings.HasPrefix(p.Key, "header:"):
				// A response's headers are the bulk of what it says besides its
				// body, and not what a script piping the body is waiting on.
			default:
				notes = append(notes, p.Key+": "+p.Value)
			}
		}
		if !found {
			return false
		}
	default:
		return false
	}
	if len(values) == 0 || slices.Contains(values, view.Mask) {
		return false
	}
	for _, val := range values {
		if !strings.HasSuffix(val, "\n") {
			val += "\n"
		}
		if _, err := io.WriteString(w, val); err != nil {
			return true
		}
	}
	if opts.Notes != nil {
		for _, n := range notes {
			csvNote(opts.Notes, n)
		}
	}
	return true
}

func columnNamed(cols []view.Column, name string) int {
	for i, c := range cols {
		if c.Name == name {
			return i
		}
	}
	for i, c := range cols {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}
