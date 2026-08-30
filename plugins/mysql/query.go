package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func queryCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "mysql.query",
		Summary: "Run a read-only query",
		// **Write, because it returns rows.** Nothing here mutates: the
		// transaction below is opened READ ONLY and the server refuses any
		// statement that would write. That is true, and it is the wrong axis.
		//
		// Almost every table in a database can hold something sensitive, not
		// only the one called `users`. Once that is taken seriously there is no
		// table this may read by default, because there is no table known to be
		// safe: orders carry addresses, events carry payloads, application logs
		// carry tokens.
		//
		// What the split buys is a read tier that means something. With the
		// read tier alone this plugin describes the database — what server it
		// is, what is in it, what shape it has, what is stuck — and returns no
		// value anybody stored in it. The write tier is where rows live.
		Safety:     plugin.Write,
		Idempotent: true,
		Description: "Runs inside a READ ONLY transaction, so the server refuses any statement " +
			"that would write. rta does not inspect the SQL and does not try to — MySQL enforces " +
			"it, which is the only place the enforcement is worth trusting.\n\n" +
			"**Classified write for what it discloses, not what it changes.** It returns rows, and " +
			"there is no table it may read by default because there is no table known to be safe. " +
			"So it needs the write tier for this namespace, which is the operator saying once that " +
			"this agent may read this database's contents; the read tier below it describes the " +
			"database and hands back nothing stored in it. Where the connection is a named profile, " +
			"every call in this namespace already needs a grant on top.\n\n" +
			"Over --limit rows it is refused rather than shortened: a truncated result set is a " +
			"different answer wearing the right shape.",
		Run: runQuery,
	}, plugin.Field{Name: "sql", Type: plugin.String, Positional: true, Required: true,
		Help: "the statement to run"},
		plugin.Field{Name: "limit", Type: plugin.Int, Default: 50, Min: 1, Max: 1000,
			Help: "how many rows to allow before refusing"})
}

func runQuery(ctx context.Context, req plugin.Request) (view.View, error) {
	return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
		return queryView(ctx, db, req)
	})
}

// queryView is split from runQuery so the read-only transaction can be
// asserted against a driver rather than only against a live server. The
// enforcement here is the server's, not rta's, which makes "was the option
// actually set" the only thing worth testing — and untestable while the
// connection was opened inside the same function.
func queryView(ctx context.Context, db *sql.DB, req plugin.Request) (view.View, error) {
	limit := req.Int("limit")

	// READ ONLY at the transaction level, which is what makes this safe
	// against statements that do not look like writes — a SELECT calling a
	// procedure that mutates, for one. Inspecting the SQL here would be a
	// blocklist, and a blocklist against a language with this many ways to
	// spell a write is a promise nobody can keep.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, classify(err, req)
	}
	// Always rolled back. There is nothing to commit and a rollback of a
	// read-only transaction cannot fail in a way that matters, so this is
	// the one path rather than a branch that has to be got right twice.
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, req.String("sql"))
	if err != nil {
		return nil, classify(err, req)
	}
	defer func() { _ = rows.Close() }()

	t, err := rowsToTable(rows, limit)
	switch {
	case errors.Is(err, ErrTooManyRows):
		return nil, view.Errorf("mysql.query.toomany",
			"the query returned more than %d rows", limit).
			WithHint("add a LIMIT to the query, or raise --limit — refused rather than " +
				"shortened, because a truncated result set is a different answer wearing " +
				"the right shape")
	case errors.Is(err, ErrTooLarge):
		return nil, view.Errorf("mysql.query.toolarge",
			"the query returned more than %s", format.Bytes(maxBytes)).
			WithHint("select fewer columns, or fewer rows — a row bound is not a size bound, " +
				"and one wide TEXT or BLOB column is usually what does this")
	case err != nil:
		return nil, classify(err, req)
	}
	return t, nil
}

func activityCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "mysql.activity",
		Summary: "What every connected session is doing right now",
		// Write, and nothing here mutates: **a running query is a place a
		// value hides.** `select * from patients where mrn = '...'` is a row of
		// somebody's data wearing a WHERE clause, and this returns the
		// statement text of everything on the server.
		//
		// mysql.overview --detail keeps the same rows without that column —
		// state, time and command, which answers "is anything stuck" and is a
		// value nobody stored — so the glanceable form stays in the read tier
		// and this one does not.
		Safety:     plugin.Write,
		Idempotent: true,
		Description: "Classified write for what it discloses rather than what it changes: the info " +
			"column carries whatever literals are in the statements currently running.\n\n" +
			"`mysql overview --detail` keeps the same rows without that column — state, time and " +
			"command — which answers \"is anything stuck\" without handing back anything anybody " +
			"stored, so the glanceable form stays in the read tier.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
				return activityView(ctx, db, req, true)
			})
		},
	}, plugin.Field{Name: "limit", Type: plugin.Int, Default: 50, Min: 1, Max: 1000,
		Help: "how many sessions to show"})
}

// activityView is shared by mysql.activity and mysql.overview --detail, and
// withStatements is the entire difference between them. Keeping it as one
// function means the two can never drift into disagreeing about what a
// session's state is called — and, more importantly, that the read-tier
// caller physically cannot produce the column it is not allowed to show.
func activityView(ctx context.Context, db *sql.DB, req plugin.Request, withStatements bool) (view.View, error) {
	limit := req.Int("limit")
	if limit == 0 {
		limit = 50
	}
	// INFORMATION_SCHEMA.PROCESSLIST rather than SHOW PROCESSLIST, because it
	// can be filtered and ordered in SQL. Sleeping sessions are excluded: on
	// any real server they are most of the list and none of the answer.
	rows, err := db.QueryContext(ctx, `
		SELECT ID, COALESCE(USER,''), COALESCE(HOST,''), COALESCE(DB,''),
		       COALESCE(COMMAND,''), TIME, COALESCE(STATE,''), COALESCE(INFO,'')
		  FROM INFORMATION_SCHEMA.PROCESSLIST
		 WHERE COMMAND <> 'Sleep'
		 ORDER BY TIME DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, classify(err, req)
	}
	defer func() { _ = rows.Close() }()

	cols := []view.Column{
		{Name: "ID", Kind: view.KindNumber},
		{Name: "User"},
		{Name: "Host"},
		{Name: "DB"},
		{Name: "Command"},
		{Name: "Time", Kind: view.KindDuration},
		{Name: "State"},
	}
	if withStatements {
		cols = append(cols, view.Column{Name: "Statement"})
	}
	t := view.Table{Columns: cols}

	for rows.Next() {
		var id, seconds int64
		var user, host, dbName, command, state, info string
		if err := rows.Scan(&id, &user, &host, &dbName, &command, &seconds, &state, &info); err != nil {
			return nil, classify(err, req)
		}
		row := []string{
			strconv.FormatInt(id, 10), user, host, dbName, command,
			(time.Duration(seconds) * time.Second).String(), state,
		}
		if withStatements {
			row = append(row, truncateStatement(info))
		}
		t.Rows = append(t.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err, req)
	}
	t.Total = len(t.Rows)
	return t, nil
}

// statementWidth is how much of a running statement is shown. Enough to
// recognise which query this is, short of pasting a whole application's
// generated SQL into a terminal.
const statementWidth = 120

// truncateStatement also collapses whitespace, because a statement written
// across twelve lines in application source arrives with all of them and
// turns one table row into a page.
func truncateStatement(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= statementWidth {
		return s
	}
	return s[:statementWidth] + "…"
}
