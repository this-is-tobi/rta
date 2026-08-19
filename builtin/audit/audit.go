// Package audit is the built-in hardening toolbox: checks that grade
// something you run against a named security control, rather than just
// describing it.
//
// It is deliberately its own plugin and not a corner of net. The web check
// began there because it happens to speak HTTP, but "which TLS version did
// we negotiate" is not what it is for — the question is whether an
// application is safe to expose, and the answer belongs next to the other
// answers to that question, not next to traceroute and the hosts file. The
// namespace is the promise: `rta audit <thing>` is where hardening lives.
//
// Two rules keep the toolbox honest as it grows:
//
//   - It does not reimplement scanners. Nothing here crawls, fuzzes,
//     brute-forces or exploits. Each check is one interaction, read-only,
//     safe to point at production, and fast enough to run in a loop. When a
//     finding warrants a real scanner, this is what tells you to reach for
//     one — ZAP, Nikto and testssl.sh exist and are better at their jobs.
//   - Every finding cites its source. A grade with no reference is an
//     opinion; a grade carrying an OWASP Top 10 category and a MITRE CWE is
//     something the reader can look up and disagree with (see refs.go).
package audit

import (
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Plugin returns the audit plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "audit",
		Summary: "Security hardening checks, each graded against a named OWASP/CWE control",
		Capabilities: []plugin.Capability{
			{
				ID:         "audit.web",
				Summary:    "Audit a web host: TLS, certificate, headers, cookies, exposure",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				Description: "One HTTPS request, graded: TLS version and cipher, certificate validity, " +
					"expiry and signature algorithm, the security headers a host should send (HSTS, CSP " +
					"— checked for weak directives like unsafe-inline, not just presence — " +
					"X-Frame-Options/frame-ancestors, cross-origin isolation headers), CORS behavior " +
					"(probed with an Origin no real browser would send, to catch blind reflection), " +
					"cookie flags, and the version/stack details a host should not leak. Every finding " +
					"cites an OWASP Top 10:2025 category and a MITRE CWE, so a result traces back to a " +
					"named control rather than an opinion. With --detail: one section per area plus the " +
					"cited references with lookup URLs. Read-only — it only inspects the response the " +
					"host volunteers, plus the one Origin header the CORS probe adds.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true, Help: "host or URL to audit"},
					{Name: "timeout", Type: plugin.Int, Default: 15, Min: 1, Max: 300, Help: "request timeout in seconds"},
				},
				Run: runWeb,
			},
			{
				ID:         "audit.mail",
				Summary:    "Audit a domain's email authentication: SPF, DKIM, DMARC, MTA-STS",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				Description: "Answers \"can somebody send mail as this domain\" from the records the " +
					"domain publishes: SPF (present, singular, how it ends, and how close it is to " +
					"RFC 7208's ten-lookup limit past which it silently stops applying), DMARC (policy, " +
					"rollout percentage, whether anything reports back), DKIM for a given selector, " +
					"MTA-STS and TLS-RPT for the server-to-server hop, and MX — including RFC 7505's " +
					"null MX, which is a hardening measure rather than an omission. Every finding cites " +
					"an OWASP Top 10:2025 category and a MITRE CWE. With --detail: one section per area " +
					"plus the cited references with lookup URLs. Read-only, a handful of lookups of " +
					"names derived from the domain by rule — nothing is enumerated, which is why DKIM " +
					"needs its selector handed to it.",
				Inputs: []plugin.Field{
					{Name: "domain", Type: plugin.String, Positional: true, Required: true, Help: "domain to audit"},
					{Name: "selector", Type: plugin.String, Help: "DKIM selector to check (the s= tag of a DKIM-Signature)"},
					{Name: "timeout", Type: plugin.Int, Default: 15, Min: 1, Max: 300, Help: "lookup timeout in seconds"},
				},
				Run: runMail,
			},
			{
				ID:         "audit.deps",
				Summary:    "Check declared dependencies against the OSV vulnerability database",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				// It reads a path with a usable default and mutates nothing,
				// which is exactly the shape the dashboard fills itself with —
				// and it would send this project's dependency list to osv.dev
				// every time somebody opened the TUI.
				NoPreview: true,
				Description: "Reads what a project already declares — go.mod, any of the four " +
					"JavaScript lockfiles (npm, pnpm, yarn, bun), uv.lock, poetry.lock, Pipfile.lock, " +
					"requirements.txt, Cargo.lock, composer.lock, Gemfile.lock, or a CycloneDX/SPDX " +
					"SBOM sitting beside them — and asks osv.dev " +
					"once about every pinned dependency, reporting which are named in an advisory. " +
					"Nothing is resolved, built or crawled: a lockfile is a list a package manager " +
					"already committed, and reading it is not scanning. The batch endpoint answers with " +
					"advisory identifiers, not severities or fixed versions, so a hit points at " +
					"osv-scanner, trivy or grype for the depth this deliberately does not have. " +
					"Components whose ecosystem OSV does not recognise are counted and named rather " +
					"than dropped. --offline inventories without asking anything. --recursive walks a " +
					"monorepo, skipping node_modules, vendor and build output, which is what a " +
					"polyglot repository needs: one lockfile per service and no single file that " +
					"knows about the others. Cites A03:2025 Software Supply Chain Failures and " +
					"CWE-1395.",
				Inputs: []plugin.Field{
					{Name: "path", Type: plugin.Path, Positional: true, Default: ".",
						Help: "directory holding the lockfile or SBOM, or the file itself"},
					{Name: "recursive", Type: plugin.Bool,
						Help: "walk subdirectories too, for a monorepo with a manifest per package"},
					{Name: "offline", Type: plugin.Bool, Help: "inventory the dependencies without querying osv.dev"},
					{Name: "timeout", Type: plugin.Int, Default: 30, Min: 1, Max: 300, Help: "query timeout in seconds"},
				},
				Run: runDeps,
			},
		},
	}
}
