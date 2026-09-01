package audit

import "testing"

// The two reference shapes (OWASP/CWE, and the CIS/NSA-CISA/Pod-Security-
// Standards one kube.* checks use) share one struct; each has to render and
// dedupe correctly without the other's fields leaking in.
func TestReferenceStringBothShapes(t *testing.T) {
	owaspCWE := reference{owasp: owaspCrypto, cwe: "CWE-319", title: "x"}
	if got := owaspCWE.String(); got != owaspCrypto+" · CWE-319" {
		t.Errorf("owasp/cwe String() = %q", got)
	}

	cis := reference{source: "CIS Kubernetes Benchmark 2.0.1", control: "5.1.3", title: "x"}
	if got := cis.String(); got != "CIS Kubernetes Benchmark 2.0.1 5.1.3" {
		t.Errorf("source/control String() = %q", got)
	}

	if (reference{}).String() != "" {
		t.Error("zero-value reference should render as empty, not a stray separator")
	}
}

// Only a CWE has a stable public lookup URL — a CIS control does not (the
// benchmark itself is gated), and a fabricated or stale link would be worse
// than none.
func TestReferenceURLOnlyForCWE(t *testing.T) {
	if got := (reference{owasp: owaspCrypto, cwe: "CWE-319"}).url(); got == "" {
		t.Error("a CWE reference should have a lookup URL")
	}
	if got := (reference{source: "CIS Kubernetes Benchmark 2.0.1", control: "5.1.3"}).url(); got != "" {
		t.Errorf("a CIS reference should have no URL (none is stable/public), got %q", got)
	}
}

// referenceTable dedupes on the rendered citation now, not the cwe field
// specifically — a report mixing both shapes (audit.kube.rbac cites both
// CIS controls in one run) must not drop the CIS ones for having an empty
// cwe, and must still collapse a control cited twice.
func TestReferenceTableDedupesBothShapes(t *testing.T) {
	r := &report{}
	r.add(grpKubeRBAC, "a", stWarn, "d1", refRBACClusterAdmin)
	r.add(grpKubeRBAC, "b", stWarn, "d2", refRBACClusterAdmin) // same control again
	r.add(grpKubeRBAC, "c", stWarn, "d3", refRBACWildcard)

	tbl := referenceTable(r.findings)
	if tbl.Total != 2 {
		t.Fatalf("referenceTable = %d rows, want 2 distinct controls: %v", tbl.Total, tbl.Rows)
	}
}
