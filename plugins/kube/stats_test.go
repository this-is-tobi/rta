package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func summaryFrom(t *testing.T, body string) summaryStats {
	t.Helper()
	var s summaryStats
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The single most important decision in this file, pinned against real data.
//
// These numbers are a verbatim reading from a live node stalling on CPU a
// third of the time: `some` at 33.44/33.62/35.01 while `full` sat at 0/0/0
// with a cumulative total of exactly zero nanoseconds since boot, against
// some's 1.26 trillion. That zero is structural — CPU `full` is defined as
// zero at system level — so a reader that took `full` would not under-report
// the pressure, it would report none, and the row would look perfectly healthy
// while the node was not.
func TestPressureReadsTheSomeSeriesAndNotFull(t *testing.T) {
	s := summaryFrom(t, `{"node":{"nodeName":"pi1","cpu":{"psi":{
		"full":{"total":0,"avg10":0,"avg60":0,"avg300":0},
		"some":{"total":1256937826314,"avg10":33.44,"avg60":33.62,"avg300":35.01}}}}}`)

	avg10, avg300, ok := pressureOf(s.Node.CPU.PSI)
	if !ok {
		t.Fatal("PSI present but reported as unavailable")
	}
	if avg10 != 33.44 {
		t.Errorf("avg10 = %v, want 33.44 — the some series, not full", avg10)
	}
	if avg300 != 35.01 {
		t.Errorf("avg300 = %v, want 35.01 — the some series, not full", avg300)
	}
}

// PSI needs cgroup v2 and a 4.20-or-newer kernel. A cluster without it is not
// broken, and must not read as a cluster under zero pressure — which is what
// a missing block decodes to if the field is a value rather than a pointer.
func TestAKernelWithoutPSIIsUnavailableRatherThanZero(t *testing.T) {
	s := summaryFrom(t, `{"node":{"nodeName":"old","cpu":{},"memory":{},"io":{}}}`)
	if _, _, ok := pressureOf(s.Node.CPU.PSI); ok {
		t.Error("a node with no PSI block reported pressure as available")
	}
	rows, unsupported := pressureRows([]nodeSummary{{node: "old", stats: s}})
	if len(rows) != 0 {
		t.Errorf("rows = %v, want the node reported as unsupported rather than as a row", rows)
	}
	if len(unsupported) != 1 || unsupported[0] != "old" {
		t.Errorf("unsupported = %v, want [old]", unsupported)
	}
}

// Worst current pressure leads, so the node closest to trouble is the first
// thing read regardless of its name.
func TestPressureRowsLeadWithTheWorstNode(t *testing.T) {
	quiet := summaryFrom(t, `{"node":{"cpu":{"psi":{"some":{"avg10":1,"avg300":1}}}}}`)
	busy := summaryFrom(t, `{"node":{"cpu":{"psi":{"some":{"avg10":40,"avg300":20}}}}}`)
	rows, _ := pressureRows([]nodeSummary{{node: "aaa-quiet", stats: quiet}, {node: "zzz-busy", stats: busy}})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].node != "zzz-busy" {
		t.Errorf("first row = %q, want the busiest node despite its name sorting last", rows[0].node)
	}
}

// A node that could not be read is a row saying so, not an omission: a stats
// endpoint refusing is exactly the nodes/proxy permission being absent, and
// silently returning fewer rows would read as a healthy cluster.
func TestAnUnreadableNodeBecomesAVisibleRow(t *testing.T) {
	rows, unsupported := pressureRows([]nodeSummary{
		{node: "pi1", err: view.Errorf("kube.forbidden", "nodes/proxy is forbidden")},
	})
	if len(unsupported) != 0 {
		t.Errorf("unsupported = %v, want a failure kept apart from an unsupported kernel", unsupported)
	}
	if len(rows) != 1 || rows[0].failed == "" {
		t.Fatalf("rows = %+v, want one row carrying the failure", rows)
	}
	if !strings.Contains(rows[0].failed, "forbidden") {
		t.Errorf("failed = %q, want the API server's own reason", rows[0].failed)
	}
}

// One claim mounted on several nodes at once is one volume, not several. The
// naive append-per-node version lists it repeatedly, which reads as several
// volumes in trouble rather than one — and the fullest reading is the one that
// matters, since they all describe the same filesystem.
func TestAClaimMountedOnSeveralNodesIsReportedOnce(t *testing.T) {
	body := func(used int) string {
		return `{"pods":[{"volume":[{"name":"data","usedBytes":` +
			itoa(used) + `,"capacityBytes":1000,"pvcRef":{"name":"shared","namespace":"apps"}}]}]}`
	}
	rows, failed := worstByClaim([]nodeSummary{
		{node: "a", stats: summaryFrom(t, body(300))},
		{node: "b", stats: summaryFrom(t, body(900))},
		{node: "c", stats: summaryFrom(t, body(100))},
	})
	if len(failed) != 0 {
		t.Errorf("failed = %v, want none", failed)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want the claim reported exactly once", len(rows))
	}
	if rows[0].used != 900 {
		t.Errorf("used = %d, want 900 — the fullest of the three readings", rows[0].used)
	}
}

// Volumes that are not claims outnumber the real ones roughly two to one on an
// ordinary node — ConfigMaps, Secrets, projected tokens, emptyDirs all appear
// in the same list. A capacity of zero is the kubelet not having measured yet,
// and dividing by it puts "+Inf%" in a column somebody scans for a number near
// 100.
func TestNonClaimsAndUnmeasuredVolumesAreLeftOut(t *testing.T) {
	rows, _ := worstByClaim([]nodeSummary{{node: "a", stats: summaryFrom(t, `{"pods":[{"volume":[
		{"name":"kube-api-access","usedBytes":10,"capacityBytes":1000},
		{"name":"unmeasured","usedBytes":0,"capacityBytes":0,"pvcRef":{"name":"pending","namespace":"apps"}},
		{"name":"data","usedBytes":500,"capacityBytes":1000,"pvcRef":{"name":"real","namespace":"apps"}}]}]}`)}})
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want only the one measured claim", rows)
	}
	if rows[0].name != "real" {
		t.Errorf("claim = %q, want real", rows[0].name)
	}
}

// Fullest first: this view exists to answer "what is about to fill up", and
// that answer has to be the first row.
func TestClaimsAreOrderedFullestFirst(t *testing.T) {
	rows, _ := worstByClaim([]nodeSummary{{node: "a", stats: summaryFrom(t, `{"pods":[{"volume":[
		{"usedBytes":100,"capacityBytes":1000,"pvcRef":{"name":"roomy","namespace":"apps"}},
		{"usedBytes":984,"capacityBytes":1000,"pvcRef":{"name":"nearly-full","namespace":"apps"}},
		{"usedBytes":500,"capacityBytes":1000,"pvcRef":{"name":"half","namespace":"apps"}}]}]}`)}})
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.name)
	}
	want := []string{"nearly-full", "half", "roomy"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// A node that failed means claims that are missing, so it is named rather than
// dropped: a short table that looks complete is worse than one admitting a gap.
func TestAFailedNodeIsNamedSoTheGapIsVisible(t *testing.T) {
	_, failed := worstByClaim([]nodeSummary{
		{node: "pi2", err: view.Errorf("kube.forbidden", "nodes/proxy is forbidden")},
	})
	if len(failed) != 1 || failed[0] != "pi2" {
		t.Errorf("failed = %v, want [pi2]", failed)
	}
}

// The node name is interpolated into an API server URL path, and the general
// checkName is the wrong tool for that: nameRe deliberately permits `:` and
// `/` because real context names carry them (`arn:aws:eks:...`), which is fine
// for an argv element and exactly wrong for a path segment.
//
// The escalation this closes is concrete rather than theoretical. The node
// proxy forwards everything after `/proxy/` to the kubelet, which serves
// `/exec/{namespace}/{pod}/{container}` — so
//
//	node = "pi1/proxy/exec/default/mypod/mycontainer"
//
// turns /api/v1/nodes/<node>/proxy/stats/summary into a path reaching exec on
// an arbitrary pod. The `node` input is not Local, so a remote caller may set
// it: without this check a read-only stats capability is a route to remote
// code execution.
func TestAnInjectableNodeNameNeverReachesTheStatsPath(t *testing.T) {
	for _, name := range []string{
		"pi1/proxy/exec/default/mypod/mycontainer", // the real escalation
		"../../secrets",
		"pi1/proxy/exec",
		"-kubeconfig=/tmp/theirs",
		"pi1:6443",
		"PI1", // node names are lowercase; an uppercase one is not a real node
	} {
		if _, verr := fetchSummary(t.Context(), selection{}, name); verr == nil {
			t.Errorf("%q was accepted as a node name", name)
		} else if verr.Code != "kube.name.invalid" {
			t.Errorf("%q refused with %s, want kube.name.invalid", name, verr.Code)
		}
	}
}

// The refusal has to happen before any cluster call, not as a consequence of
// one failing — an API server that happened to accept the path is not a
// defence, and neither is a kubectl that happens to be absent.
func TestAnInjectableNodeNameIsRefusedBeforeKubectlRuns(t *testing.T) {
	orig := kubectlBin
	kubectlBin = "/nonexistent/kubectl-must-not-run"
	t.Cleanup(func() { kubectlBin = orig })

	_, verr := fetchSummary(t.Context(), selection{}, "pi1/proxy/exec/default/mypod/mycontainer")
	if verr == nil {
		t.Fatal("the injectable node name was accepted")
	}
	// kube.kubectl.missing would mean the name got as far as building a
	// command; kube.name.invalid means it never did.
	if verr.Code != "kube.name.invalid" {
		t.Errorf("refused with %s, want kube.name.invalid before any exec", verr.Code)
	}
}

// Ordinary node names must still work — a validator that refuses everything
// passes the tests above and breaks the capability.
func TestRealNodeNamesAreStillAccepted(t *testing.T) {
	for _, name := range []string{
		"pi1", "node-1", "ip-10-0-1-23.eu-west-1.compute.internal",
		"gke-cluster-default-pool-1a2b3c4d-x9yz", "a",
	} {
		if verr := checkNodeName(name); verr != nil {
			t.Errorf("%q was refused: %v", name, verr)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
