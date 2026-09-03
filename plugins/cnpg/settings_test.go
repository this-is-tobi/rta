package main

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The settings a cluster states, the volumes it got, and the one desync
// question a single read of the Cluster resource can actually answer.

func withParams(c *cluster, kv map[string]string) {
	c.Spec.PostgreSQL.Parameters = kv
}

// `archive_mode: on` is not a recovery point on its own, and reading it as one
// was wrong on the first real cluster this met.
//
// CloudNativePG turns archiving on for every cluster it manages, so one with
// no backup destination still reports `on` with an `archive_timeout` beside
// it. Quoting that timeout as the RPO tells an operator with no backups at all
// that they can recover to within five minutes — the archive command has
// nowhere to write, which is what that cluster's own LastBackupSucceeded
// condition was failing about.
func TestArchivingOnWithNoDestinationIsNotAFiveMinuteRecoveryPoint(t *testing.T) {
	c := healthy()
	withParams(&c, map[string]string{"archive_mode": "on", "archive_timeout": "5min"})
	c.Spec.Backup = nil

	got := c.archivingLine()
	if strings.Contains(got, "recovery point objective") {
		t.Errorf("a cluster with nowhere to archive to reports %q", got)
	}
	if !strings.Contains(got, "nowhere to go") {
		t.Errorf("the row says %q, and does not say the WAL has no destination", got)
	}

	// With a destination, the timeout is the answer it always was.
	c.Spec.Backup = &clusterBackup{BarmanObjectStore: &struct{}{}}
	if got := c.archivingLine(); !strings.Contains(got, "5min") ||
		!strings.Contains(got, "recovery point objective") {
		t.Errorf("with a destination the row says %q", got)
	}
}

// Archiving turned off while a backup is configured is its own finding: the
// base backup is fine and recovers to its own instant and no further.
func TestArchivingOffWithABackupConfiguredIsReported(t *testing.T) {
	c := healthy()
	c.Spec.Backup = &clusterBackup{BarmanObjectStore: &struct{}{}}
	withParams(&c, map[string]string{"archive_mode": "off"})

	if got := problemsIn(t, c)["WAL archiving"]; !strings.Contains(got, "no further") {
		t.Errorf("WAL archiving finding = %q", got)
	}
}

// A parameter the cluster does not state is at CloudNativePG's default, which
// the resource does not publish — so it is named as unstated rather than
// filled in with a number.
//
// The number would be the problem: somebody sizing a connection pool against
// a max_connections rta made up is sizing against nothing.
func TestAnUnstatedParameterIsNamedRatherThanGuessed(t *testing.T) {
	c := healthy()
	withParams(&c, map[string]string{"max_connections": "200"})

	rows := c.settings(serverSettings)
	if len(rows) != 1 || rows[0].key != "max_connections" || rows[0].value != "200" {
		t.Fatalf("stated settings = %+v", rows)
	}
	unstated := c.unstatedSettings(serverSettings)
	for _, want := range []string{"shared_buffers", "work_mem", "fsync"} {
		if !containsStr(unstated, want) {
			t.Errorf("%s is neither stated nor named as unstated: %v", want, unstated)
		}
	}
	if containsStr(unstated, "max_connections") {
		t.Error("a stated parameter is also listed as unstated")
	}

	table := settingsTable(c, serverSettings)
	joined := ""
	for _, row := range table.Rows {
		joined += strings.Join(row, " | ") + "\n"
	}
	if !strings.Contains(joined, "not stated") {
		t.Errorf("the table hides what the cluster left to the operator:\n%s", joined)
	}
	// And it must not print a value for one of them.
	if strings.Contains(joined, "shared_buffers | ") &&
		!strings.Contains(joined, "shared_buffers,") {
		t.Errorf("an unstated parameter was given a value:\n%s", joined)
	}
}

// A cluster that states none of a list gets no section at all, rather than a
// heading over one row saying everything is unstated.
func TestAClusterThatStatesNothingGetsNoSettingsSection(t *testing.T) {
	c := healthy()
	if got := settingsTable(c, serverSettings); len(got.Rows) != 0 {
		t.Errorf("a cluster with no parameters produced %d rows", len(got.Rows))
	}
	for _, s := range statusView(c).(view.Sections).Items {
		if s.Title == "Server settings" {
			t.Error("an empty settings section was drawn")
		}
	}
}

// The counted remainder, so a curated page says how much it curated.
func TestTheParametersNeitherListNamesAreCountedNotDropped(t *testing.T) {
	c := healthy()
	withParams(&c, map[string]string{
		"archive_mode":       "on",
		"log_directory":      "/controller/log",
		"log_filename":       "postgres",
		"shared_memory_type": "mmap",
	})
	if got := c.otherSettings(); got != 3 {
		t.Errorf("otherSettings = %d, want the three neither list names", got)
	}
}

// Divergence, not lag.
//
// An instance on another timeline is not behind and will not catch up: it
// followed a history the cluster abandoned, which is what a promotion an
// instance missed leaves behind. Live replication lag is not in this resource
// at all — it needs a connection to the instance, which this plugin does not
// make — so this is the desync question the single read can answer, and it
// happens to be the one that does not resolve itself.
func TestAnInstanceOnAnotherTimelineIsReportedAsDiverged(t *testing.T) {
	c := healthy()
	st := c.Status.InstancesReportedState["app-db-3"]
	st.TimelineID = 2
	c.Status.InstancesReportedState["app-db-3"] = st

	if got := c.divergedInstances(); len(got) != 1 || got[0] != "app-db-3" {
		t.Fatalf("diverged = %v, want just app-db-3", got)
	}
	finding := problemsIn(t, c)["timeline"]
	if !strings.Contains(finding, "app-db-3") || !strings.Contains(finding, "does not catch up") {
		t.Errorf("timeline finding = %q", finding)
	}
}

// An instance that has not reported a timeline yet is not diverged. Zero is
// "has not said", and grading it as a failure would flag every cluster during
// the seconds after a new instance joins.
func TestAnInstanceThatHasNotReportedATimelineIsNotDiverged(t *testing.T) {
	c := healthy()
	st := c.Status.InstancesReportedState["app-db-3"]
	st.TimelineID = 0
	c.Status.InstancesReportedState["app-db-3"] = st

	if got := c.divergedInstances(); len(got) != 0 {
		t.Errorf("diverged = %v, want none", got)
	}
	if got := problemsIn(t, c)["timeline"]; got != "" {
		t.Errorf("a silent instance was reported as %q", got)
	}
}

// Volumes: what the cluster got, as opposed to what its spec asked for.

func aPVC(name, instance, role, requested, capacity, phase string) pvc {
	var p pvc
	p.Metadata.Name = name
	p.Metadata.Namespace = "prod"
	p.Metadata.Labels = map[string]string{
		pvcSelector: "shop", pvcInstanceLabel: instance, pvcRoleLabel: role,
	}
	p.Spec.StorageClassName = "longhorn"
	p.Spec.Resources.Requests = map[string]string{"storage": requested}
	p.Status.Phase = phase
	if capacity != "" {
		p.Status.Capacity = map[string]string{"storage": capacity}
	}
	return p
}

func servesPVCs(t *testing.T, items ...pvc) {
	t.Helper()
	serves(t, pvcList{Items: items})
}

// A claim with nothing behind it is a database that cannot start, and the spec
// this came from says 7Gi either way.
func TestAnUnboundVolumeIsAFailureAndTheSpecCannotSaySo(t *testing.T) {
	servesPVCs(t,
		aPVC("shop-1", "shop-1", pvcRoleData, "7Gi", "7Gi", "Bound"),
		aPVC("shop-2", "shop-2", pvcRoleData, "7Gi", "", "Pending"),
	)
	v, err := runStorage(context.Background(), req(map[string]any{"cluster": "shop"}))
	if err != nil {
		t.Fatal(err)
	}
	sections, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("an unbound volume rendered as %T, with no problems section", v)
	}
	var detail string
	for _, s := range sections.Items {
		if s.Title == "Needs attention" {
			detail = s.View.(view.Table).Rows[0][2]
		}
	}
	if !strings.Contains(detail, "cannot start") {
		t.Errorf("the finding says %q", detail)
	}
}

// An expansion that did not finish: the spec has the new size and the status
// still has the old one.
func TestAVolumeSmallerThanItWasAskedForIsReported(t *testing.T) {
	servesPVCs(t, aPVC("shop-1", "shop-1", pvcRoleData, "20Gi", "7Gi", "Bound"))
	v, err := runStorage(context.Background(), req(map[string]any{"cluster": "shop"}))
	if err != nil {
		t.Fatal(err)
	}
	sections := v.(view.Sections)
	var detail string
	for _, s := range sections.Items {
		if s.Title == "Needs attention" {
			detail = s.View.(view.Table).Rows[0][2]
		}
	}
	if !strings.Contains(detail, "20Gi") || !strings.Contains(detail, "7Gi") {
		t.Errorf("the finding says %q, and does not print both numbers", detail)
	}
}

// Healthy volumes produce a plain table, not a problems section over an empty
// one — the conditional-section doctrine the rest of this plugin follows.
func TestHealthyVolumesGetNoProblemsSection(t *testing.T) {
	servesPVCs(t,
		aPVC("shop-1", "shop-1", pvcRoleData, "7Gi", "7Gi", "Bound"),
		aPVC("shop-1-wal", "shop-1", pvcRoleWriteAhead, "3Gi", "3Gi", "Bound"),
	)
	v, err := runStorage(context.Background(), req(map[string]any{"cluster": "shop"}))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("healthy volumes rendered as %T", v)
	}
	if len(table.Rows) != 2 {
		t.Errorf("%d rows, want two", len(table.Rows))
	}
}

// Each instance's pair sits together, data before WAL, so the pair always
// reads in the same order.
func TestVolumesAreOrderedByInstanceThenDataBeforeWAL(t *testing.T) {
	got := pvcsFor([]pvc{
		aPVC("b-wal", "shop-2", pvcRoleWriteAhead, "3Gi", "3Gi", "Bound"),
		aPVC("a-wal", "shop-1", pvcRoleWriteAhead, "3Gi", "3Gi", "Bound"),
		aPVC("b", "shop-2", pvcRoleData, "7Gi", "7Gi", "Bound"),
		aPVC("a", "shop-1", pvcRoleData, "7Gi", "7Gi", "Bound"),
	})
	var order []string
	for _, p := range got {
		order = append(order, p.Metadata.Name)
	}
	want := []string{"a", "a-wal", "b", "b-wal"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// The volumes are selected by CloudNativePG's own label, at the API server,
// so a namespace's other storage is never read in order to be discarded.
func TestTheVolumesAreSelectedByLabelAtTheAPIServer(t *testing.T) {
	argv, _ := recordingKubectl(t, mustJSON(t, pvcList{}))
	if _, err := runStorage(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"})); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv(), " ")
	if !strings.Contains(got, "cnpg.io/cluster=shop") {
		t.Errorf("the call did not select by label: %q", got)
	}
	if !strings.Contains(got, "persistentvolumeclaims") {
		t.Errorf("the call did not ask for claims: %q", got)
	}
}

// No percentage anywhere, ever.
//
// How full a volume is comes from the kubelet's stats endpoint through the
// node proxy — a different mechanism and a different permission, and one that
// does not survive every proxy people put in front of a cluster, which is the
// property this whole plugin is built around. A column that looked like usage
// and was capacity would be worse than no column.
func TestTheVolumeTableNeverShowsAPercentage(t *testing.T) {
	servesPVCs(t, aPVC("shop-1", "shop-1", pvcRoleData, "7Gi", "7Gi", "Bound"))
	v, err := runStorage(context.Background(), req(map[string]any{"cluster": "shop"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range v.(view.Table).Columns {
		if strings.Contains(col.Name, "%") ||
			strings.Contains(strings.ToLower(col.Name), "used") ||
			strings.Contains(strings.ToLower(col.Name), "free") {
			t.Errorf("column %q promises usage this cannot compute", col.Name)
		}
	}
	if desc := storageCapability().Description; !strings.Contains(desc, "does not report how full") {
		t.Error("the capability does not say what it cannot answer")
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
