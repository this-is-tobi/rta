package audit

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// reference cites where a finding comes from, so it can be looked up rather
// than taken on faith. Two shapes share the struct: the OWASP Top 10:2025
// category (the current edition — it renumbered 2021's, so citing "A05
// Security Misconfiguration" out of habit would now point at the wrong
// entry) plus a MITRE CWE weakness, verified against cwe.mitre.org, the
// OWASP ZAP alert database and the OWASP Cheat Sheet Series; or, for the
// kube.* checks, a named framework (CIS Kubernetes Benchmark, NSA/CISA
// Kubernetes Hardening Guidance, Kubernetes' own Pod Security Standards) and
// the specific control it names, verified against the primary source where
// one is fetchable and flagged in the finding's own comment where the source
// checked was a secondary mirror instead. Both shapes were verified rather
// than recalled — a hardening tool that cites the wrong control is worse
// than one that cites none, because the wrong one still reads as
// authoritative.
type reference struct {
	owasp string // OWASP Top 10:2025 category
	cwe   string // "CWE-319"

	// source and control are the alternative shape: a named framework
	// ("CIS Kubernetes Benchmark 2.0.1") and the specific control within it
	// ("5.1.3", or a Pod Security Standards control name where CIS has no
	// number for the same thing). Set instead of owasp/cwe, never alongside.
	source  string
	control string

	title string // what the cited control actually says
}

// String is the compact citation shown in a findings row.
func (r reference) String() string {
	switch {
	case r.control != "":
		return r.source + " " + r.control
	case r.cwe != "":
		return r.owasp + " · " + r.cwe
	}
	return ""
}

// url points at the reference's own lookup page. CWEs have one stable public
// URL per weakness; a CIS Benchmark control does not (the benchmark itself
// sits behind cisecurity.org's registration gate) and reports empty rather
// than a link that goes stale or, worse, points at the wrong version.
func (r reference) url() string {
	if r.cwe == "" {
		return ""
	}
	return "https://cwe.mitre.org/data/definitions/" + strings.TrimPrefix(r.cwe, "CWE-") + ".html"
}

const (
	owaspAccessControl = "A01:2025 Broken Access Control"
	owaspMisconfig     = "A02:2025 Security Misconfiguration"
	owaspSupplyChain   = "A03:2025 Software Supply Chain Failures"
	owaspCrypto        = "A04:2025 Cryptographic Failures"
	owaspAuth          = "A07:2025 Authentication Failures"
	owaspIntegrity     = "A08:2025 Software or Data Integrity Failures"
)

var (
	refCleartext      = reference{owasp: owaspCrypto, cwe: "CWE-319", title: "Cleartext Transmission of Sensitive Information"}
	refWeakCrypto     = reference{owasp: owaspCrypto, cwe: "CWE-327", title: "Use of a Broken or Risky Cryptographic Algorithm"}
	refCertValidation = reference{owasp: owaspCrypto, cwe: "CWE-295", title: "Improper Certificate Validation"}
	refMisconfig      = reference{owasp: owaspMisconfig, cwe: "CWE-693", title: "Protection Mechanism Failure"}
	refClickjacking   = reference{owasp: owaspMisconfig, cwe: "CWE-1021", title: "Improper Restriction of Rendered UI Layers or Frames"}
	refCORS           = reference{owasp: owaspMisconfig, cwe: "CWE-942", title: "Permissive Cross-domain Policy with Untrusted Domains"}
	refInfoExposure   = reference{owasp: owaspMisconfig, cwe: "CWE-200", title: "Exposure of Sensitive Information to an Unauthorized Actor"}
	refCookieSecure   = reference{owasp: owaspAuth, cwe: "CWE-614", title: "Sensitive Cookie Without 'Secure' Attribute"}
	refCookieHTTPOnly = reference{owasp: owaspAuth, cwe: "CWE-1004", title: "Sensitive Cookie Without 'HttpOnly' Flag"}
	refCSRF           = reference{owasp: owaspAccessControl, cwe: "CWE-352", title: "Cross-Site Request Forgery"}
	refSpoofing       = reference{owasp: owaspAuth, cwe: "CWE-290", title: "Authentication Bypass by Spoofing"}
	refVulnerableDep  = reference{owasp: owaspSupplyChain, cwe: "CWE-1395", title: "Dependency on Vulnerable Third-Party Component"}
	refUnpinnedDep    = reference{owasp: owaspIntegrity, cwe: "CWE-494", title: "Download of Code Without Integrity Check"}
	// A credential a configuration file holds in plain text, and a file whose
	// mode lets somebody else read it, are two different weaknesses with two
	// different fixes — move the secret, or change the mode — so they cite
	// the two CWEs that say so rather than one that covers neither exactly.
	refCredExposed = reference{owasp: owaspMisconfig, cwe: "CWE-522", title: "Insufficiently Protected Credentials"}
	// An agent allowed every tool is a principal running with more authority
	// than the task needs, which is what CWE-250 names. It is Broken Access
	// Control rather than Misconfiguration because the setting *is* the
	// access-control decision. Reused by audit.kube.podsecurity for a
	// privileged container: the same weakness, a different mechanism.
	refExcessivePriv = reference{owasp: owaspAccessControl, cwe: "CWE-250", title: "Execution with Unnecessary Privileges"}

	// The kube.* citations below are CIS Kubernetes Benchmark 2.0.1 section
	// numbers, confirmed against kube-bench's open-source mirror of the
	// benchmark's numbering (cisecurity.org gates the PDF itself behind an
	// email-registration form with no stable per-control URL to link) — a
	// secondary source, named as one rather than presented as the PDF
	// itself. Pod Security Standards citations are confirmed directly
	// against kubernetes.io/docs/concepts/security/pod-security-standards.
	// The NSA/CISA Kubernetes Hardening Guidance citation is confirmed
	// against v1.0 (August 2021) text specifically — v1.2 (August 2022) is
	// current but blocked automated fetches, and section titles have not
	// been re-verified against it.
	refRBACClusterAdmin = reference{source: "CIS Kubernetes Benchmark 2.0.1", control: "5.1.1",
		title: "Ensure that the cluster-admin role is only used where required"}
	refRBACWildcard = reference{source: "CIS Kubernetes Benchmark 2.0.1", control: "5.1.3",
		title: "Minimize wildcard use in Roles and ClusterRoles"}
	refPodSecurityHostNS = reference{source: "Kubernetes Pod Security Standards (Baseline)", control: "Host Namespaces",
		title: "hostNetwork, hostPID and hostIPC must not be true"}
	refPodSecurityNonRoot = reference{source: "Kubernetes Pod Security Standards (Restricted)", control: "Running as Non-root",
		title: "runAsNonRoot must be true, or the effective runAsUser must not be 0"}
	refResourcePolicies = reference{source: "NSA/CISA Kubernetes Hardening Guidance v1.0", control: "Resource policies",
		title: "LimitRange and ResourceQuota limit per-namespace resource usage; CIS Kubernetes " +
			"Benchmark 2.0.1 has no numbered control for this — confirmed absent by reading every " +
			"recommendation in its Section 5 rather than assumed"}
	refNetworkPolicy = reference{source: "CIS Kubernetes Benchmark 2.0.1", control: "5.3.2",
		title: "Ensure that all Namespaces have NetworkPolicies defined"}
)

// referenceTable lists, once each, the controls this run actually cited —
// the part of the report that outlives the run. A reader who wants to fix a
// finding needs the weakness described, not just named, and looking up ten
// rows of "CWE-1021" by hand is the sort of friction that ends with the
// finding unfixed.
func referenceTable(findings []finding) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Reference"},
		{Name: "Weakness"},
		{Name: "Lookup"},
	}}
	seen := map[string]bool{}
	var cited []reference
	for _, f := range findings {
		key := f.ref.String()
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		cited = append(cited, f.ref)
	}
	sort.Slice(cited, func(i, j int) bool { return cited[i].String() < cited[j].String() })
	for _, r := range cited {
		t.Rows = append(t.Rows, []string{r.String(), r.title, r.url()})
	}
	t.Total = len(t.Rows)
	return t
}
