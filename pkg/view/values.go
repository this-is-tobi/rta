package view

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
)

// Cell is one cell of a table row: Text is what a person reads, and Value is
// what a program reads, in the spelling the column's kind gives it (ValueOK),
// or "" for a cell that has none. See Table.Values.
type Cell struct {
	Text  string
	Value string
}

// Add appends one row to t, its values beside it.
//
// The way to fill a table that carries values, because it keeps the two in
// step: Values is row for row and cell for cell with Rows, and a table that
// appended to one and forgot the other would put every value after the gap on
// the wrong row. A row without any value adds none, so a table built only of
// Plain cells carries no Values at all and encodes as it always did.
func (t *Table) Add(cells ...Cell) {
	row := make([]string, len(cells))
	var values []string
	for i, c := range cells {
		row[i] = c.Text
		if c.Value != "" {
			if values == nil {
				values = make([]string, len(cells))
			}
			values[i] = c.Value
		}
	}
	if values != nil && t.Values == nil {
		t.Values = make([][]string, len(t.Rows), len(t.Rows)+1)
	}
	t.Rows = append(t.Rows, row)
	if t.Values != nil {
		t.Values = append(t.Values, values)
	}
}

// Plain is a cell that is its own value: a name, a word, a path.
func Plain(text string) Cell { return Cell{Text: text} }

// integer is every type a count turns up in: pkg/format's, which Bytes hands
// its count to.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Bytes is a byte count, shown as pkg/format writes one (1.3 MiB) and valued
// as the count, for a KindBytes column.
func Bytes[T integer](n T) Cell { return Cell{Text: format.Bytes(n), Value: decimal(n)} }

// Count is a whole number shown as itself, for a KindNumber column.
func Count[T integer](n T) Cell {
	s := decimal(n)
	return Cell{Text: s, Value: s}
}

// Number is a number shown as text says, for a KindNumber, KindPercent or
// KindUsage column — a percentage valued in percent, 72.5 and not 0.725, as
// its column is read. A number that is not finite has no value.
func Number(text string, n float64) Cell { return Cell{Text: text, Value: float(n)} }

// Duration is a length of time shown as text says, valued in seconds, for a
// KindDuration column.
func Duration(text string, d time.Duration) Cell {
	return Cell{Text: text, Value: float(d.Seconds())}
}

// Time is a moment shown as text says, valued as RFC 3339 in UTC, for a
// KindTimestamp column. The zero time has no value, since it is how a moment
// nobody recorded is spelled.
func Time(text string, t time.Time) Cell {
	if t.IsZero() {
		return Cell{Text: text}
	}
	return Cell{Text: text, Value: t.UTC().Format(time.RFC3339Nano)}
}

// Status is a status shown as text says ("WARN soon · Oct 31"), valued as the
// one word it grades by ("warn"), for a KindStatus column. A word outside
// ValueOK's grammar for a status has no value.
func Status(text, word string) Cell {
	if !statusWord.MatchString(word) {
		return Cell{Text: text}
	}
	return Cell{Text: text, Value: word}
}

// Graded is a status valued as the first word it is shown with, in lower
// case: "WARN >80%" is valued "warn", "ok" is "ok". The common shape, where
// the word a status grades by leads its text; Status names the word where it
// does not.
func Graded(text string) Cell {
	word := text
	if i := strings.IndexFunc(text, func(r rune) bool { return !wordRune(r) }); i >= 0 {
		word = text[:i]
	}
	return Status(text, strings.ToLower(word))
}

// wordRune is a character the word leading a status is made of.
func wordRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

func decimal[T integer](n T) string {
	if n < 0 {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatUint(uint64(n), 10)
}

func float(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return ""
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// number is the JSON number grammar: what a value of a numeric kind is written
// as, so that it can be written into JSON as a number and read back as one.
var number = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// statusWord is what a status is valued as: the word it grades by, in lower
// case, as theme.ClassifyStatus reads it. A few words are several ("did not
// answer"), so single spaces are allowed between them.
var statusWord = regexp.MustCompile(`^[a-z][a-z0-9_-]*( [a-z0-9_-]+)*$`)

// ValueOK reports whether value is a well-formed value for a column of kind:
// a JSON number for a count, a size, a percentage or a duration in seconds, an
// RFC 3339 time for a timestamp, a lower-case word for a status. A text column
// has no values, so nothing is one.
//
// What a value from a plugin is held to (pkg/sdk/wire), since what is written
// here reaches JSON as a number without being quoted: a value that is not one
// would break the document a program parses, not merely be wrong in it.
func ValueOK(kind ColumnKind, value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	switch kind {
	case KindNumber, KindBytes, KindPercent, KindUsage, KindDuration:
		return number.MatchString(value)
	case KindTimestamp:
		_, err := time.Parse(time.RFC3339Nano, value)
		return err == nil
	case KindStatus:
		return statusWord.MatchString(value)
	}
	return false
}

// typedValues is t's values as JSON writes them, row for row with Rows and
// column for column: a number as a number, a time and a status as strings,
// and null where a cell has none or its value is not one its column takes.
// Nil when t carries no value at all, so that such a table encodes as it
// always did.
func typedValues(t Table) [][]any {
	if len(t.Values) == 0 {
		return nil
	}
	out := make([][]any, len(t.Rows))
	found := false
	for i := range t.Rows {
		row := make([]any, len(t.Columns))
		var values []string
		if i < len(t.Values) {
			values = t.Values[i]
		}
		for j, c := range t.Columns {
			if j >= len(values) || !ValueOK(c.Kind, values[j]) {
				continue
			}
			switch c.Kind {
			case KindTimestamp, KindStatus:
				row[j] = values[j]
			default:
				row[j] = json.Number(values[j])
			}
			found = true
		}
		out[i] = row
	}
	if !found {
		return nil
	}
	return out
}
