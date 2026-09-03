package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The slice of the CloudNativePG Cluster resource this reads.
//
// Every field below was taken from the published CRD schema
// (postgresql.cnpg.io_clusters.yaml, v1) rather than from memory, and the
// list view's columns are the ones the CRD itself declares as printer
// columns — so `rta cnpg list` and `kubectl get clusters.postgresql.cnpg.io`
// answer the same question the same way, which is what makes one a fast path
// for the other rather than a second opinion.
//
// Deliberately partial. A Cluster's status has fifty fields, most of them
// operator bookkeeping — configmap resource versions, the operator's own
// binary hash, PVC name lists — and what is not decoded cannot be
// misinterpreted. What is here is what somebody looks at a database cluster
// to find out.

type clusterList struct {
	Items []cluster `json:"items"`
}

type cluster struct {
	Metadata struct {
		Name              string    `json:"name"`
		Namespace         string    `json:"namespace"`
		CreationTimestamp time.Time `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Instances int    `json:"instances"`
		ImageName string `json:"imageName"`
		Storage   struct {
			Size string `json:"size"`
		} `json:"storage"`
		WalStorage struct {
			Size string `json:"size"`
		} `json:"walStorage"`
		// Decoded only for presence: a cluster with no `backup` stanza has
		// nothing to back it up, which is a different finding from one whose
		// configured backups never succeed.
		//
		// The two sub-stanzas are decoded for presence too, and only for
		// presence: which mechanisms this cluster has configured is the
		// question a backup request has to answer before it sends anything,
		// and the contents are the object store's address beside references
		// to the secrets holding its keys — which this plugin has no question
		// that needs.
		Backup *clusterBackup `json:"backup,omitempty"`
		// Plugins is the other way a cluster can be backed up, and reading it
		// is a correction rather than an addition.
		//
		// CloudNativePG is moving object-store backup out of `.spec.backup`
		// and into a WAL-archiver plugin — the CRD says so itself, in
		// `isWALArchiver`'s own description: "This cannot be enabled if the
		// `.spec.backup.barmanObjectStore` configuration is present." So the
		// two stanzas are alternatives, and treating the absence of the first
		// as "nothing backs this cluster up" told every cluster on the newer
		// arrangement that it had no backups, on the screen somebody opens to
		// check exactly that.
		Plugins []clusterPlugin `json:"plugins,omitempty"`

		Resources struct {
			Requests map[string]string `json:"requests"`
			Limits   map[string]string `json:"limits"`
		} `json:"resources"`
		MinSyncReplicas int `json:"minSyncReplicas"`
		MaxSyncReplicas int `json:"maxSyncReplicas"`
		// A pointer because absent and false mean different things: the
		// operator's default has changed across CNPG versions, so an unset
		// field is "whatever this operator does", not "disabled" — and
		// printing a guess would be worse than printing nothing.
		EnableSuperuserAccess *bool `json:"enableSuperuserAccess,omitempty"`
		ReplicationSlots      struct {
			HighAvailability struct {
				Enabled *bool `json:"enabled,omitempty"`
			} `json:"highAvailability"`
		} `json:"replicationSlots"`
	} `json:"spec"`
	Status struct {
		Phase       string `json:"phase"`
		PhaseReason string `json:"phaseReason"`

		Instances      int `json:"instances"`
		ReadyInstances int `json:"readyInstances"`

		CurrentPrimary string `json:"currentPrimary"`
		TargetPrimary  string `json:"targetPrimary"`
		// Set only while the primary is unhealthy, which makes its presence
		// the finding rather than its value.
		PrimaryFailingSince *time.Time `json:"currentPrimaryFailingSinceTimestamp,omitempty"`
		// When the current primary took the role — which is the answer to
		// "did this cluster fail over recently", the question a young value
		// raises on its own.
		CurrentPrimaryTimestamp string `json:"currentPrimaryTimestamp"`

		Image           string `json:"image"`
		PGDataImageInfo struct {
			Image        string `json:"image"`
			MajorVersion int    `json:"majorVersion"`
		} `json:"pgDataImageInfo"`
		TimelineID int `json:"timelineID"`

		// The operator rotates these itself, so an expiry approaching means
		// the rotation is not happening — an operator wedged or paused — not
		// a renewal somebody forgot.
		Certificates struct {
			Expirations map[string]string `json:"expirations"`
		} `json:"certificates"`

		InstanceNames          []string                 `json:"instanceNames"`
		InstancesStatus        map[string][]string      `json:"instancesStatus"`
		InstancesReportedState map[string]instanceState `json:"instancesReportedState"`

		LastSuccessfulBackup     string `json:"lastSuccessfulBackup"`
		LastFailedBackup         string `json:"lastFailedBackup"`
		FirstRecoverabilityPoint string `json:"firstRecoverabilityPoint"`

		Conditions []condition `json:"conditions"`

		Topology struct {
			NodesUsed int `json:"nodesUsed"`
		} `json:"topology"`

		DanglingPVC []string `json:"danglingPVC"`
		UnusablePVC []string `json:"unusablePVC"`
		ResizingPVC []string `json:"resizingPVC"`
	} `json:"status"`
}

// clusterBackup is `.spec.backup`, decoded for the shape of what is set up
// and never for its contents — the object store's address and the references
// to the secrets holding its keys both live inside barmanObjectStore, and
// nothing here has a question that needs either.
type clusterBackup struct {
	BarmanObjectStore *struct{} `json:"barmanObjectStore,omitempty"`
	VolumeSnapshot    *struct{} `json:"volumeSnapshot,omitempty"`
}

// clusterPlugin is one entry of `.spec.plugins`.
//
// `parameters` is not decoded: it is a free-form string map a plugin defines
// for itself, and the barman-cloud one names the ObjectStore holding an
// object store's address and credential references.
type clusterPlugin struct {
	Name string `json:"name"`
	// Enabled is a pointer because the CRD's default is true — an entry
	// present with nothing said about it is on, and reading absent as false
	// would report a backed-up cluster as unprotected.
	Enabled       *bool `json:"enabled,omitempty"`
	IsWALArchiver bool  `json:"isWALArchiver,omitempty"`
}

func (p clusterPlugin) enabled() bool { return p.Enabled == nil || *p.Enabled }

// walArchiverPlugin names the enabled WAL-archiver plugin, or "".
//
// At most one may be designated, which the CRD states, so the first is the
// answer rather than one of several.
func (c cluster) walArchiverPlugin() string {
	for _, p := range c.Spec.Plugins {
		if p.IsWALArchiver && p.enabled() && strings.TrimSpace(p.Name) != "" {
			return p.Name
		}
	}
	return ""
}

// backupConfigured reports whether anything at all would perform a backup of
// this cluster: the classic `.spec.backup` stanza, or a WAL-archiver plugin.
//
// The question a write asks before writing, and the same question the status
// view's "not configured" finding was already asking with only half the
// answer.
func (c cluster) backupConfigured() bool {
	return c.Spec.Backup != nil || c.walArchiverPlugin() != ""
}

// defaultBackupMethod is what CloudNativePG uses for a Backup that names no
// method.
//
// **A fixed default, not the cluster's preference**, which is the correction a
// live operator forced. The CRD says it in as many words — `spec.method`
// "Defaults to: `barmanObjectStore`" — while `spec.target` right beside it
// says "If empty, it defaults to `cluster.spec.backup.target`". The two fields
// read alike and behave differently, and treating method like target meant
// rta would tell an operator the cluster had chosen when nothing had: a
// cluster configured only for volume snapshots gets a barmanObjectStore
// backup it cannot perform, failing minutes later with rta's receipt already
// saying the cluster decided.
const defaultBackupMethod = "barmanObjectStore"

// configuredMethods lists the backup mechanisms this cluster has set up.
//
// Presence only, and permissive where it cannot see. A WAL-archiver plugin
// carries its own configuration in an ObjectStore resource this plugin does
// not read, so a cluster using one gets no opinion from here rather than a
// guess — refusing a legitimate backup is a worse failure than letting the
// API server have the last word, which it has either way.
func (c cluster) configuredMethods() []string {
	var out []string
	if b := c.Spec.Backup; b != nil {
		if b.BarmanObjectStore != nil {
			out = append(out, "barmanObjectStore")
		}
		if b.VolumeSnapshot != nil {
			out = append(out, "volumeSnapshot")
		}
	}
	if c.walArchiverPlugin() != "" {
		out = append(out, "plugin")
	}
	return out
}

// canTake reports whether a backup by this method is something the cluster is
// set up to perform, and is deliberately generous about what it does not
// know: an unrecognised method, or any method at all on a cluster using a
// WAL-archiver plugin, passes.
func (c cluster) canTake(method string) bool {
	if method == "" {
		method = defaultBackupMethod
	}
	if c.walArchiverPlugin() != "" || method == "plugin" {
		return true
	}
	return slices.Contains(c.configuredMethods(), method)
}

type instanceState struct {
	IP         string `json:"ip"`
	IsPrimary  bool   `json:"isPrimary"`
	TimelineID int    `json:"timeLineID"`
}

type condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ready is the ready/desired pair the way an operator says it out loud.
func (c cluster) ready() string {
	desired := c.Spec.Instances
	if desired == 0 {
		desired = c.Status.Instances
	}
	return strconv.Itoa(c.Status.ReadyInstances) + "/" + strconv.Itoa(desired)
}

// healthy reports whether every instance the spec asks for is ready.
func (c cluster) healthy() bool {
	desired := c.Spec.Instances
	if desired == 0 {
		desired = c.Status.Instances
	}
	return desired > 0 && c.Status.ReadyInstances >= desired
}

// switchingOver reports a promotion in flight.
//
// **The one derived fact that is not on any single field.** CNPG moves
// targetPrimary first and currentPrimary once the promotion lands, so the two
// differing is a switchover or a failover happening right now — which is
// exactly the moment somebody runs a status command, and exactly the thing a
// column of raw fields makes them work out for themselves.
func (c cluster) switchingOver() bool {
	t, cur := c.Status.TargetPrimary, c.Status.CurrentPrimary
	return t != "" && cur != "" && t != cur
}

// singleNode reports every instance sharing one node, which the CRD's own
// documentation calls out as the absence of high availability: a cluster with
// three replicas on one node survives nothing that takes the node.
func (c cluster) singleNode() bool {
	return c.Status.Topology.NodesUsed == 1 && c.Status.ReadyInstances > 1
}

// notTrue is every condition that is not satisfied, which is the only half
// worth printing: a list where every row says True is a list nobody reads.
func (c cluster) notTrue() []condition {
	var out []condition
	for _, cond := range c.Status.Conditions {
		if cond.Status != "True" {
			out = append(out, cond)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// instances lists the pods with what is known about each, primary first and
// then by name, so the row that matters is the row at the top.
func (c cluster) instances() []instanceRow {
	names := c.Status.InstanceNames
	if len(names) == 0 {
		for name := range c.Status.InstancesReportedState {
			names = append(names, name)
		}
	}
	byName := map[string]string{}
	for state, pods := range c.Status.InstancesStatus {
		for _, pod := range pods {
			byName[pod] = state
		}
	}
	out := make([]instanceRow, 0, len(names))
	for _, name := range names {
		st := c.Status.InstancesReportedState[name]
		out = append(out, instanceRow{
			name:     name,
			role:     role(name, c.Status.CurrentPrimary, st.IsPrimary),
			state:    orDash(byName[name]),
			ip:       orDash(st.IP),
			timeline: st.TimelineID,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].role == "primary", out[j].role == "primary"; a != b {
			return a
		}
		return out[i].name < out[j].name
	})
	return out
}

type instanceRow struct {
	name, role, state, ip string
	timeline              int
}

// role prefers the cluster's own currentPrimary over each instance's
// self-report: during a failover an instance can still believe it is primary
// while the cluster has moved on, and the cluster's view is the one every
// other field here is consistent with.
func role(name, currentPrimary string, selfReport bool) string {
	switch {
	case currentPrimary != "":
		if name == currentPrimary {
			return "primary"
		}
		return "replica"
	case selfReport:
		return "primary"
	}
	return "replica"
}

// backupAge is how long since the last successful backup, and whether there
// has ever been one. The timestamp is RFC3339 in the resource; the question is
// always asked as a duration.
func (c cluster) backupAge() (string, bool) {
	t, ok := c.lastSuccessfulBackup()
	if !ok {
		return "", false
	}
	return age(t), true
}

func (c cluster) lastSuccessfulBackup() (time.Time, bool) {
	return parseWhen(c.Status.LastSuccessfulBackup)
}

// primaryFor is how long the current primary has held the role. A young
// value is the trace of a failover: the pod may be days old, the *role* hours
// old, and only the second one says something happened.
func (c cluster) primaryFor() (string, bool) {
	t, ok := parseWhen(c.Status.CurrentPrimaryTimestamp)
	if !ok || c.Status.CurrentPrimary == "" {
		return "", false
	}
	return age(t), true
}

// soonestCert is the certificate closest to expiry, because the one nearest
// the cliff is the one the question is about — the operator rotates all of a
// cluster's certificates together, so in practice they expire together too.
func (c cluster) soonestCert() (name string, at time.Time, ok bool) {
	for n, raw := range c.Status.Certificates.Expirations {
		t, parsed := parseWhen(raw)
		if !parsed {
			continue
		}
		if !ok || t.Before(at) {
			name, at, ok = n, t, true
		}
	}
	return name, at, ok
}

// parseWhen reads the two timestamp spellings the Cluster resource actually
// uses: RFC3339 for the backup and primary fields, and Go's own time.String()
// form ("2026-11-06 01:46:28 +0000 UTC") for the certificate expirations —
// the operator writes those with fmt.Sprintf("%v"), so the second layout is
// its wire format whether anyone meant it to be or not.
func parseWhen(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST"} {
		if t, err := time.Parse(layout, s); err == nil && !t.IsZero() {
			return t, true
		}
	}
	return time.Time{}, false
}

// replicationLine says how writes are protected, which the raw fields spread
// over three places: sync replica bounds in the spec, slot management in its
// own stanza, and "there is nothing to replicate to" implied by the instance
// count.
func (c cluster) replicationLine() string {
	if c.Spec.Instances <= 1 {
		return "none — a single instance has no replica"
	}
	mode := "asynchronous"
	if c.Spec.MinSyncReplicas > 0 || c.Spec.MaxSyncReplicas > 0 {
		mode = fmt.Sprintf("synchronous, %d–%d replicas",
			c.Spec.MinSyncReplicas, c.Spec.MaxSyncReplicas)
	}
	if e := c.Spec.ReplicationSlots.HighAvailability.Enabled; e != nil && *e {
		mode += ", HA replication slots"
	}
	return mode
}

// storageLine is the data volume and, when one is split off, the WAL volume —
// a full WAL volume stops writes just as surely as a full data volume, so a
// size that exists should be visible.
func (c cluster) storageLine() string {
	s := c.Spec.Storage.Size
	if s == "" {
		return "—"
	}
	if w := c.Spec.WalStorage.Size; w != "" {
		s += " + " + w + " WAL"
	}
	return s
}

// resourceLine renders requests and limits, and is empty when neither is set
// — absence means the pods run unbounded, which is worth a word too, but not
// a row that says "requests — · limits —".
func (c cluster) resourceLine() string {
	var parts []string
	if r := quantities(c.Spec.Resources.Requests); r != "" {
		parts = append(parts, "requests "+r)
	}
	if l := quantities(c.Spec.Resources.Limits); l != "" {
		parts = append(parts, "limits "+l)
	}
	return strings.Join(parts, " · ")
}

func quantities(m map[string]string) string {
	var parts []string
	if v := m["cpu"]; v != "" {
		parts = append(parts, v+" cpu")
	}
	if v := m["memory"]; v != "" {
		parts = append(parts, v+" memory")
	}
	return strings.Join(parts, ", ")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
