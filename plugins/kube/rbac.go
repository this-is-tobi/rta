package main

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The Role half of kube.serviceaccount.provision: turning a caller-chosen
// list of this plugin's own capability IDs into the minimal Kubernetes RBAC
// rules that let a ServiceAccount actually do what those capabilities do,
// and nothing else.
//
// Kept local rather than reused from builtin/audit/kube.go's own RBAC types
// (bindingItem, roleItem, policyRule) for the same reason plugins/kube/cert.go
// can't reuse builtin/internal/x509check: this is a separate Go module,
// compiled to its own binary and run out-of-process — an internal package one
// directory over is exactly as unreachable as one on the other side of the
// world. What's duplicated here is deliberately small: just the JSON shapes
// `kubectl create -f -` needs to build a ServiceAccount, a Role and a
// RoleBinding, not a general RBAC client.

// policyRule mirrors rbacv1.PolicyRule, cut down to the three fields this
// plugin ever sets.
type policyRule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

// provisionable maps a capability ID this plugin exposes to the Kubernetes
// RBAC rule that lets a ServiceAccount do what that capability's Run
// function actually does. Every entry here is a promise that granting it
// costs exactly this rule and nothing more — see wildcards-forbidden below,
// and the two exclusions this table deliberately does not carry an entry for.
//
// Excluded, and why — both found by checking each capability's underlying
// Kubernetes resource before adding it here, not by copying a candidate list:
//
//   - kube.namespace.list, kube.node.list and kube.metrics.node read
//     cluster-scoped resources (Namespace; Node; the nodes.metrics.k8s.io
//     resource). A Role — namespaced by definition — cannot grant access to a
//     cluster-scoped resource; only a ClusterRole can, and this mechanism
//     deliberately mints only namespaced objects (see serviceaccount.go).
//     Listing any of them here would be a rule that looks like it grants the
//     capability and silently does not.
//   - kube.cert.list reads `type: kubernetes.io/tls` Secrets, but Kubernetes
//     RBAC cannot scope a rule by Secret *type* — only by resource kind, or a
//     fixed resourceNames allowlist. A rule granting this would grant read on
//     every Secret in the namespace, TLS or not, including whatever else is
//     stored there (database passwords, API keys) — a privilege-escalation-
//     via-granularity-mismatch between this plugin's capability model and
//     Kubernetes' actual access-control model, not a rule worth minting with
//     a caveat nobody will re-read at the moment it matters.
//   - kube.metrics.pressure and kube.pvc.usage read the kubelet's Summary API
//     through nodes/proxy, and that permission is indivisible: it covers the
//     entire kubelet API on every node, including /exec, /run and /logs on
//     every pod running there. There is no way to grant "just the stats". A
//     ServiceAccount minted with it would hold remote code execution on every
//     workload in the cluster in exchange for a disk-usage column — the single
//     worst trade in this table, which is why these two are excluded on their
//     blast radius and not merely on being cluster-scoped. This exclusion is
//     not a gap to close later; closing it would mean Kubernetes had split the
//     permission, and it has not.
//   - kube.overview is a composite view over several of the above, not a
//     single resource to name a rule for, and kube.context.* reads/writes the
//     local kubeconfig file — no cluster permission applies to either.
var provisionable = map[string][]policyRule{
	"kube.pod.list": {
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
	},
	"kube.deployment.list": {
		{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"get", "list"}},
	},
	"kube.quota.list": {
		{APIGroups: []string{""}, Resources: []string{"resourcequotas", "limitranges"}, Verbs: []string{"get", "list"}},
	},
	// Events are namespaced and read through the core/v1 endpoint, so unlike
	// the node and namespace reads above this one maps cleanly onto a Role.
	// Worth granting rather than withholding: "what is this namespace
	// complaining about" is most of what an agent debugging one actually
	// needs, and refusing it pushes whoever is driving that agent back to
	// handing over their own kubeconfig, which is the outcome this whole
	// mechanism exists to avoid.
	"kube.event.list": {
		{APIGroups: []string{""}, Resources: []string{"events"}, Verbs: []string{"get", "list"}},
	},
	"kube.pvc.list": {
		{APIGroups: []string{""}, Resources: []string{"persistentvolumeclaims"}, Verbs: []string{"get", "list"}},
	},
	// Two rules, not one: runMetricsPod also reads core v1 Pods (not just
	// metrics.k8s.io) to get each pod's own resource limits for the "% of
	// limit" columns — found by a review that cross-referenced this table
	// against what the Run function actually does rather than assuming the
	// obvious metrics.k8s.io rule was the whole story. Without the second
	// rule, a ServiceAccount minted with only this capability could read
	// usage but not limits, and every percentage column would render
	// silently blank rather than error — a working-but-wrong result, which
	// is worse than a refusal.
	"kube.metrics.pod": {
		{APIGroups: []string{"metrics.k8s.io"}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
	},
}

// rulesFor resolves a caller-chosen capability list into the Role rules that
// grant exactly those capabilities, deduplicated and sorted for a stable,
// readable dry-run and a stable manifest. Fails closed: any name outside
// provisionable refuses the whole request rather than silently granting the
// ones that did resolve, so a typo or an unmapped capability never mints a
// Role narrower than the operator asked for without them being told.
func rulesFor(capabilityIDs []string) ([]policyRule, *view.Error) {
	if len(capabilityIDs) == 0 {
		return nil, view.Errorf("kube.serviceaccount.norules",
			"name at least one capability to grant").
			WithHint("--capability kube.pod.list, repeatable — " + strings.Join(provisionableNames(), ", "))
	}
	seen := map[string]bool{}
	var out []policyRule
	for _, id := range capabilityIDs {
		id = strings.TrimSpace(id)
		rules, ok := provisionable[id]
		if !ok {
			return nil, view.Errorf("kube.serviceaccount.ungrantable",
				"%q cannot be granted this way", id).
				WithHint("grantable capabilities: " + strings.Join(provisionableNames(), ", "))
		}
		for _, r := range rules {
			key := strings.Join(r.APIGroups, ",") + "|" + strings.Join(r.Resources, ",") + "|" + strings.Join(r.Verbs, ",")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i].Resources, ",") < strings.Join(out[j].Resources, ",")
	})
	return out, nil
}

// provisionableNames lists what rulesFor accepts, sorted, for error hints and
// the dry-run description.
func provisionableNames() []string {
	out := make([]string, 0, len(provisionable))
	for id := range provisionable {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// roleManifest and roleBindingManifest are the exact JSON `kubectl create -f
// -` needs. ServiceAccount needs no dedicated type — objectMeta alone,
// wrapped with apiVersion/kind at the call site, is the whole object.

type objectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type serviceAccountManifest struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   objectMeta `json:"metadata"`
}

type roleManifest struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   objectMeta   `json:"metadata"`
	Rules      []policyRule `json:"rules"`
}

type roleBindingManifest struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   objectMeta `json:"metadata"`
	RoleRef    struct {
		APIGroup string `json:"apiGroup"`
		Kind     string `json:"kind"`
		Name     string `json:"name"`
	} `json:"roleRef"`
	Subjects []struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"subjects"`
}

// provisionLabels is stamped on every object this capability creates, so
// kube.serviceaccount.list can find them and kube.serviceaccount.revoke can
// confirm it is deleting something this plugin actually made.
const provisionedByLabel = "rta.dev/provisioned-by"
const provisionedByValue = "kube.serviceaccount.provision"

func provisionLabels() map[string]string {
	return map[string]string{provisionedByLabel: provisionedByValue}
}
