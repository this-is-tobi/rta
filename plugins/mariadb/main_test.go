package main

import (
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// req builds a resolved request the way the host would — defaults applied,
// caller values on top — so these test the values a handler actually sees
// rather than a hand-made map.
func req(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

// A password containing '@' or '/' silently produces a different DSN under
// hand assembly, and the failure it causes is an authentication error naming
// nothing. Built through mysql.Config so the driver's own escaping applies —
// this checks the escaping actually round-trips rather than that a string was
// concatenated.
func TestDSNSurvivesAwkwardPasswords(t *testing.T) {
	for _, pw := range []string{"p@ss/word", "with'quote'", `back\slash`, "sp ace", "a:b@c/d?e"} {
		got := dsn(req(t, "mariadb.status", map[string]any{"password": pw, "host": "db.internal"}))
		parsed, err := mysql.ParseDSN(got)
		if err != nil {
			t.Fatalf("password %q produced an unparseable DSN %q: %v", pw, got, err)
		}
		if parsed.Passwd != pw {
			t.Errorf("password %q round-tripped as %q", pw, parsed.Passwd)
		}
		// The address must still parse as its own field: bad escaping would
		// run the password into the next component.
		if parsed.Addr != "db.internal:3306" {
			t.Errorf("password %q corrupted the address: %q", pw, parsed.Addr)
		}
	}
}

// The port belongs to the address, not to a separate field the driver would
// ignore. Getting this wrong reaches the default port against the right host,
// which looks like the server being down.
func TestDSNCarriesHostAndPort(t *testing.T) {
	got := dsn(req(t, "mariadb.status", map[string]any{"host": "10.0.0.5", "port": 3307}))
	parsed, err := mysql.ParseDSN(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Addr != "10.0.0.5:3307" {
		t.Errorf("addr = %q, want 10.0.0.5:3307", parsed.Addr)
	}
	if parsed.Net != "tcp" {
		t.Errorf("net = %q, want tcp", parsed.Net)
	}
}

// Every capability here reaches off the box, so cap must have forced
// NoPreview on all of them. That is what keeps the automatic dashboard from
// deciding, on its own, that somebody else's production database is worth
// polling every few seconds.
func TestEveryCapabilityIsNoPreview(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NoPreview {
			t.Errorf("%s: NoPreview = false, want true — every capability here reaches off the box", c.ID)
		}
	}
}

// The safety split is the whole argument of this plugin, so it is pinned
// rather than left to a code review: the read tier describes the database and
// returns nothing anybody stored in it, and the two capabilities that hand
// back stored values are writes.
//
// The table fails in both directions. A new capability that is not accounted
// for fails, and an entry naming a capability that no longer exists fails —
// which is what stops this from rotting into a list of things that used to be
// true.
func TestSafetyClassesMatchWhatEachCapabilityDiscloses(t *testing.T) {
	want := map[string]plugin.Safety{
		// Read: everything here is something somebody declared, not something
		// somebody entered.
		"mariadb.status":        plugin.Read,
		"mariadb.overview":      plugin.Read,
		"mariadb.database.list": plugin.Read,
		"mariadb.table.list":    plugin.Read,
		"mariadb.schema":        plugin.Read,
		// Write: both return values stored by somebody. query returns rows;
		// activity returns the statement text of everything running, and a
		// WHERE clause is a place a value hides.
		"mariadb.query":    plugin.Write,
		"mariadb.activity": plugin.Write,
		// Read: every value is a number the server publishes about itself.
		// Cluster health is not a value anybody stored, and an operator who
		// cannot see it is an operator who finds out about a split brain from
		// their users.
		"mariadb.galera.status":      plugin.Read,
		"mariadb.replication.status": plugin.Read,
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		seen[c.ID] = true
		expect, ok := want[c.ID]
		if !ok {
			t.Errorf("%s: not accounted for in this test's table", c.ID)
			continue
		}
		if c.Safety != expect {
			t.Errorf("%s: Safety = %s, want %s", c.ID, c.Safety, expect)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: declared in this test's table but not in Plugin()", id)
		}
	}
}

// Every capability must be reachable and describable. A capability with no
// Run ships as dead weight in the MCP schema; one with no Description is one
// `rta explain` cannot answer for.
func TestEveryCapabilityIsRunnableAndDescribed(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Run == nil {
			t.Errorf("%s: no Run", c.ID)
		}
		if strings.TrimSpace(c.Description) == "" {
			t.Errorf("%s: no Description — `rta explain` has nothing to print", c.ID)
		}
		if !strings.HasPrefix(c.ID, Plugin().Name+".") {
			t.Errorf("%s: capability IDs must be namespaced by %q", c.ID, Plugin().Name)
		}
	}
}

// The fork is only stated in the version string, and somebody debugging needs
// to know which one answered — the two have diverged enough that the answer
// changes what to try next.
func TestFlavourIsReadFromTheVersionString(t *testing.T) {
	cases := map[string]string{
		"8.4.0":                            "MySQL",
		"8.0.36-0ubuntu0.22.04.1":          "MySQL",
		"11.4.2-MariaDB":                   "MariaDB",
		"10.11.8-MariaDB-1:10.11.8+maria~": "MariaDB",
		"8.0.35-27":                        "MySQL",
		"8.0.35-27.1.Percona":              "Percona",
	}
	for version, want := range cases {
		if got := flavourOf(version); got != want {
			t.Errorf("flavourOf(%q) = %q, want %q", version, got, want)
		}
	}
}

// NULL and the string "NULL" are indistinguishable once printed, so a NULL
// renders as an empty cell. Anything else makes somebody wonder whether a
// column literally holds that word.
func TestNullRendersAsAnEmptyCell(t *testing.T) {
	if got := cell(nil); got != "" {
		t.Errorf("cell(nil) = %q, want an empty cell", got)
	}
	if got := cell([]byte("NULL")); got != "NULL" {
		t.Errorf("a literal NULL string must survive: got %q", got)
	}
}

// The driver hands back []byte for most string-ish columns. A value that
// reached the table as "[52 50]" means this was skipped.
func TestBytesColumnsBecomeText(t *testing.T) {
	if got := cell([]byte("hello")); got != "hello" {
		t.Errorf("cell([]byte) = %q, want hello", got)
	}
}

// INFORMATION_SCHEMA size expressions come back untyped, and the type varies
// with the server and the driver's settings. A size column nobody can read at
// a glance is a column that gets piped into another tool instead of read.
func TestSizesRenderAsBytesWhateverTypeTheyArriveAs(t *testing.T) {
	for _, v := range []any{int64(1048576), []byte("1048576"), float64(1048576)} {
		if got := bytesCell(v); got != "1.0 MiB" {
			t.Errorf("bytesCell(%T) = %q, want 1.0 MiB", v, got)
		}
	}
	if got := bytesCell(nil); got != "-" {
		t.Errorf("bytesCell(nil) = %q, want -", got)
	}
}

// A statement written across twelve lines in application source arrives with
// all of them, and would turn one table row into a page.
func TestRunningStatementsAreCollapsedAndBounded(t *testing.T) {
	got := truncateStatement("SELECT *\n  FROM orders\n WHERE id = 1")
	if got != "SELECT * FROM orders WHERE id = 1" {
		t.Errorf("newlines survived: %q", got)
	}
	long := truncateStatement(strings.Repeat("x", statementWidth+50))
	if len([]rune(long)) != statementWidth+1 {
		t.Errorf("length = %d, want %d plus the ellipsis", len([]rune(long)), statementWidth)
	}
	if !strings.HasSuffix(long, "…") {
		t.Errorf("truncation is not marked: %q", long)
	}
}

// Guessing here would silently describe `mysql` or `information_schema`,
// which is a confidently wrong answer to a question nobody asked.
func TestSchemaRefusesRatherThanGuessing(t *testing.T) {
	_, verr := schemaOf(req(t, "mariadb.schema", map[string]any{}))
	if verr == nil {
		t.Fatal("no database named anywhere, and it did not refuse")
	}
	if !strings.Contains(verr.Hint, "config") {
		t.Errorf("refusal does not say how to fix it: %q", verr.Hint)
	}

	// The connection's own database is the fallback, so the common case — one
	// database in the config — needs no argument.
	got, verr := schemaOf(req(t, "mariadb.schema", map[string]any{"database": "app"}))
	if verr != nil {
		t.Fatalf("connection database was not used as the fallback: %v", verr)
	}
	if got != "app" {
		t.Errorf("schema = %q, want app", got)
	}

	// An explicit argument wins over the connection's own.
	got, _ = schemaOf(req(t, "mariadb.schema", map[string]any{"database": "app", "schema": "other"}))
	if got != "other" {
		t.Errorf("schema = %q, want the explicit argument to win", got)
	}
}

// The key marker comes first because it is what somebody scanning a schema is
// looking for, and the constraint is stated rather than its opposite because
// nullable is SQL's default and "not null" is the news.
func TestColumnDetailStatesTypeKeyAndConstraint(t *testing.T) {
	got := columnDetail(column{name: "id", dataType: "bigint unsigned", key: "PRI", extra: "auto_increment"})
	for _, want := range []string{"bigint unsigned", "primary key", "not null", "auto_increment"} {
		if !strings.Contains(got, want) {
			t.Errorf("columnDetail = %q, missing %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "bigint unsigned") {
		t.Errorf("type must come first: %q", got)
	}

	nullable := columnDetail(column{name: "note", dataType: "text", nullable: true})
	if strings.Contains(nullable, "not null") {
		t.Errorf("a nullable column claimed not null: %q", nullable)
	}
}

// A view is the distinction worth keeping in the Type column; "BASE TABLE" is
// the standard's word and nobody else's.
func TestClassifyReturnsAlreadyClassifiedErrorsUnchanged(t *testing.T) {
	original := view.Errorf("mariadb.something.specific", "a precise message").WithHint("a precise hint")
	got := classify(original, req(t, "mariadb.status", map[string]any{}))
	if got.Code != "mariadb.something.specific" {
		t.Errorf("a classified error was re-wrapped as %q — the specific answer is now buried", got.Code)
	}
}

// Every branch here is a sentence somebody has stared at without knowing what
// to do next, so each must produce a distinct code and a hint that names the
// next step. Switching on the error number rather than the message is what
// keeps this working across versions and locales.
func TestConnectionFailuresAreClassifiedByNumber(t *testing.T) {
	cases := []struct {
		number uint16
		code   string
	}{
		{1045, "mariadb.auth.failed"},
		{1044, "mariadb.database.denied"},
		{1049, "mariadb.database.notfound"},
		{1146, "mariadb.table.notfound"},
		{1142, "mariadb.denied"},
		{1130, "mariadb.host.denied"},
		{1290, "mariadb.readonly"},
	}
	r := req(t, "mariadb.status", map[string]any{"host": "db.internal", "database": "app"})
	for _, c := range cases {
		got := classify(&mysql.MySQLError{Number: c.number, Message: "server text"}, r)
		if got.Code != c.code {
			t.Errorf("error %d classified as %q, want %q", c.number, got.Code, c.code)
		}
		if strings.TrimSpace(got.Hint) == "" {
			t.Errorf("error %d has no hint — the code alone does not say what to do next", c.number)
		}
	}

	// An unrecognised number must still carry its own number and the server's
	// message, rather than collapsing into a generic failure that names nothing.
	other := classify(&mysql.MySQLError{Number: 1064, Message: "You have an error in your SQL syntax"}, r)
	if !strings.Contains(other.Message, "1064") || !strings.Contains(other.Message, "syntax") {
		t.Errorf("unrecognised error lost its detail: %q", other.Message)
	}
}
