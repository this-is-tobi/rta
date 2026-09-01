package audit

import "testing"

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
