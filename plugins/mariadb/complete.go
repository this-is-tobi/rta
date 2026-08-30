package main

import (
	"context"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Live completion talks to the real server, which is the only way to complete
// a name that lives there. Two consequences follow, and both are handled here
// rather than left to each suggester.
//
// It runs while somebody is holding down Tab, so it gets a short deadline of
// its own: a completion that hangs is worse than one that returns nothing,
// because the shell freezes with it. And it can fail for every ordinary reason
// a call can — server down, wrong password, no grant — so every failure here
// returns no suggestions rather than an error. There is no way to show one at
// a completion prompt, and a half-typed flag is not the place to learn the
// database is unreachable.
const completeTimeout = 2 * time.Second

// maxSuggestions bounds what comes back. A shell that offers eight hundred
// completions has not helped anybody choose one.
const maxSuggestions = 200

func complete(ctx context.Context, req plugin.Request, query string, args ...any) []string {
	ctx, cancel := context.WithTimeout(ctx, completeTimeout)
	defer cancel()

	db, verr := connect(ctx, req)
	if verr != nil {
		return nil
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() && len(out) < maxSuggestions {
		var name string
		if err := rows.Scan(&name); err != nil {
			return out
		}
		out = append(out, name)
	}
	return out
}

func suggestDatabases(ctx context.Context, req plugin.Request) []string {
	return complete(ctx, req,
		`SELECT SCHEMA_NAME FROM INFORMATION_SCHEMA.SCHEMATA ORDER BY SCHEMA_NAME`)
}

// suggestTables completes against whichever database the rest of the command
// already names, so tabbing --table after naming a schema offers that schema's
// tables rather than every table on the server.
func suggestTables(ctx context.Context, req plugin.Request) []string {
	schema := req.String("schema")
	if schema == "" {
		schema = req.String("database")
	}
	if schema == "" {
		return nil // nothing to list until a sibling input names one
	}
	return complete(ctx, req,
		`SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`,
		schema)
}

// Compile-time proof that the suggesters match what plugin.Field expects. A
// mismatch is otherwise found at the call site, which is further from the
// thing that has to change.
var (
	_ func(context.Context, plugin.Request) []string = suggestDatabases
	_ func(context.Context, plugin.Request) []string = suggestTables
)
