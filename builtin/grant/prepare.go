package grant

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	profiles "github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One validation path for both ways a grant gets asked for: the local
// `grant allow` and the operator channel's prepare verb. The channel's
// whole correctness story is that the machine whose config, policy and
// catalogue bind is the machine that builds the grant — so the builder is
// the same function the local flow runs, not a remote approximation of it.

// buildNotes carries what parseTTL decided, so each caller can word the
// capped-TTL story without re-deriving whose ceiling bit.
type buildNotes struct {
	ttl, asked time.Duration
	byPolicy   bool
	capWhere   string
}

// buildGrant validates one issuance request and constructs the grant
// exactly as it would be stored — unsigned; signing is the caller's step,
// because who signs is precisely what differs between the flows.
func buildGrant(catalog func() []plugin.Capability, artifact func(string) (string, bool),
	spec operatorid.IssueSpec, from string) (core.Grant, buildNotes, *view.Error) {
	var notes buildNotes
	target := core.Normalize(spec.Target)
	if target == "" {
		return core.Grant{}, notes, view.Errorf("grant.notarget", "name what to allow").
			WithHint("rta grant allow kv.get db-password --ttl 15m")
	}
	// A grant that authorizes nothing is worse than an error: `grant list`
	// shows it looking exactly like a working one, so a typo — kv.gett for
	// kv.get — reads back as "done" right up until the agent tries the call
	// it was supposedly just allowed to make, and is refused anyway.
	if !targetExists(catalog, target) {
		return core.Grant{}, notes, view.Errorf("grant.unknowntarget", "%q does not name a registered capability or plugin", target).
			WithHint("rta explain lists capability IDs, rta plugin list lists plugin names — check for a typo")
	}
	// The artifact behind the plugin this target names, recorded now so the
	// grant binds to the binary the operator is consenting about rather than
	// to the name a replacement would answer to. Empty for a built-in, which
	// has no artifact separate from the rta the operator chose to run.
	digest, known := artifact(core.Namespace(target))
	if !known {
		return core.Grant{}, notes, view.Errorf("grant.unknownplugin",
			"%q is registered but rta cannot say which binary answers for it", core.Namespace(target)).
			WithHint("`rta doctor` reports a plugin whose provenance went missing; a grant must name an artifact")
	}
	profile, pin, verr := checkProfile(target, spec.Profile)
	if verr != nil {
		return core.Grant{}, notes, verr
	}
	ttl, asked, byPolicy, capWhere, verr := parseTTL(spec.TTL, target)
	if verr != nil {
		return core.Grant{}, notes, verr
	}
	notes = buildNotes{ttl: ttl, asked: asked, byPolicy: byPolicy, capWhere: capWhere}
	if spec.MaxUses < 0 {
		return core.Grant{}, notes, view.Errorf("grant.badmaxuses", "--max-uses cannot be negative").
			WithHint("0 means unlimited within the TTL, which is also the default")
	}
	rateMax, rateWindow, verr := parseRate(spec.Rate)
	if verr != nil {
		return core.Grant{}, notes, verr
	}
	scope := strings.TrimSpace(spec.Scope)
	if verr := core.CheckScope(scope); verr != nil {
		return core.Grant{}, notes, verr
	}
	// Named, never inferred here. `rta grant allow` resolves an omitted
	// --agent from this machine's own known agents before it builds a spec
	// (resolveAgent); the operator channel deliberately does not, because a
	// server that filled the name in would be choosing who an operator's
	// signature authorizes — and checkPrepared would refuse the draft for
	// exactly that reason.
	agent := strings.TrimSpace(spec.Agent)
	if agent == "" {
		return core.Grant{}, notes, view.Errorf("grant.noagent",
			"name the agent this is for, with --agent").
			WithHint("the name is the one from `rta mcp serve --as`, which " +
				"`rta mcp install <client>` sets to the client's name")
	}
	if verr := core.CheckAgent(agent); verr != nil {
		return core.Grant{}, notes, verr
	}
	now := time.Now()
	return core.Grant{
		Target: target,
		Scope:  scope,
		// The name and the connection behind it. The pin is what makes this a
		// grant against a place rather than against a label: edit the
		// environment afterwards and this stops covering calls on it, instead
		// of quietly following the name to wherever it now points.
		Profile:    profile,
		ProfilePin: pin,
		// The artifact this authority is about. See core.Grant.Digest: it is
		// what `--allow-destructive <id>@<digest>` used to carry, moved onto
		// the thing that now does the authorizing.
		Digest: digest,
		// Who may spend it. Empty is not "anybody": it is the server the
		// operator launched without a name, which is the only caller a grant
		// issued before agents were named has ever had.
		Agent:      agent,
		Issued:     now,
		From:       from,
		Expires:    now.Add(ttl),
		Note:       spec.Note,
		TTL:        strings.TrimSpace(spec.TTL),
		MaxUses:    spec.MaxUses,
		RateMax:    rateMax,
		RateWindow: rateWindow,
	}, notes, nil
}

// stillCovering answers, after a revoke, whether anything left in the file
// still authorizes the target — for *whoever* holds it.
//
// Asked once per surviving grant, against that grant's own identity, rather
// than once against the revoke's selectors. The selectors are narrowing
// filters: `rta grant revoke kv.get` with no --agent takes the capability
// back from every agent, so "is anything still covering it" has to be asked
// the same way. Asking it once with an empty caller answers "does an unnamed
// server still reach this", and there are no unnamed servers — so the
// warning that exists to stop `revoke` reporting success while a wider grant
// survives would never fire again.
func stillCovering(live []core.Grant, spec operatorid.RevokeSpec) *core.Grant {
	for i := range live {
		g := live[i]
		if spec.Agent != "" && g.Agent != spec.Agent {
			continue
		}
		if spec.Profile != "" && g.Profile != spec.Profile {
			continue
		}
		by := core.Caller{Agent: g.Agent, Profile: g.Profile, Pin: g.ProfilePin, Digest: g.Digest}
		if found := core.Covering(live[i:i+1], spec.Target, spec.Scope, by); found != nil {
			return &live[i]
		}
	}
	return nil
}

// resolveAgent decides who a grant is for when the operator did not say.
//
// Every server is named (`rta mcp serve --as`), and Grant.Agent matches
// exactly with no wildcard, so a grant naming nobody authorizes nothing —
// it would be a row `grant list` shows and the gate ignores, which is the
// dead end the one-gate change exists to remove. Refusing outright would
// mean typing `--agent claude` on every grant for the overwhelmingly common
// machine that has exactly one, so: one known agent is the answer, several
// is a question, none is a setup problem worth naming.
//
// Known means connected right now, or holding a grant already. Those are
// the two populations an operator could be thinking of, and a name from
// either is one they have already used.
func resolveAgent(asked string) (string, *view.Error) {
	if agent := strings.TrimSpace(asked); agent != "" {
		return agent, nil
	}
	known := knownAgents()
	switch len(known) {
	case 1:
		return known[0], nil
	case 0:
		return "", view.Errorf("grant.noagent", "name the agent this is for, with --agent").
			WithHint("no agent has connected or holds a grant yet — the name is the one from " +
				"`rta mcp serve --as`, which `rta mcp install <client>` sets to the client's name")
	default:
		return "", view.Errorf("grant.whichagent",
			"name the agent this is for, with --agent — this machine knows %s",
			strings.Join(known, ", ")).
			WithHint("a grant matches one agent exactly, so issuing it to the wrong one " +
				"is a grant that silently authorizes nothing")
	}
}

// knownAgents is every agent name this machine has seen: connected now, or
// holding a grant. Sorted and deduplicated, so the refusal above reads the
// same way twice.
func knownAgents() []string {
	seen := map[string]bool{}
	if open, err := session.List(); err == nil {
		for _, s := range open {
			if s.Agent != "" {
				seen[s.Agent] = true
			}
		}
	}
	if grants, verr := core.Load(); verr == nil {
		for _, g := range grants {
			if g.Agent != "" {
				seen[g.Agent] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// cappedNote words a TTL that came back shorter than asked, naming which
// ceiling bit — byPolicy and capWhere are parseTTL's own verdict, not
// re-derived here a second time.
func cappedNote(n buildNotes) string {
	if n.asked <= n.ttl {
		return ""
	}
	if n.byPolicy {
		return fmt.Sprintf("capped at %s by your team's policy (you asked for %s) — %s",
			n.ttl, n.asked, n.capWhere)
	}
	return fmt.Sprintf("capped at the %s maximum (you asked for %s)", core.MaxTTL, n.asked)
}

// inactiveProfileNote warns when a grant names an environment that is not
// the one switched on: the MCP bound refuses every profile but the active
// one, so the grant does nothing until `rta use` — and the clock is running.
func inactiveProfileNote(g core.Grant) string {
	if on := profiles.Active(); on != "" && g.Profile != "" && config.RefName(g.Profile) != on {
		return fmt.Sprintf("note: %s is switched on, so this grant does nothing until "+
			"`rta use %s` — no agent can reach %s while you are working elsewhere",
			on, g.Profile, g.Profile)
	}
	return ""
}

// PrepareRemote is the server half of remote issuance, wired into the serve
// command by the app layer (the kv.Reveal pattern: "this server prepares
// grants" is a line somebody typed, never a transitive import). label is
// the enrolled operator the envelope verified; it lands in the grant's From
// as attribution the roster promised — one label, one person.
func PrepareRemote(catalog func() []plugin.Capability,
	artifact func(string) (string, bool)) func(spec operatorid.IssueSpec, label string) (operatorid.Prepared, *view.Error) {
	return func(spec operatorid.IssueSpec, label string) (operatorid.Prepared, *view.Error) {
		g, notes, verr := buildGrant(catalog, artifact, spec, core.FromOperatorPrefix+label)
		if verr != nil {
			return operatorid.Prepared{}, verr
		}
		// The binding is the guard state's, the single source of truth the
		// store will verify against — never the request's, and never the
		// channel's own config, which could drift from it.
		g.Server = guard.BoundServer()
		p := operatorid.Prepared{Grant: g}
		if n := cappedNote(notes); n != "" {
			p.Notes = append(p.Notes, n)
		}
		if n := inactiveProfileNote(g); n != "" {
			p.Notes = append(p.Notes, n)
		}
		return p, nil
	}
}

// RevokeRemote is the server half of remote revocation, wired like
// PrepareRemote. Revocation asks for no guard signature in either mode —
// taking authority away is the fail-safe direction, the same reasoning that
// lets `grant revoke` run without the passphrase locally.
func RevokeRemote(spec operatorid.RevokeSpec, write bool) (operatorid.RevokeOutcome, *view.Error) {
	spec.Target = core.Normalize(spec.Target)
	if !spec.All && spec.Target == "" && strings.TrimSpace(spec.Profile) == "" && strings.TrimSpace(spec.Agent) == "" {
		return operatorid.RevokeOutcome{}, view.Errorf("grant.notarget", "name a capability, or pass --all").
			WithHint("`rta grant list --server <name>` shows what is currently allowed there")
	}
	return revokeOutcome(spec, write)
}

// revokeOutcome runs one revocation under the store's lock. The count, the
// surviving-coverage answer and the rows written back are three answers to
// one question, so they are all decided from the snapshot the lock is held
// over — deriving them from an unlocked read let an operator be told
// "revoked 1 grant(s)" while a Reserve running at that instant put the
// grant back.
func revokeOutcome(spec operatorid.RevokeSpec, write bool) (operatorid.RevokeOutcome, *view.Error) {
	var out operatorid.RevokeOutcome
	verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
		now := time.Now()
		kept := make([]core.Grant, 0, len(stored))
		// live is the active subset of kept. The stored file also holds rows
		// that authorize nothing — expired, or spent and waiting on a refund
		// — and those must survive a revoke of some other target without ever
		// being counted or reported as covering anything.
		var live []core.Grant
		revoked := 0
		for _, g := range stored {
			// Revoking a plugin takes back every grant inside it: the point of
			// `rta grant revoke kv` in a hurry is that nothing kv-shaped survives
			// it, not that grants naming a capability slip through.
			match := spec.All || spec.Target == "" || g.Target == spec.Target ||
				core.Namespace(g.Target) == spec.Target
			if match && spec.Scope != "" && g.Scope != spec.Scope {
				match = false
			}
			// A revoke that names a profile takes back that connection and no
			// other — and --all does not widen --profile or --agent into
			// "every one of them"; each selector keeps its narrowing meaning.
			// The full reasoning lived on this logic before it moved here, in
			// runRevoke; the short version is that the narrowest request must
			// never silently do the widest thing.
			if match && spec.Profile != "" && g.Profile != spec.Profile {
				match = false
			}
			if match && spec.Agent != "" && g.Agent != spec.Agent {
				match = false
			}
			active := g.Active(now)
			if match {
				if active {
					revoked++
				}
				continue
			}
			kept = append(kept, g)
			if active {
				live = append(live, g)
			}
		}
		if revoked == 0 && len(live) == 0 {
			out.NoneActive = true
			return nil, false
		}
		// A row naming exactly this target can be gone while a wider grant
		// still authorizes every call it would ever make — reporting only
		// whether a matching row existed, without asking whether the target is
		// still reachable through something wider, is how a namespace grant on
		// kv survived `rta grant revoke kv.get` while the operator was told
		// there was nothing to revoke — true of the row, false of the access.
		out.Still = stillCovering(live, spec)
		out.Revoked = revoked
		if revoked == 0 || !write {
			return nil, false
		}
		return kept, true
	})
	return out, verr
}

// revokeBody words one outcome, for the local flow and the remote one
// alike — the sentences an operator acts on must not depend on which
// machine computed them.
func revokeBody(target string, out operatorid.RevokeOutcome, dry bool) string {
	if out.NoneActive {
		return "Nothing to revoke — no grant is active."
	}
	stillCovered := func(line string) string {
		if out.Still == nil {
			return line
		}
		record := out.Still.Scope
		if record == "" {
			record = "any"
		}
		return line + fmt.Sprintf("\nstill covered by an active grant on %s (record: %s) — revoke that too: rta grant revoke %s",
			out.Still.Target, record, out.Still.Target)
	}
	if out.Revoked == 0 {
		msg := fmt.Sprintf("No active grant for %s.", target)
		if out.Still != nil {
			// "No active grant" would be a flat lie here: nothing named this
			// target exactly, but something else still authorizes it.
			msg = fmt.Sprintf("No grant named exactly %s to remove.", target)
		}
		return stillCovered(msg)
	}
	if dry {
		return stillCovered(fmt.Sprintf("would revoke %d grant(s)", out.Revoked))
	}
	return stillCovered(fmt.Sprintf("revoked %d grant(s)", out.Revoked))
}
