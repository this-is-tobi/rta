package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The read/write split in this plugin is enforced in two places, and both are
// the kind of thing that keeps working right up until somebody simplifies it.
// Neither was covered until probing found that removing the enforcement broke
// nothing — which is the only way to learn a test suite has a hole in it.

// fakeDB opens the fake driver as a *sql.DB, for the handlers that take one
// rather than a *sql.Rows.
func fakeDB(t *testing.T, columns []string, rows [][]driver.Value) *sql.DB {
	t.Helper()
	fakeAnswer.columns, fakeAnswer.rows, fakeAnswer.err = columns, rows, nil
	db, err := sql.Open("mariadb-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// processlistColumns and one row of it, in the order activityView scans.
var processlistColumns = []string{"ID", "USER", "HOST", "DB", "COMMAND", "TIME", "STATE", "INFO"}

func processlistRow(statement string) []driver.Value {
	return []driver.Value{
		int64(42), []byte("app"), []byte("10.0.0.9:51000"), []byte("shop"),
		[]byte("Query"), int64(12), []byte("Sending data"), []byte(statement),
	}
}

// **The read tier must not be able to produce the statement column.**
//
// mariadb.overview --detail and mariadb.activity share one function, and
// withStatements is the entire difference between them. If that flag stops
// being honoured, a read-tier call starts returning the text of every
// statement running on the server — which is where the literals are, and the
// reason mariadb.activity is a write in the first place.
func TestTheReadTierNeverReturnsStatementText(t *testing.T) {
	secret := "SELECT * FROM patients WHERE mrn = 'A-12345'"
	db := fakeDB(t, processlistColumns, [][]driver.Value{processlistRow(secret)})
	r := req(t, "mariadb.overview", map[string]any{})

	v, err := activityView(context.Background(), db, r, false)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}

	for _, c := range tbl.Columns {
		if strings.EqualFold(c.Name, "statement") {
			t.Fatalf("the read tier produced a %q column", c.Name)
		}
	}
	// Belt and braces: the literal must not appear in any cell, whatever the
	// column ended up being called.
	for _, row := range tbl.Rows {
		for _, cell := range row {
			if strings.Contains(cell, "A-12345") {
				t.Fatalf("a statement literal reached the read tier: %q", cell)
			}
		}
	}
}

// The other half of the same claim: mariadb.activity, which is a write, does
// return it. A test that only checked the read tier would pass just as well
// against a version that never returns statements at all.
func TestTheWriteTierDoesReturnStatementText(t *testing.T) {
	secret := "SELECT * FROM patients WHERE mrn = 'A-12345'"
	db := fakeDB(t, processlistColumns, [][]driver.Value{processlistRow(secret)})
	r := req(t, "mariadb.activity", map[string]any{})

	v, err := activityView(context.Background(), db, r, true)
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)

	found := false
	for _, c := range tbl.Columns {
		if c.Name == "Statement" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mariadb.activity has no Statement column: %+v", tbl.Columns)
	}
	if !strings.Contains(strings.Join(tbl.Rows[0], " "), "A-12345") {
		t.Errorf("mariadb.activity did not return the statement text: %v", tbl.Rows[0])
	}

	// The statement column is bounded at its call site, not only inside the
	// helper. A statement written across twelve lines in application source
	// arrives with all of them, and would turn one table row into a page.
	long := "SELECT " + strings.Repeat("col_name, ", 60) + "1 FROM t"
	db = fakeDB(t, processlistColumns, [][]driver.Value{processlistRow(long)})
	v, err = activityView(context.Background(), db, r, true)
	if err != nil {
		t.Fatal(err)
	}
	last := v.(view.Table).Rows[0]
	if got := last[len(last)-1]; len([]rune(got)) > statementWidth+1 {
		t.Errorf("statement column is %d runes — the bound is not applied where it is used", len([]rune(got)))
	}
}

// **The transaction must be opened READ ONLY.**
//
// That is the entire enforcement for mariadb.query: rta does not inspect the SQL
// and should not — a blocklist against a language with this many ways to spell
// a write is a promise nobody can keep. The server refusing writes is the real
// mechanism, and it only exists if the option is actually set.
func TestQueryRunsInsideAReadOnlyTransaction(t *testing.T) {
	lastTxOptions = driver.TxOptions{}
	db := fakeDB(t, []string{"n"}, [][]driver.Value{{int64(1)}})

	tx, err := db.BeginTx(context.Background(),
		&sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if !lastTxOptions.ReadOnly {
		t.Fatal("the fake driver does not record ReadOnly — the assertion below would be vacuous")
	}

	// Now the real path, which is what is actually being pinned.
	lastTxOptions = driver.TxOptions{}
	fakeAnswer.columns, fakeAnswer.rows = []string{"n"}, [][]driver.Value{{int64(1)}}
	if _, err := queryView(context.Background(), db, req(t, "mariadb.query", map[string]any{"sql": "select 1"})); err != nil {
		t.Fatal(err)
	}
	if !lastTxOptions.ReadOnly {
		t.Error("mariadb.query opened a read-write transaction — the server will no longer refuse writes")
	}
}
