//go:build livecnpg

package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The backup half against a real cluster, for the two things a fixture cannot
// establish: that the API server accepts the exact document this builds, and
// that what it hands back decodes into the struct the receipt reads.
//
//	go test -tags livecnpg -run TestLiveBackup ./...
//
// **Every write here goes through `--dry-run=server`**, so admission runs in
// full — schema validation, defaulting, any webhook the operator installs —
// and nothing is persisted. That is not a compromise for the sake of a safe
// test: it is the strongest check available, because the failure this guards
// against is the API server rejecting or silently ignoring a field, and
// server-side dry run is the API server answering.

// serverDryRun posts doc with --dry-run=server and returns what came back.
//
// A refusal that is about this cluster's installation rather than about the
// document skips instead of failing. The one that turns up in practice is
// volumeSnapshot on an operator with no VolumeSnapshot CRD registered, which
// says nothing about whether rta built the object correctly — it says the
// cluster cannot take that kind of backup, which is a fine thing for a
// cluster and the wrong thing for this test to have an opinion about.
func serverDryRun(t *testing.T, s selection, doc []byte) backupObject {
	t.Helper()
	var created backupObject
	if verr := createJSON(context.Background(), s, doc, &created, "--dry-run=server"); verr != nil {
		if strings.Contains(verr.Error(), "VolumeSnapshot CRD") {
			t.Skipf("this cluster cannot take volume snapshots: %v", verr)
		}
		t.Fatalf("the API server refused the document rta builds: %v\n%s", verr, doc)
	}
	return created
}

// aLiveCluster finds any CloudNativePG cluster on the current context.
func aLiveCluster(t *testing.T) cluster {
	t.Helper()
	var list clusterList
	if verr := getJSON(context.Background(), selection{allNamespace: true}, "", &list); verr != nil {
		t.Skipf("no reachable cluster: %v", verr)
	}
	if len(list.Items) == 0 {
		t.Skip("the current context has no CloudNativePG clusters")
	}
	return list.Items[0]
}

// The document rta builds is one a real API server accepts, and the object it
// returns is one the receipt can read.
//
// The cluster is used as the reference whether or not it configures a backup:
// this is about the wire contract, and rta's own refusal for an unconfigured
// cluster is checked separately, in the unit tests, where it belongs.
func TestLiveBackupTheAPIServerAcceptsWhatRtaBuilds(t *testing.T) {
	c := aLiveCluster(t)
	s := selection{namespace: c.Metadata.Namespace}

	doc, b, verr := buildBackupRequest(
		req(map[string]any{"cluster": c.Metadata.Name, "namespace": c.Metadata.Namespace}), c, s)
	if verr != nil {
		t.Fatal(verr)
	}
	created := serverDryRun(t, s, doc)

	if created.Metadata.Name != b.Metadata.Name {
		t.Errorf("name came back %q, sent %q", created.Metadata.Name, b.Metadata.Name)
	}
	if created.Metadata.Namespace != c.Metadata.Namespace {
		t.Errorf("namespace came back %q, want %q",
			created.Metadata.Namespace, c.Metadata.Namespace)
	}
	if created.Spec.Cluster.Name != c.Metadata.Name {
		t.Errorf("spec.cluster.name came back %q, want %q",
			created.Spec.Cluster.Name, c.Metadata.Name)
	}
	if created.Metadata.Labels[requestedBy] != "rta" {
		t.Errorf("the label rta stamps did not survive: %v", created.Metadata.Labels)
	}
	// What the operator defaulted, which is the half a fixture invents.
	t.Logf("server defaulted method=%q phase=%q", created.Spec.Method, created.Status.Phase)
	if created.Spec.Method == "" && created.Status.Method == "" {
		t.Error("neither spec.method nor status.method came back — the CRD declares a " +
			"default for the first, so decoding one of them is how the receipt says how")
	}
}

// Every override this offers is a value the CRD's enum actually admits.
//
// The Options lists were read off a running operator's schema, and this is
// what keeps them read off it: an enum narrowed in a later CNPG release turns
// a suggestion rta renders into a document the API server refuses, and it
// should be this test that says so rather than an operator's terminal.
func TestLiveBackupEveryOfferedOverrideIsAcceptedByTheSchema(t *testing.T) {
	c := aLiveCluster(t)
	s := selection{namespace: c.Metadata.Namespace}

	// online is paired with the method it is only legal under. CNPG's webhook
	// refuses `spec.online` on any other method, which the CRD schema does not
	// say — this pairing is here because the unpaired version is what found
	// that, and rta now refuses it before the API server has to.
	for _, tc := range []map[string]any{
		{"method": "barmanObjectStore"},
		{"method": "volumeSnapshot"},
		{"target": "primary"},
		{"target": "prefer-standby"},
		{"method": "volumeSnapshot", "online": "true"},
		{"method": "volumeSnapshot", "online": "false"},
	} {
		var parts []string
		for _, k := range []string{"method", "target", "online"} {
			if v, ok := tc[k]; ok {
				parts = append(parts, k+"="+v.(string))
			}
		}
		name := strings.Join(parts, ",")
		t.Run(name, func(t *testing.T) {
			values := map[string]any{
				"cluster": c.Metadata.Name, "namespace": c.Metadata.Namespace,
			}
			for k, v := range tc {
				values[k] = v
			}
			doc, _, verr := buildBackupRequest(req(values), c, s)
			if verr != nil {
				t.Fatal(verr)
			}
			serverDryRun(t, s, doc)
		})
	}
}

// The plugin's own claim, checked against the operator rather than recalled:
// a Backup naming a cluster that configures nothing is admitted without
// complaint, which is precisely why rta refuses it first.
//
// Skipped rather than failed when the cluster does configure a backup — that
// is a fine cluster to have and the wrong one for this check.
func TestLiveBackupAnUnconfiguredClusterIsAdmittedByTheOperatorAnyway(t *testing.T) {
	var list clusterList
	if verr := getJSON(context.Background(), selection{allNamespace: true}, "", &list); verr != nil {
		t.Skipf("no reachable cluster: %v", verr)
	}
	var bare *cluster
	for i := range list.Items {
		if !list.Items[i].backupConfigured() {
			bare = &list.Items[i]
			break
		}
	}
	if bare == nil {
		t.Skip("every cluster on this context configures a backup")
	}
	s := selection{namespace: bare.Metadata.Namespace}
	doc, _, verr := buildBackupRequest(
		req(map[string]any{"cluster": bare.Metadata.Name}), *bare, s)
	if verr != nil {
		t.Fatal(verr)
	}
	created := serverDryRun(t, s, doc)
	t.Logf("%s/%s configures no backup and the operator admitted the object anyway "+
		"(phase %q) — this is what rta.backup.unconfigured exists to stop",
		bare.Metadata.Namespace, bare.Metadata.Name, created.Status.Phase)
}

// The CRD's own enums, read off the running operator and compared with what
// this plugin offers.
//
// The strongest form of the check above: rather than proving each value is
// accepted, this asks the cluster what the set *is*, so a value CNPG adds
// shows up as a suggestion rta is missing rather than staying invisible.
func TestLiveBackupTheOfferedOptionsMatchTheCRDsEnums(t *testing.T) {
	raw, verr := run(context.Background(), "get", "crd", "backups.postgresql.cnpg.io", "-o", "json")
	if verr != nil {
		t.Skipf("cannot read the CRD: %v", verr)
	}
	var doc struct {
		Spec struct {
			Versions []struct {
				Name   string `json:"name"`
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Spec struct {
								Properties map[string]struct {
									Enum []string `json:"enum"`
								} `json:"properties"`
							} `json:"spec"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the CRD did not parse: %v", err)
	}
	var props map[string]struct {
		Enum []string `json:"enum"`
	}
	for _, v := range doc.Spec.Versions {
		if v.Name == "v1" {
			props = v.Schema.OpenAPIV3Schema.Properties.Spec.Properties
		}
	}
	if props == nil {
		t.Skip("this operator serves no v1 Backup")
	}

	offered := map[string][]string{}
	for _, f := range backupRequestCapability().Inputs {
		if len(f.Options) > 0 {
			offered[f.Name] = f.Options
		}
	}
	// target must match exactly. method is allowed to be a subset, and is one:
	// `plugin` is in the CRD and not in rta's list, because a plugin-method
	// Backup needs a `spec.pluginConfiguration` rta does not build and this
	// same operator refuses the document without it. What is checked is the
	// direction that matters — nothing rta offers may be outside the enum,
	// which is the error the enums were carried here to prevent.
	if got, want := props["target"].Enum, offered["target"]; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("target: the CRD admits %v and rta offers %v", got, want)
	}
	for _, m := range offered["method"] {
		if !slices.Contains(props["method"].Enum, m) {
			t.Errorf("rta offers method %q, which this CRD does not admit: %v",
				m, props["method"].Enum)
		}
	}
	if slices.Contains(offered["method"], "plugin") {
		t.Error("plugin is offered again — every document rta builds for it is " +
			"missing the pluginConfiguration this operator requires")
	}
}
