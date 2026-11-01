package view

import (
	"encoding/json"
	"math"
	"regexp"
	"slices"
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
// Plain cells carries no Values at all, and is encoded with only what its
// text already holds (typedValues).
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

// Plain is a cell with no value of its own: a name, a word, a path — or a
// status or a count, which the encoder values from its text (typedValues).
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

// Status is a status shown as text says, valued as the word it grades by, for
// a KindStatus column whose text does not lead with that word: "did not
// answer", which is three, or "Init:CrashLoopBackOff", valued
// "crashloopbackoff". A status that leads with its word ("WARN soon · Oct 31")
// needs no Status, since the encoder values it as that word already
// (leadingWord). A word outside ValueOK's grammar for a status has no value.
func Status(text, word string) Cell {
	if !statusWord.MatchString(word) {
		return Cell{Text: text}
	}
	return Cell{Text: text, Value: word}
}

// leadingWord is the word text leads with, in lower case — "warn" for "WARN
// >80%", "ok" for "ok — 3 pods" — and "" where it leads with anything that is
// not a word: "—", "1/1", "n/a". What a status with no value of its own is
// valued as: the word it grades by, in the shape every status here is written
// in, which is the shape the renderer reads its grade from
// (theme.ClassifyStatus).
//
// The word ends where the renderer ends it, at a space or at the punctuation
// a status line puts after its word, and the whole of it has to be a word.
// Cut at the first character that is not a letter, n/a would be valued "n"
// and upstream.example.com "upstream": a value made of a fragment, where the
// renderer reads a name and grades nothing.
func leadingWord(text string) string {
	word := text
	if i := strings.IndexAny(text, " \t,;:()—·"); i >= 0 {
		word = text[:i]
	}
	if word = strings.ToLower(word); !statusWord.MatchString(word) {
		return ""
	}
	return word
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
// and null where a cell has none or its value is not one its column takes. A
// cell its producer gave no value is valued as its text, where the text
// already is one (textValue). Nil when no cell has a value, so that such a
// table encodes as it always did.
func typedValues(t Table) [][]any {
	if len(t.Values) == 0 && !slices.ContainsFunc(t.Columns, func(c Column) bool {
		return c.Kind == KindStatus || c.Kind == KindNumber || c.Kind == KindTimestamp
	}) {
		return nil
	}
	out := make([][]any, len(t.Rows))
	found := false
	for i := range t.Rows {
		row := make([]any, len(t.Columns))
		for j, c := range t.Columns {
			value := t.Value(i, j)
			if value == "" {
				continue
			}
			switch c.Kind {
			case KindTimestamp, KindStatus:
				row[j] = value
			default:
				row[j] = json.Number(value)
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

// Value is the value of the cell at row and col as a program reads it, the one
// json, yaml and MCP write for it: its producer's, where that is one its
// column's kind takes, and otherwise the one its text already is (textValue).
// "" for a cell with none, a malformed value of its producer's among them,
// which is none rather than replaced by its text's.
//
// For a reader beside the encoder that orders or compares cells — the TUI's
// sort — so a size sorts by the bytes a program would read, not by its text.
func (t Table) Value(row, col int) string {
	if row < 0 || row >= len(t.Rows) || col < 0 || col >= len(t.Columns) {
		return ""
	}
	kind := t.Columns[col].Kind
	if row < len(t.Values) && col < len(t.Values[row]) && t.Values[row][col] != "" {
		if v := t.Values[row][col]; ValueOK(kind, v) {
			return v
		}
		return ""
	}
	if cells := t.Rows[row]; col < len(cells) {
		if v := textValue(kind, cells[col]); ValueOK(kind, v) {
			return v
		}
	}
	return ""
}

// textValue is the value a cell's text already is, for a cell whose producer
// gave it none: a status's leading word, a count written as a number, and a
// moment written in RFC 3339, in UTC as every time is valued.
//
// **Only these three, because only for these is the text the value.** A
// status is graded by the word it leads with on every surface already, a count
// written as a JSON number is that number, and an RFC 3339 time names its own
// zone. A size shown as `926.4 GiB`, a percentage and a length of time are
// rounded or spelled for a person, and reading one back is the guess the
// values exist to replace: only the producer holds what it was made from. So
// a plugin that fills nothing still hands a program its statuses, its counts
// and its times, and fills the rest through the cell helpers; a value it gives
// always wins, and a malformed one is written as none rather than replaced by
// this. The zero time is none, as Time has it.
func textValue(kind ColumnKind, text string) string {
	switch kind {
	case KindStatus:
		return leadingWord(text)
	case KindNumber:
		if number.MatchString(text) {
			return text
		}
	case KindTimestamp:
		if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return Time(text, t).Value
		}
	}
	return ""
}
