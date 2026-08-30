package main

import (
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

		Image      string `json:"image"`
		TimelineID int    `json:"timelineID"`

		InstanceNames          []string                 `json:"instanceNames"`
		InstancesStatus        map[string][]string      `json:"instancesStatus"`
		InstancesReportedState map[string]instanceState `json:"instancesReportedState"`

		LastSuccessfulBackup     string `json:"lastSuccessfulBackup"`
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
	t, err := time.Parse(time.RFC3339, c.Status.LastSuccessfulBackup)
	if err != nil || t.IsZero() {
		return "", false
	}
	return age(t), true
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
