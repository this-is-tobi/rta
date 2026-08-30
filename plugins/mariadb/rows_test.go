package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The row bound is a security property, not a convenience: it is what stops a
// single query from pulling a whole table through an agent. Testing it against
// a real MySQL would make the suite need a server, so this registers a driver
// that answers with exactly the rows a test asks for.
//
// Roughly sixty lines to avoid a container, and it buys the thing that
// actually matters — rowsToTable is exercised through database/sql, with real
// *sql.Rows and real driver types, rather than through a hand-made fake of the
// type it is supposed to consume.

type fakeDriver struct{}

// fakeAnswer is what the next Query returns, set by the test before it opens a
// connection. A package-level variable rather than a DSN encoding, because the
// tests in this file are the only caller and a parser here would be more code
// than the thing it tests.
var fakeAnswer struct {
	columns []string
	rows    [][]driver.Value
	err     error
}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return fakeTx{}, nil }

// lastTxOptions records what the query path asked for, so a test can assert
// the transaction was opened READ ONLY. Without ConnBeginTx here, database/sql
// would reject a read-only request outright and the assertion would be about
// the fake rather than about the code under test.
var lastTxOptions driver.TxOptions

func (fakeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	lastTxOptions = opts
	return fakeTx{}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{}

func (fakeStmt) Close() error  { return nil }
func (fakeStmt) NumInput() int { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("not used")
}
func (fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	if fakeAnswer.err != nil {
		return nil, fakeAnswer.err
	}
	return &fakeRows{}, nil
}

type fakeRows struct{ at int }

func (r *fakeRows) Columns() []string { return fakeAnswer.columns }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.at >= len(fakeAnswer.rows) {
		return io.EOF
	}
	copy(dest, fakeAnswer.rows[r.at])
	r.at++
	return nil
}

func init() { sql.Register("mariadb-fake", fakeDriver{}) }

// queryFake runs one statement through database/sql against the fake driver,
// so what rowsToTable receives is a genuine *sql.Rows.
func queryFake(t *testing.T, columns []string, rows [][]driver.Value) *sql.Rows {
	t.Helper()
	fakeAnswer.columns, fakeAnswer.rows, fakeAnswer.err = columns, rows, nil
	db, err := sql.Open("mariadb-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	out, err := db.Query("select")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	return out
}

// The decision this pins: over the limit it refuses. A shortened result set is
// a different answer wearing the right shape — somebody who asked for every
// row and silently got the first fifty has been told something false about
// their data, and nothing in the output says so.
func TestOverTheRowLimitIsRefusedNotTruncated(t *testing.T) {
	rows := make([][]driver.Value, 0, 10)
	for i := range 10 {
		rows = append(rows, []driver.Value{int64(i)})
	}

	_, err := rowsToTable(queryFake(t, []string{"id"}, rows), 5)
	if !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("err = %v, want ErrTooManyRows — a truncated table would be silently wrong", err)
	}

	// Exactly at the limit is not over it. Off-by-one here would refuse an
	// answer that fits, which is the failure nobody reports because it looks
	// like the bound working.
	tbl, err := rowsToTable(queryFake(t, []string{"id"}, rows[:5]), 5)
	if err != nil {
		t.Fatalf("exactly at the limit was refused: %v", err)
	}
	if tbl.Total != 5 {
		t.Errorf("Total = %d, want 5", tbl.Total)
	}
}

// A row bound is not a size bound. One wide TEXT or BLOB column is what
// actually exhausts memory here, and fifty rows of it is not fifty rows of
// anything else.
func TestOverTheSizeLimitIsRefusedEvenWithFewRows(t *testing.T) {
	wide := strings.Repeat("x", 1<<20)
	rows := make([][]driver.Value, 0, 16)
	for range 16 {
		rows = append(rows, []driver.Value{[]byte(wide)})
	}
	_, err := rowsToTable(queryFake(t, []string{"blob"}, rows), 50)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge — 16 rows is well under the row bound", err)
	}
}

// Column names and NULL handling survive the trip through database/sql, which
// is the part a hand-made fake of *sql.Rows would not have proved.
func TestResultSetsRenderThroughDatabaseSQL(t *testing.T) {
	tbl, err := rowsToTable(queryFake(t,
		[]string{"id", "name", "note"},
		[][]driver.Value{
			{int64(1), []byte("alice"), nil},
			{int64(2), []byte("bob"), []byte("hello")},
		}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tbl.Columns) != 3 || tbl.Columns[1].Name != "name" {
		t.Fatalf("columns = %+v", tbl.Columns)
	}
	if tbl.Rows[0][1] != "alice" {
		t.Errorf("a []byte column did not become text: %q", tbl.Rows[0][1])
	}
	if tbl.Rows[0][2] != "" {
		t.Errorf("NULL rendered as %q, want an empty cell", tbl.Rows[0][2])
	}
	if tbl.Total != 2 {
		t.Errorf("Total = %d, want 2", tbl.Total)
	}
}

// An empty result is an empty table, not an error and not a nil view. A caller
// that has to distinguish "no rows" from "failed" by checking for nil is a
// caller that will eventually forget.
func TestAnEmptyResultIsAnEmptyTable(t *testing.T) {
	tbl, err := rowsToTable(queryFake(t, []string{"id"}, nil), 50)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Total != 0 {
		t.Errorf("Total = %d, want 0", tbl.Total)
	}
	if len(tbl.Columns) != 1 {
		t.Errorf("an empty result lost its column headers: %+v", tbl.Columns)
	}
}

// Kinds come from the driver's declared column type rather than from a value,
// so an all-NULL numeric column still right-aligns. The fake driver declares
// no types, which is exactly the case that must not panic.
func TestColumnKindsSurviveADriverThatDeclaresNoTypes(t *testing.T) {
	tbl, err := rowsToTable(queryFake(t, []string{"n"}, [][]driver.Value{{nil}}), 50)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Columns[0].Kind != view.ColumnKind("") {
		t.Errorf("kind = %q, want the text default when the driver declares nothing", tbl.Columns[0].Kind)
	}
}

// Sanity check on the fake itself: a test harness that silently answers
// nothing would make every assertion above vacuous.
func TestTheFakeDriverActuallyAnswers(t *testing.T) {
	rows := queryFake(t, []string{"one"}, [][]driver.Value{{int64(1)}})
	n := 0
	for rows.Next() {
		n++
	}
	if n != 1 {
		t.Fatalf("fake driver returned %d rows, want 1 — every other test here is vacuous", n)
	}
}
