package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// nodeFrom decodes a Node the way kubectl hands one over, for the same reason
// podFrom does: these cases have to exercise the struct tags the real read
// goes through, not a hand-built literal free to drift from the wire shape.
func nodeFrom(t *testing.T, body string) nodeItem {
	t.Helper()
	var n nodeItem
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Ready is three-valued, and the third value is the one that matters most.
//
// "Unknown" is what the node controller writes when the kubelet has stopped
// checking in — the machine is unreachable rather than unwell — and it is
// precisely the state pod health cannot see, because a kubelet that has
// stopped talking also stopped updating its pods. Collapsing it into NotReady
// would still count the node, but it would send an operator to read logs on a
// box that is not answering.
func TestNodeReadinessDistinguishesBrokenFromGone(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantReady bool
		wantWord  string
	}{
		{
			name:      "ready",
			body:      `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`,
			wantReady: true, wantWord: "Ready",
		},
		{
			name:      "kubelet reporting a problem",
			body:      `{"status":{"conditions":[{"type":"Ready","status":"False"}]}}`,
			wantReady: false, wantWord: "NotReady",
		},
		{
			name:      "kubelet stopped reporting",
			body:      `{"status":{"conditions":[{"type":"Ready","status":"Unknown"}]}}`,
			wantReady: false, wantWord: "Unknown",
		},
		{
			name:      "no Ready condition at all",
			body:      `{"status":{"conditions":[]}}`,
			wantReady: false, wantWord: "Unknown",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := healthOfNode(nodeFrom(t, c.body))
			if h.ready != c.wantReady {
				t.Errorf("ready = %v, want %v", h.ready, c.wantReady)
			}
			if h.status != c.wantWord {
				t.Errorf("status = %q, want %q", h.status, c.wantWord)
			}
		})
	}
}

// The node equivalent of the completed-Job false alarm, and the reason
// healthOfNode splits cordoned out of the not-ready count rather than folding
// it in: a cordoned node is an operator's own decision, mid-upgrade or
// mid-drain, and reporting it as a fault would make kube.overview cry wolf
// during exactly the planned maintenance somebody is already watching.
//
// It still has to be *reported* — a node cordoned for a ten-minute upgrade and
// still cordoned a week later is capacity nobody is using — so this pins both
// halves: out of the count, into its own list.
func TestACordonedButReadyNodeIsReportedWithoutBeingCalledNotReady(t *testing.T) {
	nodes := []nodeItem{
		nodeFrom(t, `{"metadata":{"name":"pi1"},"spec":{"unschedulable":true},
			"status":{"conditions":[{"type":"Ready","status":"True"}]}}`),
		nodeFrom(t, `{"metadata":{"name":"pi2"},
			"status":{"conditions":[{"type":"Ready","status":"True"}]}}`),
	}
	notReady, cordoned, _ := nodeTrouble(nodes)
	if len(notReady) != 0 {
		t.Errorf("not ready = %v, want a cordoned node to be left out of it", notReady)
	}
	if len(cordoned) != 1 || cordoned[0] != "pi1" {
		t.Errorf("cordoned = %v, want [pi1]", cordoned)
	}
	// kubectl's own wording, kept because it carries both facts at once.
	if got := healthOfNode(nodes[0]).status; got != "Ready,SchedulingDisabled" {
		t.Errorf("status = %q, want Ready,SchedulingDisabled", got)
	}
}

// Pressure conditions run the opposite way round from Ready: "True" is the bad
// news. A node in trouble reports `Ready: True` and `MemoryPressure: True`
// together, so code that treats every condition alike reads the second one as
// reassurance and reports nothing.
//
// Both halves asserted deliberately — that the pressure is surfaced, and that
// it does not knock the node out of Ready. Getting either backwards produces a
// plausible-looking overview that is wrong in a different direction.
func TestPressureIsReadWithItsOwnPolarityAndDoesNotUnsetReady(t *testing.T) {
	n := nodeFrom(t, `{"metadata":{"name":"pi1"},"status":{"conditions":[
		{"type":"Ready","status":"True"},
		{"type":"MemoryPressure","status":"True"},
		{"type":"DiskPressure","status":"False"},
		{"type":"PIDPressure","status":"True"}]}}`)
	h := healthOfNode(n)
	if !h.ready {
		t.Error("a node under pressure but still Ready was reported as not ready")
	}
	if len(h.pressures) != 2 {
		t.Fatalf("pressures = %v, want the two conditions that are True", h.pressures)
	}
	joined := strings.Join(h.pressures, ",")
	if !strings.Contains(joined, "MemoryPressure") || !strings.Contains(joined, "PIDPressure") {
		t.Errorf("pressures = %v, want MemoryPressure and PIDPressure", h.pressures)
	}
	if strings.Contains(joined, "DiskPressure") {
		t.Errorf("pressures = %v, want the False condition left out", h.pressures)
	}
}

// Pod-slot headroom is only honest if the denominator counts nodes the
// scheduler will actually place work on. A cordoned node's allocatable pods
// are real and unusable; a NotReady node's are neither. Counting them would
// report headroom that does not exist, in the one view an operator would
// consult to decide whether the cluster can take more.
func TestPodSlotsCountOnlyTheNodesTheSchedulerWillUse(t *testing.T) {
	nodes := []nodeItem{
		nodeFrom(t, `{"metadata":{"name":"ok"},"status":{"allocatable":{"pods":"110"},
			"conditions":[{"type":"Ready","status":"True"}]}}`),
		nodeFrom(t, `{"metadata":{"name":"cordoned"},"spec":{"unschedulable":true},
			"status":{"allocatable":{"pods":"110"},"conditions":[{"type":"Ready","status":"True"}]}}`),
		nodeFrom(t, `{"metadata":{"name":"gone"},"status":{"allocatable":{"pods":"110"},
			"conditions":[{"type":"Ready","status":"Unknown"}]}}`),
	}
	if got := podSlots(nodes); got != 110 {
		t.Errorf("podSlots = %d, want 110 — only the one schedulable node", got)
	}
}

// The kubelet's max-pods counts non-terminated pods, so a finished Job listed
// by the API server is not holding a slot. A cluster running frequent CronJobs
// would otherwise look permanently far fuller than it is.
func TestOccupiedSlotsExcludeTerminalPods(t *testing.T) {
	pods := []podItem{
		podFrom(t, `{"status":{"phase":"Running"}}`),
		podFrom(t, `{"status":{"phase":"Pending"}}`),
		podFrom(t, `{"status":{"phase":"Succeeded"}}`),
		podFrom(t, `{"status":{"phase":"Failed"}}`),
	}
	if got := occupiedSlots(pods); got != 2 {
		t.Errorf("occupiedSlots = %d, want 2 — Succeeded and Failed hold no slot", got)
	}
}

// withKubectlRefusingNodes is a fake kubectl that answers everything except a
// node read, which it refuses the way an API server refuses a namespaced
// credential asking for a cluster-scoped resource.
func withKubectlRefusingNodes(t *testing.T, stdout string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "kubectl")
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"nodes\" ]; then\n" +
		"    echo 'Error from server (Forbidden): nodes is forbidden: User \"sa\" cannot list resource \"nodes\" at the cluster scope' >&2\n" +
		"    exit 1\n" +
		"  fi\n" +
		"done\n" +
		"cat <<'RTA_FIXTURE_EOF'\n" + stdout + "\nRTA_FIXTURE_EOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })
}

// The promise kube.overview's own description makes: a credential that cannot
// list nodes still gets an overview.
//
// Nodes are cluster-scoped, and the narrow namespaced identity
// kube.serviceaccount.provision mints is exactly the kind that can list pods
// and not nodes — so treating a node refusal as fatal would break the
// always-answer view for the most common restricted credential there is. The
// refusal is reported on its own row and everything else still runs, which is
// how quotas and certificates were already handled.
func TestOverviewStillAnswersWhenNodesAreForbidden(t *testing.T) {
	withKubectlRefusingNodes(t, `{"items":[{"metadata":{"name":"gitea"},"status":{"phase":"Active"}}]}`)

	out, err := runOverview(context.Background(),
		plugin.NewRequest(map[string]any{}, false, false))
	if err != nil {
		t.Fatalf("overview refused outright when only the node read was forbidden: %v", err)
	}
	kv, ok := out.(view.KeyValue)
	if !ok {
		t.Fatalf("overview returned %T, want view.KeyValue", out)
	}
	var nodes, pods string
	for _, p := range kv.Pairs {
		switch p.Key {
		case "nodes":
			nodes = p.Value
		case "pods":
			pods = p.Value
		}
	}
	if !strings.HasPrefix(nodes, "could not be read") {
		t.Errorf("nodes = %q, want the refusal reported rather than swallowed", nodes)
	}
	if pods == "" {
		t.Error("the pod count is missing — a forbidden node read stopped the rest of the overview")
	}
	// "0 of 0 used" would be a claim about capacity made from a read that
	// never happened.
	for _, p := range kv.Pairs {
		if p.Key == "pod slots" {
			t.Errorf("pod slots = %q, want no headroom claim when nodes could not be read", p.Value)
		}
	}
}
