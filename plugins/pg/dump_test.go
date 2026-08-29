package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// reqFor resolves against a named capability rather than the first one, so
// these see the inputs the handler under test actually declares — defaults
// included, which is where a bound lives.
func reqFor(t *testing.T, id string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Request{}
}

func mcpReqFor(t *testing.T, id string, values map[string]any) plugin.Request {
	t.Helper()
	return reqFor(t, id, values).WithSurface(plugin.SurfaceMCP)
}

func dryRunReqFor(t *testing.T, id string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), true, false)
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Request{}
}

// **The rule that makes a grant on this capability mean something.**
//
// The grant gate matches the caller's raw argument byte-for-byte against the
// scope a person wrote, before anything looks in the catalogue. So a bare
// name resolved afterwards would let one unexpired grant follow whichever
// schema resolved first: drop public.orders, create archive.orders, and the
// same grant reads a different table. Requiring schema.table over MCP makes
// the string in the ledger and the object read the same thing by
// construction, with no lookup in between to drift.
//
// **That it refuses before asking the database anything is half the test.**
// A refusal that happens after the catalogue lookup is a refusal that
// happened after the work, and the recording querier is what holds that
// down.
func TestAnUnqualifiedNameIsRefusedOverMCP(t *testing.T) {
	q := &recordingQuerier{}
	_, verr := resolveRelation(context.Background(), q,
		mcpReqFor(t, "pg.table.dump", map[string]any{"table": "users"}))
	if verr == nil {
		t.Fatal("an unqualified table name was accepted from an MCP caller")
	}
	if q.asked != 0 {
		t.Errorf("the catalogue was queried %d times before the refusal", q.asked)
	}
	if verr.Code != "pg.table.unqualified" {
		t.Fatalf("code = %q, want pg.table.unqualified", verr.Code)
	}
	// The hint has to say that issuing a grant for the bare name will not
	// help, because the host's own refusal — which comes first, and which the
	// operator sees first — tells them to do exactly that.
	if !strings.Contains(verr.Hint, "will not help") {
		t.Errorf("hint = %q, want it to say a grant for the bare name does not help", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "<schema>.users") {
		t.Errorf("hint = %q, want it to show the form to grant instead", verr.Hint)
	}
}

// A person at a terminal keeps the short form. Refusing an unqualified name
// is a decision about *whether* a call is allowed, which a
// handler to take from the surface; changing *what* the call does would not
// be. The CLI has a human to disambiguate for, and an ambiguous name is
// refused there rather than resolved by precedence.
func TestABareNameIsNotRefusedForAPerson(t *testing.T) {
	q := &recordingQuerier{}
	_, verr := resolveRelation(context.Background(), q,
		reqFor(t, "pg.table.dump", map[string]any{"table": "users"}))
	if verr != nil && verr.Code == "pg.table.unqualified" {
		t.Fatal("a person at a terminal was made to qualify a table name")
	}
	if q.asked != 1 {
		t.Errorf("catalogue lookups = %d, want the bare name resolved against it", q.asked)
	}
}

// recordingQuerier counts catalogue lookups and fails them, which is enough
// to tell "refused before asking" from "refused after asking".
type recordingQuerier struct{ asked int }

func (q *recordingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	q.asked++
	return nil, errors.New("no database in this test")
}

func (q *recordingQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	q.asked++
	return nil
}

// pg.dump is the capability with no blast radius a grant could name, so it
// leaves the surface rather than asking for one.
func TestTheFullDumpRefusesMCP(t *testing.T) {
	_, err := runFullDump(context.Background(),
		mcpReqFor(t, "pg.dump", map[string]any{"out": "/tmp/should-not-exist.sql"}))
	if err == nil {
		t.Fatal("an MCP caller was allowed to dump the whole database")
	}
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "pg.human" {
		t.Fatalf("err = %v, want pg.human", err)
	}
	// The refusal names the way through, or it is a dead end that teaches an
	// agent nothing except to try again.
	if !strings.Contains(verr.Hint, "pg.table.dump") {
		t.Errorf("hint = %q, want it to name the capability that does take a grant", verr.Hint)
	}
}

// The refusal comes before the connection is opened, so an agent's call
// never spends the operator's password on a question that was always going
// to be answered no. Same discipline kv.copy's MCP refusal follows.
func TestTheFullDumpRefusesBeforeItConnects(t *testing.T) {
	// A host nothing is listening on: if this connected first, the failure
	// would be pg.conn.refused rather than the refusal.
	_, err := runFullDump(context.Background(), mcpReqFor(t, "pg.dump", map[string]any{
		"out": "/tmp/should-not-exist.sql", "host": "127.0.0.1", "port": 1,
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "pg.human" {
		t.Fatalf("err = %v, want the refusal before any connection attempt", err)
	}
}

// **The bug the live smoke found, and the reason classify is the choke
// point.** Every capability here runs its work inside a closure — withConn,
// and readOnly inside that — so a handler's own view.Error comes back to
// exactly the place a driver failure does. Falling through classify's
// switches it matched nothing and came out as `pg.conn.failed: could not
// connect to 127.0.0.1:55432: the query returned more than 1.0 MiB`: a
// connection error, naming an input hint, for a connection that was fine.
//
// The row bound had unit tests on either side of classify and none through
// it, so the refusal was correct, reached, and thrown away one frame later.
func TestAnAlreadyClassifiedErrorIsNotClassifiedTwice(t *testing.T) {
	r := reqFor(t, "pg.query", map[string]any{"host": "db.internal", "port": 5432})
	original := view.Errorf("pg.query.toomany", "the query returned more than 5 rows").
		WithHint("add a LIMIT to the query")

	got := classify(original, r)
	if got.Code != "pg.query.toomany" {
		t.Fatalf("code = %q, want the original — a classified refusal was reclassified", got.Code)
	}
	if !strings.Contains(got.Message, "more than 5 rows") {
		t.Errorf("message = %q, want the original", got.Message)
	}
	if !strings.Contains(got.Hint, "add a LIMIT") {
		t.Errorf("hint = %q, want the original", got.Hint)
	}
}

// **The one place this plugin interpolates rather than binds.** A table and
// its columns cannot be bind parameters, so the dump builds `select "a", "b"
// from "s"."t"` as text. Two things keep that safe and they are different
// things: every identifier that reaches the string came back from the
// catalogue in the first place, and it goes through pgx's own quoting on the
// way. The second is what this holds down — a name carrying a quote must
// come out escaped rather than closing the one around it.
//
// Verified live too, against a table actually named `evil"; drop table
// users; --` with a column named `col"umn`: it dumps, and users is still
// there afterwards.
func TestAnIdentifierCarryingAQuoteIsEscapedRatherThanClosing(t *testing.T) {
	rel := relation{schema: "public", name: `evil"; drop table users; --`}
	got := rel.sanitized()

	if !strings.HasPrefix(got, `"public".`) {
		t.Errorf("schema is not quoted: %s", got)
	}
	// The embedded quote is doubled, which is how SQL escapes one inside a
	// quoted identifier. Doubled means it never terminates the identifier.
	if strings.Contains(got, `evil"; drop`) {
		t.Errorf("the embedded quote closed the identifier: %s", got)
	}
	if !strings.Contains(got, `evil""; drop`) {
		t.Errorf("the embedded quote was not doubled: %s", got)
	}
	// And the whole thing is still one identifier: an even number of quotes
	// after the leading one means nothing escaped into statement text.
	if strings.Count(got, `"`)%2 != 0 {
		t.Errorf("unbalanced quoting leaves statement text exposed: %s", got)
	}
}

// And the other half: a driver error still gets classified, or the fix above
// would have turned every connection failure into a bare message.
func TestADriverErrorIsStillClassified(t *testing.T) {
	r := reqFor(t, "pg.query", map[string]any{"host": "db.internal", "port": 5432})
	got := classify(errors.New("dial tcp: connection refused"), r)
	if got.Code != "pg.conn.refused" {
		t.Fatalf("code = %q, want pg.conn.refused", got.Code)
	}
}
