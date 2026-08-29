package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// pg.query is the one capability in this plugin whose result set the *caller*
// sizes: every other listing declares a limit and applies it in SQL, and this
// one runs whatever text arrives.
//
// Unbounded, `select * from users` read every row into a slice in the plugin,
// passed it through the host, and aimed it at a model's context. Two defects
// in one: an allocation with no ceiling that an argument controls, and a bulk
// read of a table nobody consented to row by row — the second being the
// interesting half, because every other control in rta bounds a call by what
// it names, and this one named a table and returned all of it.

// The declaration is the enforcement. A limit that exists as prose in a
// description is not a limit, so this asserts the field is there with a
// default that applies when nobody passes one.
func TestTheQueryCapabilityDeclaresARowBound(t *testing.T) {
	var found bool
	for _, c := range Plugin().Capabilities {
		if c.ID != "pg.query" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name != "limit" {
				continue
			}
			found = true
			if f.Type != plugin.Int {
				t.Errorf("limit is %s, want Int", f.Type)
			}
			if f.Default == nil {
				t.Error("limit has no default, so an MCP caller that omits it is unbounded")
			}
			if f.Max == 0 {
				t.Error("limit has no maximum, so a caller can ask for an unbounded read anyway")
			}
		}
	}
	if !found {
		t.Fatal("pg.query does not declare a row bound")
	}
}

// The bound itself, at the one function every result set in this plugin goes
// through. Driven directly rather than through a live server: what is under
// test is that the reader stops, which needs no PostgreSQL to observe.
func TestRowsToTableStopsAtTheBound(t *testing.T) {
	rows := &fakeRows{cols: []string{"id"}, remaining: 10}
	tbl, err := rowsToTable(rows, 3)
	if !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("err = %v, want ErrTooManyRows", err)
	}
	if len(tbl.Rows) != 3 {
		t.Errorf("rows read = %d, want the reader to stop at the bound", len(tbl.Rows))
	}
	// One past the bound is what tells a full page from an overflowing one:
	// the reader must have asked for a fourth row and refused it, rather than
	// counting to three and calling it complete.
	if rows.asked != 4 {
		t.Errorf("Next() calls = %d, want 4 — a result set that ends exactly on the "+
			"bound must not be reported as overflowing", rows.asked)
	}
}

// A result set that ends exactly on the bound is complete, not refused.
func TestAResultSetThatFitsExactlyIsNotRefused(t *testing.T) {
	tbl, err := rowsToTable(&fakeRows{cols: []string{"id"}, remaining: 3}, 3)
	if err != nil {
		t.Fatalf("a result set of exactly the bound was refused: %v", err)
	}
	if len(tbl.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(tbl.Rows))
	}
}

// rta's own queries carry their limit in SQL, so they pass their declared
// bound and can never trip it — but they pass one, because "this query is
// small" is an assumption about a catalogue on somebody else's server.
func TestEveryQueryInThisPluginPassesABound(t *testing.T) {
	if maxRows <= 0 {
		t.Fatal("maxRows is not a bound")
	}
	tbl, err := rowsToTable(&fakeRows{cols: []string{"id"}, remaining: 2}, 0)
	if err != nil {
		t.Fatalf("a zero bound should fall back to maxRows, not refuse: %v", err)
	}
	if len(tbl.Rows) != 2 {
		t.Errorf("rows = %d, want 2", len(tbl.Rows))
	}
}

// **A row bound is not a size bound**, and the row bound was the whole
// protection here. `select body from documents` at two hundred rows is two
// hundred rows of whatever a text column holds, and a bytea column makes it
// arbitrary.
//
// It is a correctness bound rather than a courtesy: a plugin's view crosses
// go-plugin's gRPC channel, nothing configures MaxCallRecvMsgSize on either
// side, and past grpc-go's 4 MiB default the caller gets neither a large
// answer nor a truncated one — the transport fails with ResourceExhausted,
// naming gRPC rather than the query, from a layer with no flag for it.
func TestRowsToTableStopsAtTheSizeBound(t *testing.T) {
	// Well inside the row bound, well past the byte one.
	rows := &fakeRows{cols: []string{"body"}, remaining: 100, width: maxBytes / 4}
	tbl, err := rowsToTable(rows, 1000)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if len(tbl.Rows) >= 100 {
		t.Errorf("rows read = %d, want the reader to stop early on size", len(tbl.Rows))
	}
}

// The two bounds are separate answers, so an operator is told which one they
// hit and therefore which flag moves it: --limit does nothing about one wide
// column, and narrowing the columns does nothing about a million narrow rows.
func TestTheRowAndSizeBoundsAreDistinguishable(t *testing.T) {
	narrow := &fakeRows{cols: []string{"id"}, remaining: 100}
	if _, err := rowsToTable(narrow, 3); !errors.Is(err, ErrTooManyRows) {
		t.Errorf("many narrow rows: err = %v, want ErrTooManyRows", err)
	}
	wide := &fakeRows{cols: []string{"body"}, remaining: 100, width: maxBytes / 4}
	if _, err := rowsToTable(wide, 1000); !errors.Is(err, ErrTooLarge) {
		t.Errorf("few wide rows: err = %v, want ErrTooLarge", err)
	}
}

// A result set comfortably inside both bounds is returned whole.
func TestAResultSetInsideBothBoundsIsReturned(t *testing.T) {
	rows := &fakeRows{cols: []string{"body"}, remaining: 3, width: 10}
	tbl, err := rowsToTable(rows, 10)
	if err != nil {
		t.Fatalf("a small result set was refused: %v", err)
	}
	if len(tbl.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(tbl.Rows))
	}
}

// fakeRows is a pgx.Rows that yields `remaining` single-column rows and counts
// how many times it was asked for another. `width`, when set, makes each cell
// that many bytes, which is how the size bound is driven.
//
// A fake rather than a live server because what is under test is that the
// reader stops asking — which no amount of real PostgreSQL makes easier to
// observe, and which a live fixture would hide behind however many rows
// somebody remembered to insert.
type fakeRows struct {
	cols      []string
	remaining int
	width     int
	asked     int
	n         int
}

func (f *fakeRows) Close()                        {}
func (f *fakeRows) Err() error                    { return nil }
func (f *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (f *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, 0, len(f.cols))
	for _, c := range f.cols {
		out = append(out, pgconn.FieldDescription{Name: c})
	}
	return out
}
func (f *fakeRows) Next() bool {
	f.asked++
	if f.n >= f.remaining {
		return false
	}
	f.n++
	return true
}
func (f *fakeRows) Scan(...any) error { return nil }
func (f *fakeRows) Values() ([]any, error) {
	if f.width > 0 {
		return []any{strings.Repeat("x", f.width)}, nil
	}
	return []any{f.n}, nil
}
func (f *fakeRows) RawValues() [][]byte { return nil }
func (f *fakeRows) Conn() *pgx.Conn     { return nil }
