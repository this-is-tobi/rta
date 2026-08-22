package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// pg.overview composes what the other four capabilities already know how to
// answer into one screen, through the one connection its own Run already
// opened — the reason its sections are called directly rather than through
// plugin.Page.AddAs, which invokes a full Handler and would open a separate
// connection per section for what is supposed to be a single glance.
//
// It stays NoPreview like every capability here (see cap's comment): the
// automatic dashboard must not decide, on its own, that a database is worth
// polling every five seconds. An operator who has looked at their own setup
// and decided otherwise can still say so explicitly — `dashboard.tiles`
// accepts it regardless, because naming a capability in a config file is
// the asking.

// overviewTableLimit and overviewActivityLimit are the counts pg.overview
// asks for from pg.table.list and pg.activity — narrower than each
// capability's own default, because five sections share one screen and this
// one is a glance, not the report `rta pg table list` gives on its own.
const (
	overviewTableLimit    = 5
	overviewActivityLimit = 10
)

// cacheHitFloor is the textbook rule of thumb for pg_statio's cache hit
// ratio: below it, more of shared_buffers usually pays for itself. Not a
// verdict on any particular workload — a cold cache after a restart reads
// identically to one that is genuinely starved, which is why the line names
// what to check rather than declaring the number wrong.
const cacheHitFloor = 90.0

// compactOverview is four figures worth a glance, in the same "add what
// answered" style as builtin/sys's runOverview: one query failing costs the
// reader that one line, not the page.
func compactOverview(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	kv := view.KeyValue{}
	add := func(key, value string) {
		if value != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: key, Value: value})
		}
	}

	var db, size string
	if err := conn.QueryRow(ctx,
		`select current_database(), pg_size_pretty(pg_database_size(current_database()))`).
		Scan(&db, &size); err == nil {
		add("database", db+" · "+size)
	}
	if role, err := roleOf(ctx, conn); err == nil {
		add("role", role)
	}
	var active int
	if err := conn.QueryRow(ctx,
		`select count(*) from pg_stat_activity where datname = current_database() and state = 'active'`).
		Scan(&active); err == nil {
		add("active queries", fmt.Sprint(active))
	}
	if v, err := cacheView(ctx, conn, req); err == nil {
		if kvv, ok := v.(view.KeyValue); ok && len(kvv.Pairs) > 0 {
			add("cache", kvv.Pairs[0].Value)
		}
	}

	if len(kv.Pairs) == 0 {
		return nil, view.Errorf("pg.overview.unavailable", "no figure could be read")
	}
	return kv, nil
}

// detailedOverview is the full page: status, replication, cache, the
// largest tables and current activity, each dropped independently on
// failure rather than sinking the whole page — the same contract
// plugin.Page documents, used here through Put/Warn since every section is
// already in hand rather than behind a Handler to invoke.
func detailedOverview(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	p := plugin.NewPage(ctx, req)
	put := func(title string, v view.View, err error) {
		if err != nil {
			p.Warn(view.AsError(err, "page.section.failed"))
			return
		}
		p.Put(title, v)
	}

	v, err := statusView(ctx, conn, req)
	put("status", v, err)

	v, err = replicationView(ctx, conn, req)
	put("replication", v, err)

	v, err = cacheView(ctx, conn, req)
	put("cache", v, err)

	v, err = tableListView(ctx, conn, req.With(map[string]any{"limit": overviewTableLimit}))
	put("largest tables", v, err)

	v, err = activityView(ctx, conn, req.With(map[string]any{"limit": overviewActivityLimit}))
	put("activity", v, err)

	if p.Empty() {
		return nil, view.Errorf("pg.overview.unavailable", "no section could be produced")
	}
	return p.View(), nil
}

// roleOf reports "primary" or "standby". pg_is_in_recovery is true for the
// lifetime of a standby, including one that has been promoted and is still
// catching up, which is exactly the moment this fact is worth having.
//
// Returns the raw driver error, uncoded: every caller already holds a
// plugin.Request and classifies it themselves, and roleOf has no request of
// its own to classify with (compactOverview treats it as best-effort and
// never surfaces the error at all).
func roleOf(ctx context.Context, conn *pgx.Conn) (string, error) {
	var recovery bool
	if err := conn.QueryRow(ctx, `select pg_is_in_recovery()`).Scan(&recovery); err != nil {
		return "", err
	}
	if recovery {
		return "standby", nil
	}
	return "primary", nil
}

// replicationView answers the question a role alone does not: a standby
// says how far behind it is, and a primary lists who is connected to it.
// The two shapes differ because the two questions differ — a standby has
// exactly one upstream and one lag figure, a primary has zero or more
// downstreams — and view.View exists precisely so a section can be
// whichever shape answers its own question honestly.
//
// A primary with no connected standbys renders as a table with headers and
// no rows, the same as pg.table.list on a fresh database: checked, and
// there is nothing there, which is a different fact from "could not check".
func replicationView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	role, err := roleOf(ctx, conn)
	if err != nil {
		return nil, classify(err, req)
	}
	if role == "standby" {
		var lagSeconds int
		err := conn.QueryRow(ctx,
			`select coalesce(extract(epoch from now() - pg_last_xact_replay_timestamp())::int, 0)`).
			Scan(&lagSeconds)
		if err != nil {
			return nil, classify(err, req)
		}
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "role", Value: "standby"},
			{Key: "replay lag", Value: fmt.Sprintf("%ds", lagSeconds)},
		}}, nil
	}

	rows, err := conn.Query(ctx, `
		select application_name, coalesce(client_addr::text, 'local'), state,
		       coalesce(extract(epoch from replay_lag)::int, 0)
		from pg_stat_replication
		order by application_name`)
	if err != nil {
		return nil, classify(err, req)
	}
	defer rows.Close()
	t, err := rowsToTable(rows)
	if err != nil {
		return nil, classify(err, req)
	}
	t.Columns = []view.Column{
		{Name: "Standby"}, {Name: "Client"}, {Name: "State", Kind: view.KindStatus},
		{Name: "Lag", Kind: view.KindDuration},
	}
	return t, nil
}

// cacheView renders pg_statio's buffer cache hit ratio, the textbook first
// question about a database that feels slow: a low ratio means the working
// set does not fit in shared_buffers, and everything else is downstream of
// that. -1 signals "nothing to divide by yet" from SQL rather than a NULL
// pgx has to be told how to scan, matching how pg.activity already turns
// query_start's possible NULL into 0 in the query itself.
func cacheView(ctx context.Context, conn *pgx.Conn, req plugin.Request) (view.View, error) {
	var ratio float64
	err := conn.QueryRow(ctx, `
		select coalesce(round(100 * sum(heap_blks_hit) /
		       nullif(sum(heap_blks_hit) + sum(heap_blks_read), 0), 2), -1)
		from pg_statio_user_tables`).Scan(&ratio)
	if err != nil {
		return nil, classify(err, req)
	}
	if ratio < 0 {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "cache hit ratio", Value: "no read activity recorded yet"},
		}}, nil
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "cache hit ratio", Value: fmt.Sprintf("%.1f%% (%s)", ratio, cacheVerdict(ratio))},
	}}, nil
}

// cacheVerdict is cacheView's ratio-to-word mapping, pulled out to where it
// can be tested against the boundary without a database — the same reason
// builtin/sys keeps loadVerdict and usageStatus as plain functions of a
// float rather than inline in the query that produces one.
func cacheVerdict(ratio float64) string {
	if ratio < cacheHitFloor {
		return "low — more shared_buffers may help, or this is a cold cache"
	}
	return "healthy"
}
