package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The schema dump, and the one rule that makes it safe to hand to an agent.
//
// **"Zero rows" is not "zero values", and the difference is where a schema
// dump leaks.** A default expression can be `'sk_live_51H...'`. A check
// constraint can be `region in ('eu-west-1','classified-3')`. A view body
// carries whatever WHERE clause somebody wrote, a function body can carry a
// dblink connection string with a password in it, and a partial index's
// predicate is a WHERE clause by another name. None of those is a row, and
// every one of them is a value.
//
// So the rule here is mechanical rather than clever: **an expression is a
// place a value can hide, so no expression crosses.** Names, types,
// nullability, key and foreign-key column lists — all of which are shape —
// and nothing that PostgreSQL would have to evaluate. It needs no parsing,
// no heuristics and no guessing at what looks sensitive, which matters
// because scanning text for secrets is the game nobody wins; the same reason
// pg.query does not inspect SQL and lets the server enforce read-only
// instead.
//
// What it costs: the output is a description, not a restorable dump. That is
// said out loud in the header rather than left for somebody to discover when
// psql rejects it, and the header names `pg_dump --schema-only` for the
// person who wants the rest. Everything dropped is *counted*, because a dump
// that silently omits things reads exactly like a database that does not
// have them — the same defect view.Table.Page existed to fix for listings.
//
// Why it needs no grant, while pg.table.dump does: an agent holding pg.query
// can already `select table_name, column_name, data_type from
// information_schema.columns`, so refusing this would be theatre rather than
// a boundary. It discloses what a caller could already assemble, in one call
// instead of several — which is exactly the argument vault.kv.tree makes,
// including its better half: the ledger gets one line
// reading "described the schema" instead of a dozen catalogue queries
// nobody will reconstruct.

// maxSchemaTables bounds the walk. A schema with more tables than this is a
// generated one — per-tenant tables, partitions by day — and drawing all of
// them is neither useful nor cheap.
const maxSchemaTables = 500

// querier is whatever the catalogue queries run against: a connection, or
// the read-only transaction the row dump insists on. Both satisfy it.
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type schemaColumn struct {
	name    string
	typ     string
	notNull bool
	// expr records that this column has a default or generation expression,
	// which is the thing this dump does not carry. Kept as a flag rather than
	// as the text, so the value has no way to reach the output at all.
	expr bool
}

type schemaTable struct {
	name string
	// partitioned tables are declared with PARTITION BY, and the difference
	// matters to anybody reading the shape.
	partitioned bool
	columns     []schemaColumn
	// keys holds PRIMARY KEY and UNIQUE definitions, which are column lists.
	keys []string
	// foreign holds FOREIGN KEY definitions, emitted as ALTER after every
	// table so that a cycle between two tables is not an ordering problem.
	foreign []string
	indexes []string
}

// dropped counts what the structure rule left out, per kind. Every field
// here is a sentence in the header: an omission nobody is told about is
// indistinguishable from an absence.
type dropped struct {
	defaults int
	checks   int
	indexes  int
	views    int
	matviews int
	routines int
	triggers int
	// truncated is a flag rather than a count on purpose: the walk reads one
	// table past the limit and stops, so it knows there is more and does not
	// know how much. Counting to one and printing "1 further table" would be
	// a number, and a wrong one.
	truncated bool
}

func runSchemaDump(ctx context.Context, req plugin.Request) (view.View, error) {
	return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
		return schemaDDL(ctx, conn, req)
	})
}

func schemaDDL(ctx context.Context, q querier, req plugin.Request) (view.View, error) {
	schema := strings.TrimSpace(req.String("schema"))

	// The membership check before the walk, so "no such schema" is answered
	// as itself rather than as an empty dump — which reads as "this schema
	// has nothing in it" and sends somebody looking for a permissions
	// problem that is not there.
	names, err := schemaNames(ctx, q)
	if err != nil {
		return nil, classify(err, req)
	}
	if !slices.Contains(names, schema) {
		e := view.Errorf("pg.schema.missing", "%s has no schema named %q",
			req.String("database"), schema)
		if len(names) == 0 {
			return nil, e.WithHint("this role can see no schemas at all in this database — " +
				"`rta pg status` shows which role the connection is using")
		}
		return nil, e.WithHint("this database has: " + strings.Join(names, ", "))
	}

	tables, om, err := readSchema(ctx, q, schema, req.Int("limit"))
	if err != nil {
		return nil, classify(err, req)
	}

	body := renderDDL(req, schema, tables, om)
	// The same ceiling every result set here answers to, for the same
	// reason: past the transport's own limit the failure is ResourceExhausted
	// from gRPC, which names neither this capability nor the flag that fixes
	// it.
	if len(body) > maxBytes {
		return nil, view.Errorf("pg.schema.toolarge",
			"the description of schema %q is %s, over the %s a result may be",
			schema, format.Bytes(uint64(len(body))), format.Bytes(maxBytes)).
			WithHint("lower --limit to describe fewer tables, or use " +
				"`pg_dump --schema-only` for a schema this large")
	}
	return view.Text{Body: body}, nil
}

// schemaNames lists the schemas this role can see, minus PostgreSQL's own.
func schemaNames(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx, `
		select nspname from pg_namespace
		where nspname not like 'pg\_%' and nspname <> 'information_schema'
		  and has_schema_privilege(oid, 'USAGE')
		order by nspname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// readSchema assembles the structure of one schema in four catalogue queries.
func readSchema(ctx context.Context, q querier, schema string, limit int) ([]schemaTable, dropped, error) {
	var om dropped
	if limit <= 0 || limit > maxSchemaTables {
		limit = maxSchemaTables
	}

	// One past the limit, so a schema that fits exactly is not reported as
	// truncated — the same reason the object listings read one past theirs.
	rows, err := q.Query(ctx, `
		select c.relname, c.relkind = 'p'
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = $1 and c.relkind in ('r', 'p')
		order by c.relname
		limit $2`, schema, limit+1)
	if err != nil {
		return nil, om, err
	}
	byName := map[string]*schemaTable{}
	var ordered []*schemaTable
	for rows.Next() {
		var t schemaTable
		if err := rows.Scan(&t.name, &t.partitioned); err != nil {
			rows.Close()
			return nil, om, err
		}
		if len(ordered) == limit {
			om.truncated = true
			continue
		}
		ordered = append(ordered, &t)
		byName[t.name] = &t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, om, err
	}
	if len(ordered) == 0 {
		return nil, om, nil
	}

	names := make([]string, 0, len(ordered))
	for _, t := range ordered {
		names = append(names, t.name)
	}

	// Columns. attidentity and attgenerated join atthasdef because all three
	// are expressions PostgreSQL evaluates; which kind it is does not change
	// whether a value could be sitting in it.
	rows, err = q.Query(ctx, `
		select c.relname, a.attname, format_type(a.atttypid, a.atttypmod),
		       a.attnotnull,
		       a.atthasdef or a.attidentity <> '' or a.attgenerated <> ''
		from pg_class c
		  join pg_namespace n on n.oid = c.relnamespace
		  join pg_attribute a on a.attrelid = c.oid
		where n.nspname = $1 and c.relname = any($2)
		  and a.attnum > 0 and not a.attisdropped
		order by c.relname, a.attnum`, schema, names)
	if err != nil {
		return nil, om, err
	}
	for rows.Next() {
		var table string
		var col schemaColumn
		if err := rows.Scan(&table, &col.name, &col.typ, &col.notNull, &col.expr); err != nil {
			rows.Close()
			return nil, om, err
		}
		if col.expr {
			om.defaults++
		}
		if t := byName[table]; t != nil {
			t.columns = append(t.columns, col)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, om, err
	}

	// Constraints. p/u/f are column lists and cross; c is a CHECK, which is
	// an expression and does not.
	rows, err = q.Query(ctx, `
		select c.relname, con.contype::text, pg_get_constraintdef(con.oid)
		from pg_constraint con
		  join pg_class c on c.oid = con.conrelid
		  join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = $1 and c.relname = any($2)
		  and con.contype in ('p', 'u', 'f', 'c')
		order by c.relname, con.contype, con.conname`, schema, names)
	if err != nil {
		return nil, om, err
	}
	for rows.Next() {
		var table, kind, def string
		if err := rows.Scan(&table, &kind, &def); err != nil {
			rows.Close()
			return nil, om, err
		}
		t := byName[table]
		switch {
		case kind == "c":
			om.checks++
		case t == nil:
		case kind == "f":
			t.foreign = append(t.foreign, def)
		default:
			t.keys = append(t.keys, def)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, om, err
	}

	// Indexes, minus the ones a constraint already declared. A partial index
	// carries a WHERE clause and an expression index carries an expression;
	// both are counted and neither is drawn.
	rows, err = q.Query(ctx, `
		select c.relname, pg_get_indexdef(ix.indexrelid),
		       ix.indpred is not null or ix.indexprs is not null
		from pg_index ix
		  join pg_class c on c.oid = ix.indrelid
		  join pg_class i on i.oid = ix.indexrelid
		  join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = $1 and c.relname = any($2)
		  and not exists (select 1 from pg_constraint pc where pc.conindid = ix.indexrelid)
		order by c.relname, i.relname`, schema, names)
	if err != nil {
		return nil, om, err
	}
	for rows.Next() {
		var table, def string
		var expr bool
		if err := rows.Scan(&table, &def, &expr); err != nil {
			rows.Close()
			return nil, om, err
		}
		if expr {
			om.indexes++
			continue
		}
		if t := byName[table]; t != nil {
			t.indexes = append(t.indexes, def)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, om, err
	}

	// What else lives here. Not drawn — a view is a query and a routine is a
	// body — but counted, because "this schema has four views you are not
	// seeing" is the difference between a partial answer and a wrong one.
	err = q.QueryRow(ctx, `
		select
		  (select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace
		     where n.nspname = $1 and c.relkind = 'v'),
		  (select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace
		     where n.nspname = $1 and c.relkind = 'm'),
		  (select count(*) from pg_proc p join pg_namespace n on n.oid = p.pronamespace
		     where n.nspname = $1),
		  (select count(*) from pg_trigger tg
		     join pg_class c on c.oid = tg.tgrelid
		     join pg_namespace n on n.oid = c.relnamespace
		     where n.nspname = $1 and not tg.tgisinternal)`, schema).
		Scan(&om.views, &om.matviews, &om.routines, &om.triggers)
	if err != nil {
		return nil, om, err
	}

	out := make([]schemaTable, 0, len(ordered))
	for _, t := range ordered {
		out = append(out, *t)
	}
	return out, om, nil
}

// renderDDL writes the description.
//
// Uppercase keywords and PostgreSQL's own spelling of everything it
// generated, because a schema dump is a thing operators already read in one
// specific dialect — pg_dump's. The same argument statusView makes for
// pg_size_pretty: where the server has a vocabulary for this, borrowing it
// beats inventing one.
func renderDDL(req plugin.Request, schema string, tables []schemaTable, om dropped) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- schema %q of %s on %s:%d\n", schema,
		req.String("database"), req.String("host"), req.Int("port"))
	b.WriteString("--\n")
	b.WriteString("-- Structure only: no expression crosses this boundary, because an expression\n" +
		"-- is a place a value can hide. Defaults, check constraints, view and routine\n" +
		"-- bodies and partial-index predicates are omitted by rule, not by inspection.\n" +
		"-- This is a description, not a restorable dump — `pg_dump --schema-only` is the\n" +
		"-- tool when you need the rest.\n")
	if omitted := om.summary(); omitted != "" {
		b.WriteString("--\n-- Not shown: " + omitted + ".\n")
	}
	b.WriteString("\n")

	if len(tables) == 0 {
		fmt.Fprintf(&b, "-- schema %q holds no tables.\n", schema)
		return b.String()
	}

	var foreign []string
	for _, t := range tables {
		qualified := pgx.Identifier{schema, t.name}.Sanitize()
		fmt.Fprintf(&b, "CREATE TABLE %s (\n", qualified)

		// Aligned on the widest name and type, so a column list reads as a
		// column list rather than as ragged prose.
		var nameW, typeW int
		for _, c := range t.columns {
			nameW = max(nameW, len(pgx.Identifier{c.name}.Sanitize()))
			typeW = max(typeW, len(c.typ))
		}
		// code and note are kept apart all the way to the write, because a
		// trailing `-- ...` swallows whatever follows it on the line. The
		// first version of this appended the note to the line and then
		// joined with ",\n", which commented out every separator and
		// produced DDL that reads correctly and does not parse — the exact
		// failure `cert pem` is tested against with a real PEM decoder.
		type ddlLine struct{ code, note string }
		lines := make([]ddlLine, 0, len(t.columns)+len(t.keys))
		for _, c := range t.columns {
			line := ddlLine{code: fmt.Sprintf("    %-*s %-*s", nameW,
				pgx.Identifier{c.name}.Sanitize(), typeW, c.typ)}
			if c.notNull {
				line.code += " NOT NULL"
			}
			line.code = strings.TrimRight(line.code, " ")
			if c.expr {
				// Named on the column it belongs to as well as counted in the
				// header: a reader deciding whether they can recreate this
				// table needs to know which column is incomplete, not just
				// that one of them is.
				line.note = "-- default omitted"
			}
			lines = append(lines, line)
		}
		for _, k := range t.keys {
			lines = append(lines, ddlLine{code: "    " + k})
		}
		for i, l := range lines {
			b.WriteString(l.code)
			if i < len(lines)-1 {
				b.WriteString(",")
			}
			if l.note != "" {
				b.WriteString(" " + l.note)
			}
			b.WriteString("\n")
		}
		b.WriteString(");")
		if t.partitioned {
			// After the terminator, for the same reason: the clause itself is
			// an expression, so the fact is stated rather than the definition
			// given, and stating it must not eat the semicolon.
			b.WriteString(" -- partitioned; PARTITION BY clause omitted")
		}
		b.WriteString("\n")
		for _, idx := range t.indexes {
			b.WriteString(idx + ";\n")
		}
		b.WriteString("\n")
		for _, f := range t.foreign {
			foreign = append(foreign, fmt.Sprintf("ALTER TABLE %s ADD %s;", qualified, f))
		}
	}

	// Foreign keys last, all together: two tables referencing each other is
	// ordinary, and inline definitions would make the output depend on an
	// ordering no schema guarantees.
	if len(foreign) > 0 {
		b.WriteString(strings.Join(foreign, "\n") + "\n")
	}
	return b.String()
}

// summary writes the omissions as one sentence, naming only what there was
// something to omit.
func (d dropped) summary() string {
	var parts []string
	add := func(n int, one, many string) {
		if n == 0 {
			return
		}
		if n == 1 {
			parts = append(parts, "1 "+one)
			return
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, many))
	}
	add(d.defaults, "default expression", "default expressions")
	add(d.checks, "check constraint", "check constraints")
	add(d.indexes, "expression or partial index", "expression or partial indexes")
	add(d.views, "view", "views")
	add(d.matviews, "materialized view", "materialized views")
	add(d.routines, "routine", "routines")
	add(d.triggers, "trigger", "triggers")
	if d.truncated {
		parts = append(parts, "further tables beyond --limit")
	}
	return strings.Join(parts, ", ")
}
