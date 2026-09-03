package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Backups: reading the objects the Cluster resource does not carry, and
// asking for one.
//
// The write is the first this plugin has ever had, so most of what follows is
// about the two things it must never do: choose where a backup goes, and
// create an object that cannot possibly succeed.

// recordingKubectl fakes kubectl and keeps every argv and stdin it was given,
// so a test can assert on what was sent as well as on what came back.
func recordingKubectl(t *testing.T, stdout string) (argv func() []string, stdin func() string) {
	t.Helper()
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	stdinFile := filepath.Join(dir, "stdin")
	body := filepath.Join(dir, "body.json")
	if err := os.WriteFile(body, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	// cat with no argument reads stdin, which is how a create is recorded and
	// a get records an empty file.
	fakeKubectl(t, `printf '%s\n' "$@" > `+argvFile+`
cat > `+stdinFile+`
cat `+body+`
`)
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return string(raw)
	}
	return func() []string {
			return strings.Split(strings.TrimSpace(read(argvFile)), "\n")
		}, func() string {
			return read(stdinFile)
		}
}

// aCluster is a Cluster document with backup configured the classic way.
//
// The barmanObjectStore stanza is here rather than a bare `backup: {}`
// because the bare version is not a cluster anything can back up, and a
// fixture shaped like one made every request in this file exercise the
// refusal path by accident. Its contents are irrelevant and stay empty: the
// plugin decodes this stanza for presence only.
func aCluster(name, namespace string, mutate ...func(map[string]any)) map[string]any {
	c := map[string]any{
		"metadata": map[string]any{"name": name, "namespace": namespace},
		"spec": map[string]any{
			"instances": 2,
			"backup": map[string]any{
				"retentionPolicy":   "30d",
				"barmanObjectStore": map[string]any{},
			},
		},
		"status": map[string]any{
			"phase": "Cluster in healthy state", "instances": 2, "readyInstances": 2,
			"currentPrimary": name + "-1", "targetPrimary": name + "-1",
		},
	}
	for _, m := range mutate {
		m(c)
	}
	return c
}

// alsoSnapshots adds the volumeSnapshot stanza, for the cases that ask for
// that method — a cluster configured only for an object store is refused, and
// correctly so.
func alsoSnapshots(c map[string]any) {
	backup := c["spec"].(map[string]any)["backup"].(map[string]any)
	backup["volumeSnapshot"] = map[string]any{}
}

// archivedByPlugin is the newer arrangement: a WAL-archiver plugin instead of
// `.spec.backup`, which the CRD says cannot both be present. The stanza is
// deleted rather than left beside the plugin, because a real cluster of this
// shape has none.
func archivedByPlugin(c map[string]any) {
	spec := c["spec"].(map[string]any)
	delete(spec, "backup")
	spec["plugins"] = []any{
		map[string]any{"name": "barman-cloud.cloudnative-pg.io", "isWALArchiver": true},
	}
}

func decodeCluster(t *testing.T, doc map[string]any) cluster {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var c cluster
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// A cluster whose backups run through a WAL-archiver plugin is backed up, and
// reading only `.spec.backup` said it was not.
//
// CloudNativePG is moving object-store backup out of that stanza and into
// `.spec.plugins`; the CRD states the two are exclusive ("This cannot be
// enabled if the .spec.backup.barmanObjectStore configuration is present").
// So the old check told every cluster on the newer arrangement that nothing
// backed it up, on the screen somebody opens to check exactly that.
func TestAClusterBackedUpByAPluginIsNotReportedAsUnprotected(t *testing.T) {
	plugged := aCluster("shop", "prod", func(c map[string]any) {
		spec := c["spec"].(map[string]any)
		delete(spec, "backup")
		spec["plugins"] = []any{
			map[string]any{"name": "barman-cloud.cloudnative-pg.io", "isWALArchiver": true},
		}
	})
	c := decodeCluster(t, plugged)

	if !c.backupConfigured() {
		t.Error("a cluster with a WAL-archiver plugin reads as having no backup configured")
	}
	if got := c.walArchiverPlugin(); got != "barman-cloud.cloudnative-pg.io" {
		t.Errorf("archiver plugin = %q", got)
	}
	for _, row := range problemTable(c).Rows {
		if row[0] == "backup" && strings.Contains(row[2], "not configured") {
			t.Errorf("the status page says %q about a cluster a plugin archives", row[2])
		}
	}
}

// An entry present with nothing said about it is enabled — the CRD's default
// — and reading absent as false would report a backed-up cluster as
// unprotected, which is the failure this whole check exists to prevent.
func TestAPluginEntrySaysNothingAboutEnabledMeansOn(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{"unset", nil, true},
		{"explicitly off", &off, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cluster{}
			c.Spec.Plugins = []clusterPlugin{
				{Name: "barman", IsWALArchiver: true, Enabled: tc.enabled},
			}
			if got := c.backupConfigured(); got != tc.want {
				t.Errorf("backupConfigured = %v, want %v", got, tc.want)
			}
		})
	}
}

// A cluster with neither stanza is still reported as unprotected, so the
// correction above did not turn the finding off.
func TestAClusterWithNeitherStanzaIsStillReportedAsUnprotected(t *testing.T) {
	bare := aCluster("shop", "prod", func(c map[string]any) {
		delete(c["spec"].(map[string]any), "backup")
	})
	c := decodeCluster(t, bare)
	if c.backupConfigured() {
		t.Fatal("a cluster with no backup stanza and no plugins reads as configured")
	}
	var found bool
	for _, row := range problemTable(c).Rows {
		if row[0] == "backup" && strings.Contains(row[2], "not configured") {
			found = true
		}
	}
	if !found {
		t.Error("nothing on the status page says this cluster is not backed up")
	}
}

// The refusal that only a live cluster could have shown was needed.
//
// CloudNativePG accepts a Backup for a cluster that configures none — checked
// against a running operator with `kubectl create --dry-run=server`, which
// admitted the object without complaint — and fails it asynchronously minutes
// later, somewhere nobody is watching. Creating it and reporting success
// would make the receipt a lie.
func TestABackupIsRefusedForAClusterThatConfiguresNone(t *testing.T) {
	bare := aCluster("shop", "prod", func(c map[string]any) {
		delete(c["spec"].(map[string]any), "backup")
	})
	argv, stdin := recordingKubectl(t, mustJSON(t, bare))

	_, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"}))
	verr := asViewError(t, err)
	if verr.Code != "cnpg.backup.unconfigured" {
		t.Fatalf("error code = %q, want cnpg.backup.unconfigured", verr.Code)
	}
	if !strings.Contains(verr.Hint, "plugins") {
		t.Errorf("the hint is %q, and does not mention the other way to configure one", verr.Hint)
	}
	// The read happened and the write did not.
	if got := strings.Join(argv(), " "); !strings.Contains(got, "get") {
		t.Errorf("the cluster was not read first: %q", got)
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("something was sent to the cluster anyway: %q", body)
	}
}

// The ordinary call states no method, no target and no online flag, and the
// document says so by leaving all three out. An empty string is a value CNPG
// has to interpret; an absent key is "you decide", which is the whole
// contract of this capability.
func TestAnOrdinaryRequestSendsNothingButTheClusterReference(t *testing.T) {
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))

	if _, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"})); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(stdin()), &sent); err != nil {
		t.Fatalf("what was sent is not JSON: %v\n%s", err, stdin())
	}
	spec, _ := sent["spec"].(map[string]any)
	if got := spec["cluster"].(map[string]any)["name"]; got != "shop" {
		t.Errorf("spec.cluster.name = %v", got)
	}
	for _, key := range []string{"method", "target", "online"} {
		if _, present := spec[key]; present {
			t.Errorf("spec.%s was sent as %v — an unstated override must be absent, "+
				"so the cluster's own configuration decides", key, spec[key])
		}
	}
	if sent["kind"] != "Backup" || sent["apiVersion"] != "postgresql.cnpg.io/v1" {
		t.Errorf("wrong object: %v %v", sent["apiVersion"], sent["kind"])
	}
}

// The object lands beside the cluster it names, taken from the cluster rta
// actually read rather than from the flag — which is the same answer whenever
// the flag was given, and the resolved one when it was not.
func TestTheObjectIsCreatedInTheClustersOwnNamespace(t *testing.T) {
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))
	if _, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop"})); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(stdin()), &sent); err != nil {
		t.Fatal(err)
	}
	meta := sent["metadata"].(map[string]any)
	if meta["namespace"] != "prod" {
		t.Errorf("namespace = %v, want the one the cluster was read from", meta["namespace"])
	}
	if !strings.HasPrefix(meta["name"].(string), "shop-rta-") {
		t.Errorf("name = %v, want something an operator can find again", meta["name"])
	}
	labels, _ := meta["labels"].(map[string]any)
	if labels[requestedBy] != "rta" {
		t.Errorf("labels = %v, want the object to say what asked for it", labels)
	}
}

// A dry run reads the cluster and writes nothing, and shows the exact
// document — "what would this send" being the only question a dry run of a
// create is asked.
func TestADryRunReadsTheClusterAndSendsNothing(t *testing.T) {
	argv, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))

	v, err := runBackupRequest(context.Background(),
		plugin.NewRequest(map[string]any{"cluster": "shop", "namespace": "prod"}, true, false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv(), " "), "create") {
		t.Errorf("a dry run reached for create: %v", argv())
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("a dry run sent %q", body)
	}
	text := renderPairs(t, v)
	if !strings.Contains(text, `"kind":"Backup"`) {
		t.Errorf("the dry run does not show the document it would send:\n%s", text)
	}
	if !strings.Contains(text, "nothing was sent") {
		t.Errorf("the dry run does not say it wrote nothing:\n%s", text)
	}
}

// Every receipt says where the backup goes, and every one of them says the
// cluster chose it. This is the property the whole capability rests on: there
// is no destination for a caller to point somewhere useful to them.
func TestNoReceiptEverClaimsRtaChoseTheDestination(t *testing.T) {
	for _, dry := range []bool{true, false} {
		recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))
		v, err := runBackupRequest(context.Background(),
			plugin.NewRequest(map[string]any{"cluster": "shop", "namespace": "prod"}, dry, false))
		if err != nil {
			t.Fatal(err)
		}
		text := renderPairs(t, v)
		if !strings.Contains(text, "this cluster's `.spec.backup` sends it") {
			t.Errorf("dry=%v: the receipt does not say the cluster chose the destination:\n%s",
				dry, text)
		}
		if !strings.Contains(text, "rta stated none") {
			t.Errorf("dry=%v: the receipt does not say rta named no method:\n%s", dry, text)
		}
	}
}

// An override the CRD's enum does not admit is refused by rta, with the list
// in hand — rather than by the API server, with a schema error naming a JSON
// path instead of a flag.
func TestAnOverrideOutsideTheCRDsEnumIsRefusedBeforeAnythingIsSent(t *testing.T) {
	for field, bad := range map[string]string{
		"method": "rsync",
		"target": "any-replica",
		"online": "yes",
	} {
		t.Run(field, func(t *testing.T) {
			_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))
			values := map[string]any{"cluster": "shop", "namespace": "prod", field: bad}
			_, err := runBackupRequest(context.Background(), req(values))
			verr := asViewError(t, err)
			if !strings.Contains(verr.Code, field) {
				t.Errorf("code = %q, want it to name %s", verr.Code, field)
			}
			if body := strings.TrimSpace(stdin()); body != "" {
				t.Errorf("the document was sent anyway: %q", body)
			}
		})
	}
}

// `online` without `--method volumeSnapshot` is refused here, because the API
// server refuses it there.
//
// CNPG's webhook enforces it — "Online parameter can be specified only if the
// backup method is volumeSnapshot" — and the CRD schema does not say so, so
// nothing about the document looks wrong until a real operator sees it. Found
// by putting what rta builds through one. `method` also carries a schema
// default of barmanObjectStore, which is why omitting it is choosing the
// method that forbids this rather than leaving the question open.
func TestOnlineWithoutAVolumeSnapshotIsRefusedBeforeTheAPIServerHasTo(t *testing.T) {
	// Every method rta offers except the one that permits it, plus the empty
	// spelling, which is the same as barmanObjectStore by CRD default.
	for _, method := range []string{"", "barmanObjectStore"} {
		t.Run("method="+method, func(t *testing.T) {
			_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))
			values := map[string]any{"cluster": "shop", "namespace": "prod", "online": "true"}
			if method != "" {
				values["method"] = method
			}
			_, err := runBackupRequest(context.Background(), req(values))
			if got := asViewError(t, err).Code; got != "cnpg.backup.online.unavailable" {
				t.Errorf("code = %q, want cnpg.backup.online.unavailable", got)
			}
			if body := strings.TrimSpace(stdin()); body != "" {
				t.Errorf("the document was sent anyway: %q", body)
			}
		})
	}

	// And with the method it belongs to, on a cluster set up for it, it goes
	// through.
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod", alsoSnapshots)))
	if _, err := runBackupRequest(context.Background(), req(map[string]any{
		"cluster": "shop", "namespace": "prod", "method": "volumeSnapshot", "online": "true",
	})); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(stdin()), &sent); err != nil {
		t.Fatal(err)
	}
	if got := sent["spec"].(map[string]any)["online"]; got != true {
		t.Errorf("online = %v with volumeSnapshot, want it sent", got)
	}
}

// An unstated method is barmanObjectStore, CloudNativePG's own fixed default,
// and not a preference read off the cluster.
//
// `spec.method` and `spec.target` sit next to each other in the CRD and read
// alike: one says "Defaults to: barmanObjectStore" and the other says "If
// empty, it defaults to cluster.spec.backup.target". Treating method like
// target meant a cluster set up only for volume snapshots would be sent a
// barmanObjectStore backup it cannot take — admitted by the API server, then
// failed minutes later, with rta's receipt already saying the cluster had
// chosen.
func TestAClusterThatCannotTakeTheDefaultMethodIsRefusedRatherThanSentOne(t *testing.T) {
	snapshotsOnly := aCluster("shop", "prod", func(c map[string]any) {
		backup := c["spec"].(map[string]any)["backup"].(map[string]any)
		delete(backup, "barmanObjectStore")
		backup["volumeSnapshot"] = map[string]any{}
	})
	_, stdin := recordingKubectl(t, mustJSON(t, snapshotsOnly))

	_, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"}))
	verr := asViewError(t, err)
	if verr.Code != "cnpg.backup.method.unconfigured" {
		t.Fatalf("code = %q, want cnpg.backup.method.unconfigured", verr.Code)
	}
	if !strings.Contains(verr.Hint, "--method volumeSnapshot") {
		t.Errorf("the hint is %q, and does not name the flag that fixes it", verr.Hint)
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("a backup nothing could take was sent anyway: %q", body)
	}
}

// The ordinary call — no --method — against the newer arrangement.
//
// This is the one the pre-flight was blind to, and it is the likeliest call
// there is. `canTake` used to pass every method on a cluster naming a
// WAL-archiver plugin, on the reasoning that rta cannot read that plugin's own
// configuration. But the method it was letting through was CloudNativePG's
// fixed default, barmanObjectStore, and a cluster of this shape has no
// `.spec.backup` at all — the CRD says the two cannot coexist. So the Backup
// was created, accepted, and failed minutes later where nobody looks, with
// rta's receipt already saying it had checked the cluster could take it.
func TestTheDefaultMethodIsRefusedForAClusterThatArchivesThroughAPlugin(t *testing.T) {
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod", archivedByPlugin)))

	_, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"}))

	verr := asViewError(t, err)
	if verr.Code != "cnpg.backup.method.unconfigured" {
		t.Fatalf("code = %q, want cnpg.backup.method.unconfigured", verr.Code)
	}
	if !strings.Contains(verr.Hint, "barman-cloud.cloudnative-pg.io") {
		t.Errorf("the hint is %q, and does not name what does archive this cluster", verr.Hint)
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("a backup the cluster cannot perform was sent anyway: %q", body)
	}
}

// The refusal names no flag, because there is no flag that works.
//
// A plugin-method Backup needs a `spec.pluginConfiguration` rta does not
// build — the operator's own webhook says so, put to a running one with
// `kubectl create --dry-run=server`: "spec.pluginConfiguration: Invalid value:
// null: cannot be empty when the backup method is plugin". So sending somebody
// to `--method plugin` would be a second dead end one round trip further on,
// which is the same failure as the first one with an extra step.
func TestThePluginArchivedRefusalDoesNotSendSomebodyToAFlagThatFailsToo(t *testing.T) {
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod", archivedByPlugin)))

	_, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"}))

	verr := asViewError(t, err)
	if strings.Contains(verr.Hint, "--method plugin") {
		t.Errorf("the hint sends them to a flag rta cannot fulfil: %q", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "pluginConfiguration") {
		t.Errorf("the hint is %q, and does not say what rta is missing", verr.Hint)
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("a backup the cluster cannot perform was sent anyway: %q", body)
	}
}

// `plugin` is not an offered method, so asking for it is refused by rta with
// the list in hand rather than by the API server with a schema error — which
// is what the three overrides carrying the CRD's enums are for.
func TestThePluginMethodIsNotOffered(t *testing.T) {
	if slices.Contains(backupMethods, "plugin") {
		t.Fatal("plugin is offered, and every document rta builds for it is incomplete")
	}
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))
	_, err := runBackupRequest(context.Background(), req(map[string]any{
		"cluster": "shop", "namespace": "prod", "method": "plugin",
	}))
	if verr := asViewError(t, err); verr.Code != "cnpg.backup.method.invalid" {
		t.Fatalf("code = %q, want the value refused by rta", verr.Code)
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("a document was sent for a method rta cannot complete: %q", body)
	}
}

// A method rta does not recognise is still the API server's call, which is the
// half of the generosity that was right: refusing a legitimate backup is worse
// than letting the server have the last word, which it has either way.
func TestAMethodRtaDoesNotKnowIsLeftToTheAPIServer(t *testing.T) {
	c := decodeCluster(t, aCluster("shop", "prod", archivedByPlugin))
	if !c.canTake("somethingNewInTheCRD") {
		t.Error("an unrecognised method was refused locally rather than by the server")
	}
	barman := decodeCluster(t, aCluster("shop", "prod"))
	if barman.canTake("volumeSnapshot") {
		t.Error("a snapshot was allowed on a cluster whose .spec.backup states none")
	}
}

// A `.spec.backup` naming no mechanism has a message of its own, and does not
// take the plugin down producing it.
//
// It passes backupConfigured — the stanza is there — and leaves
// configuredMethods empty, so the obvious wording ("this cluster configures
// %s, pass --method %s" off the first entry) indexes an empty slice. Found by
// a fixture that turned out to be shaped exactly like it.
func TestABackupStanzaNamingNoMechanismIsExplainedAndDoesNotPanic(t *testing.T) {
	hollow := aCluster("shop", "prod", func(c map[string]any) {
		c["spec"].(map[string]any)["backup"] = map[string]any{"retentionPolicy": "30d"}
	})
	recordingKubectl(t, mustJSON(t, hollow))

	_, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "shop", "namespace": "prod"}))
	verr := asViewError(t, err)
	if verr.Code != "cnpg.backup.method.unconfigured" {
		t.Fatalf("code = %q", verr.Code)
	}
	if !strings.Contains(verr.Hint, "names no mechanism") {
		t.Errorf("the hint is %q, and does not say the stanza is empty", verr.Hint)
	}
}

// A stated override reaches the document and is reported as an override, so
// the receipt never implies the cluster chose something rta chose.
func TestAStatedOverrideIsSentAndNamedAsOne(t *testing.T) {
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod", alsoSnapshots)))
	v, err := runBackupRequest(context.Background(), req(map[string]any{
		"cluster": "shop", "namespace": "prod",
		"method": "volumeSnapshot", "target": "prefer-standby", "online": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(stdin()), &sent); err != nil {
		t.Fatal(err)
	}
	spec := sent["spec"].(map[string]any)
	if spec["method"] != "volumeSnapshot" || spec["target"] != "prefer-standby" {
		t.Errorf("overrides did not reach the document: %v", spec)
	}
	if spec["online"] != false {
		t.Errorf("online = %v, want false", spec["online"])
	}
	// "stated" rather than "overridden", because the two fields are not both
	// overriding something: an unstated target really does defer to the
	// cluster, and an unstated method is a fixed CloudNativePG default that
	// the cluster has no say in. One word that is true of both.
	if text := renderPairs(t, v); !strings.Contains(text, "stated for this backup") {
		t.Errorf("the receipt does not mark what rta chose:\n%s", text)
	}
}

// The listing filters on the field the CRD declares as a printer column, and
// never claims a backup belongs to a cluster it does not name.
func TestTheListingFiltersOnTheClusterTheBackupNames(t *testing.T) {
	items := backupList{}
	for _, tc := range []struct{ name, cluster string }{
		{"shop-1", "shop"}, {"other-1", "other"}, {"shop-2", "shop"},
	} {
		var b backupObject
		b.Metadata.Name = tc.name
		b.Spec.Cluster.Name = tc.cluster
		items.Items = append(items.Items, b)
	}
	got := backupsFor(items.Items, "shop")
	if len(got) != 2 {
		t.Fatalf("got %d rows, want the two that name shop", len(got))
	}
	for _, b := range got {
		if b.Spec.Cluster.Name != "shop" {
			t.Errorf("%s belongs to %s", b.Metadata.Name, b.Spec.Cluster.Name)
		}
	}
	if len(backupsFor(items.Items, "")) != 3 {
		t.Error("no cluster named should list every backup in the namespace")
	}
}

// Newest first, because the question a backup listing is opened with is
// almost always about the most recent one.
func TestTheNewestBackupIsFirst(t *testing.T) {
	now := time.Now()
	var items []backupObject
	for i, when := range []time.Time{now.Add(-2 * time.Hour), now, now.Add(-time.Hour)} {
		var b backupObject
		b.Metadata.Name = string(rune('a' + i))
		b.Metadata.CreationTimestamp = when
		items = append(items, b)
	}
	if got := backupsFor(items, ""); got[0].Metadata.Name != "b" {
		t.Errorf("first row is %q, want the newest", got[0].Metadata.Name)
	}
}

// The credential fields are not decoded, so no view can print them by
// accident. Asserted on the decode rather than on a rendering, because a
// struct that cannot hold them is the guarantee; a view that happens not to
// show them is a habit.
func TestACredentialInABackupStatusIsNotDecodedAtAll(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"b1"},"spec":{"cluster":{"name":"shop"}},
	 "status":{"phase":"completed","destinationPath":"s3://bucket/path",
	  "s3Credentials":{"accessKeyId":{"name":"creds","key":"ACCESS_KEY_ID"}},
	  "azureCredentials":{"connectionString":{"name":"az","key":"conn"}},
	  "endpointCA":{"name":"ca-bundle","key":"ca.crt"}}}]}`
	var list backupList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	round, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"creds", "ACCESS_KEY_ID", "az", "ca-bundle", "Credentials"} {
		if strings.Contains(string(round), secret) {
			t.Errorf("%q survived the decode — the struct can hold a credential reference",
				secret)
		}
	}
	// What it does keep is what a listing is for.
	if got := list.Items[0].where(); !strings.Contains(got, "s3://bucket/path") {
		t.Errorf("destination = %q", got)
	}
}

// A failure is a paragraph of somebody else's stderr, so it gets a section
// rather than a ninth column.
func TestAFailedBackupsErrorIsShownWhereItCanBeRead(t *testing.T) {
	doc := map[string]any{"items": []any{
		map[string]any{
			"metadata": map[string]any{"name": "shop-1", "namespace": "prod"},
			"spec":     map[string]any{"cluster": map[string]any{"name": "shop"}},
			"status": map[string]any{
				"phase": "failed",
				"error": "while creating backup: exit status 2\ncontext: barman-cloud-backup",
			},
		},
	}}
	serves(t, doc)

	v, err := runBackupList(context.Background(), req(map[string]any{"cluster": "shop"}))
	if err != nil {
		t.Fatal(err)
	}
	sections, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("a failed backup rendered as %T, want sections carrying the error", v)
	}
	var sawError bool
	for _, s := range sections.Items {
		if s.Title != "What failed" {
			continue
		}
		sawError = true
		if got := s.View.(view.Table).Rows[0][2]; !strings.Contains(got, "exit status 2") {
			t.Errorf("the error row says %q", got)
		}
	}
	if !sawError {
		t.Error("the failure has no section of its own")
	}
}

// A backup in flight is not a problem, and colouring it warn would make every
// backup look wrong for as long as it works.
func TestABackupStillRunningIsNotGradedAsAFault(t *testing.T) {
	for phase, want := range map[string]string{
		"completed":                            "ok",
		"failed":                               "fail",
		"running":                              "info",
		"":                                     "info",
		"something the operator learned since": "info",
	} {
		var b backupObject
		b.Status.Phase = phase
		if got := b.status(); got != want {
			t.Errorf("phase %q graded %q, want %q", phase, got, want)
		}
	}
}

// The two empties send somebody to different places.
func TestAnEmptyListingSaysWhichKindOfEmptyItIs(t *testing.T) {
	serves(t, map[string]any{"items": []any{}})
	v, err := runBackupList(context.Background(), req(map[string]any{"cluster": "shop"}))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "rta cnpg status shop") {
		t.Errorf("a named cluster with no backups says %q, and points nowhere", body)
	}

	serves(t, map[string]any{"items": []any{}})
	v, err = runBackupList(context.Background(), req(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; strings.Contains(body, "rta cnpg status") {
		t.Errorf("a whole namespace with no backups is not a configuration question: %q", body)
	}
}

// A cluster name that kubectl would read as a flag is refused before the
// document is built, the same discipline every other capability here applies.
func TestABackupRequestRefusesAClusterNameThatWouldBeReadAsAFlag(t *testing.T) {
	_, stdin := recordingKubectl(t, mustJSON(t, aCluster("shop", "prod")))
	_, err := runBackupRequest(context.Background(),
		req(map[string]any{"cluster": "--kubeconfig=/tmp/mine"}))
	if asViewError(t, err).Code == "" {
		t.Fatal("a flag-shaped cluster name was accepted")
	}
	if body := strings.TrimSpace(stdin()); body != "" {
		t.Errorf("something was sent: %q", body)
	}
}

// Helpers.

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func asViewError(t *testing.T, err error) *view.Error {
	t.Helper()
	if err == nil {
		t.Fatal("no error")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("error is %T, want *view.Error", err)
	}
	return verr
}

func renderPairs(t *testing.T, v view.View) string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("view is %T, want key/value", v)
	}
	var b strings.Builder
	for _, p := range kv.Pairs {
		b.WriteString(p.Key + ": " + p.Value + "\n")
	}
	return b.String()
}
