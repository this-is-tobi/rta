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
	// From names the files this ceiling was assembled from, nearest first, so
	// a refusal can say which one to go and edit.
	From []string `yaml:"-"`
}

// Empty reports whether this ceiling constrains nothing, which is the answer
// on a machine with no policy file — and the answer that must leave every
// existing behaviour exactly as it was.
func (c Ceiling) Empty() bool {
	return c.MaxTTL == 0 && len(c.Never) == 0 &&
		len(c.NeverProfile) == 0 && len(c.RequireScope) == 0
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
// ADR's own consequence is that a ceiling which applies must say so out loud —
// a silent clamp teaches people to distrust the number they typed.
func (c Ceiling) Forbids(target, scope, profile string) string {
	for _, t := range c.Never {
		// A namespace in the file covers every capability in it, the same
		// widening a grant target already has: "pg" in `never` means no
		// grant on pg at all, not a grant on something literally named pg.
		if t == target || t == namespaceOf(target) {
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
			if t == target || t == namespaceOf(target) {
				return fmt.Sprintf("a grant on %q must name a record", t)
			}
		}
	}
	return ""
}

// namespaceOf is the plugin part of a capability ID.
//
// Duplicated from internal/grant rather than imported, and deliberately: this
// package is below that one (grant reads a ceiling on every load), so the
// dependency has to run one way. It is four lines and it is the definition of
// a dot.
func namespaceOf(id string) string {
	if i := strings.IndexByte(id, '.'); i > 0 {
		return id[:i]
	}
	return id
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

	if explicit := strings.TrimSpace(os.Getenv("RTA_POLICY")); explicit != "" {
		c, verr := read(explicit, true)
		if verr != nil {
			return Ceiling{}, verr
		}
		found = append(found, c)
	}

	dir, err := os.Getwd()
	if err == nil {
		for range maxWalk {
			candidate := filepath.Join(dir, RepoFile)
			if _, err := os.Stat(candidate); err == nil {
				c, verr := read(candidate, false)
				if verr != nil {
					return Ceiling{}, verr
				}
				found = append(found, c)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	if base, err := os.UserConfigDir(); err == nil {
		c, verr := read(filepath.Join(base, "rta", "policy.yaml"), false)
		if verr != nil {
			return Ceiling{}, verr
		}
		found = append(found, c)
	}

	return intersect(found), nil
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
