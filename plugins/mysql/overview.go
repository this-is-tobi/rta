package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func statusCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "mysql.status",
		Summary:    "Whether the database answers, and what it is",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "The cheapest possible call: connect, ask the server what it is, disconnect. " +
			"Useful on its own as a reachability check, and it is the call whose failure carries " +
			"the classified hint for every connection problem the others would hit later.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
				return statusView(ctx, db, req)
			})
		},
	})
}

// serverInfo is the handful of scalars every glance here starts from. Read in
// one round trip rather than four, because a status call that costs four
// round trips against a loaded server is a status call people stop making.
type serverInfo struct {
	version  string
	flavour  string
	uptime   time.Duration
	threads  int64
	running  int64
	maxConns int64
}

func readServerInfo(ctx context.Context, db *sql.DB) (serverInfo, error) {
	var info serverInfo
	row := db.QueryRowContext(ctx, `SELECT VERSION(), @@max_connections`)
	if err := row.Scan(&info.version, &info.maxConns); err != nil {
		return info, err
	}
	info.flavour = flavourOf(info.version)

	// SHOW GLOBAL STATUS rather than performance_schema: it is available to
	// an unprivileged user and on every version back to 5.x, where the
	// performance_schema tables need a grant somebody has to have thought to
	// give. A description of the server should not be the call that needs the
	// most privilege.
	rows, err := db.QueryContext(ctx,
		`SHOW GLOBAL STATUS WHERE Variable_name IN ('Uptime','Threads_connected','Threads_running')`)
	if err != nil {
		return info, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return info, err
		}
		n, _ := strconv.ParseInt(value, 10, 64)
		switch name {
		case "Uptime":
			info.uptime = time.Duration(n) * time.Second
		case "Threads_connected":
			info.threads = n
		case "Threads_running":
			info.running = n
		}
	}
	return info, rows.Err()
}

// flavourOf reads the fork out of the version string, which is the only place
// it is stated. MariaDB reports something like "11.4.2-MariaDB"; MySQL does
// not carry the word at all. Worth surfacing because the two have diverged
// enough that somebody debugging needs to know which one answered — and
// because the answer decides whether plugins/mariadb has anything to add.
func flavourOf(version string) string {
	lower := strings.ToLower(version)
	for _, marker := range []string{"MariaDB", "Percona"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return marker
		}
	}
	return "MySQL"
}

func statusView(ctx context.Context, db *sql.DB, req plugin.Request) (view.View, error) {
	info, err := readServerInfo(ctx, db)
	if err != nil {
		return nil, classify(err, req)
	}
	pairs := []view.Pair{
		{Key: "server", Value: fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))},
		{Key: "flavour", Value: info.flavour},
		{Key: "version", Value: info.version},
		{Key: "uptime", Value: info.uptime.Round(time.Second).String()},
		{Key: "connections", Value: fmt.Sprintf("%d of %d", info.threads, info.maxConns)},
		{Key: "running", Value: strconv.FormatInt(info.running, 10)},
	}
	if dbName := req.String("database"); dbName != "" {
		pairs = append(pairs, view.Pair{Key: "database", Value: dbName})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

func overviewCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "mysql.overview",
		Summary:    "Everything about this connection at a glance",
		Safety:     plugin.Read,
		Idempotent: true,
		Detailed:   true,
		Description: "What server this is, how long it has been up, how much of its connection " +
			"budget is in use, and the largest databases on it.\n\n" +
			"--detail adds what every session is doing, without the statement text — state, " +
			"time and command, which answers \"is anything stuck\" and hands back nothing " +
			"anybody stored. The statement text is mysql.activity, and it is a write for " +
			"exactly that reason.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
				return overviewView(ctx, db, req)
			})
		},
	})
}

// overviewView composes its sections through the one connection its own Run
// already opened, rather than through plugin.Page.AddAs, which would open a
// connection per section for what is meant to be a single glance.
func overviewView(ctx context.Context, db *sql.DB, req plugin.Request) (view.View, error) {
	status, err := statusView(ctx, db, req)
	if err != nil {
		return nil, err
	}
	p := plugin.NewPage(ctx, req)
	p.Put("status", status)

	databases, err := databaseTable(ctx, db, req, 10)
	if err != nil {
		return nil, err
	}
	p.Put("databases", databases)

	if req.Bool("detail") {
		activity, err := activityView(ctx, db, req, false)
		if err != nil {
			return nil, err
		}
		p.Put("activity", activity)
	}
	return p.View(), nil
}

func databaseListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "mysql.database.list",
		Summary:    "List databases on this server, with their sizes",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Names, table counts and on-disk sizes. Sizes come from INFORMATION_SCHEMA " +
			"and are what the storage engine last reported rather than a live measurement — " +
			"close enough to find the big one, not close enough to bill on.\n\n" +
			"Only the databases this user may see: MySQL filters INFORMATION_SCHEMA by grant, so " +
			"a short list here means a narrow grant and not an empty server.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
				return databaseTable(ctx, db, req, req.Int("limit"))
			})
		},
	}, plugin.Field{Name: "limit", Type: plugin.Int, Default: 100, Min: 1, Max: 10000,
		Help: "how many databases to show"})
}

func databaseTable(ctx context.Context, db *sql.DB, req plugin.Request, limit int) (view.Table, error) {
	// Aggregated from INFORMATION_SCHEMA.TABLES rather than SHOW DATABASES,
	// because the size is the column that makes this worth running and SHOW
	// does not carry it. LEFT JOIN off SCHEMATA so a database holding no
	// tables still appears — an empty database is a fact, and dropping it
	// would make the list disagree with SHOW DATABASES for no stated reason.
	rows, err := db.QueryContext(ctx, `
		SELECT s.SCHEMA_NAME,
		       COUNT(t.TABLE_NAME),
		       COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0)
		  FROM INFORMATION_SCHEMA.SCHEMATA s
		  LEFT JOIN INFORMATION_SCHEMA.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
		 GROUP BY s.SCHEMA_NAME
		 ORDER BY 3 DESC
		 LIMIT ?`, limit)
	if err != nil {
		return view.Table{}, classify(err, req)
	}
	defer func() { _ = rows.Close() }()

	t := view.Table{Columns: []view.Column{
		{Name: "Database"},
		{Name: "Tables", Kind: view.KindNumber},
		{Name: "Size", Kind: view.KindBytes},
	}}
	for rows.Next() {
		var name string
		var tables int64
		var size any
		if err := rows.Scan(&name, &tables, &size); err != nil {
			return view.Table{}, classify(err, req)
		}
		t.Rows = append(t.Rows, []string{name, strconv.FormatInt(tables, 10), bytesCell(size)})
	}
	if err := rows.Err(); err != nil {
		return view.Table{}, classify(err, req)
	}
	t.Total = len(t.Rows)
	return t, nil
}
