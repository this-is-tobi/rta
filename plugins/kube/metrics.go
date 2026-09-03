package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// podMetricColumns and nodeMetricColumns are named rather than inline because
// the kinds they declare are the whole of what makes a figure graded: KindUsage
// is a percentage of something with a ceiling — a container's limit, a node's
// allocatable — where nearing 100 is nearing the throttle or the OOM kill.
// TestOnlyTheColumnsWithACapacityBehindThemAreGraded is what holds that apart
// from the percentages in this same package that have no bad end.
func podMetricColumns(allNamespaces bool) []view.Column {
	cols := []view.Column{}
	if allNamespaces {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	return append(cols,
		view.Column{Name: "pod"},
		view.Column{Name: "cpu", Kind: view.KindNumber},
		view.Column{Name: "cpu %", Kind: view.KindUsage},
		view.Column{Name: "memory", Kind: view.KindBytes},
		view.Column{Name: "memory %", Kind: view.KindUsage},
	)
}

func nodeMetricColumns() []view.Column {
	return []view.Column{
		{Name: "node"},
		{Name: "cpu", Kind: view.KindNumber},
		{Name: "cpu %", Kind: view.KindUsage},
		{Name: "memory", Kind: view.KindBytes},
		{Name: "memory %", Kind: view.KindUsage},
	}
}

// kube.metrics.pod / kube.metrics.node: usage against what was actually
// asked for, not a `kubectl top` clone.
//
// Raw usage numbers answer "how busy is this pod" for one pod somebody
// already suspects; the SRE question is "which pods are close enough to
// their own limit to be the next OOMKilled or throttled one", which needs
// the limit joined in. Both come from the same `kubectl get -o json` shape
// this plugin already reads (pods, nodes) plus one more source: the
// metrics.k8s.io aggregation API, reached through `kubectl get --raw`
// because `kubectl top` has never had reliable `-o json` support across
// versions, and this plugin's own error classification (kubectl.go) already
// exists to turn a raw API response into something readable — `--raw`
// composes with that for free, `top`'s own text output would not.
//
// metrics.k8s.io is the metrics-server add-on, not a given: a cluster
// without it answers this with "the server could not find the requested
// resource", which classify() already turns into kube.notfound. That code is
// generic across every "not found" this plugin can hit, so the specific,
// actionable hint is added here rather than in the shared classifier.

// rawArgs builds `kubectl get --raw <path>` arguments. Deliberately not
// selection.args(): --namespace and --all-namespaces are flags of the
// resource-listing machinery `--raw` bypasses entirely, and passing them
// would be an error kubectl reports about the wrong thing, the same class of
// bug selection.args' own doc comment already describes for --all-namespaces
// placed before the verb. The namespace, when there is one, is already
// embedded in path by the caller.
func rawArgs(s selection, path string) []string {
	out := []string{"get", "--raw", path, "--request-timeout=" + requestTimeout}
	if s.Context != "" {
		out = append(out, "--context="+s.Context)
	}
	return out
}

func getRawJSON(ctx context.Context, s selection, path string, out any) *view.Error {
	raw, verr := run(ctx, rawArgs(s, path)...)
	if verr != nil {
		if verr.Code == "kube.notfound" {
			return verr.WithHint("the metrics-server add-on is not installed on this cluster, " +
				"or metrics.k8s.io is not yet ready — `kubectl get apiservices` shows whether it is registered")
		}
		return verr
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return view.Errorf("kube.unreadable", "kubectl's answer for %s could not be read: %v", path, err)
	}
	return nil
}

type podMetricsItem struct {
	Metadata   meta `json:"metadata"`
	Containers []struct {
		Usage struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"containers"`
}

// podSpecItem reads only what pressure needs: every container's limits,
// summed per pod the same way the kubelet sums them for eviction. Requests
// are not read — a limit is the number that gets a container OOMKilled or
// CPU-throttled, which is the failure mode this view exists to give a head
// start on; a request is a scheduling input, a different question.
type podSpecItem struct {
	Metadata meta `json:"metadata"`
	Spec     struct {
		Containers []struct {
			Resources struct {
				Limits map[string]string `json:"limits"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
}

func runMetricsPod(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if !s.AllNS && s.Namespace != "" {
		path = "/apis/metrics.k8s.io/v1beta1/namespaces/" + s.Namespace + "/pods"
	}
	var metrics list[podMetricsItem]
	if verr := getRawJSON(ctx, s, path, &metrics); verr != nil {
		return nil, verr
	}
	var specs list[podSpecItem]
	specErr := getJSON(ctx, s, "pods", &specs)
	requests := make(map[string]podBudget, len(specs.Items))
	if specErr == nil {
		for _, p := range specs.Items {
			requests[p.Metadata.Namespace+"/"+p.Metadata.Name] = budgetOf(p)
		}
	}

	type row struct {
		ns, name       string
		cpu, mem       float64
		cpuLim, memLim float64
		cpuPct, memPct string
		cpuPctN        float64
		memPctN        float64
	}
	rows := make([]row, 0, len(metrics.Items))
	for _, m := range metrics.Items {
		var cpu, mem float64
		for _, c := range m.Containers {
			if v, ok := parseCPU(c.Usage.CPU); ok {
				cpu += v
			}
			if v, ok := parseBytes(c.Usage.Memory); ok {
				mem += float64(v)
			}
		}
		b := requests[m.Metadata.Namespace+"/"+m.Metadata.Name]
		r := row{ns: m.Metadata.Namespace, name: m.Metadata.Name, cpu: cpu, mem: mem,
			cpuLim: b.cpuLimit, memLim: b.memLimit}
		if b.memLimit > 0 {
			r.memPct, r.memPctN = percentOf(mem, b.memLimit), mem/b.memLimit
		}
		if b.cpuLimit > 0 {
			r.cpuPct, r.cpuPctN = percentOf(cpu, b.cpuLimit), cpu/b.cpuLimit
		}
		rows = append(rows, r)
	}
	// Worst memory pressure first — OOMKilled is the failure mode this view
	// exists to give an SRE a head start on, so the pod closest to it leads.
	// CPU pressure as the tiebreaker for pods with no memory limit set at
	// all, rather than falling back to an arbitrary name sort.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].memPctN != rows[j].memPctN {
			return rows[i].memPctN > rows[j].memPctN
		}
		return rows[i].cpuPctN > rows[j].cpuPctN
	})

	cols := podMetricColumns(s.AllNS)
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		line := []string{}
		if s.AllNS {
			line = append(line, r.ns)
		}
		out = append(out, append(line, r.name, cpuCores(r.cpu), r.cpuPct, format.Bytes(uint64(r.mem)), r.memPct))
	}
	return view.Table{Columns: cols, Rows: out, Total: len(out)}, nil
}

// podBudget is one pod's summed container requests/limits — the ceiling
// usage is measured against, not the usage itself.
type podBudget struct {
	cpuLimit, memLimit float64
}

func budgetOf(p podSpecItem) podBudget {
	var b podBudget
	for _, c := range p.Spec.Containers {
		if v, ok := parseCPU(c.Resources.Limits["cpu"]); ok {
			b.cpuLimit += v
		}
		if v, ok := parseBytes(c.Resources.Limits["memory"]); ok {
			b.memLimit += float64(v)
		}
	}
	return b
}

// cpuCores renders a core count the way `kubectl top` does for anything
// under a whole core: millicores below 1, two decimal cores at and above it.
func cpuCores(cores float64) string {
	if cores < 1 {
		return fmt.Sprintf("%dm", int(cores*1000))
	}
	return fmt.Sprintf("%.2f", cores)
}

type nodeMetricsItem struct {
	Metadata meta `json:"metadata"`
	Usage    struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	} `json:"usage"`
}

func runMetricsNode(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	var metrics list[nodeMetricsItem]
	if verr := getRawJSON(ctx, s, "/apis/metrics.k8s.io/v1beta1/nodes", &metrics); verr != nil {
		return nil, verr
	}
	var nodes list[nodeItem]
	nodeErr := getJSON(ctx, s, "nodes", &nodes)
	allocatable := make(map[string]nodeItem, len(nodes.Items))
	if nodeErr == nil {
		for _, n := range nodes.Items {
			allocatable[n.Metadata.Name] = n
		}
	}

	sort.Slice(metrics.Items, func(i, j int) bool {
		return metrics.Items[i].Metadata.Name < metrics.Items[j].Metadata.Name
	})
	cols := nodeMetricColumns()
	rows := make([][]string, 0, len(metrics.Items))
	for _, m := range metrics.Items {
		cpu, _ := parseCPU(m.Usage.CPU)
		mem, _ := parseBytes(m.Usage.Memory)
		var cpuPct, memPct string
		if n, ok := allocatable[m.Metadata.Name]; ok {
			if allocCPU, ok := parseCPU(n.Status.Allocatable.CPU); ok {
				cpuPct = percentOf(cpu, allocCPU)
			}
			if allocMem, ok := parseBytes(n.Status.Allocatable.Memory); ok {
				memPct = percentOf(float64(mem), float64(allocMem))
			}
		}
		rows = append(rows, []string{m.Metadata.Name, cpuCores(cpu), cpuPct, format.Bytes(mem), memPct})
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}, nil
}
