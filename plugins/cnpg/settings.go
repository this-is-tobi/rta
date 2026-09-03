package main

import (
	"sort"
	"strings"
)

// What a cluster's own PostgreSQL configuration says about recovery and
// replication, and where a replica has actually come adrift.
//
// # What is not here, and why
//
// **Replication lag is not in the Cluster resource.** `kubectl cnpg status`
// reports it, and gets it by connecting to the instances and reading
// `pg_stat_replication` — which is the thing this plugin does not do, for the
// reason main.go states: reaching into pods is the half that stops working
// behind the proxies people put in front of clusters, and a read that works
// everywhere is the whole point of this plugin existing beside theirs. A
// column that looked like lag and was actually something else would be worse
// than no column, the same trap plugins/kube's `pvc.list` refuses when it
// declines to show a percentage it cannot compute.
//
// So what is offered instead is the two things the resource does carry, and
// both answer a real question:
//
//   - **Timeline divergence.** Every instance reports its own timeline, and
//     the cluster reports the one it believes in. An instance on a different
//     number has diverged — it is not behind, it is on another history — and
//     that is the failure a lag figure would not have told you about anyway.
//   - **The tolerance, and the floor.** `archive_timeout` bounds how old the
//     newest archived WAL segment can be, which is the recovery point
//     objective in the only place it is actually written down.
//     `wal_keep_size` and `max_slot_wal_keep_size` bound how far a replica may
//     fall behind before the primary stops keeping WAL for it, which is the
//     setting that decides whether "behind" becomes "must be rebuilt".

// walSettings are the parameters that decide what a backup can recover to and
// how far a replica may drift, in the order somebody reasons about them.
//
// A curated list rather than the whole map, which on a real cluster is two
// dozen entries most of which the operator sets for itself — log_directory,
// log_filename, shared_memory_type. A page that prints all of them is a page
// nobody reads twice; the rest are counted rather than hidden, so nothing is
// silently dropped.
var walSettings = []struct{ key, means string }{
	{"archive_mode", "WAL archiving"},
	{"archive_timeout", "at worst, this much data is unarchived — the recovery point floor"},
	{"wal_level", "what the WAL records, and so what can replicate from it"},
	{"wal_keep_size", "WAL kept for a replica with no slot"},
	{"max_slot_wal_keep_size", "WAL kept for a replica with one, before its slot is dropped"},
	{"max_replication_slots", "how many replicas and subscribers can hold one"},
}

// serverSettings are the capacity and durability parameters, the second thing
// somebody looks for and the first thing they ask about after an incident.
var serverSettings = []struct{ key, means string }{
	{"max_connections", "connections, including replicas and the operator's own"},
	{"shared_buffers", "the server's own page cache"},
	{"work_mem", "per sort or hash, per query — multiplied by concurrency"},
	{"maintenance_work_mem", "per vacuum, index build or restore"},
	{"synchronous_commit", "whether a commit waits for the WAL to be durable"},
	{"fsync", "whether the WAL is flushed at all"},
	{"full_page_writes", "torn-page protection"},
	{"max_worker_processes", "background workers"},
	{"max_parallel_workers", "of those, how many may run a query in parallel"},
}

// settingRow is one parameter and what it decides.
type settingRow struct {
	key, value, means string
}

// settings picks the listed parameters this cluster actually states.
//
// Only the ones it states. An absent parameter is at CloudNativePG's own
// default, which the CRD does not publish, so printing a number for it would
// be rta inventing a fact about somebody's database — and a wrong
// max_connections is exactly the fact somebody would size a connection pool
// against.
func (c cluster) settings(want []struct{ key, means string }) []settingRow {
	var out []settingRow
	for _, s := range want {
		if v := strings.TrimSpace(c.Spec.PostgreSQL.Parameters[s.key]); v != "" {
			out = append(out, settingRow{key: s.key, value: v, means: s.means})
		}
	}
	return out
}

// otherSettings counts the parameters this cluster sets that neither list
// names, so a curated page says how much it curated.
func (c cluster) otherSettings() int {
	named := map[string]bool{}
	for _, list := range [][]struct{ key, means string }{walSettings, serverSettings} {
		for _, s := range list {
			named[s.key] = true
		}
	}
	n := 0
	for key := range c.Spec.PostgreSQL.Parameters {
		if !named[key] {
			n++
		}
	}
	return n
}

// unstatedSettings names the listed parameters this cluster leaves to
// CloudNativePG, sorted.
//
// Worth naming rather than omitting: "max_connections is whatever the
// operator defaults to" is a different answer from "max_connections is 100",
// and only the first one is true here. An operator sizing a pool against the
// second would be sizing against a number rta made up.
func (c cluster) unstatedSettings(want []struct{ key, means string }) []string {
	var out []string
	for _, s := range want {
		if strings.TrimSpace(c.Spec.PostgreSQL.Parameters[s.key]) == "" {
			out = append(out, s.key)
		}
	}
	sort.Strings(out)
	return out
}

// divergedInstances lists the instances whose timeline is not the cluster's.
//
// **Divergence, not lag.** An instance one timeline behind has not fallen
// behind; it is on a different history, which happens after a promotion an
// instance did not follow, and no amount of waiting brings it back. The
// Cluster resource carries every instance's own timeLineID and its own, so
// this is derivable from the single read — which is the only reason it is
// here and lag is not.
func (c cluster) divergedInstances() []string {
	want := c.Status.TimelineID
	if want == 0 {
		return nil
	}
	var out []string
	for name, st := range c.Status.InstancesReportedState {
		if st.TimelineID != 0 && st.TimelineID != want {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// archivingLine is the recovery-point story in one row: whether WAL is being
// archived at all, and the worst-case age of what has been.
//
// **`archive_mode: on` is not on its own a recovery point, and reading it as
// one was wrong on the first real cluster this met.** CloudNativePG turns
// archiving on for every cluster it manages, so a cluster with no backup
// destination configured still reports `on` with an `archive_timeout` beside
// it — and quoting that timeout as the RPO tells an operator with no backups
// at all that they can recover to within five minutes. The archive command has
// nowhere to write, which is what the cluster's own LastBackupSucceeded
// condition is failing about.
//
// So the destination is part of the sentence. Empty when the cluster states
// nothing, because a cluster that sets neither parameter has CloudNativePG's
// default rather than a number this can quote.
func (c cluster) archivingLine() string {
	p := c.Spec.PostgreSQL.Parameters
	mode := strings.TrimSpace(p["archive_mode"])
	timeout := strings.TrimSpace(p["archive_timeout"])
	if mode == "" {
		return ""
	}
	if mode == "off" {
		return "off — nothing is shipping WAL, so a backup recovers only to its own instant"
	}
	if !c.backupConfigured() {
		return mode + ", but this cluster configures no backup — the WAL has nowhere to " +
			"go, so there is no recovery point at all"
	}
	if timeout == "" {
		return mode + ", with no archive_timeout stated — the recovery point is " +
			"CloudNativePG's default rather than one this cluster chose"
	}
	return mode + ", every " + timeout + " at worst — which is the recovery point objective"
}
