// Command rta-plugin-pg is the first external rta plugin.
//
// It exists to prove the contract rather than to be a database client: if the
// declaration model survives a plugin that needs a connection, a credential,
// operator-supplied configuration, arbitrary result shapes and a dozen
// distinct failure modes, it survives anything.
//
// Build it and put it on your $PATH as `rta-plugin-pg`. Note the cd: this is
// its own module (that is the point — it consumes the SDK the way a stranger
// does), so `go build ./plugins/pg` from the repository root fails with
// "main module does not contain package", which is what the first person to
// follow these instructions hit.
//
//	cd plugins/pg && go build -o ~/.local/bin/rta-plugin-pg .
//
// Then state the connection once, in rta's config, under the artifact's own
// section — `rta explain pg.status` prints the exact heading including the
// digest:
//
//	plugins:
//	  pg@<digest>:
//	    host: db.internal
//	    database: app
//
// and export RTA_PG_PASSWORD. After that `rta pg status` is the whole command,
// and `rta pg overview --detail` is the one worth reaching for first: status,
// replication, cache hit ratio, largest tables and current activity in one
// screen, through one connection. It stays off the automatic dashboard on
// purpose — every capability here reaches off the box, and an unattended
// timer opening a real connection every few seconds is a cost nothing here
// may decide on its own. Put it there yourself once you have looked:
//
//	dashboard:
//	  tiles:
//	    - id: pg.overview
//
// Through `kubectl port-forward`, set `sslmode: disable`. PostgreSQL TLS
// kills a forward on the first clean disconnect — the trailing close_notify
// reaches a socket PostgreSQL has already closed, kubectl reads the reset as
// `lost connection to pod`, and exits. `psql --sslmode=require` does it too,
// so it is the transport rather than this plugin, and nothing is lost by
// turning it off: that hop is already inside the API server's TLS.

package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// withConn is the shape every capability here has: connect, or return the
// classified error; run; close.
func withConn(ctx context.Context, req plugin.Request,
	fn func(context.Context, *pgx.Conn) (view.View, error)) (view.View, error) {
	conn, verr := connect(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer func() { _ = conn.Close(ctx) }()
	return fn(ctx, conn)
}

// cap builds a capability with the shared connection inputs appended, so no
// declaration here can forget one and no two can disagree about a default.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(own, connFields()...)
	// Every capability here reaches off the box, so none of them belong on the
	// dashboard, which runs Read capabilities unasked every few seconds. That
	// is not what Safety expresses: the question is not whether the caller may
	// run it but whether the host may run it without being asked.
	//
	// pg.overview included, on purpose, despite existing so an operator has
	// something worth glancing at: what makes a glance safe on the automatic
	// timer is that nothing about it costs anything off the box, and every
	// capability here fails that by one call. An operator who has examined
	// their own connection and decided it is fine to poll can still put it on
	// their dashboard explicitly — `dashboard.tiles` runs whatever is named
	// there regardless of NoPreview, because naming a capability in a config
	// file is the asking (pkg/plugin's own doc for the field says so). What
	// this refuses is the guess: rta choosing, on its own, to open a
	// connection every five seconds to a database it was never told it may
	// poll — and a database reached through a `kubectl port-forward` under
	// TLS is not a hypothetical cost; it was measured killing the
	// forward on every clean disconnect.
	c.NoPreview = true
	return c
}

// statusView answers "can I reach it, as whom, and what is it" — the shared
// query behind pg.status and the lead section of pg.overview.
func statusView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	// server, not version: this is PostgreSQL's own banner, and the package
	// now has a `version` of its own that this would shadow.
	var server, db, user, size string
	// pg_size_pretty rather than a raw count. Every producer formats its own
	// numbers — view.ColumnKind aligns and does not render — so the only
	// question is whose vocabulary, and PostgreSQL's own is the one an
	// operator already reads in psql. pkg/format is the answer when the
	// number does not come from a server that can name it.
	err := conn.QueryRow(ctx,
		`select version(), current_database(), current_user,
		        pg_size_pretty(pg_database_size(current_database()))`).
		Scan(&server, &db, &user, &size)
	if err != nil {
		return nil, classify(err, req)
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "server", Value: server},
		{Key: "database", Value: db},
		{Key: "connected as", Value: user},
		{Key: "size", Value: size},
	}}, nil
}

// tableListView lists tables with their row estimates and sizes — pg.table.list
// and a "largest tables" section of pg.overview at a tighter limit.
func tableListView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	// pg_size_pretty, not the raw count: a view carries pre-formatted strings
	// and view.ColumnKind selects alignment, not rendering — so declaring
	// KindBytes and handing over an integer prints the integer. Ordering
	// still uses the number, which is the reason to format here rather than
	// in SQL's order by.
	rows, err := conn.Query(ctx, `
		select schemaname, relname,
		       n_live_tup,
		       pg_size_pretty(pg_total_relation_size(relid))
		from pg_stat_user_tables
		where ($1 = '' or schemaname = $1)
		order by pg_total_relation_size(relid) desc
		limit $2`, req.String("schema"), req.Int("limit"))
	if err != nil {
		return nil, classify(err, req)
	}
	defer rows.Close()
	t, err := rowsToTable(rows, req.Int("limit"))
	if err != nil {
		return nil, classify(err, req)
	}
	t.Columns = []view.Column{
		{Name: "Schema"}, {Name: "Table"},
		{Name: "Rows", Kind: view.KindNumber},
		{Name: "Size", Kind: view.KindBytes},
	}
	return t, nil
}

// databaseListView lists databases on this server, with their sizes.
func databaseListView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	rows, err := conn.Query(ctx, `
		select datname, pg_size_pretty(pg_database_size(datname)),
		       pg_get_userbyid(datdba)
		from pg_database
		where not datistemplate
		order by pg_database_size(datname) desc`)
	if err != nil {
		return nil, classify(err, req)
	}
	defer rows.Close()
	t, err := rowsToTable(rows, maxRows)
	if err != nil {
		return nil, classify(err, req)
	}
	t.Columns = []view.Column{{Name: "Database"}, {Name: "Size"}, {Name: "Owner"}}
	return t, nil
}

// activityView shows what every connected session is doing right now.
//
// Excludes this call's own backend. Every query against pg_stat_activity
// sees itself mid-execution — psql does too — but here it is not a person
// reading a live terminal, it is a fixed capability whose whole contract is
// "what is running", and its own introspection query showing up as the
// newest active row on every single call is not a session an operator asked
// to see.
//
// withQuery is what separates pg.activity from the summary pg.overview
// embeds, and it is a disclosure boundary rather than a layout choice. **A
// running query is a place a value hides** — `select * from patients where
// mrn = '...'` is a row of somebody's data wearing a WHERE clause — which is
// the same rule the schema dump follows, and it is why pg.activity is Write
// while pg.overview stays Read. The overview answers "is anything stuck"
// from state and duration, which needs no literal; pg.activity answers "what
// exactly is running", which is the question whose answer carries values.
// activitySQL builds the statement and names its last column.
//
// **The query text is selected or not in SQL, not trimmed off a table that
// already holds it.** Dropping a column after the fact is the kind of
// protection that holds until somebody logs the intermediate, adds a debug
// dump, or reorders the code; not asking the server for it means the literal
// never crosses the connection for the summary form at all. Split out as its
// own function so the property is testable without a database, which is the
// other half of the same point.
func activitySQL(withQuery bool) (string, view.Column) {
	tail, col := "coalesce(wait_event_type, '')",
		view.Column{Name: "Waiting on", Kind: view.KindStatus}
	if withQuery {
		tail, col = "left(coalesce(query, ''), 80)", view.Column{Name: "Query"}
	}
	return `
		select pid, usename, application_name, state,
		       coalesce(extract(epoch from now() - query_start)::int, 0),
		       ` + tail + `
		from pg_stat_activity
		where datname = current_database() and pid <> pg_backend_pid()
		order by query_start nulls last
		limit $1`, col
}

func activityView(ctx context.Context, conn *pgx.Conn, req plugin.Request, withQuery bool) (view.View, error) {
	sql, tail := activitySQL(withQuery)
	rows, err := conn.Query(ctx, sql, req.Int("limit"))
	if err != nil {
		return nil, classify(err, req)
	}
	defer rows.Close()
	t, err := rowsToTable(rows, req.Int("limit"))
	if err != nil {
		return nil, classify(err, req)
	}
	t.Columns = []view.Column{
		{Name: "PID", Kind: view.KindNumber}, {Name: "User"},
		{Name: "Application"}, {Name: "State", Kind: view.KindStatus},
		{Name: "Seconds", Kind: view.KindNumber}, tail,
	}
	return t, nil
}

// version is what this build claims to be, stamped by whatever built it:
// `-X main.version=`, which is the Makefile's flag and GoReleaser's own
// default. A build nobody stamped says "dev" rather than claiming a release
// number that was never cut — an index entry carries this verbatim, and a
// version is a fact about a release, not about the source it came from.
var version = "dev"

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "pg",
		Summary: "PostgreSQL: connection health, schema, rows and activity",
		Version: version,
		Capabilities: []plugin.Capability{
			cap(plugin.Capability{
				ID:         "pg.status",
				Summary:    "Whether the database answers, and what it is",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "The first thing to run. Answers \"can I reach it, as whom, and " +
					"what is it\" in one call, and every way of failing that question has its " +
					"own error code and a hint naming the next thing to try.",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						return statusView(ctx, conn, req)
					})
				},
			}),

			cap(plugin.Capability{
				ID:         "pg.table.list",
				Summary:    "List tables with their row estimates and sizes",
				Safety:     plugin.Read,
				Idempotent: true,
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						return tableListView(ctx, conn, req)
					})
				},
			},
				plugin.Field{Name: "schema", Type: plugin.String, Default: "",
					Help: "only this schema; empty means all"},
				plugin.Field{Name: "limit", Type: plugin.Int, Default: 50, Min: 1, Max: 1000,
					Help: "how many tables to show"}),

			cap(plugin.Capability{
				ID:      "pg.query",
				Summary: "Run a read-only query",
				// **Write, because it returns rows.** It was Read, on the
				// reasoning that a READ ONLY transaction mutates nothing —
				// true, and the wrong axis, the one the safety model opens by
				// rejecting. From the user: *"almost every table could store
				// sensitive data in a db, not only user table"*. Once that is
				// taken seriously there is no table this may read by default,
				// because there is no table known to be safe: orders carry
				// addresses, events carry payloads, logs carry tokens.
				//
				// What it buys is a read tier that means something. With
				// --allow-read alone this plugin now describes the database —
				// what server it is, what is in it, what shape it has, what
				// is stuck — and returns no value stored in it. --allow-write
				// is where rows live. That sentence was not true before and
				// the description claimed it anyway.
				Safety:     plugin.Write,
				Idempotent: true,
				Description: "Runs inside a READ ONLY transaction, so the server refuses any " +
					"statement that would write — including the ones that do not look like " +
					"writes, such as a SELECT over a data-modifying CTE. rta does not inspect " +
					"the SQL, PostgreSQL enforces it.\n\n" +
					"**Classified write for what it discloses, not what it changes.** It returns rows, " +
					"and there is no table it may read by default because there is no table known to " +
					"be safe — orders carry addresses, events carry payloads, application logs carry " +
					"tokens. So it needs --allow-write pg, which is the operator saying once that this " +
					"agent may read this database's contents; the read tier below it describes the " +
					"database and hands back nothing stored in it. Where the connection is a named " +
					"profile, every call in this namespace already needs a grant on top.",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						var out view.View
						err := readOnly(ctx, conn, func(tx pgx.Tx) error {
							rows, err := tx.Query(ctx, req.String("sql"))
							if err != nil {
								return err
							}
							defer rows.Close()
							t, err := rowsToTable(rows, req.Int("limit"))
							if errors.Is(err, ErrTooManyRows) {
								return view.Errorf("pg.query.toomany",
									"the query returned more than %d rows", req.Int("limit")).
									WithHint("add a LIMIT to the query, or raise --limit — refused rather " +
										"than shortened, because a truncated result set is a different " +
										"answer wearing the right shape")
							}
							if errors.Is(err, ErrTooLarge) {
								return view.Errorf("pg.query.toolarge",
									"the query returned more than %s", format.Bytes(maxBytes)).
									WithHint("select fewer columns, or fewer rows — a row bound is not a " +
										"size bound, and one wide text or bytea column is usually what " +
										"does this")
							}
							if err != nil {
								return err
							}
							out = t
							return nil
						})
						if err != nil {
							return nil, classify(err, req)
						}
						return out, nil
					})
				},
			},
				plugin.Field{Name: "sql", Type: plugin.Text, Required: true, Positional: true,
					Help: "the query to run"},
				// **The one result set in this plugin whose size the caller
				// decides.** Every other listing here declares a limit and
				// applies it in SQL; this one is the caller's own text, and
				// without a bound `select * from users` streamed every row into
				// a slice in the plugin, through the host, and at a model's
				// context — an unbounded allocation driven by an argument, and
				// a bulk read of a table nobody consented to row by row.
				plugin.Field{Name: "limit", Type: plugin.Int, Default: 200, Min: 1, Max: 10000,
					Help: "maximum rows to return; a larger result set is refused, not shortened"}),

			cap(plugin.Capability{
				ID:         "pg.schema.dump",
				Summary:    "Describe a schema's tables, columns and keys — no values",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "The shape of a schema in one call: tables, columns, types, nullability, " +
					"primary and unique keys, foreign keys and plain indexes, written the way pg_dump " +
					"writes them.\n\n" +
					"**Zero rows is not zero values, and the difference is where a schema leaks.** A " +
					"default can be an API key, a check constraint can enumerate customer tiers, and a " +
					"view or function body carries whatever WHERE clause and connection string somebody " +
					"wrote in it. So one mechanical rule decides what crosses: an expression is a place " +
					"a value can hide, and no expression crosses. No scanning for what looks sensitive, " +
					"which is the game nobody wins — the same reason pg.query does not inspect SQL and " +
					"lets the server enforce read-only instead.\n\n" +
					"What that costs is stated in the output rather than left to be discovered: this is " +
					"a description, not a restorable dump, everything omitted is counted in the header, " +
					"and `pg_dump --schema-only` is named for the person who needs the rest. Ungated " +
					"because anyone holding pg.query can already read information_schema.columns, so " +
					"refusing it would be theatre; what it changes is the record, one ledger line " +
					"instead of a dozen catalogue queries nobody will reconstruct.",
				Run: runSchemaDump,
			},
				plugin.Field{Name: "schema", Type: plugin.String, Default: "public", Positional: true,
					Help: "schema to describe"},
				plugin.Field{Name: "limit", Type: plugin.Int, Default: 100, Min: 1, Max: maxSchemaTables,
					Help: "how many tables to describe"}),

			cap(plugin.Capability{
				ID:      "pg.table.dump",
				Summary: "Read every row of one named table",
				// Write, and nothing here mutates: the transaction is READ
				// ONLY and the server enforces it. The class is about
				// disclosure, exactly as kv.get is read — and it is
				// what keeps the read-only MCP tier coherent, since with
				// --allow-read alone this plugin answers about health, shape
				// and activity and hands over no bulk rows.
				Safety:     plugin.Write,
				NeedsGrant: true,
				Scope:      "table",
				Idempotent: true,
				Description: "One table, named in the grant. **A capability whose blast radius cannot be " +
					"named in a grant does not belong on the agent surface** — which is why keys.backup " +
					"and kv.copy refuse MCP outright, and why there is no whole-database dump here: its " +
					"single authorized use would be \"everything\". One table has a radius a person can " +
					"consent to, so `grant allow pg.table.dump --scope public.orders` authorizes that " +
					"relation and nothing beside it.\n\n" +
					"That is not a claim the named table is the harmless one. Almost any table holds " +
					"something you would not hand over — orders carry addresses, events carry payloads, " +
					"application logs carry tokens — so the per-table scope is not sorting tables into " +
					"safe and unsafe. It exists because \"this one, now, for the next fifteen minutes\" " +
					"is the only thing a person can meaningfully consent to about a database, and the " +
					"mechanism's job is to make sure that is the only table that moves.\n\n" +
					"Over MCP the name must already be qualified as schema.table. The grant gate matches " +
					"the argument byte-for-byte against the scope a person wrote, before anything looks " +
					"in the catalogue, so a bare name would let one unexpired grant follow whichever " +
					"schema resolved first — drop public.orders, create archive.orders, and the same " +
					"grant reads a different table. A person at a terminal keeps the short form, and an " +
					"ambiguous name is refused rather than resolved by precedence.\n\n" +
					"Bounded on rows and on bytes, and over either bound it is refused rather than " +
					"shortened: a truncated dump is a different answer wearing the right shape. Ordered " +
					"by primary key where there is one, so the first thousand rows are the same thousand " +
					"next time. --columns narrows what is read and can never widen it — useful, but it " +
					"is the caller minimising its own ask, not a control the operator holds, since a " +
					"grant names a record and has no way to say \"orders but not the email column\".",
				Run: runTableDump,
			},
				// No Config key, deliberately: a Scope input that config can
				// fill is checked as the empty scope when the caller omits it
				// and then runs against the configured value, so the string a
				// person granted and the object read would not be the same
				// string.
				plugin.Field{Name: "table", Type: plugin.String, Positional: true, Required: true,
					Help: "table to dump, as schema.table (required over MCP; bare names are resolved for a person)"},
				plugin.Field{Name: "columns", Type: plugin.StringSlice,
					Help: "only these columns; empty means all"},
				plugin.Field{Name: "limit", Type: plugin.Int, Default: 1000, Min: 1, Max: maxDumpRows,
					Help: "maximum rows to return; a larger table is refused, not shortened"}),

			cap(plugin.Capability{
				ID:      "pg.dump",
				Summary: "Back up the whole database to a file, for a person at a terminal",
				Safety:  plugin.Write,
				// Not idempotent, and the reason is the guarantee: running it
				// twice at the same --out refuses rather than overwriting.
				Idempotent: false,
				Description: "The whole database as a file you can restore. **Refuses MCP outright " +
					"rather than asking for a grant**, which is the same line keys.backup and kv.copy " +
					"draw: every control in rta bounds a call by what it names, and a full dump's one " +
					"authorized use is everything, so a grant covering it would be a rubber stamp with " +
					"an expiry date rather than consent. An agent that needs rows asks for pg.table.dump " +
					"and a person names the table in the grant.\n\n" +
					"Runs `pg_dump` rather than reimplementing it: a restorable dump has to get " +
					"sequences, extensions, ownership, row-level security and COPY escaping right, " +
					"and a file that will not restore is worse than no capability at all. The " +
					"password reaches it through the child's environment, never through argv, which " +
					"`ps` shows to everyone on the machine. Created with O_EXCL at 0600, so an " +
					"existing file is never written over; a failed run takes its half-written file " +
					"with it, because a partial dump is the one that gets restored six months " +
					"later.\n\n" +
					"The receipt says the file is unencrypted, names the restore command, states " +
					"which consistency guarantee it had, and reports whether the source was a " +
					"primary or a replica — a standby dump is only as current as its replay lag, " +
					"and a standby can cancel a long dump to keep up (that refusal names " +
					"hot_standby_feedback).\n\n" +
					"**For a big database, `--format directory --jobs N`** dumps N tables at once, " +
					"measured at 7x on 834 MB. It stays consistent: the leader exports its snapshot " +
					"and every worker joins it. `--no-synchronized-snapshots` is never passed, not " +
					"even as a fallback — it turns a parallel dump into unrelated reads at different " +
					"times, producing a file that restores without complaint into a state the " +
					"database was never in. --jobs where it cannot work is refused by name rather " +
					"than by changing the format under you, and carries into the printed " +
					"`pg_restore` command. The bytes never pass through rta: the destination " +
					"descriptor is handed to pg_dump directly.",
				Run: runFullDump,
			},
				// Local for the usual reason — a destination is a destination —
				// and so a caller can never choose which file on the host is
				// written. Belt and braces beside the MCP refusal, since the
				// two protect against different mistakes: the refusal is this
				// capability's, and Local is the contract's.
				plugin.Field{Name: "out", Type: plugin.Path, Local: true,
					Help: "file to write; refused if it already exists"},
				plugin.Field{Name: "format", Type: plugin.String, Default: "plain",
					Options: []string{"plain", "custom", "directory"},
					Help: "plain is SQL for psql; custom is one compressed file for pg_restore; " +
						"directory is one compressed file per table, and the only one --jobs can use"},
				plugin.Field{Name: "jobs", Type: plugin.Int, Default: 1, Min: 1, Max: 32,
					Help: "dump this many tables at once (needs --format directory)"},
				plugin.Field{Name: "include", Type: plugin.String, Default: "all",
					Options: []string{"all", "schema", "data"},
					Help:    "what to put in the file"}),

			cap(plugin.Capability{
				ID:      "pg.restore",
				Summary: "Restore a pg.dump backup into a database, for a person at a terminal",
				// Destructive, because that is what it is: it writes a file's
				// whole contents into a live database, and with --clean it
				// drops objects on the way in. The class buys the --yes gate
				// a person should have to type through.
				Safety:     plugin.Destructive,
				Idempotent: false,
				Description: "The other half of pg.dump — the file back into a database. **Refuses MCP " +
					"outright** for the dump's reason run in reverse: the dump refuses because " +
					"everything would leave, and a restore is everything arriving, written into a live " +
					"database. Neither direction has a blast radius a grant could name, so both belong " +
					"to the person at the keyboard.\n\n" +
					"The format is read from the bytes, never the filename: a directory holding " +
					"toc.dat restores through pg_restore --jobs, a file beginning PGDMP is a custom " +
					"archive, anything else replays through psql — so a custom archive named " +
					"backup.sql cannot be handed to the wrong tool.\n\n" +
					"**A non-empty target is refused unless --clean says that is the point**, which is " +
					"the dump's O_EXCL pointing the other way: the dump never writes over an existing " +
					"file, and the restore never lands on a database that already holds relations. A " +
					"replica is refused before anything runs — a standby cannot be written, and the " +
					"only path that keeps it matching its primary is restoring there.\n\n" +
					"All-or-nothing by default: one transaction that rolls back entirely on failure, " +
					"with ON_ERROR_STOP so psql cannot count errors quietly and commit the half that " +
					"worked. --jobs N trades that guarantee for speed — parallel workers cannot share " +
					"a transaction, the same reason a parallel dump needs pg_export_snapshot — and " +
					"the receipt says which guarantee the run actually had. rta does not create the " +
					"target database: a capability that invented a database on a typo'd name would " +
					"turn every misspelling into a new database, so `createdb` stays one command away.",
				Run: runRestore,
			},
				plugin.Field{Name: "file", Type: plugin.Path, Local: true, Positional: true,
					Required: true,
					Help: "the dump to restore — plain SQL, a custom-format file, or a " +
						"directory-format dump; the format is read from the bytes, not the name"},
				plugin.Field{Name: "jobs", Type: plugin.Int, Default: 1, Min: 1, Max: 32,
					Help: "restore this many objects at once (custom or directory format; trades " +
						"the single-transaction guarantee for speed)"},
				plugin.Field{Name: "clean", Type: plugin.Bool,
					Help: "drop existing objects before recreating them (custom or directory format)"},
				plugin.Field{Name: "no-owner", Type: plugin.Bool,
					Help: "skip ownership changes so restored objects belong to the connecting " +
						"role (custom or directory format)"}),

			cap(plugin.Capability{
				ID:         "pg.database.list",
				Summary:    "List databases on this server, with their sizes",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Exists because pg.status's \"no database named X\" hint promises it. " +
					"A hint naming a command that does not exist is worse than no hint: it sends " +
					"somebody to type something that fails differently.",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						return databaseListView(ctx, conn, req)
					})
				},
			}),

			cap(plugin.Capability{
				ID:      "pg.activity",
				Summary: "What every connected session is doing right now",
				// Write, and nothing here mutates: **a running query is a
				// place a value hides.** `select * from patients where mrn =
				// '...'` is a row of somebody's data wearing a WHERE clause,
				// and this returns eighty characters of every statement on
				// the server. The same rule pg.schema.dump follows, applied
				// to the one other capability that hands back text somebody
				// else wrote.
				Safety:     plugin.Write,
				Idempotent: true,
				Description: "Classified write for what it discloses rather than what it changes, the " +
					"same reading kv.get gets: the query column carries whatever literals are in " +
					"the statements currently running. `pg overview --detail` keeps the same rows " +
					"without that column — state, duration and what each session is waiting on, which " +
					"answers \"is anything stuck\" and is a value nobody stored — so the glanceable " +
					"form stays in the read tier and this one does not.",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						return activityView(ctx, conn, req, true)
					})
				},
			},
				plugin.Field{Name: "limit", Type: plugin.Int, Default: 50, Min: 1, Max: 1000,
					Help: "how many sessions to show"}),

			cap(plugin.Capability{
				ID:         "pg.overview",
				Summary:    "Everything about this connection at a glance",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				Description: "The compact form is four figures worth a glance: role, size, active " +
					"queries, cache hit ratio. The full page (--detail) adds replication, the " +
					"largest tables and current activity — everything pg.status, pg.table.list " +
					"and pg.activity would otherwise take three calls to assemble, through the " +
					"one connection this call already opened.",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						if req.Bool("detail") {
							return detailedOverview(ctx, conn, req)
						}
						return compactOverview(ctx, conn, req)
					})
				},
			}),
		},
	}
}
