// Command rta-plugin-pg is the first external rta plugin.
//
// It exists to prove the contract rather than to be a database client: if the
// declaration model survives a plugin that needs a connection, a credential,
// operator-supplied configuration, arbitrary result shapes and a dozen
// distinct failure modes, it survives anything (PROJECT.md §7.3).
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
// may decide on its own (D60). Put it there yourself once you have looked:
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
// ADR 0018 §7.
package main

import (
	"context"

	"github.com/jackc/pgx/v5"

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
	// TLS is not a hypothetical cost, ADR 0018 §7 measured it killing the
	// forward on every clean disconnect.
	c.NoPreview = true
	return c
}

// statusView answers "can I reach it, as whom, and what is it" — the shared
// query behind pg.status and the lead section of pg.overview.
func statusView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	var version, db, user, size string
	// pg_size_pretty rather than a raw count. Every producer formats its own
	// numbers — view.ColumnKind aligns and does not render — so the only
	// question is whose vocabulary, and PostgreSQL's own is the one an
	// operator already reads in psql. pkg/format is the answer when the
	// number does not come from a server that can name it.
	err := conn.QueryRow(ctx,
		`select version(), current_database(), current_user,
		        pg_size_pretty(pg_database_size(current_database()))`).
		Scan(&version, &db, &user, &size)
	if err != nil {
		return nil, classify(err, req)
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "server", Value: version},
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
	t, err := rowsToTable(rows)
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
	t, err := rowsToTable(rows)
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
func activityView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	rows, err := conn.Query(ctx, `
		select pid, usename, application_name, state,
		       coalesce(extract(epoch from now() - query_start)::int, 0),
		       left(coalesce(query, ''), 80)
		from pg_stat_activity
		where datname = current_database() and pid <> pg_backend_pid()
		order by query_start nulls last
		limit $1`, req.Int("limit"))
	if err != nil {
		return nil, classify(err, req)
	}
	defer rows.Close()
	t, err := rowsToTable(rows)
	if err != nil {
		return nil, classify(err, req)
	}
	t.Columns = []view.Column{
		{Name: "PID", Kind: view.KindNumber}, {Name: "User"},
		{Name: "Application"}, {Name: "State", Kind: view.KindStatus},
		{Name: "Seconds", Kind: view.KindNumber}, {Name: "Query"},
	}
	return t, nil
}

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "pg",
		Summary: "PostgreSQL: connection health, schema, rows and activity",
		Version: "0.1.0",
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
				ID:         "pg.query",
				Summary:    "Run a read-only query",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Runs inside a READ ONLY transaction, so the server refuses any " +
					"statement that would write — including the ones that do not look like " +
					"writes, such as a SELECT over a data-modifying CTE. That is what makes " +
					"the read safety class a fact rather than a claim: rta does not inspect " +
					"the SQL, PostgreSQL enforces it.",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						var out view.View
						err := readOnly(ctx, conn, func(tx pgx.Tx) error {
							rows, err := tx.Query(ctx, req.String("sql"))
							if err != nil {
								return err
							}
							defer rows.Close()
							t, err := rowsToTable(rows)
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
					Help: "the query to run"}),

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
				ID:         "pg.activity",
				Summary:    "What every connected session is doing right now",
				Safety:     plugin.Read,
				Idempotent: true,
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return withConn(ctx, req, func(ctx context.Context, conn *pgx.Conn) (view.View, error) {
						return activityView(ctx, conn, req)
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
