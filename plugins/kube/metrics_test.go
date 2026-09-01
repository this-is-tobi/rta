package main

import "testing"

func TestCPUCores(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.5, "500m"},
		{0.05, "50m"},
		{1, "1.00"},
		{2.5, "2.50"},
	}
	for _, c := range cases {
		if got := cpuCores(c.in); got != c.want {
			t.Errorf("cpuCores(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBudgetOf(t *testing.T) {
	p := podSpecItem{}
	p.Spec.Containers = []struct {
		Resources struct {
			Limits map[string]string `json:"limits"`
		} `json:"resources"`
	}{
		{Resources: struct {
			Limits map[string]string `json:"limits"`
		}{Limits: map[string]string{"cpu": "500m", "memory": "256Mi"}}},
		{Resources: struct {
			Limits map[string]string `json:"limits"`
		}{Limits: map[string]string{"cpu": "250m", "memory": "128Mi"}}},
	}
	b := budgetOf(p)
	if b.cpuLimit != 0.75 {
		t.Errorf("cpuLimit = %v, want 0.75 (sum of both containers)", b.cpuLimit)
	}
	if b.memLimit != 384*1024*1024 {
		t.Errorf("memLimit = %v, want 384Mi in bytes", b.memLimit)
	}
}

func TestBudgetOfNoLimits(t *testing.T) {
	// A container with no limits set is the ordinary case, not an error —
	// the pod simply has no ceiling to measure pressure against.
	p := podSpecItem{}
	p.Spec.Containers = []struct {
		Resources struct {
			Limits map[string]string `json:"limits"`
		} `json:"resources"`
	}{{}}
	b := budgetOf(p)
	if b.cpuLimit != 0 || b.memLimit != 0 {
		t.Errorf("budgetOf(no limits) = %+v, want the zero value", b)
	}
}

func TestRawArgs(t *testing.T) {
	got := rawArgs(selection{Context: "kind-lab"}, "/apis/metrics.k8s.io/v1beta1/nodes")
	want := []string{"get", "--raw", "/apis/metrics.k8s.io/v1beta1/nodes",
		"--request-timeout=" + requestTimeout, "--context=kind-lab"}
	if len(got) != len(want) {
		t.Fatalf("rawArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rawArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// No --namespace, no --all-namespaces, ever — --raw does not understand
	// them, and rawArgs' whole reason to exist rather than reusing
	// selection.args is not emitting them.
	got = rawArgs(selection{AllNS: true, Namespace: "prod"}, "/apis/metrics.k8s.io/v1beta1/pods")
	for _, a := range got {
		if a == "--all-namespaces" || a == "--namespace=prod" {
			t.Errorf("rawArgs leaked a resource-listing flag into a --raw call: %v", got)
		}
	}
}
