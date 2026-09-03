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
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The suite this plugin is held to as a stranger's plugin, before anything
// specific to CloudNativePG.
func TestPluginPassesTheConformanceSuite(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(func(string) map[string]map[string]any {
		return map[string]map[string]any{
			// A name nothing is called, so the dry-run rule drives the
			// capability without needing a cluster to exist. There is no
			// cluster in a test anyway: kubectl is not on the path this
			// resolves, and the failure it produces is the one being checked.
			"cnpg.status": {"name": "absent"},
			// The same, for the one capability that writes — and here the
			// dry run is worth driving for its own sake rather than only to
			// satisfy the rule: it reads the cluster before it decides
			// anything, so a name nothing answers for exercises the refusal
			// path rather than the create path.
			"cnpg.backup.request": {"cluster": "absent"},
		}
	}))
}

// fakeKubectl points the plugin at a script instead of a cluster.
func fakeKubectl(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := kubectlBin
	kubectlBin = path
	t.Cleanup(func() { kubectlBin = saved })
}

// serves makes a kubectl that prints one JSON document and exits.
func serves(t *testing.T, doc any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fakeKubectl(t, "cat "+path+"\n")
}

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

// healthy is a three-instance cluster with nothing wrong with it, which is
// the baseline every finding below is a departure from.
func healthy() cluster {
	var c cluster
	c.Metadata.Name, c.Metadata.Namespace = "app-db", "databases"
	c.Metadata.CreationTimestamp = time.Now().Add(-72 * time.Hour)
	c.Spec.Instances, c.Spec.ImageName = 3, "ghcr.io/cloudnative-pg/postgresql:16.4"
	c.Spec.Storage.Size = "50Gi"
	c.Status.Phase = "Cluster in healthy state"
	c.Status.Instances, c.Status.ReadyInstances = 3, 3
	c.Status.CurrentPrimary, c.Status.TargetPrimary = "app-db-1", "app-db-1"
	c.Status.InstanceNames = []string{"app-db-1", "app-db-2", "app-db-3"}
	c.Status.InstancesStatus = map[string][]string{"healthy": {"app-db-1", "app-db-2", "app-db-3"}}
	c.Status.InstancesReportedState = map[string]instanceState{
		"app-db-1": {IP: "10.1.0.1", IsPrimary: true, TimelineID: 3},
		"app-db-2": {IP: "10.1.0.2", TimelineID: 3},
		"app-db-3": {IP: "10.1.0.3", TimelineID: 3},
	}
	c.Status.TimelineID = 3
	c.Status.Topology.NodesUsed = 3
	c.Status.LastSuccessfulBackup = time.Now().Add(-4 * time.Hour).Format(time.RFC3339)
	c.Status.Conditions = []condition{{Type: "Ready", Status: "True"}}
	return c
}

func problemsIn(t *testing.T, c cluster) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, row := range problemTable(c).Rows {
		out[row[0]] = row[2]
	}
	return out
}

// **A healthy cluster produces no problems section at all**, which is the
// conditional-section doctrine the rest of rta follows: a "Needs attention"
// heading with nothing under it teaches people to skip the heading.
func TestAHealthyClusterHasNothingToSay(t *testing.T) {
	if got := problemsIn(t, healthy()); len(got) != 0 {
		t.Errorf("a healthy cluster reported %v", got)
	}
	sections := statusView(healthy()).(view.Sections)
	for _, s := range sections.Items {
		if s.Title == "Needs attention" {
			t.Error("the section is present with nothing in it")
		}
	}
}

// **The switchover is the derived fact worth having.** CNPG moves
// targetPrimary first and currentPrimary once the promotion lands, so the two
// differing means a promotion is happening right now — which is exactly the
// moment somebody runs a status command, and exactly what a column of raw
// fields makes them work out for themselves.
func TestAPromotionInFlightIsNamedRatherThanLeftToBeInferred(t *testing.T) {
	c := healthy()
	c.Status.TargetPrimary = "app-db-2"

	if !c.switchingOver() {
		t.Fatal("targetPrimary differing from currentPrimary is not read as a switchover")
	}
	got := problemsIn(t, c)
	if !strings.Contains(got["switchover"], "app-db-2") {
		t.Errorf("switchover = %q, want it to name where the primary is going", got["switchover"])
	}
	if line := primaryLine(c); !strings.Contains(line, "→") {
		t.Errorf("primary = %q, want both ends of the move", line)
	}
}

// The role comes from the cluster's own currentPrimary, not from each
// instance's self-report: during a failover an instance can still believe it
// is primary while the cluster has moved on, and two rows saying "primary" is
// a status page contradicting itself.
func TestOnlyOneInstanceIsEverThePrimary(t *testing.T) {
	c := healthy()
	c.Status.CurrentPrimary = "app-db-2"
	st := c.Status.InstancesReportedState["app-db-1"]
	st.IsPrimary = true // the old primary has not caught up
	c.Status.InstancesReportedState["app-db-1"] = st

	primaries := 0
	for _, r := range c.instances() {
		if r.role == "primary" {
			primaries++
			if r.name != "app-db-2" {
				t.Errorf("primary is %q, want the cluster's own currentPrimary", r.name)
			}
		}
	}
	if primaries != 1 {
		t.Errorf("%d instances claim to be primary", primaries)
	}
	if first := c.instances()[0].name; first != "app-db-2" {
		t.Errorf("first row is %q — the primary is the row that matters", first)
	}
}

// Every finding this plugin can make, each from the field that carries it.
func TestEachProblemIsFoundFromItsOwnField(t *testing.T) {
	failing := time.Now().Add(-10 * time.Minute)
	for _, tc := range []struct {
		what   string
		mutate func(*cluster)
		key    string
		want   string
	}{
		{"a primary that is unhealthy", func(c *cluster) {
			c.Status.PrimaryFailingSince = &failing
		}, "primary", "unhealthy"},
		{"fewer ready than the spec asks for", func(c *cluster) {
			c.Status.ReadyInstances = 2
		}, "instances", "2/3"},
		{"no primary at all", func(c *cluster) {
			c.Status.CurrentPrimary, c.Status.TargetPrimary = "", ""
		}, "primary", "no primary"},
		{"every instance on one node", func(c *cluster) {
			c.Status.Topology.NodesUsed = 1
		}, "topology", "one node"},
		{"backup not configured at all", func(c *cluster) {
			c.Status.LastSuccessfulBackup = ""
		}, "backup", "not configured"},
		{"backup configured but never once succeeded", func(c *cluster) {
			c.Status.LastSuccessfulBackup = ""
			c.Spec.Backup = &clusterBackup{BarmanObjectStore: &struct{}{}}
			c.Status.LastFailedBackup = time.Now().Add(-30 * time.Minute).Format(time.RFC3339)
		}, "backup", "no backup has ever succeeded"},
		{"a backup failing after an earlier success", func(c *cluster) {
			c.Status.LastFailedBackup = time.Now().Add(-time.Hour).Format(time.RFC3339)
		}, "backup", "most recent attempt failed"},
		{"a certificate close to expiry", func(c *cluster) {
			c.Status.Certificates.Expirations = map[string]string{
				"app-db-server": time.Now().Add(10 * 24 * time.Hour).UTC().
					Format("2006-01-02 15:04:05 -0700 MST"),
			}
		}, "certificates", "expires in"},
		{"a certificate already expired", func(c *cluster) {
			c.Status.Certificates.Expirations = map[string]string{
				"app-db-server": time.Now().Add(-24 * time.Hour).UTC().
					Format("2006-01-02 15:04:05 -0700 MST"),
			}
		}, "certificates", "expired"},
		{"a stuck PVC", func(c *cluster) {
			c.Status.UnusablePVC = []string{"app-db-4"}
		}, "unusable PVCs", "app-db-4"},
		{"a condition that is not satisfied", func(c *cluster) {
			c.Status.Conditions = append(c.Status.Conditions, condition{
				Type: "ContinuousArchiving", Status: "False",
				Reason: "Continuous archiving failed", Message: "wal-archive exit 1"})
		}, "ContinuousArchiving", "wal-archive exit 1"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			c := healthy()
			tc.mutate(&c)
			got := problemsIn(t, c)
			if tc.key == "" {
				if len(got) == 0 {
					t.Error("nothing was reported")
				}
				return
			}
			if !strings.Contains(got[tc.key], tc.want) {
				t.Errorf("%s = %q, want it to mention %q", tc.key, got[tc.key], tc.want)
			}
		})
	}
}

// A certificate months from expiry belongs in the overview, never in the
// problems — the operator rotates these itself, so far-out expiry is the
// system working.
func TestAFarOffCertificateExpiryIsInformationNotAProblem(t *testing.T) {
	c := healthy()
	c.Status.Certificates.Expirations = map[string]string{
		"app-db-ca": time.Now().Add(90 * 24 * time.Hour).UTC().
			Format("2006-01-02 15:04:05 -0700 MST"),
	}
	if got := problemsIn(t, c); got["certificates"] != "" {
		t.Errorf("a healthy expiry was graded a problem: %q", got["certificates"])
	}
	if v := overviewOf(t, c)["Certificates"]; !strings.Contains(v, "expires in 89d") {
		t.Errorf("Certificates = %q, want the soonest expiry as an age", v)
	}
}

// The overview rows that are derived rather than copied: version before image
// tag, WAL storage beside data, replication posture assembled from three
// spec stanzas, and the primary's tenure — the failover trace.
func TestTheOverviewDerivesWhatTheRawFieldsSpread(t *testing.T) {
	c := healthy()
	c.Status.PGDataImageInfo.Image = "ghcr.io/cloudnative-pg/postgresql:17.10"
	c.Status.PGDataImageInfo.MajorVersion = 17
	c.Status.CurrentPrimaryTimestamp = time.Now().Add(-3 * time.Hour).Format(time.RFC3339)
	c.Spec.WalStorage.Size = "1Gi"
	c.Spec.Resources.Requests = map[string]string{"cpu": "100m", "memory": "256Mi"}
	c.Spec.Resources.Limits = map[string]string{"cpu": "250m", "memory": "512Mi"}
	c.Spec.MinSyncReplicas, c.Spec.MaxSyncReplicas = 1, 2
	slots := true
	c.Spec.ReplicationSlots.HighAvailability.Enabled = &slots
	superuser := false
	c.Spec.EnableSuperuserAccess = &superuser

	got := overviewOf(t, c)
	for key, want := range map[string]string{
		"PostgreSQL":       "17 — ghcr.io/cloudnative-pg/postgresql:17.10",
		"Primary":          "primary for 3h",
		"Storage":          "50Gi + 1Gi WAL",
		"Resources":        "requests 100m cpu, 256Mi memory · limits 250m cpu, 512Mi memory",
		"Replication":      "synchronous, 1–2 replicas, HA replication slots",
		"Superuser access": "disabled",
	} {
		if !strings.Contains(got[key], want) {
			t.Errorf("%s = %q, want it to carry %q", key, got[key], want)
		}
	}
	if _, ok := got["Image"]; ok {
		t.Error("the Image row should give way when the version row can be built")
	}
}

// A single instance replicates nowhere, and the row should say so rather
// than claim "asynchronous" about a replica that does not exist.
func TestASingleInstanceDoesNotClaimAReplicationMode(t *testing.T) {
	c := healthy()
	c.Spec.Instances = 1
	if v := overviewOf(t, c)["Replication"]; !strings.Contains(v, "no replica") {
		t.Errorf("Replication = %q, want it to say there is none", v)
	}
}

func overviewOf(t *testing.T, c cluster) map[string]string {
	t.Helper()
	kv, ok := statusView(c).(view.Sections).Items[0].View.(view.KeyValue)
	if !ok {
		t.Fatal("the first status section is not the key/value overview")
	}
	out := map[string]string{}
	for _, p := range kv.Pairs {
		out[p.Key] = p.Value
	}
	return out
}

// A condition that IS satisfied is not a row: a list where every line says
// True is a list nobody reads.
func TestSatisfiedConditionsAreNotReported(t *testing.T) {
	c := healthy()
	c.Status.Conditions = []condition{
		{Type: "Ready", Status: "True"},
		{Type: "ContinuousArchiving", Status: "True"},
	}
	if got := problemsIn(t, c); len(got) != 0 {
		t.Errorf("satisfied conditions produced rows: %v", got)
	}
}

// The list reads a real kubectl answer end to end, so the JSON field names
// are checked against the CRD rather than assumed.
func TestTheListReadsWhatKubectlActuallyPrints(t *testing.T) {
	serves(t, clusterList{Items: []cluster{healthy()}})

	v, err := runList(context.Background(), req(map[string]any{"all-namespaces": true}))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok || len(tbl.Rows) != 1 {
		t.Fatalf("view = %#v", v)
	}
	row := tbl.Rows[0]
	for i, want := range []string{"databases", "app-db", "3/3", "ok", "app-db-1"} {
		if row[i] != want {
			t.Errorf("column %d = %q, want %q", i, row[i], want)
		}
	}
}

// An empty answer is a sentence, not an empty table: "no clusters here" and
// "a table with no rows" read differently to somebody who is checking.
func TestNoClustersIsSaidInWords(t *testing.T) {
	serves(t, clusterList{})
	v, err := runList(context.Background(), req(map[string]any{"namespace": "empty"}))
	if err != nil {
		t.Fatal(err)
	}
	text, ok := v.(view.Text)
	if !ok || !strings.Contains(text.Body, "namespace empty") {
		t.Errorf("view = %#v, want a sentence naming where it looked", v)
	}
}

// **A missing operator is its own answer.** "the server could not find the
// requested resource" from a cluster with no CloudNativePG installed reads
// like a broken plugin; it is the one failure where the next step is
// installing something rather than fixing something.
func TestAClusterWithoutTheOperatorSaysSo(t *testing.T) {
	fakeKubectl(t, `echo 'error: the server doesn'"'"'t have a resource type "clusters"' >&2
exit 1
`)
	_, err := runList(context.Background(), req(nil))
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("err = %#v", err)
	}
	if verr.Code != "cnpg.notinstalled" {
		t.Errorf("code = %q, want cnpg.notinstalled", verr.Code)
	}
}

// Authentication and permission are told apart, which matters more here than
// anywhere: this plugin exists to be usable behind a proxy handing out
// short-lived credentials, so an expired one is the ordinary failure and
// reporting it as RBAC sends people to argue with the wrong team.
func TestAnExpiredLoginIsNotReportedAsRBAC(t *testing.T) {
	for _, tc := range []struct{ stderr, code string }{
		{`error: You must be logged in to the server (Unauthorized)`, "cnpg.unauthenticated"},
		{`Unable to connect to the server: getting credentials: exec: executable tsh failed`,
			"cnpg.unauthenticated"},
		{`Error from server (Forbidden): clusters.postgresql.cnpg.io is forbidden`, "cnpg.denied"},
	} {
		fakeKubectl(t, "echo '"+tc.stderr+"' >&2\nexit 1\n")
		_, err := runList(context.Background(), req(nil))
		verr, ok := err.(*view.Error)
		if !ok {
			t.Fatalf("err = %#v", err)
		}
		if verr.Code != tc.code {
			t.Errorf("%q → %q, want %q", tc.stderr, verr.Code, tc.code)
		}
	}
}

// **Argument injection, refused before kubectl sees it.** A context name
// beginning with a dash is read by kubectl as a flag, and
// `--kubeconfig=/tmp/mine` where a context was expected points the call at a
// different cluster entirely.
func TestAValueThatWouldBeReadAsAFlagIsRefused(t *testing.T) {
	fakeKubectl(t, "echo '{}'\n")
	for _, bad := range []string{"--kubeconfig=/tmp/mine", "-n", "a b", "a\nb", `a"b`} {
		if _, err := runList(context.Background(), req(map[string]any{"context": bad})); err == nil {
			t.Errorf("context %q was accepted", bad)
		}
		if _, err := runStatus(context.Background(),
			req(map[string]any{"name": bad})); err == nil {
			t.Errorf("cluster name %q was accepted", bad)
		}
	}
}

// Nothing here can change a database. The capability model says so, and this
// asserts it rather than trusting a reviewer to notice a Safety that drifted.
// Exactly one capability writes, and the gates on it are stated here rather
// than only in its declaration.
//
// The blanket "everything is Read" this used to assert was the plugin's whole
// safety story, and replacing it with a table is the point: the property that
// survives is not "nothing changes" but "the one thing that changes is off the
// default MCP surface and needs a grant naming the cluster". A capability
// added later that quietly reaches Write without both is what this now fails
// on.
func TestOnlyTheBackupRequestWritesAndItIsGatedTwice(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID == "cnpg.backup.request" {
			switch {
			case c.Safety != plugin.Write:
				t.Errorf("%s is %v, want Write — Read would put it on the default MCP surface",
					c.ID, c.Safety)
			case !c.NeedsGrant:
				t.Errorf("%s does not need a grant, so --allow-write cnpg would be the "+
					"whole decision, made once and never expiring", c.ID)
			case c.Scope != "cluster":
				t.Errorf("%s is scoped to %q, want the cluster — otherwise a grant to back "+
					"up one database is a grant to back up every one the kubeconfig reaches",
					c.ID, c.Scope)
			case c.Idempotent:
				t.Errorf("%s claims idempotence, but two calls are two backups", c.ID)
			}
			continue
		}
		if c.Safety != plugin.Read {
			t.Errorf("%s is %v — everything but the backup request reads and nothing else",
				c.ID, c.Safety)
		}
	}
}

// **Every flag goes after the subcommand, and this is not cosmetic.**
//
// `--all-namespaces` is a flag of `get`, not a kubectl global. Placed before
// the verb, kubectl decides it is being asked for a plugin and refuses with
// "flags cannot be placed before plugin name: --request-timeout=15s" — which
// names the first flag it saw rather than the one it could not place. Every
// `--all-namespaces` call this plugin made failed that way, and the message
// sent the reader after a timeout that was not the problem.
func TestEveryFlagFollowsTheSubcommand(t *testing.T) {
	got := selection{context: "homelab", allNamespace: true}.
		args("get", clusterCRD, "-o", "json")
	if len(got) == 0 || got[0] != "get" {
		t.Fatalf("args = %v, want the subcommand first", got)
	}
	verb := -1
	for i, a := range got {
		if a == "get" {
			verb = i
			break
		}
	}
	for i, a := range got {
		if strings.HasPrefix(a, "-") && i < verb {
			t.Errorf("args = %v: %q comes before the subcommand", got, a)
		}
	}
	for _, want := range []string{"--context=homelab", "--all-namespaces", "-o", "json"} {
		if !slices.Contains(got, want) {
			t.Errorf("args = %v, missing %q", got, want)
		}
	}
}

// A namespace and every-namespace are exclusive, and the namespace form is
// the one a global flag would have tolerated in either position — so it is
// the one whose regression this would not catch without asking directly.
func TestANamespaceIsPassedAndNotBothForms(t *testing.T) {
	got := selection{namespace: "keycloak-system"}.args("get", clusterCRD)
	if !slices.Contains(got, "--namespace=keycloak-system") {
		t.Errorf("args = %v, missing the namespace", got)
	}
	if slices.Contains(got, "--all-namespaces") {
		t.Errorf("args = %v names both a namespace and every namespace", got)
	}
}

// The kube twin of this refusal was closing a real grant scope bypass; here
// it is closing a trap. These capabilities declare no Scope, so there is
// nothing to bypass today — but rta derives the scope a call is checked
// against from the `namespace` value alone, so giving either of them
// `Scope: "namespace"` (the natural thing, since both already take a
// namespace) would make args()' preference for --all-namespaces into a way
// past that scope. A trap with no test is one a future refactor removes
// without noticing, which is the whole reason this is here.
func TestANamespaceAndEveryNamespaceTogetherAreRefused(t *testing.T) {
	_, verr := selectionOf(plugin.NewRequest(map[string]any{
		"namespace": "gitea", "all-namespaces": true,
	}, false, false))
	if verr == nil {
		t.Fatal("a scoped namespace sent alongside --all-namespaces was accepted")
	}
	if verr.Code != "cnpg.namespace.ambiguous" || verr.Hint == "" {
		t.Errorf("want a coded, hinted cnpg.namespace.ambiguous, got %+v", verr)
	}
}

// Neither ordinary form may be caught by the refusal above.
func TestEitherNamespaceFormAloneIsAccepted(t *testing.T) {
	one, verr := selectionOf(plugin.NewRequest(map[string]any{"namespace": "gitea"}, false, false))
	if verr != nil || one.namespace != "gitea" || one.allNamespace {
		t.Errorf("a plain namespace was not accepted: %+v %v", one, verr)
	}
	every, verr := selectionOf(plugin.NewRequest(map[string]any{"all-namespaces": true}, false, false))
	if verr != nil || !every.allNamespace || every.namespace != "" {
		t.Errorf("--all-namespaces alone was not accepted: %+v %v", every, verr)
	}
}
