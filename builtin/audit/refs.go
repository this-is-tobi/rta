package audit

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// reference cites where a finding comes from, so it can be looked up rather
// than taken on faith: the OWASP Top 10:2025 category (the current edition —
// it renumbered 2021's, so citing "A05 Security Misconfiguration" out of
// habit would now point at the wrong entry) and the specific MITRE CWE
// weakness. Both were verified against cwe.mitre.org, the OWASP ZAP alert
// database and the OWASP Cheat Sheet Series rather than recalled — a
// hardening tool that cites the wrong control is worse than one that cites
// none, because the wrong one still reads as authoritative.
type reference struct {
	owasp string // OWASP Top 10:2025 category
	cwe   string // "CWE-319"
	title string // the CWE's own name for the weakness
}

// String is the compact citation shown in a findings row.
func (r reference) String() string {
	if r.cwe == "" {
		return ""
	}
	return r.owasp + " · " + r.cwe
}

// url points at the CWE's definition page, which is the entry point to its
// description, mitigations and related weaknesses.
func (r reference) url() string {
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
	refCleartext      = reference{owaspCrypto, "CWE-319", "Cleartext Transmission of Sensitive Information"}
	refWeakCrypto     = reference{owaspCrypto, "CWE-327", "Use of a Broken or Risky Cryptographic Algorithm"}
	refCertValidation = reference{owaspCrypto, "CWE-295", "Improper Certificate Validation"}
	refMisconfig      = reference{owaspMisconfig, "CWE-693", "Protection Mechanism Failure"}
	refClickjacking   = reference{owaspMisconfig, "CWE-1021", "Improper Restriction of Rendered UI Layers or Frames"}
	refCORS           = reference{owaspMisconfig, "CWE-942", "Permissive Cross-domain Policy with Untrusted Domains"}
	refInfoExposure   = reference{owaspMisconfig, "CWE-200", "Exposure of Sensitive Information to an Unauthorized Actor"}
	refCookieSecure   = reference{owaspAuth, "CWE-614", "Sensitive Cookie Without 'Secure' Attribute"}
	refCookieHTTPOnly = reference{owaspAuth, "CWE-1004", "Sensitive Cookie Without 'HttpOnly' Flag"}
	refCSRF           = reference{owaspAccessControl, "CWE-352", "Cross-Site Request Forgery"}
	refSpoofing       = reference{owaspAuth, "CWE-290", "Authentication Bypass by Spoofing"}
	refVulnerableDep  = reference{owaspSupplyChain, "CWE-1395", "Dependency on Vulnerable Third-Party Component"}
	refUnpinnedDep    = reference{owaspIntegrity, "CWE-494", "Download of Code Without Integrity Check"}
	// A credential a configuration file holds in plain text, and a file whose
	// mode lets somebody else read it, are two different weaknesses with two
	// different fixes — move the secret, or change the mode — so they cite
	// the two CWEs that say so rather than one that covers neither exactly.
	refCredExposed = reference{owaspMisconfig, "CWE-522", "Insufficiently Protected Credentials"}
	// An agent allowed every tool is a principal running with more authority
	// than the task needs, which is what CWE-250 names. It is Broken Access
	// Control rather than Misconfiguration because the setting *is* the
	// access-control decision.
	refExcessivePriv = reference{owaspAccessControl, "CWE-250", "Execution with Unnecessary Privileges"}
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
		if f.ref.cwe == "" || seen[f.ref.cwe] {
			continue
		}
		seen[f.ref.cwe] = true
		cited = append(cited, f.ref)
	}
	sort.Slice(cited, func(i, j int) bool { return cited[i].String() < cited[j].String() })
	for _, r := range cited {
		t.Rows = append(t.Rows, []string{r.String(), r.title, r.url()})
	}
	t.Total = len(t.Rows)
	return t
}
