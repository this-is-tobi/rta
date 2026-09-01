//go:build livepg

// The backup/restore chain against a real server — the questions the unit
// tests cannot answer: does what pg.dump writes actually come back through
// pg.restore, in every format, with the rows intact.
//
//	docker run --rm -d --name rta-pg-lab -e POSTGRES_PASSWORD=lab -p 5499:5432 postgres:18
//	RTA_TEST_PG_PORT=5499 RTA_TEST_PG_PASSWORD=lab \
//	  go test . -tags livepg -count=1 -v -run TestTheChain
//	docker rm -f rta-pg-lab
//
// The tunnel package's livecluster tag is the same shape for the same
// reason: a stub can prove the argv and the refusals, and only a server can
// prove the round trip.
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func liveValues(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	port := os.Getenv("RTA_TEST_PG_PORT")
	if port == "" {
		t.Skip("set RTA_TEST_PG_PORT and RTA_TEST_PG_PASSWORD — setup is the package doc above")
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("RTA_TEST_PG_PORT = %q, want a port", port)
	}
	base := map[string]any{
		"host": "127.0.0.1", "port": p, "user": "postgres",
		"password": os.Getenv("RTA_TEST_PG_PASSWORD"),
	}
	for k, v := range values {
		base[k] = v
	}
	return base
}

// admin runs one statement as the postgres superuser against the named
// database, through the plugin's own connect so the DSN discipline under
// test is the one used to arrange the test.
func admin(t *testing.T, database, sql string) {
	t.Helper()
	ctx := context.Background()
	conn, verr := connect(ctx, reqFor(t, "pg.status", liveValues(t, map[string]any{"database": database})))
	if verr != nil {
		t.Fatalf("connecting to %s: %v", database, verr)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func rowsIn(t *testing.T, database string) (count int, note string) {
	t.Helper()
	ctx := context.Background()
	conn, verr := connect(ctx, reqFor(t, "pg.status", liveValues(t, map[string]any{"database": database})))
	if verr != nil {
		t.Fatalf("connecting to %s: %v", database, verr)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx,
		"select count(*), coalesce(min(note), '') from orders").Scan(&count, &note); err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return 0, ""
		}
		t.Fatal(err)
	}
	return count, note
}

func TestTheChainRoundTripsAgainstARealServer(t *testing.T) {
	ctx := context.Background()

	const src = "rta_chain_src"
	admin(t, "postgres", "drop database if exists "+src)
	admin(t, "postgres", "create database "+src)
	t.Cleanup(func() { admin(t, "postgres", "drop database if exists "+src) })

	// The note carries an embedded quote and non-ASCII, because COPY escaping
	// is exactly what "shells out rather than reimplements" is buying.
	admin(t, src, "create table orders (id int primary key, note text)")
	admin(t, src, `insert into orders values (1, 'it''s café №1'), (2, 'two'), (3, 'three')`)

	for _, tc := range []struct {
		format string
		jobs   int
	}{
		{"plain", 1},
		{"custom", 1},
		{"directory", 3},
	} {
		t.Run(tc.format, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "chain-"+tc.format+backupSuffix(tc.format))
			if _, err := runFullDump(ctx, reqFor(t, "pg.dump", liveValues(t, map[string]any{
				"database": src, "out": out, "format": tc.format, "jobs": tc.jobs,
			}))); err != nil {
				t.Fatalf("dump: %v", err)
			}

			tgt := "rta_chain_tgt_" + tc.format
			admin(t, "postgres", "drop database if exists "+tgt)
			admin(t, "postgres", "create database "+tgt)
			t.Cleanup(func() { admin(t, "postgres", "drop database if exists "+tgt) })

			v, err := runRestore(ctx, reqFor(t, "pg.restore", liveValues(t, map[string]any{
				"database": tgt, "file": out, "jobs": tc.jobs,
			})))
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if _, ok := v.(view.KeyValue); !ok {
				t.Fatalf("want a receipt, got %s", view.TypeOf(v))
			}

			count, note := rowsIn(t, tgt)
			if count != 3 || note != "it's café №1" {
				t.Fatalf("round trip lost data: count=%d note=%q", count, note)
			}

			// The mirror of the dump's O_EXCL: the same restore again lands on
			// a database that now holds relations, and is refused.
			_, err = runRestore(ctx, reqFor(t, "pg.restore", liveValues(t, map[string]any{
				"database": tgt, "file": out, "jobs": tc.jobs,
			})))
			var verr *view.Error
			if !errors.As(err, &verr) || verr.Code != "pg.restore.notempty" {
				t.Fatalf("second restore = %v, want pg.restore.notempty", err)
			}
			if tc.format == "plain" && strings.Contains(verr.Hint, "--clean") {
				t.Errorf("a plain refusal suggests --clean, which plain restores refuse: %q", verr.Hint)
			}

			// And --clean is the named way through, for the formats that have it.
			if tc.format != "plain" {
				if _, err := runRestore(ctx, reqFor(t, "pg.restore", liveValues(t, map[string]any{
					"database": tgt, "file": out, "jobs": tc.jobs, "clean": true,
				}))); err != nil {
					t.Fatalf("restore --clean: %v", err)
				}
				if count, _ := rowsIn(t, tgt); count != 3 {
					t.Fatalf("--clean restore left %d rows, want 3", count)
				}
			}
		})
	}

	// A missing target is a classified refusal naming createdb, not a child
	// process exiting oddly.
	out := filepath.Join(t.TempDir(), "missing.sql")
	if _, err := runFullDump(ctx, reqFor(t, "pg.dump", liveValues(t, map[string]any{
		"database": src, "out": out, "format": "plain",
	}))); err != nil {
		t.Fatalf("dump: %v", err)
	}
	_, err := runRestore(ctx, reqFor(t, "pg.restore", liveValues(t, map[string]any{
		"database": "rta_chain_never_created", "file": out,
	})))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "pg.restore.nodatabase" {
		t.Fatalf("missing database = %v, want pg.restore.nodatabase", err)
	}
	if !strings.Contains(verr.Hint, "createdb") {
		t.Errorf("hint = %q, want it to name createdb", verr.Hint)
	}
}

// A failed plain restore rolls back to nothing rather than to half a
// database — ON_ERROR_STOP inside --single-transaction is the pair that
// makes the receipt's guarantee true, and this is the test that would catch
// either half going missing.
func TestAFailedRestoreLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()

	tgt := "rta_chain_rollback"
	admin(t, "postgres", "drop database if exists "+tgt)
	admin(t, "postgres", "create database "+tgt)
	t.Cleanup(func() { admin(t, "postgres", "drop database if exists "+tgt) })

	// A dump whose second half fails: the table lands, then a reference to a
	// relation that does not exist.
	bad := filepath.Join(t.TempDir(), "bad.sql")
	if err := os.WriteFile(bad, []byte(
		"create table orders (id int primary key, note text);\n"+
			"insert into orders values (1, 'one');\n"+
			"insert into no_such_table values (1);\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runRestore(ctx, reqFor(t, "pg.restore", liveValues(t, map[string]any{
		"database": tgt, "file": bad,
	})))
	if err == nil {
		t.Fatal("a dump referencing a missing table restored without complaint")
	}
	if count, _ := rowsIn(t, tgt); count != 0 {
		t.Fatalf("the failed restore committed %d rows — the transaction did not roll back", count)
	}
}
