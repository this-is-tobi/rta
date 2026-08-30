package audit

import (
	"path"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Where an audit stops, and what goes further.
//
// Every capability here reads something somebody already has — a response a
// host volunteered, a lockfile a package manager committed, a DNS zone — and
// grades it. That is deliberately not a scanner: nothing is resolved,
// crawled, or synced from a vulnerability database, because a badly
// reimplemented trivy is worse than no trivy. The cost is a ceiling, and a
// report that hits its ceiling quietly is a report that reads as an
// all-clear.
//
// So each audit names what it could not answer and the tool that answers it,
// with the subject already substituted in — the handoff `audit deps` already
// makes to `go mod why`, applied to the whole plugin. It is a pointer and
// never a wrapper: rta does not run these, does not parse their output and
// does not track their flags. What it owes is the invocation with the target
// already in it, so the next step is a paste and not a search.
//
// Keyed by the question rather than by the word "deeper", because "severity"
// and "unused" tell somebody whether the row is for them and "deeper" does
// not. On the detail page only: the compact table is the grade, and a list of
// other people's tools is not a finding.

// nextStep is one thing this audit did not answer, and what does.
//
// Named nextStep and not deeper because graph.go already spends that word on
// the ellipsis a truncated chain ends with.
type nextStep struct {
	// question is the pair's key: two or three words, the thing being asked.
	question string
	// cmds are invocations with the target already in them. More than one
	// where the tools are genuinely alternatives.
	cmds []string
	// because says what they add. Without it the row is a tool name, and a
	// tool name is a search.
	because string
}

func (d nextStep) pair() view.Pair {
	quoted := make([]string, len(d.cmds))
	for i, c := range d.cmds {
		quoted[i] = "`" + c + "`"
	}
	return view.Pair{Key: d.question, Value: strings.Join(quoted, " or ") + " — " + d.because}
}

func nextStepPairs(steps []nextStep) []view.Pair {
	out := make([]view.Pair, 0, len(steps))
	for _, s := range steps {
		if len(s.cmds) == 0 {
			continue
		}
		out = append(out, s.pair())
	}
	return out
}

// nativeAudit is an ecosystem's own "check my dependencies" command.
//
// Named ahead of the generic scanners because a package manager's auditor
// knows its own resolution rules, and one of them knows more than that:
// govulncheck answers reachability rather than presence, which is the
// difference between "this module is in your build" and "your code can
// actually get there". No manifest reader can say that, this one least of
// all.
//
// Dispatched on the lockfile rather than the ecosystem wherever the
// ecosystem is coarser than the answer — four JavaScript package managers
// share one OSV ecosystem name, and `npm audit` in a pnpm repository is a
// wasted paste. The same reasoning whyCommand records.
func nativeAudit(manifest string) string {
	switch path.Base(manifest) {
	case "go.mod":
		return "govulncheck ./..."
	case "package-lock.json":
		return "npm audit"
	case "pnpm-lock.yaml":
		return "pnpm audit"
	case "yarn.lock":
		return "yarn npm audit"
	case "bun.lock", "bun.lockb":
		return "bun audit"
	case "Cargo.lock":
		return "cargo audit"
	case "poetry.lock", "uv.lock", "Pipfile.lock", "requirements.txt":
		return "pip-audit"
	case "composer.lock":
		return "composer audit"
	case "Gemfile.lock":
		return "bundle audit check --update"
	}
	return ""
}

// unusedCommand finds what is declared and never imported.
//
// It belongs in a supply-chain report and not beside it: the cheapest fix for
// a dependency named in an advisory is deleting it, and a direct dependency
// nothing imports is one nobody will miss. Every ecosystem has a tool and
// none of them agrees on a name.
func unusedCommand(manifest string) string {
	switch path.Base(manifest) {
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb":
		return "knip"
	case "go.mod":
		return "go mod tidy"
	case "Cargo.lock":
		return "cargo machete"
	case "poetry.lock", "uv.lock", "Pipfile.lock", "requirements.txt":
		return "deptry ."
	}
	return ""
}

// depsDeeper is the set for one dependency audit, chosen from what was
// actually read rather than listed unconditionally: a Go project has no use
// for a row about knip, and a report that names tools for ecosystems it did
// not find is a report nobody finishes reading.
func depsDeeper(target string, remote bool, manifests []string) []view.Pair {
	native := pickCommands(manifests, nativeAudit)
	unused := pickCommands(manifests, unusedCommand)
	// A repository read over the network has no path to hand anybody. trivy
	// is the one of these that takes a URL and does its own clone, so it gets
	// the URL; the rest are shown as they would be run in a checkout, which
	// is the honest instruction rather than a command that would fail.
	severity := []string{"trivy fs " + target, "grype dir:" + target}
	adds := "the OSV batch endpoint answers with advisory identifiers and carries no severity " +
		"or fixed version, which is the one thing you need to decide whether to stop what you are doing"
	if remote {
		severity = []string{"trivy repo " + target}
		adds += ". trivy takes the URL and clones it itself; the rows below run in a checkout"
	}
	return nextStepPairs([]nextStep{
		{"severity", severity, adds},
		{nativeQuestion(native), native, nativeBecause(native)},
		{"unused", unused,
			"declared dependencies nothing imports. Deleting one is the cheapest fix an advisory " +
				"ever has, and it is the fix nobody looks for"},
		{"an sbom to keep", []string{"syft " + local(target, remote) + " -o cyclonedx-json"},
			"this inventory is read once and thrown away; a committed SBOM is what the next " +
				"advisory gets checked against, and `rta audit deps` reads one back"},
	})
}

// pickCommands maps the manifests that were read onto commands, deduplicated
// and ordered, so a monorepo with three ecosystems names three tools once
// each rather than eleven times in file order.
func pickCommands(manifests []string, of func(string) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range manifests {
		if c := of(m); c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// local is the path a command that needs files on disk should be pointed at:
// what was typed for a directory here, and the working directory for a
// repository nobody has checked out yet.
func local(target string, remote bool) string {
	if remote {
		return "."
	}
	return target
}

// The Go row is a different claim from the others, and makes it only where it
// applies. govulncheck answers reachability — whether your code can get to
// the vulnerable function — which none of the other auditors here does;
// printing that sentence over `pnpm audit` describes something pnpm does not
// do, and a report that overstates one tool is a report you check the rest of.
func nativeQuestion(cmds []string) string {
	if hasGovulncheck(cmds) {
		return "reachability"
	}
	return "the native auditor"
}

func nativeBecause(cmds []string) string {
	base := "each ecosystem's own auditor, which knows its own resolution rules — a lockfile " +
		"reader does not"
	if hasGovulncheck(cmds) {
		return base + ". govulncheck goes further and answers whether your code can reach the " +
			"vulnerable function at all, rather than whether the module is in your build"
	}
	return base
}

func hasGovulncheck(cmds []string) bool {
	for _, c := range cmds {
		if strings.HasPrefix(c, "govulncheck") {
			return true
		}
	}
	return false
}

// webDeeper is the set for a web audit. One request, one URL, so what it
// cannot answer is everything about a *conversation* — which protocol
// versions and cipher suites the host would accept, and what the pages it
// serves are made of.
func webDeeper(hostPort string) []view.Pair {
	return nextStepPairs([]nextStep{
		{"protocols and ciphers", []string{"testssl.sh " + hostPort, "sslyze " + hostPort},
			"this grades the one handshake that happened, not the whole set the host would " +
				"agree to — a server that still accepts TLS 1.0 answers TLS 1.3 to a modern client"},
		{"the whole site", []string{"nuclei -u https://" + hostPort, "zap-cli quick-scan https://" + hostPort},
			"one URL was fetched and graded, and headers differ per route. Both of these are " +
				"*active* scanners where everything above is one passive request — run them on a " +
				"host you are responsible for, and not on one you were merely curious about"},
	})
}

// mailDeeper is the set for a mail audit. The records are public and this
// reads all of them; what it cannot do is watch what actually arrives.
func mailDeeper(domain string) []view.Pair {
	return nextStepPairs([]nextStep{
		{"what receivers see", []string{"checkdmarc " + domain},
			"the same records, cross-checked against each other and against the SPF lookup " +
				"limit in more detail than one pass over the zone"},
		{"who is sending as you", []string{"parsedmarc"},
			"a record says what you authorised; only the DMARC aggregate reports say who is " +
				"actually sending mail with your domain on it. It needs a `rua=` mailbox first, " +
				"which is what p=none is for — collecting those reports before you enforce"},
	})
}
