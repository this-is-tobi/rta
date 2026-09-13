package findings

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Reference cites where a finding comes from, so it can be looked up rather
// than taken on faith. Two shapes share the struct: an OWASP Top 10:2025
// category (the current edition — it renumbered 2021's, so citing "A05
// Security Misconfiguration" out of habit would now point at the wrong
// entry) plus a MITRE CWE weakness; or a named framework (a CIS Benchmark,
// the NSA/CISA Kubernetes Hardening Guidance, Kubernetes' own Pod Security
// Standards) and the specific control it names.
//
// Whichever shape, verify the citation against the primary source before
// shipping it, and say in the finding's own comment when the source checked
// was a secondary mirror instead. A hardening tool that cites the wrong
// control is worse than one that cites none, because the wrong one still
// reads as authoritative.
type Reference struct {
	OWASP string // an OWASP Top 10:2025 category, one of the constants below
	CWE   string // "CWE-319"

	// Source and Control are the alternative shape: a named framework
	// ("CIS Kubernetes Benchmark 2.0.1") and the specific control within it
	// ("5.1.3", or a Pod Security Standards control name where CIS has no
	// number for the same thing). Set instead of OWASP/CWE, never alongside.
	Source  string
	Control string

	Title string // what the cited control actually says

	// Link is where the control is read, for the Source/Control shape: an
	// RFC section, a guide's heading. A CWE needs none — every weakness has
	// one stable page and URL derives it — and a framework whose text sits
	// behind a registration gate (a CIS Benchmark) leaves it empty rather
	// than pointing at a mirror that goes stale or, worse, at the wrong
	// version. Verify it the way the citation itself was verified: a link
	// that lands somewhere else is a wrong citation with a click attached.
	Link string
}

// String is the compact citation shown in a findings row.
func (r Reference) String() string {
	switch {
	case r.Control != "":
		return r.Source + " " + r.Control
	case r.CWE != "":
		return r.OWASP + " · " + r.CWE
	}
	return ""
}

// URL points at the reference's own lookup page: Link when the citation
// carries one, the weakness's page on cwe.mitre.org for a CWE, and empty
// otherwise — a report with no link in a cell is better than one with a
// link that lands somewhere else.
func (r Reference) URL() string {
	switch {
	case r.Link != "":
		return r.Link
	case r.CWE != "":
		return "https://cwe.mitre.org/data/definitions/" + strings.TrimPrefix(r.CWE, "CWE-") + ".html"
	}
	return ""
}

// The OWASP Top 10:2025 categories, titled as the edition titles them and
// verified against top10.owasp.org/2025. Constants rather than strings a
// plugin types, because References dedupes on the rendered citation: two
// audits spelling one category two ways would list it twice.
const (
	OWASPAccessControl = "A01:2025 Broken Access Control"
	OWASPMisconfig     = "A02:2025 Security Misconfiguration"
	OWASPSupplyChain   = "A03:2025 Software Supply Chain Failures"
	OWASPCrypto        = "A04:2025 Cryptographic Failures"
	OWASPInjection     = "A05:2025 Injection"
	OWASPDesign        = "A06:2025 Insecure Design"
	OWASPAuth          = "A07:2025 Authentication Failures"
	OWASPIntegrity     = "A08:2025 Software or Data Integrity Failures"
	OWASPLogging       = "A09:2025 Security Logging and Alerting Failures"
	OWASPExceptions    = "A10:2025 Mishandling of Exceptional Conditions"
)

// References lists, once each, the controls this report actually cited — the
// part of the report that outlives the run. A reader who wants to fix a
// finding needs the weakness described, not just named, and looking up ten
// rows of "CWE-1021" by hand is the sort of friction that ends with the
// finding unfixed.
func (r *Report) References() view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Reference"},
		{Name: "Weakness"},
		{Name: "Lookup"},
	}}
	seen := map[string]bool{}
	var cited []Reference
	for _, f := range r.Findings {
		key := f.Ref.String()
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		cited = append(cited, f.Ref)
	}
	sort.Slice(cited, func(i, j int) bool { return cited[i].String() < cited[j].String() })
	for _, ref := range cited {
		t.Rows = append(t.Rows, []string{ref.String(), ref.Title, ref.URL()})
	}
	t.Total = len(t.Rows)
	return t
}
