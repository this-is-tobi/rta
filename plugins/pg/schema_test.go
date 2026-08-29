package main

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// renderDDL is the half of the schema dump that needs no database: given the
// structure, does it write SQL that says what it means and parses?
//
// The catalogue half is covered by the live smoke against a real server —
// the fixture plants a secret in a column default, a customer tier in a
// check constraint, a connection string in a function body and a WHERE
// clause in a partial index, and asserts none of them reach the output. What
// is here is everything that broke on the way, which was all rendering.

func fixture() []schemaTable {
	return []schemaTable{{
		name: "users",
		columns: []schemaColumn{
			{name: "id", typ: "bigint", notNull: true, expr: true},
			{name: "email", typ: "text", notNull: true},
			{name: "note", typ: "text"},
		},
		keys:    []string{"PRIMARY KEY (id)", "UNIQUE (email)"},
		indexes: []string{"CREATE INDEX users_note_idx ON public.users USING btree (note)"},
		foreign: []string{"FOREIGN KEY (id) REFERENCES public.accounts(id)"},
	}}
}

func render(t *testing.T, tables []schemaTable, om dropped) string {
	t.Helper()
	return renderDDL(req(t, map[string]any{"database": "app", "host": "db.internal", "port": 5432}),
		"public", tables, om)
}

// **The bug this file exists for.** The note explaining that a column's
// default was withheld used to be appended to the column line, and the lines
// were then joined with ",\n" — so every separator ended up after a `--` and
// the DDL, which reads perfectly, does not parse. Caught by feeding the
// output to PostgreSQL rather than by reading it.
//
// Asserted as "the comma is before the comment", which is the actual
// property, rather than by matching the whole line.
func TestACommentNeverSwallowsTheSeparator(t *testing.T) {
	body := render(t, fixture(), dropped{})
	for _, line := range strings.Split(body, "\n") {
		// Only lines where a comment follows code: the header is prose and
		// its commas are its own.
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		i := strings.Index(line, "--")
		if i < 0 {
			continue
		}
		if strings.Contains(line[i:], ",") {
			t.Errorf("a comma is commented out: %q", line)
		}
		if strings.Contains(line[i:], ";") {
			t.Errorf("a statement terminator is commented out: %q", line)
		}
	}
}

// Same failure one line down: the partitioned marker used to be written
// before the semicolon, so the CREATE TABLE was never terminated and
// everything after it became part of the same statement.
func TestThePartitionedMarkerComesAfterTheTerminator(t *testing.T) {
	tables := fixture()
	tables[0].partitioned = true
	body := render(t, tables, dropped{})

	line := lineContaining(t, body, "partitioned")
	if !strings.HasPrefix(strings.TrimSpace(line), ");") {
		t.Errorf("the marker is not after the terminator: %q", line)
	}
}

// A column whose default was withheld says so on the column, not only in the
// header count: a reader deciding whether they can recreate this table needs
// to know which one is incomplete.
func TestAWithheldDefaultIsNamedOnItsColumn(t *testing.T) {
	body := render(t, fixture(), dropped{})
	line := lineContaining(t, body, `"id"`)
	if !strings.Contains(line, "default omitted") {
		t.Errorf("the column with a withheld default does not say so: %q", line)
	}
	if strings.Contains(lineContaining(t, body, `"email"`), "default omitted") {
		t.Error("a column with no default is marked as having one withheld")
	}
}

// **An omission nobody is told about is indistinguishable from an absence.**
// A schema dump that quietly drops four views reads exactly like a schema
// with no views in it — the same defect view.Table.Page existed to fix for
// listings, and the reason every count is in the header.
func TestTheHeaderCountsWhatWasLeftOut(t *testing.T) {
	body := render(t, fixture(), dropped{
		defaults: 3, checks: 1, indexes: 2, views: 4,
		matviews: 1, routines: 12, triggers: 1, truncated: true,
	})
	for _, want := range []string{
		"3 default expressions", "1 check constraint", "2 expression or partial indexes",
		"4 views", "1 materialized view", "12 routines", "1 trigger",
		"further tables beyond --limit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the header does not report %q:\n%s", want, firstLines(body, 12))
		}
	}
}

// Nothing omitted, nothing claimed. A "not shown" line on a complete
// description sends somebody looking for something that is not missing.
func TestACompleteDescriptionClaimsNoOmissions(t *testing.T) {
	if body := render(t, fixture(), dropped{}); strings.Contains(body, "Not shown") {
		t.Errorf("a complete description reports omissions:\n%s", firstLines(body, 12))
	}
}

// The count is a count, not a number the walk made up. It reads one table
// past the limit and stops, so it knows there is more and not how much —
// printing "1 further table" would have been a number, and a wrong one.
func TestTruncationIsReportedWithoutInventingACount(t *testing.T) {
	body := render(t, fixture(), dropped{truncated: true})
	line := lineContaining(t, body, "further tables")
	if strings.ContainsAny(line, "0123456789") {
		t.Errorf("the truncation note invents a count: %q", line)
	}
}

// A schema with nothing in it says so. An empty body reads as a permissions
// problem somewhere else.
func TestAnEmptySchemaSaysSo(t *testing.T) {
	body := render(t, nil, dropped{})
	if !strings.Contains(body, "holds no tables") {
		t.Errorf("an empty schema rendered as nothing:\n%s", body)
	}
}

// The header states the two things a reader has to know before trusting the
// output: which server it came from, and that it is a description rather
// than a restorable dump.
func TestTheHeaderSaysWhereItCameFromAndWhatItIsNot(t *testing.T) {
	body := render(t, fixture(), dropped{})
	for _, want := range []string{"db.internal:5432", `schema "public" of app`,
		"not a restorable dump", "pg_dump --schema-only"} {
		if !strings.Contains(body, want) {
			t.Errorf("the header does not say %q:\n%s", want, firstLines(body, 12))
		}
	}
}

// Foreign keys come last and all together, because two tables referencing
// each other is ordinary and inline definitions would make the output depend
// on an ordering no schema guarantees.
func TestForeignKeysAreEmittedAfterEveryTable(t *testing.T) {
	body := render(t, fixture(), dropped{})
	create := strings.Index(body, "CREATE TABLE")
	alter := strings.Index(body, "ALTER TABLE")
	if alter < 0 {
		t.Fatalf("no foreign key emitted:\n%s", body)
	}
	if alter < create {
		t.Error("a foreign key is emitted before the table it constrains")
	}
}

// pg.schema.dump is the one dump that needs no grant, and the entire
// argument for that is that it carries no values. The declaration has to say
// so where an operator reading `rta explain` will see it.
func TestTheDeclarationStatesWhyItIsUngated(t *testing.T) {
	var found bool
	for _, c := range Plugin().Capabilities {
		if c.ID != "pg.schema.dump" {
			continue
		}
		found = true
		if c.NeedsGrant {
			t.Error("pg.schema.dump needs a grant, which the description argues it does not")
		}
		if c.Safety != plugin.Read {
			t.Errorf("safety = %s, want read", c.Safety)
		}
		for _, want := range []string{"expression", "information_schema"} {
			if !strings.Contains(c.Description, want) {
				t.Errorf("the description does not mention %q, so an operator reading "+
					"`rta explain` cannot check the argument for themselves", want)
			}
		}
	}
	if !found {
		t.Fatal("pg.schema.dump is not declared")
	}
}

func lineContaining(t *testing.T, body, want string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line containing %q in:\n%s", want, body)
	return ""
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
