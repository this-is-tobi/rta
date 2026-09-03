package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.metrics.pressure and kube.pvc.usage: the two answers that live in the
// kubelet's own Summary API and nowhere else.
//
// Both read /api/v1/nodes/<node>/proxy/stats/summary, which is why they share
// a file. That endpoint is qualitatively different from everything else this
// plugin reads, in two ways worth knowing before using either.
//
// # It costs a permission you should think about
//
// Reaching it needs `nodes/proxy`, and that is one indivisible permission
// covering the *whole* kubelet API — including /exec, /run and /logs on every
// pod on the node. There is no way to grant "just the stats". An operator
// running these from their own kubeconfig already has it; a ServiceAccount
// minted to run them would be handed remote code execution on every workload
// in the cluster, which is why neither capability appears in rbac.go's
// provisionable table and why that is not a gap to fill later.
//
// # It is one call per node
//
// The Summary API is served by each kubelet about itself, so a cluster-wide
// answer is a fan-out. Nodes are read concurrently and bounded, non-Ready
// nodes are skipped rather than waited on — a kubelet that is not answering
// the API server will not answer this either, and a timeout per dead node is
// the difference between a view that returns and one that appears hung — and
// a node that fails is named in the result rather than silently dropped.

// psiSeries is one Pressure Stall Information window: the share of a ten-,
// sixty- and three-hundred-second window during which tasks were stalled
// waiting for a resource.
//
// Three points on one curve is the reason this is worth reading at all. A
// single "CPU is at 80%" number cannot tell you whether that is a spike
// levelling off or a slope you are about to fall down, and answering it
// normally means a time-series database. avg10 against avg300 answers it in
// one call: rising when the short window is above the long one, recovering
// when it is below.
type psiSeries struct {
	Total  float64 `json:"total"`
	Avg10  float64 `json:"avg10"`
	Avg60  float64 `json:"avg60"`
	Avg300 float64 `json:"avg300"`
}

// psi carries both series the kernel exposes. Only Some is reported — see
// pressureOf for why, which is a correctness matter and not a preference.
type psi struct {
	Full psiSeries `json:"full"`
	Some psiSeries `json:"some"`
}

// volumeStat is one mounted volume as the kubelet measures it. PVCRef is nil
// for everything that is not a PersistentVolumeClaim — ConfigMaps, Secrets,
// projected tokens and emptyDirs all appear here too, and outnumber the real
// claims roughly two to one on an ordinary node.
type volumeStat struct {
	Name           string `json:"name"`
	AvailableBytes uint64 `json:"availableBytes"`
	CapacityBytes  uint64 `json:"capacityBytes"`
	UsedBytes      uint64 `json:"usedBytes"`
	PVCRef         *struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"pvcRef,omitempty"`
}

// summaryStats is the part of the Summary API these two capabilities read.
type summaryStats struct {
	Node struct {
		NodeName string `json:"nodeName"`
		CPU      struct {
			PSI *psi `json:"psi,omitempty"`
		} `json:"cpu"`
		Memory struct {
			PSI *psi `json:"psi,omitempty"`
		} `json:"memory"`
		IO struct {
			PSI *psi `json:"psi,omitempty"`
		} `json:"io"`
	} `json:"node"`
	Pods []struct {
		Volume []volumeStat `json:"volume"`
	} `json:"pods"`
}

// summaryConcurrency bounds the fan-out. Chosen to keep a large cluster's
// wall-clock reasonable without opening one connection per node at once: the
// API server proxies every one of these, and a hundred simultaneous kubelet
// proxies is a burst it has no reason to absorb on a read somebody ran out of
// curiosity.
const summaryConcurrency = 8

// nodeSummary pairs a node's stats with whatever went wrong reading them, so
// a partial answer can say which part is missing.
type nodeSummary struct {
	node  string
	stats summaryStats
	err   *view.Error
}

// nodeNameRe is what may be interpolated into the stats URL path, and it is
// deliberately much stricter than nameRe.
//
// **checkName is not sufficient here, and assuming it was is a live
// vulnerability rather than a tidiness point.** nameRe permits `:` and `/`
// on purpose — real kubeconfig context names carry both, `arn:aws:eks:...`
// among them — because its job is argv safety, where the only hazard is a
// value being read as a flag and the `--flag=value` form already prevents it.
// A URL path has the opposite property: `/` is the structure, so a value
// containing one is not a malformed name, it is a different request.
//
// Concretely, the node proxy forwards everything after `/proxy/` to the
// kubelet, and the kubelet serves `/exec/{namespace}/{pod}/{container}`. So a
// node of `pi1/proxy/exec/default/mypod/mycontainer` turns
//
//	/api/v1/nodes/<node>/proxy/stats/summary
//
// into a path reaching exec on an arbitrary pod. The `node` input is not
// Local, so a remote caller may set it — which makes this the difference
// between a read-only stats capability and remote code execution reachable
// through one.
//
// The character set is a Kubernetes node name and nothing else: an RFC 1123
// subdomain. No colon, no slash, no `@`, and never a leading dot.
var nodeNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

func checkNodeName(v string) *view.Error {
	if v == "" {
		return nil
	}
	if !nodeNameRe.MatchString(v) {
		return view.Errorf("kube.name.invalid", "%q is not a usable node name", v).
			WithHint("node names are lowercase letters, digits, dots and dashes — " +
				"this one goes into an API server URL, so it is held to that and nothing wider")
	}
	return nil
}

func fetchSummary(ctx context.Context, s selection, node string) (summaryStats, *view.Error) {
	var out summaryStats
	// Checked here as well as at the entry point, because this is the function
	// that builds the path: a later caller reaching it by another route must
	// not be able to skip the validation by not knowing it was needed.
	if verr := checkNodeName(node); verr != nil {
		return out, verr
	}
	path := "/api/v1/nodes/" + node + "/proxy/stats/summary"
	if verr := getRawJSON(ctx, s, path, &out); verr != nil {
		return out, verr
	}
	return out, nil
}

// fetchSummaries reads every schedulable node's stats concurrently.
//
// Non-Ready nodes are skipped deliberately rather than attempted and reported:
// their kubelet is by definition not answering, so each one would cost the
// full request timeout to learn something kube.node.list already says plainly.
func fetchSummaries(ctx context.Context, s selection, nodes []nodeItem) []nodeSummary {
	var wanted []string
	for _, n := range nodes {
		if healthOfNode(n).ready {
			wanted = append(wanted, n.Metadata.Name)
		}
	}
	out := make([]nodeSummary, len(wanted))
	sem := make(chan struct{}, summaryConcurrency)
	var wg sync.WaitGroup
	for i, name := range wanted {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, verr := fetchSummary(ctx, s, name)
			out[i] = nodeSummary{node: name, stats: st, err: verr}
		}()
	}
	wg.Wait()
	return out
}

// nodesForStats resolves the node set both capabilities fan out over, honoring
// a caller-named single node.
func nodesForStats(ctx context.Context, s selection, only string) ([]nodeItem, *view.Error) {
	if verr := checkNodeName(only); verr != nil {
		return nil, verr
	}
	nodes, verr := fetchNodes(ctx, s)
	if verr != nil {
		return nil, verr
	}
	if only == "" {
		return nodes.Items, nil
	}
	for _, n := range nodes.Items {
		if n.Metadata.Name == only {
			return []nodeItem{n}, nil
		}
	}
	return nil, view.Errorf("kube.node.unknown", "%s has no node called %q", s.where(), only).
		WithHint("`rta kube node list` shows the nodes this cluster has")
}

// pressureOf picks the series to report, and the choice is a correctness one.
//
// The kernel exposes two: `some` is the share of the window during which *at
// least one* task was stalled on the resource, `full` the share during which
// *every* runnable task was. Only `some` is reported, uniformly, for two
// reasons that point the same way.
//
// For CPU, `full` is structurally zero at system level — not usually zero,
// definitionally so. Measured on a real node stalling a third of the time:
// cpu.psi.some read avg10 33.44 / avg60 33.62 / avg300 35.01 while
// cpu.psi.full read 0/0/0 with a cumulative total of exactly 0 nanoseconds
// since boot, against some's 1.26 trillion. Reading `full` there does not
// under-report CPU pressure, it reports none at all, and the row looks
// perfectly healthy while the node is not.
//
// For memory and IO `full` is meaningful but late: by the time every task is
// stalled the node is already failing in ways kube.overview's not-ready and
// eviction signals surface on their own. `some` is the leading indicator,
// which is what a view meant to give a head start should carry — and a mixed
// policy would put differently-defined numbers in adjacent columns of one
// table, which is how somebody comes to compare them.
func pressureOf(p *psi) (avg10, avg300 float64, ok bool) {
	if p == nil {
		return 0, 0, false
	}
	return p.Some.Avg10, p.Some.Avg300, true
}

// pressureRow is one node's line, kept separate from rendering so the
// ordering and the unsupported/failed split can be tested without a cluster.
type pressureRow struct {
	node   string
	cells  []string
	worst  float64
	failed string
}

// pressureRows reduces the fan-out to rows, worst current pressure first —
// the same ordering kube.metrics.pod uses and for the same reason: the node
// closest to trouble leads regardless of its name. Nodes whose kernel reports
// no PSI at all come back separately rather than as rows of blanks.
func pressureRows(summaries []nodeSummary) (rows []pressureRow, unsupported []string) {
	for _, sum := range summaries {
		if sum.err != nil {
			rows = append(rows, pressureRow{node: sum.node, failed: sum.err.Message})
			continue
		}
		cpu10, cpu300, cpuOK := pressureOf(sum.stats.Node.CPU.PSI)
		mem10, mem300, memOK := pressureOf(sum.stats.Node.Memory.PSI)
		io10, io300, ioOK := pressureOf(sum.stats.Node.IO.PSI)
		if !cpuOK && !memOK && !ioOK {
			// PSI needs cgroup v2 and a 4.20-or-newer kernel. A cluster
			// without it is not broken, and saying so once beats printing a
			// row of dashes per node with no explanation.
			unsupported = append(unsupported, sum.node)
			continue
		}
		rows = append(rows, pressureRow{
			node:  sum.node,
			worst: maxOf(cpu10, mem10, io10),
			cells: []string{
				pct(cpu10, cpuOK), pct(cpu300, cpuOK),
				pct(mem10, memOK), pct(mem300, memOK),
				pct(io10, ioOK), pct(io300, ioOK),
			},
		})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].worst > rows[j].worst })
	return rows, unsupported
}

func runMetricsPressure(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	nodes, verr := nodesForStats(ctx, s, req.String("node"))
	if verr != nil {
		return nil, verr
	}
	rows, unsupported := pressureRows(fetchSummaries(ctx, s, nodes))

	cols := pressureColumns()
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		if r.failed != "" {
			out = append(out, []string{r.node, "could not be read — " + r.failed, "", "", "", "", ""})
			continue
		}
		out = append(out, append([]string{r.node}, r.cells...))
	}
	table := view.Table{Columns: cols, Rows: out, Total: len(out)}
	if len(unsupported) == 0 {
		return table, nil
	}
	return view.Sections{Items: []view.Section{
		{ID: "pressure", Title: "Pressure", View: table},
		{ID: "unsupported", Title: "Nodes not reporting pressure",
			View: view.Text{Body: truncate(unsupported, 12) +
				"\n\nPSI needs cgroup v2 and a Linux kernel of 4.20 or newer."}},
	}}, nil
}

func maxOf(vs ...float64) float64 {
	out := 0.0
	for _, v := range vs {
		if v > out {
			out = v
		}
	}
	return out
}

// pct renders a PSI average, which the kernel already reports as a percentage
// rather than a ratio — so percentOf, which divides, is the wrong helper here.
// pvcUsageColumns: used against the claim's own capacity, so 100 is a volume
// that has stopped accepting writes — KindUsage, which the renderer grades
// green, amber and red.
func pvcUsageColumns() []view.Column {
	return []view.Column{
		{Name: "namespace"}, {Name: "claim"},
		{Name: "used", Kind: view.KindBytes},
		{Name: "capacity", Kind: view.KindBytes},
		{Name: "used %", Kind: view.KindUsage},
	}
}

// pressureColumns: KindPercent and deliberately not KindUsage, which is the
// difference between a graded column and a misleading one.
//
// PSI is the share of an interval tasks spent stalled, not a share of a
// capacity. A node at 40% memory pressure is in serious trouble and one at 8%
// sustained io pressure has a disk that cannot keep up — both of which a
// capacity's 80/90 bands would paint green, because they are nowhere near
// either. There is no single threshold to use instead, since cpu, memory and
// io pressure become interesting at different values, so the number is shown
// and the reading is left to somebody who knows which of the three they are
// looking at. This is the same restraint kube.pvc.list applies when it
// declines to show a percentage it cannot compute.
func pressureColumns() []view.Column {
	return []view.Column{
		{Name: "node"},
		{Name: "cpu 10s", Kind: view.KindPercent},
		{Name: "cpu 5m", Kind: view.KindPercent},
		{Name: "memory 10s", Kind: view.KindPercent},
		{Name: "memory 5m", Kind: view.KindPercent},
		{Name: "io 10s", Kind: view.KindPercent},
		{Name: "io 5m", Kind: view.KindPercent},
	}
}

func pct(v float64, ok bool) string {
	if !ok {
		return ""
	}
	return fmt.Sprintf("%.1f%%", v)
}

// claimUsage is one PersistentVolumeClaim as the kubelet measures it.
type claimUsage struct {
	ns, name       string
	used, capacity uint64
	ratio          float64
}

// worstByClaim reduces the fan-out to one row per claim, fullest first, and
// names the nodes that could not be read.
//
// Keyed by claim rather than appended per node, because one PVC can be mounted
// on several nodes at once (ReadWriteMany) and would otherwise appear once per
// node — the same volume listed repeatedly, which reads as several volumes in
// trouble rather than one. The fullest reading wins: those readings describe
// one underlying filesystem, and the highest is the one that matters.
//
// A zero capacity is skipped rather than divided by. The kubelet reports one
// for a volume it has not finished measuring, and "NaN%" or "+Inf%" in a
// column somebody scans for a number near 100 is worse than an absent row.
func worstByClaim(summaries []nodeSummary) (rows []claimUsage, failed []string) {
	seen := map[string]claimUsage{}
	for _, sum := range summaries {
		if sum.err != nil {
			failed = append(failed, sum.node)
			continue
		}
		for _, p := range sum.stats.Pods {
			for _, v := range p.Volume {
				if v.PVCRef == nil || v.CapacityBytes == 0 {
					continue
				}
				key := v.PVCRef.Namespace + "/" + v.PVCRef.Name
				u := claimUsage{
					ns: v.PVCRef.Namespace, name: v.PVCRef.Name,
					used: v.UsedBytes, capacity: v.CapacityBytes,
					ratio: float64(v.UsedBytes) / float64(v.CapacityBytes),
				}
				if prev, ok := seen[key]; ok && prev.ratio >= u.ratio {
					continue
				}
				seen[key] = u
			}
		}
	}
	rows = make([]claimUsage, 0, len(seen))
	for _, u := range seen {
		rows = append(rows, u)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ratio != rows[j].ratio {
			return rows[i].ratio > rows[j].ratio
		}
		return rows[i].ns+"/"+rows[i].name < rows[j].ns+"/"+rows[j].name
	})
	return rows, failed
}

func runPVCUsage(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	nodes, verr := nodesForStats(ctx, s, req.String("node"))
	if verr != nil {
		return nil, verr
	}
	rows, failed := worstByClaim(fetchSummaries(ctx, s, nodes))

	cols := pvcUsageColumns()
	out := make([][]string, 0, len(rows))
	for _, u := range rows {
		out = append(out, []string{u.ns, u.name, format.Bytes(u.used), format.Bytes(u.capacity),
			percentOf(float64(u.used), float64(u.capacity))})
	}
	table := view.Table{Columns: cols, Rows: out, Total: len(out)}
	if len(failed) == 0 {
		return table, nil
	}
	// Named rather than dropped: a missing node means missing claims, and a
	// short list that looks complete is worse than a long one that admits a
	// gap.
	return view.Sections{Items: []view.Section{
		{ID: "usage", Title: "Volume usage", View: table},
		{ID: "unread", Title: "Nodes that could not be read",
			View: view.Text{Body: truncate(failed, 12) +
				"\n\nClaims mounted only on these nodes are missing from the table above."}},
	}}, nil
}
