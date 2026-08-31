// Package policy is the ceiling: what a team says no grant on this machine
// may exceed, in a file they can commit to the repository they already share.
//
// # Why this is shareable when the grant file is not
//
// A grant is consent — a first-person, time-boxed act by a person at a
// terminal — so a shared grant file would mean one person's
// consent authorizing another person's agent, on another machine, against
// another set of credentials. That is not a sharing feature; it is the
// removal of the control. The grant file is also sealed with a machine-local
// key, so sharing one means dropping the seal or sharing the key.
//
// A ceiling has the opposite shape, and one property is the whole argument:
// **it can only subtract authority.** A grant file has to be trusted because
// a forged line *adds* permission. Nothing here can add anything — the worst
// a hostile edit achieves is that rta refuses more than it should, which is
// loud, local and recoverable. So this file needs no seal, no key
// distribution and no trust in its transport, and several of them intersect
// rather than override: on every axis the strictest wins.
//
// # It is advice to a machine, not a control over one
//
// The operator owns the machine and the binary. A ceiling they can delete is
// a ceiling they can escape, and saying so is the point rather than a
// caveat. The alternative is a feature that presents as enforcement and is
// not, and a bound that reports itself without being enforced is worse than
// no bound at all.
//
// What this defends against is the 3am "allow everything for twenty-four
// hours so it stops failing", which is the failure teams actually have and
// the one just-in-time consent was written against from the other direction.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/internal/config"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// RepoFile is the name looked for on the way up from the working directory.
// Dotted and rta-named, so a repository holding one is obviously holding one.
const RepoFile = ".rta-policy.yaml"

// maxWalk bounds the climb toward the filesystem root. A path deeper than
// this is not a repository checkout, and an unbounded loop over a path is
// worth refusing to write even where the loop would terminate anyway.
const maxWalk = 64

// Ceiling is what no grant may exceed. Every field narrows; none widens, and
// there is deliberately no field that could.
type Ceiling struct {
	// MaxTTL caps how long any grant may stand, under grant.MaxTTL rather
	// than over it: this can only bring the day down.
	MaxTTL time.Duration `yaml:"-"`
	// RawTTL is what the file said, kept for the refusal to quote back.
	RawTTL string `yaml:"maxTTL,omitempty"`
	// Never lists targets — capability IDs or plugin names — that may not be
	// granted at all. `pg.dump` on a team that has decided a whole-database
	// dump is never an agent's business.
	Never []string `yaml:"never,omitempty"`
	// NeverProfile lists connections no grant may name. The blunt instrument
	// for "agents do not touch production", and blunt is the point.
	NeverProfile []string `yaml:"neverProfile,omitempty"`
	// RequireScope lists targets that may only be granted against a named
	// record. This is the "every secret at once" grant, refused: `kv.get`
	// with no scope authorizes the whole store, which is exactly the pressure
	// folder scopes exist to relieve.
	RequireScope []string `yaml:"requireScope,omitempty"`
	// RequireRepo says a repository policy must have been found, and is the
	// answer to the one attack the subtract-only property does not cover.
	//
	// Every other axis here defends against a hostile *edit*, which can only
	// ever remove authority. None of them defends against the file not being
	// there — and the failure modes are inverted, which is what makes it worth
	// a mechanism. Corrupt .rta-policy.yaml and every grant load fails loudly.
	// Delete it, or replace its contents with `{}`, and rta runs with no
	// ceiling and says nothing, because a machine whose policy vanished is
	// indistinguishable from a machine that never had one.
	//
	// A file cannot close that on its own: a repository policy demanding a
	// repository policy is deleted along with the demand. So this is honoured
	// only from the operator's own policy file and from RTA_POLICY — sources
	// that live outside the repository and survive a bad merge, a branch that
	// never carried the file, a `git clean`, and a client that launched rta
	// from somewhere else entirely.
	//
	// It only ever causes more refusals, so it keeps the property that makes
	// the rest of this package safe.
	RequireRepo bool `yaml:"requireRepoPolicy,omitempty"`
	// From names the files this ceiling was assembled from, nearest first, so
	// a refusal can say which one to go and edit.
	From []string `yaml:"-"`
	// RepoFound records whether the walk up from the working directory found
	// anything. Distinct from From being non-empty: the operator's own file is
	// also a source, and it is not the one RequireRepo is asking about.
	RepoFound bool `yaml:"-"`
	// SearchedFrom is the directory the walk started in, so a report can say
	// where rta looked rather than only that it found nothing. For an MCP
	// server this is whatever directory the client launched it from, which is
	// the fact most likely to be surprising.
	SearchedFrom string `yaml:"-"`
}

// Empty reports whether this ceiling constrains nothing, which is the answer
// on a machine with no policy file — and the answer that must leave every
// existing behaviour exactly as it was.
func (c Ceiling) Empty() bool {
	return c.MaxTTL == 0 && len(c.Never) == 0 &&
		len(c.NeverProfile) == 0 && len(c.RequireScope) == 0 && !c.RequireRepo
}

// Where names the policy files in force, for a message that has to send
// somebody to the right file.
func (c Ceiling) Where() string {
	if len(c.From) == 0 {
		return ""
	}
	return strings.Join(c.From, ", ")
}

// Forbids reports why this ceiling refuses a grant of target on scope through
// profile, or "" if it does not.
//
// The sentence is the return value because every caller needs it: refusing at
// `grant allow` is only useful if it says which rule and which file, and the
// consequence of that is that a ceiling which applies must say so out loud —
// a silent clamp teaches people to distrust the number they typed.
func (c Ceiling) Forbids(target, scope, profile string) string {
	for _, t := range c.Never {
		// A namespace in the file covers every capability in it, the same
		// widening a grant target already has: "pg" in `never` means no
		// grant on pg at all, not a grant on something literally named pg.
		if t == target || t == plugin.Namespace(target) {
			return fmt.Sprintf("%q is not grantable here", t)
		}
	}
	if profile != "" {
		for _, p := range c.NeverProfile {
			if p == profile {
				return fmt.Sprintf("no grant may name the %q connection", p)
			}
		}
	}
	if scope == "" {
		for _, t := range c.RequireScope {
			if t == target || t == plugin.Namespace(target) {
				return fmt.Sprintf("a grant on %q must name a record", t)
			}
		}
	}
	return ""
}

// Load assembles the ceiling in force for the working directory.
//
// **Every file found intersects; none overrides.** A monorepo whose root says
// 15m and whose subdirectory says 5m gets 5m, because a subdirectory may
// tighten and must not loosen — which is the same rule as everything else
// here and needs no separate mechanism.
//
// Three sources, all optional:
//
//   - RTA_POLICY, an explicit path. Unlike the others it must exist: naming a
//     file that is not there is a mistake somebody made, not a machine
//     without a policy, and quietly running with no ceiling is the failure
//     mode this whole package is written against.
//   - the operator's own, beside their config.
//   - .rta-policy.yaml on the way up from the working directory, which is the
//     one a team commits.
//
// Walking up from an arbitrary directory is safe *because* of the subtract-only
// property: the worst a file planted somewhere can do is make rta refuse more
// than the operator wanted, which is loud and immediate. rta already reads
// ./.rta.yaml as a config fallback, which is a strictly larger trust surface
// than this one.
func Load() (Ceiling, *view.Error) {
	var found []Ceiling
	// requireRepo is collected only from the two sources outside the
	// repository. A repository file asking for a repository file is deleted
	// along with its own demand, so honouring it there would be a check that
	// passes exactly when it is not needed.
	requireRepo := false

	if explicit := strings.TrimSpace(os.Getenv("RTA_POLICY")); explicit != "" {
		c, verr := read(explicit, true)
		if verr != nil {
			return Ceiling{}, verr
		}
		requireRepo = requireRepo || c.RequireRepo
		found = append(found, c)
	}

	repoFound, repoConstrains := false, false
	start, err := os.Getwd()
	if err == nil {
		dir := start
		for range maxWalk {
			candidate := filepath.Join(dir, RepoFile)
			if _, err := os.Stat(candidate); err == nil {
				c, verr := read(candidate, false)
				if verr != nil {
					return Ceiling{}, verr
				}
				repoFound = true
				// Presence is not the same as a ceiling. A file holding `{}`
				// parses, is found, and constrains nothing — so checking only
				// that something is there would make the quietest edit the
				// most effective one, which is the shape of the problem this
				// whole mechanism exists to fix.
				repoConstrains = repoConstrains || !c.Empty()
				found = append(found, c)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	if own := OperatorPath(); own != "" {
		c, verr := read(own, false)
		if verr != nil {
			return Ceiling{}, verr
		}
		requireRepo = requireRepo || c.RequireRepo
		found = append(found, c)
	}

	out := intersect(found)
	// **The one place the requirement is decided.** The assignment rather than
	// the intersect is deliberate: requireRepo was collected only inside the
	// two branches reading sources outside the repository, so a
	// .rta-policy.yaml setting the key cannot satisfy its own demand. Folding
	// it in through intersect would let a repository file require itself,
	// which passes in exactly the case this exists to catch — the file being
	// gone.
	out.RequireRepo, out.RepoFound, out.SearchedFrom = requireRepo, repoFound, start

	if requireRepo && repoFound && !repoConstrains {
		return out, view.Errorf("policy.repo.empty",
			"the %s found from %s constrains nothing, and this machine requires a ceiling",
			RepoFile, start).
			WithHint("a policy file holding no limits is not a ceiling — set maxTTL, never, " +
				"neverProfile or requireScope in it, or unset requireRepoPolicy in your own " +
				"policy file")
	}
	if requireRepo && !repoFound {
		// Fail closed, and say all three things somebody needs: what was
		// expected, where rta looked, and who asked for it. The last one
		// matters because this refusal appears on a machine whose repository
		// looks fine — the demand lives in a file they may have forgotten
		// setting.
		return out, view.Errorf("policy.repo.missing",
			"no %s found from %s, and this machine requires one", RepoFile, start).
			WithHint("either you are not in the repository you meant to be in — an MCP client " +
				"launches rta from a directory it chooses, not one you do — or the file was " +
				"removed. `rta policy show` says where rta looked; unset requireRepoPolicy in " +
				"your own policy file if this machine no longer needs one")
	}
	return out, nil
}

// OperatorPath is the operator's own policy file, beside their config.
//
// Derived from the config's own location rather than from os.UserConfigDir
// directly, because RTA_CONFIG moves the config and this used not to follow
// it. On a default install the two are identical; on a portable or managed
// setup they were not, so "beside their config" was false in exactly the
// deployments most likely to be relying on a policy — and requireRepoPolicy
// set there would have been written to a file rta never reads.
func OperatorPath() string {
	dir := filepath.Dir(config.Path())
	if dir == "" || dir == "." {
		return ""
	}
	return filepath.Join(dir, "policy.yaml")
}

// read parses one file. A missing file is an empty ceiling unless the caller
// asked for it by name.
func read(path string, mustExist bool) (Ceiling, *view.Error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if mustExist {
			return Ceiling{}, view.Errorf("policy.missing",
				"RTA_POLICY names %s, which does not exist", path).
				WithHint("unset RTA_POLICY, or create the file — running with no ceiling " +
					"because a path was wrong is the one outcome this must not have")
		}
		return Ceiling{}, nil
	}
	if err != nil {
		return Ceiling{}, view.Errorf("policy.unreadable", "reading %s: %v", path, err)
	}
	var c Ceiling
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Ceiling{}, view.Errorf("policy.malformed", "%s: %v", path, err).
			WithHint("a policy that cannot be parsed is not a policy — fix it, or " +
				"remove it and say so to whoever shares it")
	}
	if c.RawTTL != "" {
		d, err := time.ParseDuration(c.RawTTL)
		if err != nil || d <= 0 {
			return Ceiling{}, view.Errorf("policy.badttl",
				"%s: maxTTL %q is not a duration", path, c.RawTTL).
				WithHint("15m, 1h, 30s")
		}
		c.MaxTTL = d
	}
	c.From = []string{path}
	return c, nil
}

// intersect takes the strictest value on every axis.
func intersect(all []Ceiling) Ceiling {
	var out Ceiling
	seen := map[string]bool{}
	for _, c := range all {
		if c.MaxTTL > 0 && (out.MaxTTL == 0 || c.MaxTTL < out.MaxTTL) {
			out.MaxTTL, out.RawTTL = c.MaxTTL, c.RawTTL
		}
		out.Never = append(out.Never, c.Never...)
		out.NeverProfile = append(out.NeverProfile, c.NeverProfile...)
		out.RequireScope = append(out.RequireScope, c.RequireScope...)
		for _, f := range c.From {
			if !seen[f] {
				seen[f] = true
				out.From = append(out.From, f)
			}
		}
	}
	return out
}
