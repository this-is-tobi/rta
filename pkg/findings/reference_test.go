package findings

import "testing"

var (
	refRBACClusterAdmin = Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.1.1",
		Title: "Ensure that the cluster-admin role is only used where required"}
	refRBACWildcard = Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.1.3",
		Title: "Minimize wildcard use in Roles and ClusterRoles"}
)

// The two reference shapes (OWASP/CWE, and the CIS/NSA-CISA/Pod-Security-
// Standards one) share one struct; each has to render and dedupe correctly
// without the other's fields leaking in.
func TestReferenceStringBothShapes(t *testing.T) {
	owaspCWE := Reference{OWASP: OWASPCrypto, CWE: "CWE-319", Title: "x"}
	if got := owaspCWE.String(); got != OWASPCrypto+" · CWE-319" {
		t.Errorf("owasp/cwe String() = %q", got)
	}

	cis := Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.1.3", Title: "x"}
	if got := cis.String(); got != "CIS Kubernetes Benchmark 2.0.1 5.1.3" {
		t.Errorf("source/control String() = %q", got)
	}

	if (Reference{}).String() != "" {
		t.Error("zero-value reference should render as empty, not a stray separator")
	}
}

// Only a CWE has a stable public lookup URL — a CIS control does not (the
// benchmark itself is gated), and a fabricated or stale link would be worse
// than none.
func TestReferenceURLOnlyForCWE(t *testing.T) {
	if got := (Reference{OWASP: OWASPCrypto, CWE: "CWE-319"}).URL(); got != "https://cwe.mitre.org/data/definitions/319.html" {
		t.Errorf("a CWE reference should have its lookup URL, got %q", got)
	}
	if got := (Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.1.3"}).URL(); got != "" {
		t.Errorf("a CIS reference should have no URL (none is stable/public), got %q", got)
	}
}

// References dedupes on the rendered citation, not the CWE field — a report
// mixing both shapes must not drop the CIS ones for having an empty CWE, and
// must still collapse a control cited twice. A finding that cites nothing
// contributes no row rather than an empty one.
func TestReferencesDedupeBothShapes(t *testing.T) {
	g := Group{ID: "rbac", Title: "rbac"}
	r := &Report{}
	r.Add(g, "a", Warn, "d1", refRBACClusterAdmin)
	r.Add(g, "b", Warn, "d2", refRBACClusterAdmin) // same control again
	r.Add(g, "c", Warn, "d3", refRBACWildcard)
	r.Add(g, "d", Info, "d4", Reference{})

	tbl := r.References()
	if tbl.Total != 2 {
		t.Fatalf("References() = %d rows, want 2 distinct controls: %v", tbl.Total, tbl.Rows)
	}
}
