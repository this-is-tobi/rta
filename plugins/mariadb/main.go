// Command rta-plugin-mariadb talks to a MariaDB server: what it is, what is
// in it, what shape that has, what is running, whether its cluster is healthy,
// and — behind the write tier — the rows themselves.
//
// # Why this is a separate artifact from plugins/mysql
//
// MariaDB answers the MySQL wire protocol and carries the same
// INFORMATION_SCHEMA, so the connection handling, the row bounds and the
// schema queries here are the same work. They are duplicated rather than
// shared, and that is deliberate rather than an oversight.
//
// A plugin is the unit rta approves, and it approves an artifact's content
// digest rather than its name. Two plugins that shared a library would be two
// artifacts whose behaviour moves together while their approvals do not — so
// approving one would be partly approving the other, which is exactly the
// property digest-pinned trust exists to prevent. Every plugin in this
// repository carries its own connection handling for the same reason.
//
// What is actually different is what MariaDB has that MySQL does not: a Galera
// cluster's replication state, and MariaDB's own replica status. Those are the
// two capabilities below that plugins/mysql has no equivalent of, and they are
// the reason somebody running MariaDB wants this artifact rather than that one.
//
// Build it and put it on your $PATH as `rta-plugin-mariadb`:
//
//	cd plugins/mariadb && go build -o ~/.local/bin/rta-plugin-mariadb .
//
// State the connection once, in rta's config, under the artifact's own
// section — `rta explain mariadb.overview` prints the exact heading including
// the digest:
//
//	plugins:
//	  mariadb@<digest>:
//	    host: db.internal
//	    user: app
//	    database: app
//
// and export RTA_MARIADB_PASSWORD. Every capability here reaches off the box,
// so none of them — including mariadb.overview — appear on the automatic
// dashboard on their own (see cap's comment); add one explicitly once you
// have decided polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: mariadb.overview
package main

import (
	"context"
	"database/sql"

	_ "github.com/go-sql-driver/mysql"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// withDB is the shape every capability here has: connect, or return the
// classified error; run; close.
//
// The close is deferred here rather than left to each handler because there
// are nine of them and the one that forgot would leak a connection against
// somebody else's server, which is the kind of bug that only shows up under
// load somebody else is carrying.
func withDB(ctx context.Context, req plugin.Request, fn func(context.Context, *sql.DB) (view.View, error)) (view.View, error) {
	db, verr := connect(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer func() { _ = db.Close() }()
	return fn(ctx, db)
}

// cap builds a capability with the shared connection inputs appended, so no
// declaration here can forget one and no two can disagree about a default.
//
// Every capability here is NoPreview because every one reaches off the box:
// the automatic dashboard runs Read capabilities unasked, and a live database
// somebody else depends on is not something this plugin gets to decide, on
// its own, is fine to poll every few seconds. An operator who has looked at
// their own deployment and decided otherwise still can — dashboard.tiles
// accepts any capability regardless of NoPreview, because naming one in a
// config file is the asking.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(own, connFields()...)
	c.NoPreview = true
	return c
}

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "mariadb",
		Summary: "MariaDB: connection health, schema, rows, activity and cluster state",
		Version: "0.1.0",
		Capabilities: []plugin.Capability{
			overviewCapability(),
			statusCapability(),
			databaseListCapability(),
			tableListCapability(),
			schemaCapability(),
			queryCapability(),
			activityCapability(),
			// The two MariaDB has and MySQL does not.
			galeraCapability(),
			replicationCapability(),
		},
	}
}
