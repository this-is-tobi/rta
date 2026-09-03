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

// The line the rollout entry must never cross: an identity minted from this
// table can restart and rescale, but it can never remove, fabricate, or
// escalate. Walked over every entry like the wildcard test, so a future
// grant has to clear it on its own.
func TestNoEntryCarriesADestructiveOrEscalatingVerb(t *testing.T) {
	forbidden := []string{"create", "delete", "deletecollection", "escalate", "bind", "impersonate"}
	for id, rules := range provisionable {
		for _, r := range rules {
			for _, v := range r.Verbs {
				if slices.Contains(forbidden, v) {
					t.Errorf("%s: verb %q — this table restarts and rescales, it never removes or escalates", id, v)
				}
			}
		}
	}
}

// The lexical contract: a dotted name promises "what that capability's Run
// does" and must therefore name a capability this plugin actually declares;
// a bare word names a cluster permission with no capability behind it and
// must never look like one. Either half drifting — a capability renamed
// without its table key, or a bare word growing a dot — would make the
// grant list promise something that does not exist.
func TestDottedGrantsAreDeclaredCapabilitiesAndBareWordsAreNot(t *testing.T) {
	declared := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		declared[c.ID] = true
	}
	for id := range provisionable {
		if strings.Contains(id, ".") {
			if !declared[id] {
				t.Errorf("%s is dotted but this plugin declares no such capability", id)
			}
		} else if declared[id] {
			t.Errorf("%s is a bare word colliding with a declared capability", id)
		}
	}
}

// logs follows metrics.pod's lesson: pods/log alone mints an identity that
// can stream nothing, because kubectl resolves the pod before reading its
// log — so the entry carries the pods read too, and a grant of just "logs"
// actually works.
func TestLogsCarriesThePodReadItNeeds(t *testing.T) {
	rules, verr := rulesFor([]string{"logs"})
	if verr != nil {
		t.Fatal(verr)
	}
	podsRead, logRead := false, false
	for _, r := range rules {
		if slices.Contains(r.Resources, "pods") && slices.Contains(r.Verbs, "list") {
			podsRead = true
		}
		if slices.Contains(r.Resources, "pods/log") && slices.Contains(r.Verbs, "get") {
			logRead = true
		}
	}
	if !podsRead || !logRead {
		t.Fatalf("logs must grant both the pod read and the log read, got %+v", rules)
	}
}

// The grant picker, the validation and the enforcement must be one list:
// the field's Options are computed from the table, and this pins that a
// future hand-maintained copy cannot silently drift from what rulesFor
// accepts.
func TestTheGrantInputOffersExactlyTheTable(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID != "kube.serviceaccount.provision" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name != "grant" {
				continue
			}
			if !slices.Equal(f.Options, provisionableNames()) {
				t.Fatalf("the grant input offers %v, the table accepts %v", f.Options, provisionableNames())
			}
			return
		}
	}
	t.Fatal("kube.serviceaccount.provision has no grant input")
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
