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
				// **The same primitive http.get is gated for, under a
				// different name.** The lesson is that `Read` is the
				// class that costs nothing to reach — read capabilities go
				// onto every `rta mcp serve` with no --allow-write, no grant
				// and read_only_hint: true — so a capability that fetches a
				// caller-chosen URL and returns what came back cannot be one.
				//
				// http.get carries NeedsGrant + Scope:"url" because the
				// response arrives in a model's context and because the
				// request itself is an outbound channel: everything after the
				// `?` is bytes an agent chose, delivered to a host an agent
				// chose. This performed exactly that request while http.get
				// was refused beside it — two blast radii wearing one name,
				// which is the shape that split net.probe and net.send
				// over.
				//
				// Scoped to the host rather than the URL: an audit is about a
				// host, and "this agent may audit staging.internal for the
				// next fifteen minutes" is the sentence an operator can
				// actually reason about.
				NeedsGrant: true,
				Scope:      "host",
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
				ID:           "audit.deps",
				Summary:      "Check declared dependencies against the OSV vulnerability database",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Detailed:     true,
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
					"already committed, and reading it is not scanning. Each advisory it finds is then " +
					"read for its severity and the versions that fix it — a second question asked only " +
					"about findings, so a project with nothing wrong asks nothing extra — and the " +
					"records every database publishes for one vulnerability are collapsed onto it, " +
					"since a package can otherwise be counted twice under a GHSA and a GO identifier " +
					"for the same CVE. It still points at osv-scanner, trivy or grype for the depth " +
					"this does not have: whether your code reaches the vulnerable function at all, " +
					"which no advisory can say. " +
					"Components whose ecosystem OSV does not recognise are counted and named rather " +
					"than dropped. --offline inventories without asking anything. --recursive walks a " +
					"monorepo, skipping node_modules, vendor and build output, which is what a " +
					"polyglot repository needs: one lockfile per service and no single file that " +
					"knows about the others. From a terminal --path also takes a repository URL, " +
					"read shallowly in memory and never written to disk, which is how you audit a " +
					"repository you have not checked out — refused over MCP, since a URL a caller " +
					"composes is a request rta makes on its behalf. Cites A03:2025 Software Supply " +
					"Chain Failures and CWE-1395.",
				Inputs: []plugin.Field{
					{Name: "path", Type: plugin.Path, Positional: true, Default: ".",
						Help: pathHelp("directory holding the lockfile or SBOM, or the file itself")},
					{Name: "recursive", Type: plugin.Bool,
						Help: "walk subdirectories too, for a monorepo with a manifest per package"},
					{Name: "offline", Type: plugin.Bool, Help: "inventory the dependencies without querying osv.dev"},
					{Name: "timeout", Type: plugin.Int, Default: 30, Min: 1, Max: 300, Help: "query timeout in seconds"},
				},
				Run: runDeps,
			},
			{
				ID:           "audit.why",
				Summary:      "Trace one dependency back to what pulled it in",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				// Same argument as audit.deps: it reads a path with a usable
				// default and mutates nothing, which is the shape the dashboard
				// fills itself with — and a tile asking "why is lodash here"
				// about a project nobody named is a question with no subject.
				NoPreview: true,
				Description: "Answers the question an advisory raises second: did we ask for this, " +
					"or did something else pull it in — and if something else, what. Reads the same " +
					"manifests `audit deps` does and draws the package at the root with everything " +
					"that requires it beneath, up to the dependencies the project asks for by name. " +
					"Nothing is resolved, installed or fetched. Formats differ in what they record: " +
					"go.mod marks a require direct or indirect and stores no edges, while the four " +
					"JavaScript lockfiles, Cargo, uv, Poetry, composer, Gemfile.lock, pip-compile's " +
					"`# via` annotations and a CycloneDX SBOM all carry the graph. Where the file " +
					"does not say, this says so and hands over the package manager's own command — " +
					"`go mod why`, `pnpm why`, `cargo tree --invert` — with the package already in it.",
				Inputs: []plugin.Field{
					{Name: "package", Type: plugin.String, Positional: true, Required: true,
						Help: "the package to trace, as the lockfile spells it"},
					{Name: "path", Type: plugin.Path, Default: ".",
						Help: pathHelp("directory holding the lockfile or SBOM, or the file itself")},
					{Name: "recursive", Type: plugin.Bool,
						Help: "walk subdirectories too, for a monorepo with a manifest per package"},
				},
				Run: runWhy,
			},
			{
				ID:         "audit.agents",
				Summary:    "Audit the AI agents configured on this machine — tools, servers, credentials",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				// A dashboard tile that read every agent config on a timer
				// would be a tile listing where each of them keeps its
				// credential, redrawn every few seconds.
				NoPreview: true,
				Description: "An agent's configuration decides more about a machine's exposure than " +
					"most of what this plugin grades, and nothing reads it. This does: which tools " +
					"the model may run, which MCP servers launch with it, whether any of them is " +
					"handed a credential in plain text, whether one is fetched from a registry at " +
					"launch rather than pinned, whether the files are readable by anybody else, and " +
					"whether the endpoint every prompt is sent to is the one you expect. " +
					"**It reads and never writes** — that file is what grants an agent access to " +
					"your secrets, and the change is yours to make. Credential *names* are reported " +
					"and values never are. It also states the boundary out loud: an agent that can " +
					"run commands can run rta directly, so rta bounds an agent without a shell and " +
					"is hygiene rather than containment for one with. Refused over MCP, because the " +
					"subject of this audit is the agent asking. Cites A01:2025 Broken Access " +
					"Control and A02:2025 Security Misconfiguration. `--fix` prints the exact edit " +
					"for each finding that has one — a chmod, a pinned version, a scoped shell " +
					"allowlist, a deny list for rta's own authority-expanding commands — and still " +
					"writes nothing: the change stays yours to make.",
				Inputs: []plugin.Field{
					{Name: "fix", Type: plugin.Bool,
						Help: "print the exact edit for each finding that has one, instead of the grades"},
				},
				Run: runAgents,
			},
			{
				ID:         "audit.kube.rbac",
				Summary:    "Overly-broad RBAC: cluster-admin bindings and wildcard rules",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				// Reaches a configured cluster the same way kube.pod.list
				// does, so it gets the same treatment: not on a dashboard
				// that redraws on a timer with no one having asked for it.
				NoPreview: true,
				Description: "Every ClusterRoleBinding to cluster-admin, and every Role/ClusterRole " +
					"rule using a wildcard verb, resource or apiGroup. cluster-admin bound to a broad " +
					"group (system:authenticated, system:unauthenticated, system:masters) grades fail; " +
					"bound to anything else grades warn — sometimes a real operator's binding, always " +
					"worth reading. Cites CIS Kubernetes Benchmark 2.0.1 5.1.1 and 5.1.3.\n\nNarrowed to a " +
					"namespace it checks that namespace's Roles only. ClusterRoleBindings and ClusterRoles " +
					"belong to no namespace, so they are not examined at all — reported in the result " +
					"rather than left implied, since a scoped run that silently skipped the cluster-admin " +
					"check would read as a clean RBAC posture.",
				Scope:  "namespace",
				Inputs: []plugin.Field{namespaceField(), contextField()},
				Run:    runKubeRBAC,
			},
			{
				ID:         "audit.kube.podsecurity",
				Summary:    "Pods running privileged, in a host namespace, or with root not excluded",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				NoPreview:  true,
				Description: "Every pod cluster-wide, checked against three Pod Security Standards " +
					"controls: a privileged container (fail), hostNetwork/hostPID/hostIPC (fail), and " +
					"whether anything in the pod asserts non-root — neither the pod nor any container " +
					"sets runAsNonRoot or a non-zero runAsUser (warn). The non-root check approximates " +
					"Kubernetes' own securityContext merge rather than fully simulating it: a pod that " +
					"asserts non-root at the pod level and never overrides it per container is not " +
					"flagged, which is the common, correct case; one relying only on an image's own " +
					"non-root USER with no Kubernetes-level assertion at all is. Cites CWE-250 and " +
					"Kubernetes Pod Security Standards.\n\nA namespace narrows which pods are read, and the " +
					"clean result says so — it reports that no pod in that namespace is affected, never " +
					"that no pod in the cluster is.",
				Scope:  "namespace",
				Inputs: []plugin.Field{namespaceField(), contextField()},
				Run:    runKubePodSecurity,
			},
			{
				ID:         "audit.kube.quotas",
				Summary:    "Namespaces with no ResourceQuota — unbounded resource consumption",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				NoPreview:  true,
				Description: "Every non-system namespace checked for at least one ResourceQuota. CIS " +
					"Kubernetes Benchmark has no numbered control for this — confirmed absent rather " +
					"than assumed — so this cites NSA/CISA Kubernetes Hardening Guidance's " +
					"\"Resource policies\" section instead.\n\nNarrowed to a namespace it checks that one, " +
					"including a system namespace if you name it explicitly — the system-namespace filter " +
					"exists so a cluster-wide sweep stays quiet, not to refuse a direct question.",
				Scope:  "namespace",
				Inputs: []plugin.Field{namespaceField(), contextField()},
				Run:    runKubeQuotas,
			},
			{
				ID:         "audit.kube.netpol",
				Summary:    "Namespaces with no NetworkPolicy — unrestricted pod-to-pod traffic",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				NoPreview:  true,
				Description: "Every non-system namespace checked for at least one NetworkPolicy. With " +
					"none, the cluster's default applies: every pod in the namespace can reach every " +
					"other pod, in either direction. Cites CIS Kubernetes Benchmark 2.0.1 5.3.2.\n\nNarrowed " +
					"to a namespace it checks that one, including a system namespace if you name it " +
					"explicitly.",
				Scope:  "namespace",
				Inputs: []plugin.Field{namespaceField(), contextField()},
				Run:    runKubeNetworkPolicy,
			},
		},
	}
}
