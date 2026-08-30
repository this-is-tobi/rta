package main

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// statusRows renders a map as the two-column result SHOW GLOBAL STATUS
// returns, so a cluster state can be stated in the test and checked against
// the verdict it produces.
func statusRows(vars map[string]string) [][]driver.Value {
	out := make([][]driver.Value, 0, len(vars))
	for k, v := range vars {
		out = append(out, []driver.Value{[]byte(k), []byte(v)})
	}
	return out
}

func galeraOf(t *testing.T, vars map[string]string) view.KeyValue {
	t.Helper()
	db := fakeDB(t, []string{"Variable_name", "Value"}, statusRows(vars))
	v, err := galeraView(context.Background(), db, req(t, "mariadb.galera.status", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	return kv
}

func valueOf(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// **The failure this capability exists for.** A Galera node that has lost
// quorum still accepts connections and still answers SELECT — it just stops
// being part of the cluster. Nothing else about the server looks wrong while
// it is happening, so the verdict has to say it in words.
func TestANodeOutsideThePrimaryComponentIsCalledOut(t *testing.T) {
	kv := galeraOf(t, map[string]string{
		"wsrep_provider_name":       "Galera",
		"wsrep_cluster_status":      "non-Primary",
		"wsrep_cluster_size":        "1",
		"wsrep_ready":               "OFF",
		"wsrep_connected":           "ON",
		"wsrep_local_state_comment": "Initialized",
	})
	verdict := valueOf(kv, "verdict")
	if !strings.Contains(verdict, "SPLIT BRAIN RISK") {
		t.Errorf("verdict = %q — a node outside the primary component must not read as merely degraded", verdict)
	}
	if !strings.Contains(verdict, "must not be written to") {
		t.Errorf("verdict does not say what to do about it: %q", verdict)
	}
}

// The combination that gets misread at three in the morning: connected, ready,
// and not caught up. Every individual row looks fine.
func TestANodeThatIsUpButNotSyncedIsNotCalledHealthy(t *testing.T) {
	kv := galeraOf(t, map[string]string{
		"wsrep_provider_name":       "Galera",
		"wsrep_cluster_status":      "Primary",
		"wsrep_cluster_size":        "3",
		"wsrep_ready":               "ON",
		"wsrep_connected":           "ON",
		"wsrep_local_state_comment": "Donor/Desynced",
	})
	verdict := valueOf(kv, "verdict")
	if strings.HasPrefix(verdict, "healthy") {
		t.Errorf("verdict = %q — a desynced node is not serving current data", verdict)
	}
	if !strings.Contains(verdict, "Donor/Desynced") {
		t.Errorf("verdict does not name the state: %q", verdict)
	}
}

func TestAHealthyNodeIsCalledHealthy(t *testing.T) {
	kv := galeraOf(t, map[string]string{
		"wsrep_provider_name":       "Galera",
		"wsrep_cluster_status":      "Primary",
		"wsrep_cluster_size":        "3",
		"wsrep_ready":               "ON",
		"wsrep_connected":           "ON",
		"wsrep_local_state_comment": "Synced",
	})
	if v := valueOf(kv, "verdict"); !strings.HasPrefix(v, "healthy") {
		t.Errorf("verdict = %q, want healthy", v)
	}
	if v := valueOf(kv, "cluster size"); v != "3" {
		t.Errorf("cluster size = %q, want 3", v)
	}
}

// A standalone MariaDB is a normal thing to run. Returning an empty table for
// one would read exactly like a cluster that has fallen apart.
func TestAServerWithoutGaleraSaysSoRatherThanLookingBroken(t *testing.T) {
	for _, vars := range []map[string]string{
		{},                              // no wsrep variables at all
		{"wsrep_provider_name": "none"}, // compiled in, not configured
		{"wsrep_provider_name": ""},     // present and empty
		{"wsrep_cluster_size": "0"},     // wsrep variables, no provider
	} {
		kv := galeraOf(t, vars)
		if v := valueOf(kv, "clustered"); v != "no" {
			t.Errorf("vars %v: clustered = %q, want no", vars, v)
		}
		if !strings.Contains(valueOf(kv, "note"), "not a broken cluster") {
			t.Errorf("vars %v: does not distinguish standalone from broken", vars)
		}
	}
}

func replicationOf(t *testing.T, columns []string, row []driver.Value) view.KeyValue {
	t.Helper()
	var rows [][]driver.Value
	if row != nil {
		rows = [][]driver.Value{row}
	}
	db := fakeDB(t, columns, rows)
	v, err := replicationView(context.Background(), db, req(t, "mariadb.replication.status", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	return v.(view.KeyValue)
}

// **The trap this capability exists for.** Seconds_Behind_Master reads 0 both
// when a replica is caught up and when it has stopped receiving anything at
// all, so the lag figure on its own is not an answer.
func TestAStoppedReplicaIsNotReportedAsCaughtUp(t *testing.T) {
	kv := replicationOf(t,
		[]string{"Master_Host", "Slave_IO_Running", "Slave_SQL_Running", "Seconds_Behind_Master", "Last_Error"},
		[]driver.Value{[]byte("primary.internal"), []byte("No"), []byte("No"), nil, []byte("could not connect")})

	verdict := valueOf(kv, "verdict")
	if !strings.Contains(verdict, "STOPPED") {
		t.Errorf("verdict = %q — a stopped replica must not read as healthy", verdict)
	}
	if !strings.Contains(verdict, "lag figure means nothing") {
		t.Errorf("verdict does not warn that the lag is meaningless: %q", verdict)
	}
	if lag := valueOf(kv, "lag"); !strings.Contains(lag, "unknown") {
		t.Errorf("NULL lag rendered as %q — indistinguishable from caught up", lag)
	}
	if valueOf(kv, "last error") != "could not connect" {
		t.Errorf("the error was dropped: %q", valueOf(kv, "last error"))
	}
}

// MariaDB renamed these columns in 10.5 and kept the old spelling as a
// deprecated alias, so both are in the wild. Reading only one is how this
// silently reports nothing on half the versions people run.
func TestBothColumnSpellingsAreRead(t *testing.T) {
	modern := replicationOf(t,
		[]string{"Source_Host", "Replica_IO_Running", "Replica_SQL_Running", "Seconds_Behind_Source"},
		[]driver.Value{[]byte("primary.internal"), []byte("Yes"), []byte("Yes"), []byte("0")})
	if valueOf(modern, "source") != "primary.internal" {
		t.Errorf("10.5+ column names were not read: %+v", modern.Pairs)
	}
	if !strings.HasPrefix(valueOf(modern, "verdict"), "healthy") {
		t.Errorf("verdict = %q, want healthy", valueOf(modern, "verdict"))
	}

	legacy := replicationOf(t,
		[]string{"Master_Host", "Slave_IO_Running", "Slave_SQL_Running", "Seconds_Behind_Master"},
		[]driver.Value{[]byte("primary.internal"), []byte("Yes"), []byte("Yes"), []byte("0")})
	if valueOf(legacy, "source") != "primary.internal" {
		t.Errorf("pre-10.5 column names were not read: %+v", legacy.Pairs)
	}

	// Every column that feeds the verdict, not only the one that names the
	// source. Reading source_host from both spellings while reading the thread
	// states from one leaves a pre-10.5 replica reporting a verdict built from
	// two empty strings — which is "STOPPED" for a replica that is running.
	for _, kv := range []view.KeyValue{modern, legacy} {
		if v := valueOf(kv, "io thread"); v != "Yes" {
			t.Errorf("io thread = %q, want Yes — a spelling was missed", v)
		}
		if v := valueOf(kv, "sql thread"); v != "Yes" {
			t.Errorf("sql thread = %q, want Yes — a spelling was missed", v)
		}
		if v := valueOf(kv, "verdict"); !strings.HasPrefix(v, "healthy") {
			t.Errorf("verdict = %q, want healthy — a running replica read as stopped", v)
		}
	}
}

// A replica that is running and an hour behind is a different problem from one
// that has stopped, and both are different from healthy.
func TestALaggingReplicaIsDistinguishedFromAStoppedOne(t *testing.T) {
	kv := replicationOf(t,
		[]string{"Source_Host", "Replica_IO_Running", "Replica_SQL_Running", "Seconds_Behind_Source"},
		[]driver.Value{[]byte("primary.internal"), []byte("Yes"), []byte("Yes"), []byte("3600")})
	verdict := valueOf(kv, "verdict")
	if !strings.Contains(verdict, "BEHIND") {
		t.Errorf("verdict = %q, want BEHIND", verdict)
	}
	if strings.Contains(verdict, "STOPPED") {
		t.Errorf("a running replica was reported as stopped: %q", verdict)
	}
	if valueOf(kv, "lag") != "3600s" {
		t.Errorf("lag = %q, want 3600s", valueOf(kv, "lag"))
	}
}

// A primary returns an empty result here, and that is not a failure.
func TestAPrimarySaysSoRatherThanLookingBroken(t *testing.T) {
	kv := replicationOf(t, []string{"Master_Host", "Slave_IO_Running"}, nil)
	if v := valueOf(kv, "replica"); v != "no" {
		t.Errorf("replica = %q, want no", v)
	}
	if !strings.Contains(valueOf(kv, "note"), "not a broken replica") {
		t.Errorf("does not distinguish a primary from a broken replica: %+v", kv.Pairs)
	}
}
