package main

import (
	"slices"
	"strings"
	"testing"
)

// The mapping table is rta's own promise that it never mints what
// audit.kube.rbac's wildcardRule would flag on a real cluster. Walked rather
// than spot-checked, so a future entry has to clear this on its own — no
// entry gets to be the one nobody thought to check.
func TestProvisionableRulesNeverUseAWildcard(t *testing.T) {
	for id, rules := range provisionable {
		for _, r := range rules {
			for _, g := range r.APIGroups {
				if g == "*" {
					t.Errorf("%s: apiGroup wildcard", id)
				}
			}
			for _, res := range r.Resources {
				if res == "*" {
					t.Errorf("%s: resource wildcard", id)
				}
			}
			for _, v := range r.Verbs {
				if v == "*" {
					t.Errorf("%s: verb wildcard", id)
				}
			}
		}
	}
}

// kube.cert.list and kube.overview are deliberately absent from
// provisionable — see its own doc comment for why (Secret-type granularity
// RBAC cannot express, and a composite view respectively) — pinned here so
// either reappearing is a decision somebody makes on purpose, not a table
// entry that crept back in.
func TestUngrantableCapabilitiesStayUngrantable(t *testing.T) {
	for _, id := range []string{"kube.cert.list", "kube.overview", "kube.namespace.list",
		"kube.metrics.node", "kube.node.list",
		// These two for a sharper reason than being cluster-scoped: nodes/proxy
		// is indivisible and carries exec on every pod on the node.
		"kube.metrics.pressure", "kube.pvc.usage"} {
		if _, ok := provisionable[id]; ok {
			t.Errorf("%s must not be grantable — see provisionable's doc comment for why", id)
		}
	}
}

// A review caught this the table itself couldn't: runMetricsPod (metrics.go)
// reads core v1 Pods as well as metrics.k8s.io Pods, to get each pod's own
// resource limits for the percentage columns. A grant covering only the
// metrics.k8s.io rule would not error — it would silently render every
// percentage blank, a working-but-wrong result. Pinned against the actual
// Run function's behavior, not just the table's own shape.
func TestMetricsPodGrantsCorePodsToo(t *testing.T) {
	rules, verr := rulesFor([]string{"kube.metrics.pod"})
	if verr != nil {
		t.Fatal(verr)
	}
	found := false
	for _, r := range rules {
		if len(r.APIGroups) == 1 && r.APIGroups[0] == "" &&
			slices.Contains(r.Resources, "pods") && slices.Contains(r.Verbs, "get") {
			found = true
		}
	}
	if !found {
		t.Errorf("kube.metrics.pod's rules = %v, missing a core-group pods read runMetricsPod needs for limits", rules)
	}
}

func TestRulesForRefusesAnUnmappedCapability(t *testing.T) {
	_, verr := rulesFor([]string{"kube.pod.list", "kube.cert.list"})
	if verr == nil {
		t.Fatal("an unmapped capability was silently accepted")
	}
	if verr.Code != "kube.serviceaccount.ungrantable" || verr.Hint == "" {
		t.Errorf("want a coded, hinted refusal, got %+v", verr)
	}
	if !strings.Contains(verr.Message, "kube.cert.list") {
		t.Errorf("error does not name the offending capability: %q", verr.Message)
	}
}

func TestRulesForRefusesAnEmptyList(t *testing.T) {
	_, verr := rulesFor(nil)
	if verr == nil || verr.Code != "kube.serviceaccount.norules" {
		t.Errorf("want kube.serviceaccount.norules, got %+v", verr)
	}
}

func TestRulesForDeduplicatesAndSorts(t *testing.T) {
	rules, verr := rulesFor([]string{"kube.pod.list", "kube.pod.list", "kube.deployment.list"})
	if verr != nil {
		t.Fatal(verr)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %v, want 2 deduplicated entries", rules)
	}
	sorted := slices.IsSortedFunc(rules, func(a, b policyRule) int {
		return strings.Compare(strings.Join(a.Resources, ","), strings.Join(b.Resources, ","))
	})
	if !sorted {
		t.Errorf("rules = %v, want sorted by resource for a stable manifest", rules)
	}
}
