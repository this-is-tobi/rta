// Command rta-plugin-mysql talks to a MySQL server: what it is, what is in
// it, what shape that has, what is running, and — behind the write tier —
// the rows themselves.
//
// It speaks to MariaDB too, since MariaDB answers the same wire protocol and
// carries the same INFORMATION_SCHEMA. What it does not do is know anything
// MariaDB-specific: a Galera cluster's replication state has no equivalent
// here and is not guessed at. plugins/mariadb is the one that knows about
// that, and it is a separate artifact for the reason every plugin is —
// approving one binary is not approving another.
//
// Build it and put it on your $PATH as `rta-plugin-mysql`:
//
//	cd plugins/mysql && go build -o ~/.local/bin/rta-plugin-mysql .
//
// State the connection once, in rta's config, under the artifact's own
// section — `rta explain mysql.overview` prints the exact heading including
// the digest:
//
//	plugins:
//	  mysql@<digest>:
//	    host: db.internal
//	    user: app
//	    database: app
//
// and export RTA_MYSQL_PASSWORD. Every capability here reaches off the box,
// so none of them — including mysql.overview — appear on the automatic
// dashboard on their own (see cap's comment); add one explicitly once you
// have decided polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: mysql.overview
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
// are eight of them and the one that forgot would leak a connection against
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

// version is what this build claims to be, stamped by whatever built it:
// `-X main.version=`, which is the Makefile's flag and GoReleaser's own
// default. A build nobody stamped says "dev" rather than claiming a release
// number that was never cut — an index entry carries this verbatim, and a
// version is a fact about a release, not about the source it came from.
var version = "dev"

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "mysql",
		Summary: "MySQL: connection health, schema, rows and activity",
		Version: version,
		Capabilities: []plugin.Capability{
			overviewCapability(),
			statusCapability(),
			databaseListCapability(),
			tableListCapability(),
			schemaCapability(),
			queryCapability(),
			activityCapability(),
			dumpCapability(),
			restoreCapability(),
		},
	}
}
