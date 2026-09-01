package main

import (
	"context"
	"sort"
	"strconv"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.node.list, and the node half of kube.overview.
//
// The gap this closes is one an overview built only from pods cannot see. Pod
// health is reported by a kubelet, so a node whose kubelet has stopped talking
// altogether does not produce unhealthy pods — it produces pods that are still
// described as they were the last time anyone heard, until the node controller
// times out and starts evicting minutes later. For that window kube.overview
// could say every pod is fine while a machine was gone, which is the one
// answer an overview must never give.
//
// Deliberately not metrics. kube.metrics.node already reads usage and says in
// overview.go why metrics stay out of the composed view: metrics-server is an
// add-on a bare cluster often lacks, and folding a frequently-absent
// dependency into the always-answer view makes the common case noisier. Node
// *conditions* have no such dependency — they are on the Node object itself,
// which any cluster that answers at all will serve.

// nodeCondition is one entry of a Node's status.conditions.
//
// Status is a string and not a bool because Kubernetes conditions are
// three-valued: "True", "False", and "Unknown" — and for Ready the third value
// is the interesting one, meaning the node controller stopped hearing from the
// kubelet rather than the kubelet reporting a problem. Decoding this into a
// bool would collapse "broken" and "gone" into the same answer.
type nodeCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// nodeItem is the Node shape this plugin reads. Shared with metrics.go, which
// needs only Status.Allocatable.
type nodeItem struct {
	Metadata meta `json:"metadata"`
	Spec     struct {
		Unschedulable bool `json:"unschedulable"`
	} `json:"spec"`
	Status struct {
		Allocatable struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
			Pods   string `json:"pods"`
		} `json:"allocatable"`
		Conditions []nodeCondition `json:"conditions"`
		NodeInfo   struct {
			KubeletVersion string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

// pressureConditions are the three conditions whose polarity is inverted
// relative to Ready: for these, "True" is the bad news.
//
// Worth naming as a list rather than testing inline, because that inversion is
// the single easiest thing to get backwards here — a node in trouble reports
// `Ready: True` *and* `MemoryPressure: True` at the same time, and code that
// treats every condition the same way reads the second one as reassurance.
var pressureConditions = []string{"MemoryPressure", "DiskPressure", "PIDPressure"}

// nodeHealth is what a Node's conditions say, reduced to the things an
// overview has room for.
type nodeHealth struct {
	status    string
	ready     bool
	cordoned  bool
	pressures []string
}

// healthOfNode reduces a Node's conditions the way healthOf reduces a pod's
// container statuses, and draws the same distinction that one draws for
// Succeeded pods: deliberate is not broken.
//
// A cordoned node is a node an operator told the scheduler to stop using —
// during a kernel upgrade, before a drain, while a disk is replaced. Counting
// it as unhealthy would make kube.overview cry wolf during exactly the planned
// maintenance an operator is already watching, which is the failure healthOf's
// own comment describes for completed Jobs. So cordoned is reported, never
// counted: it stays out of the "not ready" number and gets a row of its own,
// because a node cordoned for a ten-minute upgrade and still cordoned a week
// later is real capacity nobody is using and nothing else would mention it.
func healthOfNode(n nodeItem) nodeHealth {
	h := nodeHealth{cordoned: n.Spec.Unschedulable, status: "Unknown"}
	for _, c := range n.Status.Conditions {
		if c.Type == "Ready" {
			switch c.Status {
			case "True":
				h.ready, h.status = true, "Ready"
			case "False":
				h.status = "NotReady"
			default:
				// "Unknown" — and anything else the API ever adds. The node
				// controller sets this when the kubelet has not checked in
				// within its grace period, so it means the machine is
				// unreachable rather than unwell. Left as its own word instead
				// of folded into NotReady: they send an operator to different
				// places, one to the node's logs and one to whether the box is
				// still on the network.
				h.status = "Unknown"
			}
			continue
		}
		for _, p := range pressureConditions {
			if c.Type == p && c.Status == "True" {
				h.pressures = append(h.pressures, c.Type)
			}
		}
	}
	// Matches kubectl's own column, which appends this rather than replacing
	// the readiness word — an operator reading "Ready,SchedulingDisabled"
	// knows both facts, and dropping either one loses something they need.
	if h.cordoned {
		h.status += ",SchedulingDisabled"
	}
	return h
}

// schedulable is whether this node can actually accept a new pod, which is the
// honest denominator for pod-slot headroom: a node that is cordoned or not
// Ready has allocatable capacity the scheduler will not use.
func (h nodeHealth) schedulable() bool { return h.ready && !h.cordoned }

// fetchNodes reads every Node.
//
// Nodes are cluster-scoped, so the namespace selection is cleared rather than
// passed — the same thing runNamespaceList does, and for the same reason: the
// request rta makes should be the request it describes, and kubectl would
// silently ignore a namespace here.
func fetchNodes(ctx context.Context, s selection) (list[nodeItem], *view.Error) {
	s.Namespace, s.AllNS = "", false
	var out list[nodeItem]
	if verr := getJSON(ctx, s, "nodes", &out); verr != nil {
		return out, verr
	}
	sort.Slice(out.Items, func(i, j int) bool {
		return out.Items[i].Metadata.Name < out.Items[j].Metadata.Name
	})
	return out, nil
}

// podSlots is how many pods the schedulable nodes can hold between them.
//
// allocatable.pods is the kubelet's own max-pods for that node, so this is a
// real ceiling rather than an estimate — and it is the one number that says
// whether a cluster can still take work, which neither CPU nor memory headroom
// answers on its own. A cluster can sit at 30% memory and still refuse every
// new pod because its nodes are at their pod cap.
func podSlots(nodes []nodeItem) int {
	total := 0
	for _, n := range nodes {
		if !healthOfNode(n).schedulable() {
			continue
		}
		if v, err := strconv.Atoi(n.Status.Allocatable.Pods); err == nil {
			total += v
		}
	}
	return total
}

// occupiedSlots counts the pods actually holding a slot.
//
// Succeeded and Failed pods are excluded because the kubelet's max-pods limit
// counts non-terminated pods only: a finished Job still listed by the API
// server is not occupying scheduling capacity. Counting them would make a
// cluster that runs a lot of CronJobs look permanently much fuller than it is.
func occupiedSlots(pods []podItem) int {
	n := 0
	for _, p := range pods {
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		n++
	}
	return n
}

// nodeTrouble splits nodes into the three things an overview reports about
// them: the ones that are not Ready, the ones deliberately cordoned, and the
// ones under a resource pressure the kubelet is reporting.
func nodeTrouble(nodes []nodeItem) (notReady, cordoned, pressured []string) {
	for _, n := range nodes {
		h := healthOfNode(n)
		switch {
		case !h.ready:
			notReady = append(notReady, n.Metadata.Name+" ("+h.status+")")
		case h.cordoned:
			cordoned = append(cordoned, n.Metadata.Name)
		}
		for _, p := range h.pressures {
			pressured = append(pressured, n.Metadata.Name+" ("+p+")")
		}
	}
	return notReady, cordoned, pressured
}

func runNodeList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	nodes, verr := fetchNodes(ctx, s)
	if verr != nil {
		return nil, verr
	}
	return nodeTable(nodes.Items), nil
}

func nodeTable(nodes []nodeItem) view.Table {
	cols := []view.Column{
		{Name: "node"},
		{Name: "status", Kind: view.KindStatus},
		{Name: "pressure"},
		{Name: "pods", Kind: view.KindNumber},
		{Name: "version"},
		{Name: "age", Kind: view.KindDuration},
	}
	rows := make([][]string, 0, len(nodes))
	for _, n := range nodes {
		h := healthOfNode(n)
		pressure := ""
		if len(h.pressures) > 0 {
			pressure = truncate(h.pressures, 3)
		}
		rows = append(rows, []string{
			n.Metadata.Name, h.status, pressure,
			n.Status.Allocatable.Pods, n.Status.NodeInfo.KubeletVersion,
			age(n.Metadata.CreationTimestamp),
		})
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}
}
