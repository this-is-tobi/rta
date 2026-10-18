package audit

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/eolapi"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The shape confirmed against a real k3s cluster's own cluster-admin
// binding while writing this check.
func TestIsDefaultClusterAdminBinding(t *testing.T) {
	cases := []struct {
		name string
		b    bindingItem
		want bool
	}{
		{"the real default", bindingItem{
			Metadata: meta{Name: "cluster-admin"},
			Subjects: []subject{{Kind: "Group", Name: "system:masters"}},
		}, true},
		{"same subject, different binding name", bindingItem{
			Metadata: meta{Name: "custom-admin-binding"},
			Subjects: []subject{{Kind: "Group", Name: "system:masters"}},
		}, false},
		{"default name, extra subject added", bindingItem{
			Metadata: meta{Name: "cluster-admin"},
			Subjects: []subject{
				{Kind: "Group", Name: "system:masters"},
				{Kind: "User", Name: "someone@example.com"},
			},
		}, false},
		{"default name, dangerous group instead", bindingItem{
			Metadata: meta{Name: "cluster-admin"},
			Subjects: []subject{{Kind: "Group", Name: "system:authenticated"}},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDefaultClusterAdminBinding(c.b); got != c.want {
				t.Errorf("isDefaultClusterAdminBinding = %v, want %v", got, c.want)
			}
		})
	}
}

// What the audit grades a cluster-admin binding, and what its description says
// it does. The description named system:masters among the groups that grade
// fail; the code grades the default binding info (every installer creates it)
// and any other binding naming it, like any other subject, warn.
func TestAClusterAdminBindingIsGradedAsTheDescriptionSays(t *testing.T) {
	fakeKubectl(t, map[string]string{
		"clusterrolebindings": `{"items":[
			{"metadata":{"name":"cluster-admin"},"roleRef":{"name":"cluster-admin"},
			 "subjects":[{"kind":"Group","name":"system:masters"}]},
			{"metadata":{"name":"more-masters"},"roleRef":{"name":"cluster-admin"},
			 "subjects":[{"kind":"Group","name":"system:masters"},{"kind":"User","name":"bob"}]},
			{"metadata":{"name":"everyone"},"roleRef":{"name":"cluster-admin"},
			 "subjects":[{"kind":"Group","name":"system:authenticated"}]}]}`,
		"roles": `{"items":[]}`, "clusterroles": `{"items":[]}`,
	})
	out, err := runKubeRBAC(t.Context(), newScopedRequest(""))
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderText(t, out)
	for _, want := range []string{
		"cluster-admin binding: cluster-admin | " + findings.Info,
		"cluster-admin binding: more-masters | " + findings.Warn,
		"cluster-admin binding: everyone | " + findings.Fail,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}

	for _, c := range Plugin(nil, nil).Capabilities {
		if c.ID != "audit.kube.rbac" {
			continue
		}
		if strings.Contains(c.Description, "system:masters) grades fail") ||
			!strings.Contains(c.Description, "system:authenticated or system:unauthenticated grades fail") {
			t.Errorf("the description does not say which bindings grade fail:\n%s", c.Description)
		}
	}
}

func TestWildcardRule(t *testing.T) {
	cases := []struct {
		name  string
		rules []policyRule
		want  string
	}{
		{"clean", []policyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}}}, ""},
		{"wildcard verb", []policyRule{{Verbs: []string{"*"}}}, `verbs: ["*"]`},
		{"wildcard resource", []policyRule{{Resources: []string{"*"}}}, `resources: ["*"]`},
		{"wildcard apiGroup", []policyRule{{APIGroups: []string{"*"}}}, `apiGroups: ["*"]`},
		{"no rules", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wildcardRule(c.rules); got != c.want {
				t.Errorf("wildcardRule = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSubjectNames(t *testing.T) {
	got := subjectNames([]subject{
		{Kind: "Group", Name: "system:masters"},
		{Kind: "ServiceAccount", Name: "deployer", Namespace: "ci"},
	})
	want := "Group system:masters, ServiceAccount ci/deployer"
	if got != want {
		t.Errorf("subjectNames = %q, want %q", got, want)
	}
}

func TestAssertsNonRoot(t *testing.T) {
	yes := true
	nonZero := int64(1000)
	zero := int64(0)

	cases := []struct {
		name string
		pod  podSecurityItem
		want bool
	}{
		{"pod-level runAsNonRoot", podSecurityItem{Spec: podSecuritySpec{
			SecurityContext: podSecurityContext{RunAsNonRoot: &yes}}}, true},
		{"container-level runAsUser nonzero", podSecurityItem{Spec: podSecuritySpec{
			Containers: []podSecurityCtn{{SecurityContext: podSecurityContext{RunAsUser: &nonZero}}}}}, true},
		{"runAsUser explicitly zero", podSecurityItem{Spec: podSecuritySpec{
			SecurityContext: podSecurityContext{RunAsUser: &zero}}}, false},
		// The Restricted profile holds init and ephemeral containers to the
		// same rule as the main ones, so an assertion on either counts as one.
		{"init container runAsNonRoot", podSecurityItem{Spec: podSecuritySpec{
			InitContainers: []podSecurityCtn{{SecurityContext: podSecurityContext{RunAsNonRoot: &yes}}}}}, true},
		{"ephemeral container runAsUser nonzero", podSecurityItem{Spec: podSecuritySpec{
			EphemeralContainers: []podSecurityCtn{{SecurityContext: podSecurityContext{RunAsUser: &nonZero}}}}}, true},
		{"nothing set", podSecurityItem{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := assertsNonRoot(c.pod); got != c.want {
				t.Errorf("assertsNonRoot = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHostNamespaceDetail(t *testing.T) {
	p := podSecurityItem{}
	p.Spec.HostNetwork = true
	p.Spec.HostPID = true
	if got := hostNamespaceDetail(p); got != "hostNetwork, hostPID is true" {
		t.Errorf("hostNamespaceDetail = %q", got)
	}
}

// Pod Security Admission grades every container list a pod has: the Baseline
// profile's "Privileged Containers" control names spec.containers[*],
// spec.initContainers[*] and spec.ephemeralContainers[*].securityContext.privileged
// as its restricted fields, all three alike. A check that reads spec.containers
// alone grades a pod whose only privileged container is an init container — the
// Elasticsearch chart's sysctl init container is the everyday example — as
// clean, and that is a compliance report saying the cluster passes baseline
// where admission would reject the pod.
func TestPrivilegedInitAndEphemeralContainersAreGraded(t *testing.T) {
	fakeKubectl(t, map[string]string{
		"pods": `{"items":[
			{"metadata":{"namespace":"logging","name":"es-0"},
			 "spec":{"securityContext":{"runAsNonRoot":true},
			         "initContainers":[{"name":"sysctl","securityContext":{"privileged":true}}],
			         "containers":[{"name":"elasticsearch"}]}},
			{"metadata":{"namespace":"debug","name":"web-0"},
			 "spec":{"securityContext":{"runAsNonRoot":true},
			         "containers":[{"name":"web"}],
			         "ephemeralContainers":[{"name":"shell","securityContext":{"privileged":true}}]}},
			{"metadata":{"namespace":"infra","name":"agent-0"},
			 "spec":{"securityContext":{"runAsNonRoot":true},
			         "containers":[{"name":"agent","securityContext":{"privileged":true}}]}}
		]}`,
	})

	out, err := runKubePodSecurity(t.Context(), newScopedRequest(""))
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderText(t, out)
	for _, want := range []string{
		"privileged container: logging/es-0/sysctl (init) | " + findings.Fail,
		"privileged container: debug/web-0/shell (ephemeral) | " + findings.Fail,
		// A main container's row is exactly what it was: the kind is said
		// only where kubectl describe would list the name elsewhere.
		"privileged container: infra/agent-0/agent | " + findings.Fail,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "no pod") {
		t.Errorf("a pod running privileged graded clean:\n%s", rendered)
	}
}

// A tile re-runs a capability on a timer, and the pace a capability that
// reaches a configured connection gets when it says nothing is thirty seconds
// (plugin.PaceRemote): three or four whole-cluster lists, every half minute,
// for as long as a terminal is open. These are read by the day, and the person
// who just fixed a binding presses `r`.
func TestKubeAuditsDeclareARefresh(t *testing.T) {
	var seen int
	for _, c := range Plugin(nil, nil).Capabilities {
		if !strings.HasPrefix(c.ID, "audit.kube.") {
			continue
		}
		seen++
		if c.Refresh < kubeAuditRefresh {
			t.Errorf("%s declares a refresh of %s, want at least %s", c.ID, c.Refresh, kubeAuditRefresh)
		}
		if c.ID == "audit.kube.eol" && c.Refresh != eolapi.TileRefresh {
			t.Errorf("audit.kube.eol refreshes every %s, want the pace of the API it asks (%s)", c.Refresh, eolapi.TileRefresh)
		}
	}
	if seen != 5 {
		t.Errorf("found %d audit.kube.* capabilities, want 5", seen)
	}
}

// The same for the audits that reach outside: audit.deps sends one request to
// osv.dev for every dependency a project pins, audit.web an HTTPS request and
// audit.mail a handful of DNS lookups at a host or a domain somebody named. At
// the pace a capability gets when it says nothing (thirty seconds, from a Config
// key among its inputs) that is a scan of somebody else's service, or of a public
// API, for as long as a terminal is open. What they say moves by the day. The
// pace is a floor: a tile that states a faster one is held to it.
func TestTheAuditsThatReachOutDeclareAnHourlyFloor(t *testing.T) {
	want := map[string]bool{"audit.deps": false, "audit.web": false, "audit.mail": false}
	for _, c := range Plugin(nil, nil).Capabilities {
		if _, ok := want[c.ID]; !ok {
			continue
		}
		want[c.ID] = true
		if c.Pace() != time.Hour {
			t.Errorf("%s runs on a tile every %s, want 1h0m0s", c.ID, c.Pace())
		}
		if got, cut := plugin.ClampPace(c, 30*time.Second); got != time.Hour || !cut {
			t.Errorf("%s: a tile asking for 30s runs every %s (cut %v), want the floor of 1h0m0s", c.ID, got, cut)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("%s is not declared", id)
		}
	}
}

// The kube audits reach whichever cluster kubectl has current, and an agent
// is never offered the context, so what it can aim them at is only a
// namespace. That is the record a grant names: a grant on `payments` covers
// that namespace and not the cluster, and a call naming none is about the
// whole cluster, which only an unscoped grant covers. Declared with a Scope
// and not a Required input, because the namespace is optional and a
// whole-cluster audit is the default question.
func TestKubeAuditsCostAGrantNarrowedToANamespace(t *testing.T) {
	var seen int
	for _, c := range Plugin(nil, nil).Capabilities {
		if !strings.HasPrefix(c.ID, "audit.kube.") {
			continue
		}
		seen++
		if !c.NeedsGrant {
			t.Errorf("%s needs no grant, so an agent with none runs kubectl on the operator's current context", c.ID)
		}
		if c.Scope != "namespace" {
			t.Errorf("%s scopes a grant by %q, want the namespace it narrows to", c.ID, c.Scope)
		}
	}
	if seen != 5 {
		t.Errorf("found %d audit.kube.* capabilities, want 5", seen)
	}
}

// auditDetailCase is one kube audit with the cluster it reads in the
// detail-page tests: at least one finding each, built from the same fake
// kubectl the scope tests use.
type auditDetailCase struct {
	name   string
	bodies map[string]string
	run    func(ctx context.Context, req plugin.Request) (view.View, error)
}

func auditDetailCases(t *testing.T) []auditDetailCase {
	t.Helper()
	srv, _ := newEOLServer(t)
	return []auditDetailCase{
		{"rbac", map[string]string{
			"clusterrolebindings": `{"items":[{"metadata":{"name":"ops-admin"},"roleRef":{"name":"cluster-admin"},
				"subjects":[{"kind":"User","name":"alice@example.com"}]}]}`,
			"roles":        `{"items":[{"metadata":{"namespace":"a","name":"wild"},"rules":[{"verbs":["*"]}]}]}`,
			"clusterroles": `{"items":[]}`,
		}, runKubeRBAC},
		{"podsecurity", map[string]string{
			"pods": `{"items":[{"metadata":{"namespace":"infra","name":"agent-0"},
				"spec":{"containers":[{"name":"agent","securityContext":{"privileged":true}}]}}]}`,
		}, runKubePodSecurity},
		{"quotas", map[string]string{
			"namespaces":     `{"items":[{"metadata":{"name":"gitea"}},{"metadata":{"name":"argo-cd"}}]}`,
			"resourcequotas": `{"items":[]}`,
		}, runKubeQuotas},
		{"netpol", map[string]string{
			"namespaces":      `{"items":[{"metadata":{"name":"gitea"}},{"metadata":{"name":"argo-cd"}}]}`,
			"networkpolicies": `{"items":[]}`,
		}, runKubeNetworkPolicy},
		{"eol", map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody, "pods": clusterPodsBody},
			func(ctx context.Context, req plugin.Request) (view.View, error) {
				return runKubeEOLAt(ctx, req, srv.URL)
			}},
	}
}

func detailPage(t *testing.T, c auditDetailCase) view.Sections {
	t.Helper()
	fakeKubectl(t, c.bodies)
	req := plugin.NewRequest(map[string]any{"detail": true, "warn-days": 90}, false, false)
	out, err := c.run(t.Context(), req)
	if err != nil {
		t.Fatalf("%s: %v", c.name, err)
	}
	page, ok := out.(view.Sections)
	if !ok {
		t.Fatalf("%s: a detail run returned %T, want view.Sections", c.name, out)
	}
	return page
}

// The "runs twice" report on audit.kube.eol and audit.kube.rbac was never two
// runs: kubectl and the API are each asked once. It was a detail page whose
// opening section was the whole findings table, handed over as the page's
// summary, followed by the group section holding the same rows — so every
// finding was on the screen twice, and the TUI opens a Detailed capability on
// the detail page by default. Counted per section, since two findings of one
// name inside a single section are a different matter.
func TestKubeAuditDetailPagesListEachCheckOnce(t *testing.T) {
	for _, c := range auditDetailCases(t) {
		t.Run(c.name, func(t *testing.T) {
			page := detailPage(t, c)
			sections := map[string][]string{}
			for _, s := range page.Items {
				table, ok := s.View.(view.Table)
				if !ok || s.ID == "references" {
					continue
				}
				seen := map[string]bool{}
				for _, row := range table.Rows {
					if !seen[row[0]] {
						seen[row[0]] = true
						sections[row[0]] = append(sections[row[0]], s.ID)
					}
				}
			}
			if len(sections) == 0 {
				t.Fatalf("no findings table on the page: %+v", page.Items)
			}
			for check, in := range sections {
				if len(in) > 1 {
					t.Errorf("%q is on the detail page in %d sections (%s)", check, len(in), strings.Join(in, ", "))
				}
			}
		})
	}
}

// The page opens with the answer: the grade, how many checks stood behind it,
// and which cluster and scope it was about. Same shape as web, mail, deps and
// agents, and what Page's summary is for.
func TestKubeDetailPageOpensWithTheGradeTheContextAndTheScope(t *testing.T) {
	for _, c := range auditDetailCases(t) {
		t.Run(c.name, func(t *testing.T) {
			page := detailPage(t, c)
			if len(page.Items) == 0 || page.Items[0].ID != "summary" {
				t.Fatalf("the first section is not the summary: %+v", page.Items)
			}
			kv, ok := page.Items[0].View.(view.KeyValue)
			if !ok {
				t.Fatalf("summary is %T, want view.KeyValue", page.Items[0].View)
			}
			got := map[string]string{}
			for _, p := range kv.Pairs {
				got[p.Key] = p.Value
			}
			for _, key := range []string{"context", "scope", "grade", "checks"} {
				if got[key] == "" {
					t.Errorf("summary has no %q pair: %v", key, kv.Pairs)
				}
			}
			if got["scope"] != "cluster-wide" {
				t.Errorf("scope = %q, want cluster-wide", got["scope"])
			}
		})
	}
}

func TestKubeDetailSummaryNamesTheContextAndANarrowedNamespace(t *testing.T) {
	fakeKubectl(t, map[string]string{"pods": `{"items":[]}`})
	req := plugin.NewRequest(map[string]any{"detail": true, "namespace": "gitea", "context": "homelab"}, false, false)
	out, err := runKubePodSecurity(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := out.(view.Sections).Items[0].View.(view.KeyValue)
	if !ok {
		t.Fatalf("summary is %T, want view.KeyValue", out.(view.Sections).Items[0].View)
	}
	got := map[string]string{}
	for _, p := range kv.Pairs {
		got[p.Key] = p.Value
	}
	if got["context"] != "homelab" || got["scope"] != "namespace gitea" {
		t.Errorf("summary pairs = %v", kv.Pairs)
	}
}
