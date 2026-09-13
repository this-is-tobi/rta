package audit

import "github.com/this-is-tobi/rta/pkg/findings"

// The controls this plugin's checks cite. Every one was verified rather than
// recalled — the OWASP/CWE pairs against cwe.mitre.org, the OWASP ZAP alert
// database and the OWASP Cheat Sheet Series; the kube.* framework citations
// against the primary source where one is fetchable, and flagged below where
// the source checked was a secondary mirror instead. A hardening tool that
// cites the wrong control is worse than one that cites none, because the
// wrong one still reads as authoritative.

// The one Source/Control citation here with a public page to link. CIS
// gates its benchmark and the NSA/CISA guidance is a PDF whose location has
// moved between versions, so those two keep an empty Link on purpose.
const podSecurityStandards = "https://kubernetes.io/docs/concepts/security/pod-security-standards/"

var (
	refCleartext      = findings.Reference{OWASP: findings.OWASPCrypto, CWE: "CWE-319", Title: "Cleartext Transmission of Sensitive Information"}
	refWeakCrypto     = findings.Reference{OWASP: findings.OWASPCrypto, CWE: "CWE-327", Title: "Use of a Broken or Risky Cryptographic Algorithm"}
	refCertValidation = findings.Reference{OWASP: findings.OWASPCrypto, CWE: "CWE-295", Title: "Improper Certificate Validation"}
	refMisconfig      = findings.Reference{OWASP: findings.OWASPMisconfig, CWE: "CWE-693", Title: "Protection Mechanism Failure"}
	refClickjacking   = findings.Reference{OWASP: findings.OWASPMisconfig, CWE: "CWE-1021", Title: "Improper Restriction of Rendered UI Layers or Frames"}
	refCORS           = findings.Reference{OWASP: findings.OWASPMisconfig, CWE: "CWE-942", Title: "Permissive Cross-domain Policy with Untrusted Domains"}
	refInfoExposure   = findings.Reference{OWASP: findings.OWASPMisconfig, CWE: "CWE-200", Title: "Exposure of Sensitive Information to an Unauthorized Actor"}
	refCookieSecure   = findings.Reference{OWASP: findings.OWASPAuth, CWE: "CWE-614", Title: "Sensitive Cookie Without 'Secure' Attribute"}
	refCookieHTTPOnly = findings.Reference{OWASP: findings.OWASPAuth, CWE: "CWE-1004", Title: "Sensitive Cookie Without 'HttpOnly' Flag"}
	refCSRF           = findings.Reference{OWASP: findings.OWASPAccessControl, CWE: "CWE-352", Title: "Cross-Site Request Forgery"}
	refSpoofing       = findings.Reference{OWASP: findings.OWASPAuth, CWE: "CWE-290", Title: "Authentication Bypass by Spoofing"}
	refVulnerableDep  = findings.Reference{OWASP: findings.OWASPSupplyChain, CWE: "CWE-1395", Title: "Dependency on Vulnerable Third-Party Component"}
	refUnpinnedDep    = findings.Reference{OWASP: findings.OWASPIntegrity, CWE: "CWE-494", Title: "Download of Code Without Integrity Check"}
	// A credential a configuration file holds in plain text, and a file whose
	// mode lets somebody else read it, are two different weaknesses with two
	// different fixes — move the secret, or change the mode — so they cite
	// the two CWEs that say so rather than one that covers neither exactly.
	refCredExposed = findings.Reference{OWASP: findings.OWASPMisconfig, CWE: "CWE-522", Title: "Insufficiently Protected Credentials"}
	// An agent allowed every tool is a principal running with more authority
	// than the task needs, which is what CWE-250 names. It is Broken Access
	// Control rather than Misconfiguration because the setting *is* the
	// access-control decision. Reused by audit.kube.podsecurity for a
	// privileged container: the same weakness, a different mechanism.
	refExcessivePriv = findings.Reference{OWASP: findings.OWASPAccessControl, CWE: "CWE-250", Title: "Execution with Unnecessary Privileges"}

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
	refRBACClusterAdmin = findings.Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.1.1",
		Title: "Ensure that the cluster-admin role is only used where required"}
	refRBACWildcard = findings.Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.1.3",
		Title: "Minimize wildcard use in Roles and ClusterRoles"}
	refPodSecurityHostNS = findings.Reference{Source: "Kubernetes Pod Security Standards (Baseline)", Control: "Host Namespaces",
		Title: "hostNetwork, hostPID and hostIPC must not be true", Link: podSecurityStandards}
	refPodSecurityNonRoot = findings.Reference{Source: "Kubernetes Pod Security Standards (Restricted)", Control: "Running as Non-root",
		Title: "runAsNonRoot must be true, or the effective runAsUser must not be 0", Link: podSecurityStandards}
	refResourcePolicies = findings.Reference{Source: "NSA/CISA Kubernetes Hardening Guidance v1.0", Control: "Resource policies",
		Title: "LimitRange and ResourceQuota limit per-namespace resource usage; CIS Kubernetes " +
			"Benchmark 2.0.1 has no numbered control for this — confirmed absent by reading every " +
			"recommendation in its Section 5 rather than assumed"}
	refNetworkPolicy = findings.Reference{Source: "CIS Kubernetes Benchmark 2.0.1", Control: "5.3.2",
		Title: "Ensure that all Namespaces have NetworkPolicies defined"}
)
