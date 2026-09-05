package audit

import (
	"context"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// audit.kube.*: cluster policy graded against a named control, the same
// promise every other check in this plugin makes — "checks that grade
// something you run against a named security control, rather than just
// describing it" (see audit.go). What plugins/kube deliberately does not do
// (describe, scale, mutate) is untouched here too: every kube.* audit is one
// or two `kubectl get -o json` calls and a judgement, nothing that reaches
// for a verb kubectl already has.

var (
	grpKubeRBAC     = group{"rbac", "rbac"}
	grpKubePod      = group{"pod-security", "pod security"}
	grpKubeQuota    = group{"resource-policy", "resource policy"}
	grpKubeNetwork  = group{"network-policy", "network policy"}
	kubeGroupOrder1 = []group{grpKubeRBAC}
	kubeGroupOrder2 = []group{grpKubeQuota}
	kubeGroupOrder3 = []group{grpKubeNetwork}
)

// systemNamespaces are excluded from the per-namespace checks (quotas,
// network policies): they are not user workload namespaces, nobody expects
// a ResourceQuota on kube-system, and flagging them would bury the findings
// that are actually actionable under ones that never were.
var systemNamespaces = map[string]bool{
	"kube-system": true, "kube-public": true, "kube-node-lease": true,
}

// ---- audit.kube.rbac ----

type roleRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type subject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type bindingItem struct {
	Metadata meta      `json:"metadata"`
	RoleRef  roleRef   `json:"roleRef"`
	Subjects []subject `json:"subjects"`
}

type policyRule struct {
	APIGroups []string `json:"apiGroups"`
	Resources []string `json:"resources"`
	Verbs     []string `json:"verbs"`
}

type roleItem struct {
	Metadata meta         `json:"metadata"`
	Rules    []policyRule `json:"rules"`
}

// meta is the part of every object these checks read — a smaller cousin of
// plugins/kube's own meta, kept local because this package cannot import a
// separate module's internal type any more than it could import
// x509check (see kubectl.go).
type meta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

func runKubeRBAC(ctx context.Context, req plugin.Request) (view.View, error) {
	kubeContext := req.String("context")
	ns, verr := scopeOf(req)
	if verr != nil {
		return nil, verr
	}
	r := &report{}

	// **Narrowing this audit means dropping half its subject, and the half it
	// drops has to be said out loud.** Two of the three things checked here —
	// ClusterRoleBindings to cluster-admin, and wildcard ClusterRoles — are
	// cluster-scoped objects that do not belong to any namespace. There is no
	// honest way to narrow them: they either apply everywhere or they are not
	// examined.
	//
	// Skipping them silently is the dangerous option, because the finding this
	// audit exists to surface is precisely a cluster-admin binding, and a
	// scoped run would then report a clean RBAC posture while never having
	// looked. So the skip is itself a finding — info rather than warn, since
	// nothing is wrong, but present in the output where somebody reading the
	// result will see it rather than buried in the capability's description
	// where they will not.
	if ns != "" {
		r.add(grpKubeRBAC, "cluster-scoped RBAC not examined", stInfo,
			"narrowed to namespace "+ns+", so ClusterRoleBindings to cluster-admin and wildcard "+
				"ClusterRoles were not checked — they belong to no namespace. Run without a "+
				"namespace to include them.", refRBACClusterAdmin)
	}
	// Everything added from here on is a real finding about what was examined.
	examined := len(r.findings)

	var crb list[bindingItem]
	if ns == "" {
		crbErr := kubeGetJSON(ctx, kubeContext, "", "clusterrolebindings", &crb)
		if crbErr != nil {
			return nil, crbErr
		}
	}
	// system:authenticated and system:unauthenticated bound to cluster-admin
	// are never a sane default — no installer creates them, and the group
	// itself means every request, authenticated or not. system:masters is
	// different: it is not a dangerous outlier, it is the group every
	// distribution (kubeadm, k3s, the rest) binds cluster-admin to out of
	// the box, on a ClusterRoleBinding it also always names "cluster-admin"
	// — confirmed against a real k3s cluster while writing this, not
	// assumed. Auto-failing that exact shape would flag CIS 5.1.1 on every
	// vanilla cluster's first run for its own installer default, which is
	// not a finding; reported as info instead, still visible, not chosen to
	// look for.
	dangerousSubjects := map[string]bool{"system:authenticated": true, "system:unauthenticated": true}
	for _, b := range crb.Items {
		if b.RoleRef.Name != "cluster-admin" {
			continue
		}
		names := subjectNames(b.Subjects)
		status := stWarn
		switch {
		case isDefaultClusterAdminBinding(b):
			status = stInfo
		default:
			for _, s := range b.Subjects {
				if dangerousSubjects[s.Name] {
					status = stFail
				}
			}
		}
		r.add(grpKubeRBAC, "cluster-admin binding: "+b.Metadata.Name, status,
			"bound to "+names, refRBACClusterAdmin)
	}

	roles, rolesErr := fetchAllRules(ctx, kubeContext, ns)
	if rolesErr != nil {
		return nil, rolesErr
	}
	for _, ro := range roles {
		if wild := wildcardRule(ro.Rules); wild != "" {
			label := ro.Metadata.Name
			if ro.Metadata.Namespace != "" {
				label = ro.Metadata.Namespace + "/" + ro.Metadata.Name
			}
			r.add(grpKubeRBAC, "wildcard rule: "+label, stWarn, wild, refRBACWildcard)
		}
	}

	// Counted from the mark rather than from zero, because a narrowed run has
	// already added the "cluster-scoped RBAC not examined" note above — and
	// `len(r.findings) == 0` would then be false on a perfectly clean
	// namespace, so the clean result would silently stop being reported for
	// exactly the runs that added the note.
	if len(r.findings) == examined {
		r.add(grpKubeRBAC, "cluster-admin and wildcard rules", stOK,
			rbacClean(ns), refRBACClusterAdmin)
	}

	if !req.Bool("detail") {
		return r.table(true), nil
	}
	return detailPage(ctx, req, r, kubeGroupOrder1, r.table(true)), nil
}

// isDefaultClusterAdminBinding matches the exact shape every distribution's
// installer creates: a ClusterRoleBinding named "cluster-admin", binding
// only the "system:masters" group, nothing added to it.
func isDefaultClusterAdminBinding(b bindingItem) bool {
	return b.Metadata.Name == "cluster-admin" &&
		len(b.Subjects) == 1 &&
		b.Subjects[0].Kind == "Group" &&
		b.Subjects[0].Name == "system:masters"
}

func subjectNames(subs []subject) string {
	names := make([]string, 0, len(subs))
	for _, s := range subs {
		if s.Namespace != "" {
			names = append(names, s.Kind+" "+s.Namespace+"/"+s.Name)
		} else {
			names = append(names, s.Kind+" "+s.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// fetchAllRules reads every Role and ClusterRole and returns them as one
// slice with a shared shape — the wildcard check does not care which kind a
// rule came from, only what it grants.
//
// Narrowed to a namespace it reads Roles there and no ClusterRoles at all: a
// ClusterRole belongs to no namespace, so including them would attribute
// cluster-wide findings to a namespace that does not own them. The caller
// reports that omission; see runKubeRBAC.
func fetchAllRules(ctx context.Context, kubeContext, ns string) ([]roleItem, *view.Error) {
	var roles, clusterRoles list[roleItem]
	if verr := kubeGetJSON(ctx, kubeContext, ns, "roles", &roles); verr != nil {
		return nil, verr
	}
	if ns != "" {
		return roles.Items, nil
	}
	if verr := kubeGetJSON(ctx, kubeContext, "", "clusterroles", &clusterRoles); verr != nil {
		return nil, verr
	}
	return append(roles.Items, clusterRoles.Items...), nil
}

// wildcardRule reports the first rule that grants "*" in any of its three
// dimensions, as the sentence CIS 5.1.3 is asking about — a rule need only
// fail once to be worth reading.
func wildcardRule(rules []policyRule) string {
	for _, ru := range rules {
		switch {
		case slicesContain(ru.APIGroups, "*"):
			return "apiGroups: [\"*\"]"
		case slicesContain(ru.Resources, "*"):
			return "resources: [\"*\"]"
		case slicesContain(ru.Verbs, "*"):
			return "verbs: [\"*\"]"
		}
	}
	return ""
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---- audit.kube.podsecurity ----

type podSecurityContext struct {
	RunAsNonRoot *bool  `json:"runAsNonRoot"`
	RunAsUser    *int64 `json:"runAsUser"`
	Privileged   *bool  `json:"privileged"`
}

// podSecuritySpec is a named type rather than inlined, so a test can build
// one without repeating the anonymous struct's exact shape at every call
// site — Go requires that repetition for an unnamed struct literal.
type podSecuritySpec struct {
	HostNetwork     bool               `json:"hostNetwork"`
	HostPID         bool               `json:"hostPID"`
	HostIPC         bool               `json:"hostIPC"`
	SecurityContext podSecurityContext `json:"securityContext"`
	Containers      []podSecurityCtn   `json:"containers"`
}

type podSecurityItem struct {
	Metadata meta            `json:"metadata"`
	Spec     podSecuritySpec `json:"spec"`
}

type podSecurityCtn struct {
	Name            string             `json:"name"`
	SecurityContext podSecurityContext `json:"securityContext"`
}

func runKubePodSecurity(ctx context.Context, req plugin.Request) (view.View, error) {
	kubeContext := req.String("context")
	ns, verr := scopeOf(req)
	if verr != nil {
		return nil, verr
	}
	var pods list[podSecurityItem]
	if verr := kubeGetJSON(ctx, kubeContext, ns, "pods", &pods); verr != nil {
		return nil, verr
	}

	r := &report{}
	for _, p := range pods.Items {
		label := p.Metadata.Namespace + "/" + p.Metadata.Name
		if p.Spec.HostNetwork || p.Spec.HostPID || p.Spec.HostIPC {
			r.add(grpKubePod, "host namespace: "+label, stFail,
				hostNamespaceDetail(p), refPodSecurityHostNS)
		}
		for _, c := range p.Spec.Containers {
			if c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
				r.add(grpKubePod, "privileged container: "+label+"/"+c.Name, stFail,
					"securityContext.privileged is true", refExcessivePriv)
			}
		}
		if !assertsNonRoot(p) {
			r.add(grpKubePod, "root not excluded: "+label, stWarn,
				"neither the pod nor any container sets runAsNonRoot or a non-zero runAsUser",
				refPodSecurityNonRoot)
		}
	}
	if len(r.findings) == 0 {
		r.add(grpKubePod, "host namespaces, privileged containers, non-root", stOK,
			"no pod "+within(ns)+" uses a host namespace, runs privileged, or leaves root unexcluded",
			refPodSecurityHostNS)
	}

	if !req.Bool("detail") {
		return r.table(true), nil
	}
	return detailPage(ctx, req, r, []group{grpKubePod}, r.table(true)), nil
}

func hostNamespaceDetail(p podSecurityItem) string {
	var which []string
	if p.Spec.HostNetwork {
		which = append(which, "hostNetwork")
	}
	if p.Spec.HostPID {
		which = append(which, "hostPID")
	}
	if p.Spec.HostIPC {
		which = append(which, "hostIPC")
	}
	return strings.Join(which, ", ") + " is true"
}

// assertsNonRoot approximates Kubernetes' own securityContext merge (pod
// level, overridden per container) rather than fully simulating it: true if
// the pod or *any* container positively asserts non-root, so this reports a
// gap only where nothing in the spec asserts anything — a pod that sets
// runAsNonRoot at the pod level and never overrides it per container is not
// flagged, which is the common, correct case; one relying on an image's own
// non-root USER with no Kubernetes-level assertion at all is, which is the
// gap Pod Security Standards' "Restricted" level exists to close.
func assertsNonRoot(p podSecurityItem) bool {
	if nonRootAsserted(p.Spec.SecurityContext) {
		return true
	}
	for _, c := range p.Spec.Containers {
		if nonRootAsserted(c.SecurityContext) {
			return true
		}
	}
	return false
}

func nonRootAsserted(sc podSecurityContext) bool {
	if sc.RunAsNonRoot != nil && *sc.RunAsNonRoot {
		return true
	}
	return sc.RunAsUser != nil && *sc.RunAsUser != 0
}

// ---- audit.kube.quotas ----

type limitedNamespace struct {
	Metadata meta `json:"metadata"`
}

// namespacesToCheck resolves the set a coverage audit walks.
//
// **When narrowed, the namespace list is not read at all, and that is a
// correctness fix rather than an optimisation.** Namespaces are cluster-scoped,
// so `kubectl get namespaces --namespace=gitea` does not filter — kubectl
// accepts the flag and ignores it. Passing the narrowing to both reads
// therefore narrows only the second one: the quota (or policy) read comes back
// holding gitea alone while the namespace read comes back holding every
// namespace in the cluster, and every namespace that is not gitea then looks
// like one with no coverage. The result is a report naming nineteen namespaces
// as findings when four qualify — each one a namespace the caller explicitly
// excluded, which is both wrong and the opposite of what narrowing was asked
// for.
func namespacesToCheck(ctx context.Context, kubeContext, ns string) ([]string, *view.Error) {
	if ns != "" {
		return []string{ns}, nil
	}
	var namespaces list[limitedNamespace]
	if verr := kubeGetJSON(ctx, kubeContext, "", "namespaces", &namespaces); verr != nil {
		return nil, verr
	}
	out := make([]string, 0, len(namespaces.Items))
	for _, n := range namespaces.Items {
		out = append(out, n.Metadata.Name)
	}
	return out, nil
}

// coverageGaps names the namespaces with no object of this kind in them.
//
// A namespace named explicitly is never skipped as a system one: the
// systemNamespaces filter exists so a cluster-wide sweep does not report
// kube-system for a policy it is not expected to carry, and somebody who typed
// `kube-system` is asking about kube-system.
func coverageGaps(ctx context.Context, kubeContext, ns, kind string) ([]string, *view.Error) {
	names, verr := namespacesToCheck(ctx, kubeContext, ns)
	if verr != nil {
		return nil, verr
	}
	var found list[limitedNamespace] // only .metadata.namespace is read
	if verr := kubeGetJSON(ctx, kubeContext, ns, kind, &found); verr != nil {
		return nil, verr
	}
	has := map[string]bool{}
	for _, f := range found.Items {
		has[f.Metadata.Namespace] = true
	}
	var missing []string
	for _, name := range names {
		if has[name] || (ns == "" && systemNamespaces[name]) {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing, nil
}

func runKubeQuotas(ctx context.Context, req plugin.Request) (view.View, error) {
	kubeContext := req.String("context")
	ns, verr := scopeOf(req)
	if verr != nil {
		return nil, verr
	}
	missing, verr := coverageGaps(ctx, kubeContext, ns, "resourcequotas")
	if verr != nil {
		return nil, verr
	}

	r := &report{}
	for _, ns := range missing {
		r.add(grpKubeQuota, "no ResourceQuota: "+ns, stWarn,
			"namespace has no ResourceQuota, so it can consume unbounded cluster resources", refResourcePolicies)
	}
	if len(missing) == 0 {
		r.add(grpKubeQuota, "ResourceQuota coverage", stOK,
			coverageClean(ns, "ResourceQuota"), refResourcePolicies)
	}

	if !req.Bool("detail") {
		return r.table(true), nil
	}
	return detailPage(ctx, req, r, kubeGroupOrder2, r.table(true)), nil
}

// ---- audit.kube.netpol ----

func runKubeNetworkPolicy(ctx context.Context, req plugin.Request) (view.View, error) {
	kubeContext := req.String("context")
	ns, verr := scopeOf(req)
	if verr != nil {
		return nil, verr
	}
	missing, verr := coverageGaps(ctx, kubeContext, ns, "networkpolicies")
	if verr != nil {
		return nil, verr
	}

	r := &report{}
	for _, ns := range missing {
		r.add(grpKubeNetwork, "no NetworkPolicy: "+ns, stWarn,
			"namespace has no NetworkPolicy, so pod-to-pod traffic is unrestricted by default", refNetworkPolicy)
	}
	if len(missing) == 0 {
		r.add(grpKubeNetwork, "NetworkPolicy coverage", stOK,
			coverageClean(ns, "NetworkPolicy"), refNetworkPolicy)
	}

	if !req.Bool("detail") {
		return r.table(true), nil
	}
	return detailPage(ctx, req, r, kubeGroupOrder3, r.table(true)), nil
}
