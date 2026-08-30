package main

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The two capabilities MariaDB has and MySQL does not, and the reason somebody
// running MariaDB wants this artifact rather than plugins/mysql.
//
// Both answer the same underlying question — is this node actually part of a
// working cluster, or is it quietly serving stale data on its own — and both
// are reads: every value here is a number the server publishes about itself,
// not a value anybody stored in it.

func galeraCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "mariadb.galera.status",
		Summary:    "Galera cluster state: size, health, and whether this node is really in it",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "A Galera node that has lost quorum still accepts connections and still " +
			"answers SELECT — it just stops being part of the cluster. That is the failure this " +
			"exists for, because nothing else about the server looks wrong while it is happening.\n\n" +
			"Reports cluster size, the node's own state, whether it is receiving writes, and how " +
			"much flow control is being applied. Every value comes from the server's own wsrep " +
			"status variables — numbers it publishes about itself, never a value anybody stored, " +
			"which is what keeps this in the read tier.\n\n" +
			"Says so plainly when the server is not clustered at all, rather than returning an " +
			"empty table that reads like a broken cluster.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
				return galeraView(ctx, db, req)
			})
		},
	})
}

// wsrepVars reads the handful of wsrep_% status variables that answer "is this
// node healthy in a healthy cluster". Read as a map because the interesting
// ones are scattered across a list of about seventy, and naming them here
// keeps the query cheap on a busy node.
func wsrepVars(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SHOW GLOBAL STATUS LIKE 'wsrep_%'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = value
	}
	return out, rows.Err()
}

func galeraView(ctx context.Context, db *sql.DB, req plugin.Request) (view.View, error) {
	vars, err := wsrepVars(ctx, db)
	if err != nil {
		return nil, classify(err, req)
	}

	// wsrep_provider_name is absent on a server built without Galera and reads
	// as "none" on one that has it compiled in but not configured. Both mean
	// the same thing to somebody asking this question, and neither is an
	// error: a standalone MariaDB is a normal thing to run.
	provider := vars["wsrep_provider_name"]
	if provider == "" || strings.EqualFold(provider, "none") {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "clustered", Value: "no"},
			{Key: "note", Value: "this server is not running Galera — a standalone MariaDB, not a broken cluster"},
		}}, nil
	}

	pairs := []view.Pair{
		{Key: "clustered", Value: "yes"},
		{Key: "provider", Value: provider},
		{Key: "cluster size", Value: vars["wsrep_cluster_size"]},
		// The three that actually say whether this node is usable.
		//
		// cluster_status is Primary on a node that holds quorum and
		// non-Primary on one that has lost it. local_state_comment is Synced
		// when the node is caught up; Donor/Desynced and Joining are both
		// states where it is up and not serving current data. connected and
		// ready are the node's own summary of both.
		{Key: "cluster status", Value: vars["wsrep_cluster_status"]},
		{Key: "node state", Value: vars["wsrep_local_state_comment"]},
		{Key: "connected", Value: vars["wsrep_connected"]},
		{Key: "ready", Value: vars["wsrep_ready"]},
	}
	if v := vars["wsrep_local_send_queue_avg"]; v != "" {
		// Flow control is the cluster telling a slow node to stop sending. A
		// non-zero average here is the early warning for a node that is about
		// to be evicted, and it is the number nobody looks at until afterwards.
		pairs = append(pairs, view.Pair{Key: "send queue avg", Value: v})
	}
	if v := vars["wsrep_flow_control_paused"]; v != "" {
		pairs = append(pairs, view.Pair{Key: "flow control paused", Value: v})
	}
	if v := vars["wsrep_local_recv_queue_avg"]; v != "" {
		pairs = append(pairs, view.Pair{Key: "recv queue avg", Value: v})
	}

	// The verdict, stated rather than left to be assembled from four rows. A
	// node can be connected, ready and still non-Primary, and that combination
	// is exactly the one somebody misreads at three in the morning.
	pairs = append(pairs, view.Pair{Key: "verdict", Value: galeraVerdict(vars)})
	return view.KeyValue{Pairs: pairs}, nil
}

func galeraVerdict(vars map[string]string) string {
	primary := strings.EqualFold(vars["wsrep_cluster_status"], "Primary")
	ready := strings.EqualFold(vars["wsrep_ready"], "ON")
	synced := strings.EqualFold(vars["wsrep_local_state_comment"], "Synced")
	switch {
	case primary && ready && synced:
		return "healthy — in the primary component and caught up"
	case !primary:
		return "SPLIT BRAIN RISK — this node is not in the primary component and must not be written to"
	case !ready:
		return "NOT READY — the node is up but refusing cluster traffic"
	default:
		return "in the primary component but not Synced (" +
			vars["wsrep_local_state_comment"] + ") — up, and not serving current data"
	}
}

func replicationCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "mariadb.replication.status",
		Summary:    "Whether this replica is running, and how far behind it is",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Replica threads, error state, and seconds behind the primary.\n\n" +
			"The lag figure is the one worth understanding: it measures the replica's own " +
			"progress through the relay log, so it reads 0 both when a replica is caught up " +
			"and when it has stopped receiving anything at all. The thread states beside it " +
			"are what tell those two apart, which is why they are in the same answer.\n\n" +
			"Says so plainly when the server is not a replica, rather than returning an empty " +
			"table that reads like a broken one.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return withDB(ctx, req, func(ctx context.Context, db *sql.DB) (view.View, error) {
				return replicationView(ctx, db, req)
			})
		},
	})
}

// replicaStatusStatements are tried in order. MariaDB renamed this in 10.5 and
// keeps the old spelling as an alias, but the alias is deprecated and a future
// release may drop it — while anything older than 10.5 only has the old one.
// Trying both is how this keeps answering across the versions people actually
// run, rather than picking one and being wrong on half of them.
var replicaStatusStatements = []string{
	"SHOW REPLICA STATUS",
	"SHOW SLAVE STATUS",
}

func replicationView(ctx context.Context, db *sql.DB, req plugin.Request) (view.View, error) {
	var rows *sql.Rows
	var lastErr error
	for _, stmt := range replicaStatusStatements {
		r, err := db.QueryContext(ctx, stmt)
		if err == nil {
			rows = r
			break
		}
		lastErr = err
	}
	if rows == nil {
		return nil, classify(lastErr, req)
	}
	defer func() { _ = rows.Close() }()

	names, err := rows.Columns()
	if err != nil {
		return nil, classify(err, req)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, classify(err, req)
		}
		// An empty result is what a primary returns, and it is not a failure.
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "replica", Value: "no"},
			{Key: "note", Value: "this server is not replicating from anything — a primary or a standalone, not a broken replica"},
		}}, nil
	}

	scan := make([]any, len(names))
	holders := make([]any, len(names))
	for i := range scan {
		holders[i] = &scan[i]
	}
	if err := rows.Scan(holders...); err != nil {
		return nil, classify(err, req)
	}
	status := map[string]string{}
	for i, n := range names {
		status[strings.ToLower(n)] = cell(scan[i])
	}

	// Both spellings, for the same reason both statements are tried.
	pick := func(names ...string) string {
		for _, n := range names {
			if v, ok := status[n]; ok && v != "" {
				return v
			}
		}
		return ""
	}
	ioRunning := pick("replica_io_running", "slave_io_running")
	sqlRunning := pick("replica_sql_running", "slave_sql_running")
	lag := pick("seconds_behind_master", "seconds_behind_source")

	pairs := []view.Pair{
		{Key: "replica", Value: "yes"},
		{Key: "source", Value: pick("source_host", "master_host")},
		{Key: "io thread", Value: ioRunning},
		{Key: "sql thread", Value: sqlRunning},
		{Key: "lag", Value: lagText(lag)},
	}
	if e := pick("last_error", "last_sql_error"); e != "" {
		pairs = append(pairs, view.Pair{Key: "last error", Value: e})
	}
	if e := pick("last_io_error"); e != "" {
		pairs = append(pairs, view.Pair{Key: "last io error", Value: e})
	}
	pairs = append(pairs, view.Pair{Key: "verdict", Value: replicaVerdict(ioRunning, sqlRunning, lag)})
	return view.KeyValue{Pairs: pairs}, nil
}

// lagText spells out the ambiguity rather than printing a bare 0. NULL means
// not replicating, which is a different fact from being caught up, and the two
// are indistinguishable once both are rendered as a number.
func lagText(v string) string {
	switch {
	case v == "", strings.EqualFold(v, "NULL"):
		return "unknown — the replica is not currently connected"
	case v == "0":
		return "0s"
	default:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return strconv.FormatInt(n, 10) + "s"
		}
		return v
	}
}

func replicaVerdict(io, sqlThread, lag string) string {
	running := strings.EqualFold(io, "Yes") && strings.EqualFold(sqlThread, "Yes")
	if !running {
		return "STOPPED — this replica is not applying changes, and its lag figure means nothing"
	}
	if n, err := strconv.ParseInt(lag, 10, 64); err == nil && n > 60 {
		return "BEHIND — running, and more than a minute behind the source"
	}
	return "healthy — both threads running"
}
