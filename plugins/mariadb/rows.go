package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// ErrTooManyRows and ErrTooLarge are the two ways a result set refuses to
// become a table.
//
// Both are refusals rather than truncations, and that is the decision worth
// stating: a shortened result set is a different answer wearing the right
// shape. Somebody who asked for every row in a table and silently got the
// first fifty has been told something false about their data, and nothing in
// the output says so. Refusing names the bound and names the flag.
var (
	ErrTooManyRows = errors.New("too many rows")
	ErrTooLarge    = errors.New("result too large")
)

// maxBytes bounds a result by size as well as by count, because a row bound
// is not a size bound. One wide TEXT or BLOB column is what actually exhausts
// memory here — fifty rows of it is not fifty rows of anything else.
const maxBytes = 8 << 20

// rowsToTable renders a result set, bounded in both directions.
//
// The scan target is []any of *any, which is how database/sql hands back a
// value whose type is only known at runtime. The driver gives []byte for most
// string-ish columns, so cell below is where every type becomes text exactly
// once rather than at each of the seven call sites.
func rowsToTable(rows *sql.Rows, limit int) (view.Table, error) {
	names, err := rows.Columns()
	if err != nil {
		return view.Table{}, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return view.Table{}, err
	}

	t := view.Table{Columns: make([]view.Column, len(names))}
	for i, n := range names {
		t.Columns[i] = view.Column{Name: n, Kind: kindOf(types[i])}
	}

	size := 0
	for rows.Next() {
		if len(t.Rows) == limit {
			// Drained before returning: leaving a result set open holds the
			// connection, and this one is about to be handed an error rather
			// than closed by a happy path.
			return view.Table{}, ErrTooManyRows
		}
		scan := make([]any, len(names))
		holders := make([]any, len(names))
		for i := range scan {
			holders[i] = &scan[i]
		}
		if err := rows.Scan(holders...); err != nil {
			return view.Table{}, err
		}
		row := make([]string, len(names))
		for i, v := range scan {
			row[i] = cell(v)
			size += len(row[i])
		}
		if size > maxBytes {
			return view.Table{}, ErrTooLarge
		}
		t.Rows = append(t.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return view.Table{}, err
	}
	t.Total = len(t.Rows)
	return t, nil
}

// cell turns one scanned value into text.
//
// NULL renders as an empty cell rather than the word "NULL", because a table
// somebody reads should not make them wonder whether a column literally holds
// that string — and the two are genuinely indistinguishable once printed.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

// kindOf maps a column's declared database type onto the view kind that
// decides alignment and formatting. Getting this from the driver rather than
// from the value means an all-NULL numeric column still right-aligns.
func kindOf(ct *sql.ColumnType) view.ColumnKind {
	switch name := strings.ToUpper(ct.DatabaseTypeName()); name {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT",
		"UNSIGNED TINYINT", "UNSIGNED SMALLINT", "UNSIGNED INT", "UNSIGNED BIGINT",
		"DECIMAL", "FLOAT", "DOUBLE", "NEWDECIMAL":
		return view.KindNumber
	case "DATE", "DATETIME", "TIMESTAMP":
		return view.KindTimestamp
	default:
		return ""
	}
}

// bytesCell renders a byte count that arrived as an untyped scan value, which
// is what INFORMATION_SCHEMA size expressions produce. A size column nobody
// can read at a glance is a column that gets piped into another tool instead
// of being looked at.
func bytesCell(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case int64:
		if x < 0 {
			return "-"
		}
		return format.Bytes(uint64(x))
	case []byte:
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil || n < 0 {
			return "-"
		}
		return format.Bytes(uint64(n))
	case float64:
		if x < 0 {
			return "-"
		}
		return format.Bytes(uint64(x))
	default:
		return fmt.Sprint(x)
	}
}
