package audit

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
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
