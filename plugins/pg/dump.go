package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The row dump — one relation, named in the grant.
//
// **The rule this exists to satisfy: a capability whose blast radius cannot
// be named in a grant does not belong on the agent surface.** rta's entire
// consent model rests on a call having a nameable radius — that is what
// Scope is for, and it is why grants are per-record: `kv.get <key>`,
// `s3.object.get <key>`, `net.hosts.toggle <hostname>`. A full database dump
// has no such radius; its single authorized use is "everything", which is
// why keys.backup and kv.copy refuse MCP outright rather than merely asking
// for a grant. One table does have a radius, so this is the shape a dump can
// take and still be consented to: `grant allow pg.table.dump --scope
// public.orders` authorizes that relation and nothing beside it.
//
// **Which is not a claim that the named table is the harmless one.** From
// the user: *"almost every table could store sensitive data in a db, not
// only user table"*. Orders carry addresses, events carry payloads,
// application logs carry tokens, and an audit table is a record of who did
// what to whom. There is no safe table to point at, and the per-table scope
// is not sorting them into safe and unsafe — it exists because "this one,
// now, for the next fifteen minutes" is the only thing a person can
// meaningfully consent to about a database. The operator decides whether the
// table they named is one they will hand over; the mechanism's job is to
// make sure that is the only table that moves.
//
// Three gates, and each one is a different question:
//
//   - **Safety: Write**, so it is not a tool at all unless the operator
//     passed --allow-write pg. Nothing here mutates and the transaction is
//     READ ONLY, so the class is about disclosure rather than mutation —
//     exactly the reading kv.get gets, whose entire purpose is
//     revealing something and which is classified write for that reason.
//     It also keeps the read-only MCP tier coherent: with --allow-read
//     alone, pg answers about health, shape and activity, and hands over no
//     bulk rows.
//   - **NeedsGrant with Scope "table"**, so a person named the relation and
//     the grant expires. Write does not imply a grant (grant.Required is
//     NeedsGrant, Destructive, or a profiled call), so this is opted into.
//   - **A bound on rows and on bytes**, refused rather than truncated,
//     because a shortened dump is a different answer wearing the right
//     shape.
//
// Why pg.query next door stays Read and ungated, which is a fair thing to
// ask: its result is bounded per call by --limit, and where it matters —
// a profiled connection, i.e. any environment an operator has actually
// named — grant.Required is already true for every call in the namespace,
// pg.query included. The line this draws is magnitude: the dump is the one
// that says "all of it", and it is the one that has to be asked for.

// maxDumpRows is the ceiling on --limit. Above it the byte bound would
// almost always fire first, so a larger number would only ever be an
// invitation to discover that the hard way.
const maxDumpRows = 100000

// relation is one resolved, unambiguous relation.
type relation struct {
	oid    uint32
	schema string
	name   string
}

func (r relation) qualified() string { return r.schema + "." + r.name }
func (r relation) sanitized() string { return pgx.Identifier{r.schema, r.name}.Sanitize() }

func runTableDump(ctx context.Context, req plugin.Request) (view.View, error) {
	return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
		var out view.View
		// READ ONLY for the reason pg.query is: it makes the safety class a
		// fact the server enforces rather than a claim this code makes. Here
		// it is also the second lock on identifier interpolation — the table
		// name is sanitized before it reaches the string, and if that were
		// ever wrong, the transaction is still one PostgreSQL will not let
		// write.
		err := readOnly(ctx, conn, func(tx pgx.Tx) error {
			rel, verr := resolveRelation(ctx, tx, req)
			if verr != nil {
				return verr
			}
			v, verr := dumpRows(ctx, tx, req, rel)
			if verr != nil {
				return verr
			}
			out = v
			return nil
		})
		if err != nil {
			// classify passes a view.Error through untouched, so a refusal
			// raised inside the transaction arrives as itself.
			return nil, classify(err, req)
		}
		return out, nil
	})
}

// resolveRelation turns what the caller typed into exactly one relation, or
// refuses.
//
// **Over MCP the name must already be qualified**, and that is the
// interesting rule here rather than a nicety. The grant gate matches the
// caller's argument byte-for-byte against the scope a person wrote, before
// anything looks in the catalogue — so if a bare `orders` were resolved
// afterwards, one grant string would name whichever `orders` the search
// order happened to reach. Drop public.orders and create archive.orders and
// the same unexpired grant now reads a different table. Requiring
// schema.table over MCP makes the string in the ledger and the object read
// the same thing by construction, with no lookup in between to drift.
//
// A person at a terminal keeps the short form: refusing an unqualified name
// is a decision about *whether* a call is allowed, which a
// handler to take from the surface, and the CLI has a human to disambiguate
// for.
func resolveRelation(ctx context.Context, q querier, req plugin.Request) (relation, *view.Error) {
	raw := strings.TrimSpace(req.String("table"))
	schema, name, qualified := strings.Cut(raw, ".")
	if !qualified {
		if req.Surface() == plugin.SurfaceMCP {
			return relation{}, view.Refusef("pg.table.unqualified",
				"name the table as schema.table, not %q", raw).
				WithHint("a grant for this name will not help — grants are matched exactly, so " +
					"an unqualified one would follow whichever schema resolves first. Issue it " +
					"as `rta grant allow pg.table.dump <schema>." + raw + "`; " +
					"`rta pg table list` shows the schema of each table")
		}
		schema, name = "", raw
	}

	rows, err := q.Query(ctx, `
		select c.oid, n.nspname, c.relname
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where c.relname = $1
		  and ($2 = '' or n.nspname = $2)
		  and c.relkind in ('r', 'p', 'v', 'm')
		  and n.nspname not like 'pg\_%' and n.nspname <> 'information_schema'
		order by n.nspname`, name, schema)
	if err != nil {
		return relation{}, classify(err, req)
	}
	defer rows.Close()
	var found []relation
	for rows.Next() {
		var r relation
		if err := rows.Scan(&r.oid, &r.schema, &r.name); err != nil {
			return relation{}, classify(err, req)
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return relation{}, classify(err, req)
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return relation{}, view.Errorf("pg.table.missing",
			"%s has no table named %q", req.String("database"), raw).
			WithHint("`rta pg table list` shows what is there — foreign tables and the " +
				"system catalogues are deliberately not dumpable, since neither has rows " +
				"this database owns")
	default:
		var where []string
		for _, r := range found {
			where = append(where, r.qualified())
		}
		// Ambiguity is refused rather than resolved by precedence, so that a
		// name never quietly means a different table than it did yesterday.
		return relation{}, view.Errorf("pg.table.ambiguous",
			"%q names more than one table", raw).
			WithHint("say which: " + strings.Join(where, ", "))
	}
}

// dumpRows reads the relation, narrowed to the requested columns and bounded
// twice.
func dumpRows(ctx context.Context, q querier, req plugin.Request, rel relation) (view.View, *view.Error) {
	cols, err := columnsOf(ctx, q, rel.oid)
	if err != nil {
		return nil, classify(err, req)
	}
	if len(cols) == 0 {
		return nil, view.Errorf("pg.table.nocolumns",
			"%s has no readable columns", rel.qualified())
	}

	// **--columns narrows and can never widen.** A grant is per-record and
	// has no way to say "orders but not the email column", so this is the
	// caller minimising what it asks for rather than a control the operator
	// holds — worth having for exactly that reason, and worth not
	// mistaking for a permission. Every name is checked against the
	// catalogue, so the only identifiers that reach the query are ones
	// PostgreSQL handed back.
	selected := cols
	if want := req.StringSlice("columns"); len(want) > 0 {
		selected = nil
		for _, w := range want {
			w = strings.TrimSpace(w)
			if w == "" {
				continue
			}
			if !slices.Contains(cols, w) {
				return nil, view.Errorf("pg.column.missing",
					"%s has no column %q", rel.qualified(), w).
					WithHint("it has: " + strings.Join(cols, ", "))
			}
			selected = append(selected, w)
		}
		if len(selected) == 0 {
			selected = cols
		}
	}

	limit := req.Int("limit")
	quoted := make([]string, len(selected))
	for i, c := range selected {
		quoted[i] = pgx.Identifier{c}.Sanitize()
	}
	sql := fmt.Sprintf("select %s from %s", strings.Join(quoted, ", "), rel.sanitized())

	// Ordered by primary key where there is one, so that "the first thousand
	// rows" is the same thousand rows on the next call. Without a key
	// PostgreSQL promises no order at all, and a dump that returns a
	// different sample each time is not a dump.
	key, err := primaryKeyOf(ctx, q, rel.oid)
	if err != nil {
		return nil, classify(err, req)
	}
	if len(key) > 0 {
		ordered := make([]string, len(key))
		for i, k := range key {
			ordered[i] = pgx.Identifier{k}.Sanitize()
		}
		sql += " order by " + strings.Join(ordered, ", ")
	}
	// One past the bound, so rowsToTable can tell a table that fits exactly
	// from one that overflows.
	sql += " limit $1"

	rows, err := q.Query(ctx, sql, limit+1)
	if err != nil {
		return nil, classify(err, req)
	}
	defer rows.Close()

	t, err := rowsToTable(rows, limit)
	switch {
	case errors.Is(err, ErrTooManyRows):
		return nil, view.Errorf("pg.dump.toomany",
			"%s has more than %d rows", rel.qualified(), limit).
			WithHint("raise --limit, or narrow the dump with --columns — refused rather " +
				"than shortened, because a truncated dump is a different answer wearing " +
				"the right shape. `psql \\copy` is the tool for a whole table")
	case errors.Is(err, ErrTooLarge):
		return nil, view.Errorf("pg.dump.toolarge",
			"the rows of %s are over the %s a result may be",
			rel.qualified(), format.Bytes(maxBytes)).
			WithHint("lower --limit, or name the columns you need with --columns — " +
				"one wide column is usually what does this")
	case err != nil:
		return nil, classify(err, req)
	}
	return t, nil
}

// columnsOf lists a relation's live columns in declaration order.
func columnsOf(ctx context.Context, q querier, oid uint32) ([]string, error) {
	rows, err := q.Query(ctx, `
		select a.attname from pg_attribute a
		where a.attrelid = $1 and a.attnum > 0 and not a.attisdropped
		order by a.attnum`, oid)
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

// primaryKeyOf returns the primary key's columns in key order, or nothing.
func primaryKeyOf(ctx context.Context, q querier, oid uint32) ([]string, error) {
	rows, err := q.Query(ctx, `
		select a.attname
		from pg_index ix
		  cross join lateral unnest(ix.indkey::smallint[]) with ordinality as k(attnum, ord)
		  join pg_attribute a on a.attrelid = ix.indrelid and a.attnum = k.attnum
		where ix.indrelid = $1 and ix.indisprimary
		order by k.ord`, oid)
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
