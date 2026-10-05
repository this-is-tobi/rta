package grant

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/config"
	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/lockdown"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/plugindist"
	profiles "github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/format"
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
// because who signs is precisely what differs between the flows. sf is the
// surface asking, for the names a refusal gives inputs and calls.
func buildGrant(sf plugin.Surface, catalog func() []plugin.Capability, artifact func(string) (string, bool),
	spec operatorid.IssueSpec, from string) (core.Grant, buildNotes, *view.Error) {
	var notes buildNotes
	target := core.Normalize(spec.Target)
	if target == "" {
		return core.Grant{}, notes, view.Errorf("grant.notarget", "name what to allow").
			WithHint("for example `" + sf.Call("grant.allow", plugin.Arg{Name: "target", Value: "kv.get", Positional: true},
				plugin.Arg{Name: "scope", Value: "db-password", Positional: true}, plugin.Arg{Name: "ttl", Value: "15m"}) + "`")
	}
	// A grant that authorizes nothing is worse than an error: `grant list`
	// shows it looking exactly like a working one, so a typo — kv.gett for
	// kv.get — reads back as "done" right up until the agent tries the call
	// it was supposedly just allowed to make, and is refused anyway.
	if !targetExists(catalog, target) {
		hint := "rta explain lists capability IDs, rta plugin list lists plugin names — check for a typo"
		if more := notInstalledHint(catalog, target); more != "" {
			hint += "; " + more
		}
		return core.Grant{}, notes, view.Errorf("grant.unknowntarget", "%q does not name a registered capability or plugin", target).
			WithHint(hint)
	}
	if verr := grantNeeded(sf, catalog, target, spec.Profile); verr != nil {
		return core.Grant{}, notes, verr
	}
	// The artifact behind the plugin this target names, recorded now so the
	// grant binds to the binary the operator is consenting about rather than
	// to the name a replacement would answer to. Empty for a built-in, which
	// has no artifact separate from the rta the operator chose to run.
	digest, known := artifact(core.Namespace(target))
	if !known {
		return core.Grant{}, notes, view.Errorf("grant.unknownplugin",
			"%q is registered but rta cannot say which binary answers for it", core.Namespace(target)).
			WithHint(sf.CapabilityName("audit.doctor") + " reports a plugin whose provenance went missing; a grant must name an artifact")
	}
	profile, pin, verr := checkProfile(target, spec.Profile)
	if verr != nil {
		return core.Grant{}, notes, verr
	}
	ttl, asked, byPolicy, capWhere, verr := parseTTL(sf, spec.TTL, target)
	if verr != nil {
		return core.Grant{}, notes, verr
	}
	notes = buildNotes{ttl: ttl, asked: asked, byPolicy: byPolicy, capWhere: capWhere}
	if spec.MaxUses < 0 {
		return core.Grant{}, notes, view.Errorf("grant.badmaxuses", "%s cannot be negative", sf.InputName("max-uses")).
			WithHint("0 means unlimited within the TTL, which is also the default")
	}
	rateMax, rateWindow, verr := parseRate(sf, spec.Rate)
	if verr != nil {
		return core.Grant{}, notes, verr
	}
	scope := spec.Scope
	if verr := givenRecord(sf, scope); verr != nil {
		return core.Grant{}, notes, verr
	}
	if verr := core.CheckScope(scope); verr != nil {
		return core.Grant{}, notes, verr
	}
	// A scope only ever narrows a grant by matching the record a call names
	// through the capability's own Scope field. If nothing under target
	// declares one, scopes() derives an empty record from every call it
	// makes, and a non-empty stored scope would then compare a value the
	// operator wrote against "" forever — a row `grant list` shows looking
	// narrowed that the gate can never satisfy. The same dead end
	// grantNeeded above refuses for a target nothing could ever spend.
	if scope != "" && !scopable(catalog, target) {
		return core.Grant{}, notes, view.Errorf("grant.scope.unscoped",
			"%s has no scoped input — every capability it reaches takes no record, so a scope here would never match a call", target).
			WithHint("leave " + sf.ArgumentName("scope") + " out to grant the whole target")
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
			"name the agent this is for, with %s", sf.InputName("agent")).
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

// notInstalledHint answers the other reading of an unknown target: not a typo
// of a capability but a plugin that is not here yet — `pg.query` on a machine
// that has not installed pg. Read from what is already on the machine, the
// attached indexes' manifests, so a grant never reaches a network to find out.
// A first-party name no index is attached for is named as that, and the install
// attaches the index itself; any other word is as often a typo as a service, and
// gets no guess.
func notInstalledHint(catalog func() []plugin.Capability, target string) string {
	ns := core.Namespace(target)
	if targetExists(catalog, ns) || !plugin.ValidName(ns) {
		return ""
	}
	if listed, verr := plugindist.Resolve(ns); verr == nil {
		return ns + " is a plugin in the " + listed.Index + " index, not installed here — `rta plugin install " + ns +
			"` installs it, and then it can be granted"
	}
	return plugindist.FirstPartyHintFor(target, catalog())
}

// breadthNote says how much a plugin-wide grant covers. `grant allow kv` reads
// like one grant and is every capability in kv that needs one, destructive
// ones included, and the receipt used to say the same as it did for a single
// key: "claude may call kv for 1h".
func breadthNote(catalog func() []plugin.Capability, g core.Grant) string {
	if strings.Contains(g.Target, ".") {
		return ""
	}
	var gated, destructive []string
	for _, c := range catalog() {
		if core.Namespace(c.ID) != g.Target || c.HumanOnly || !core.Required(c, g.Profile) {
			continue
		}
		gated = append(gated, c.ID)
		if c.Safety == plugin.Destructive {
			destructive = append(destructive, c.ID)
		}
	}
	if len(gated) == 0 {
		return ""
	}
	sort.Strings(destructive)
	which := destructiveSummary(destructive)
	return fmt.Sprintf("note: %s covers %s an agent needs a grant for, %s — name one to allow only it",
		g.Target, format.Count(len(gated), "capability", "capabilities"), which)
}

// destructiveSummary counts the destructive capabilities and names the first
// few. Never "and 1 other": the fourth name is shorter than saying there is one.
func destructiveSummary(ids []string) string {
	if len(ids) == 0 {
		return "none destructive"
	}
	shown := ids[:min(3, len(ids))]
	if len(ids) == 4 {
		shown = ids
	}
	which := strconv.Itoa(len(ids)) + " destructive (" + strings.Join(shown, ", ")
	if rest := len(ids) - len(shown); rest > 0 {
		which += " and " + format.Count(rest, "other", "others")
	}
	return which + ")"
}

// givenRecord refuses a record that is nothing but white space, the one
// record allow, renew and revoke do not take as given.
//
// **Every other record is taken as typed, never trimmed.** The gate compares
// a record byte for byte, and the three trimmed theirs — strings.TrimSpace
// takes a no-break space as readily as a space. So the command the
// core.grant.required refusal hands the operator, shell-quoted with the
// padding the parked call named, issued a grant on the bare record, which
// covers nothing that call asked for; and `grant revoke kv.get` on the
// padded record took back the bare grant and left the padded one standing,
// the one an answer given with --ttl had issued.
//
// **White space alone is refused rather than read either way.** Trimmed, it
// was the empty record, and the empty record is every record: `grant allow
// kv.get " "` issued a grant over the whole store, and `grant revoke kv.get
// " "` took back every grant on it. Taken as given it would name a record
// no store holds. Such an argument is a slip — a variable that expanded to
// padding, a stray pair of quotes — and neither reading is what was meant,
// so the one answer that can neither widen a grant nor miss one is to ask.
func givenRecord(sf plugin.Surface, scope string) *view.Error {
	if !core.BlankRecord(scope) {
		return nil
	}
	return view.Errorf("grant.scope.blank", "%s is %q, only white space, which names no record",
		sf.ArgumentName("scope"), scope).
		WithHint("name the record, or leave " + sf.ArgumentName("scope") + " out to mean every record")
}

// exactField is the switch that makes revoke's and renew's selectors name
// one grant; verb is the command's, for its help.
func exactField(verb string) plugin.Field {
	return plugin.Field{Name: "exact", Type: plugin.Bool,
		Help: verb + " the one grant named: a record, profile or agent left out means the grant naming " +
			"none rather than every one, and a plugin name its grant on the whole plugin alone"}
}

// selector is what revoke and renew match stored grants by: one spelling of
// the rule for both, the local flows and the operator channel's revoke.
//
// **Every selector narrows, and one left out matches every grant** — a
// record left out is every record, a profile every connection, an agent
// every agent, a plugin name every grant inside it. That is the command
// line's reading, and the right one there: `grant revoke kv.get` in a
// hurry takes back every grant on it.
//
// **It left the grant naming no record, or the base connection, with no
// selector at all**, since the empty value was already "any". So x and n on
// a TUI roster row whose record read "any" or whose profile read "—" acted
// on every grant for that target and agent the other cells matched — and a
// renewal extended grants the row never pointed at, which is the widening
// direction. exact is that selector: the target, record, profile and agent
// each as given, a left-out one meaning the grant that names none, and a
// plugin name its grant on the whole plugin rather than everything in it.
// A grant is stored once per target, record, connection and agent
// (core.Issue), so exact names at most one; role still narrows.
type selector struct {
	all                                 bool
	target, scope, profile, agent, role string
	exact                               bool
}

func (s selector) matches(g core.Grant) bool {
	if s.role != "" && g.Role != s.role {
		return false
	}
	if s.exact {
		return g.Target == s.target && g.Scope == s.scope && g.Profile == s.profile && g.Agent == s.agent
	}
	// A plugin name takes every grant inside it, and --all every target —
	// never widening --profile or --agent into "every one of them": the
	// narrowest request must never silently do the widest thing.
	if !s.all && s.target != "" && g.Target != s.target && core.Namespace(g.Target) != s.target {
		return false
	}
	switch {
	case s.scope != "" && g.Scope != s.scope,
		s.profile != "" && g.Profile != s.profile,
		s.agent != "" && g.Agent != s.agent:
		return false
	}
	return true
}

// check refuses an exact selector that can name no grant: one with no
// target, which every grant has, and one beside all, which names every
// grant there is. sf is the surface asking, for the names of the inputs.
func (s selector) check(sf plugin.Surface) *view.Error {
	if !s.exact {
		return nil
	}
	if s.all {
		return view.Errorf("grant.exact.all", "%s takes every grant and %s one — give one of them",
			sf.InputName("all"), sf.InputName("exact")).
			WithHint(sf.CapabilityName("grant.list") + " shows the grants, one row each")
	}
	if s.target == "" {
		return view.Errorf("grant.exact.target", "%s names one grant, and a grant names what it allows — give %s",
			sf.InputName("exact"), sf.ArgumentName("target")).
			WithHint(sf.CapabilityName("grant.list") + " shows the grants, one row each")
	}
	return nil
}

// described is the grant an exact selector names, for a sentence saying
// none is standing: "kv.get on no record, on the base connection, for
// claude". Every part said, since the parts left out are what exact reads
// differently — and the role when one was given, which still narrows: left
// unsaid, the sentence denied a grant the roster showed standing under
// another role, or under none.
func (s selector) described() string {
	record := "on no record"
	if s.scope != "" {
		record = "on " + core.ShownRecord(s.scope, "any")
	}
	connection := "on the base connection"
	if s.profile != "" {
		connection = "via profile " + s.profile
	}
	who := "for no named agent"
	if s.agent != "" {
		who = "for " + s.agent
	}
	described := s.target + " " + record + ", " + connection + ", " + who
	if s.role != "" {
		described += ", under role " + s.role
	}
	return described
}

// narrowed is what a selector with no target narrows to, for a sentence
// saying no grant matched it: "for agent claude via profile prod". Only the
// parts given, since each one left out matched every grant.
func (s selector) narrowed() string {
	var parts []string
	if s.agent != "" {
		parts = append(parts, "for agent "+s.agent)
	}
	if s.scope != "" {
		parts = append(parts, "on "+core.ShownRecord(s.scope, "any"))
	}
	if s.profile != "" {
		parts = append(parts, "via profile "+s.profile)
	}
	if s.role != "" {
		parts = append(parts, "under role "+s.role)
	}
	return strings.Join(parts, " ")
}

// stillArgs is the revoke that takes back still, the grant left covering
// what spec took back: its target, as it always was, and after an exact
// revoke that grant exactly. The target alone takes back every grant on
// it, for every agent and connection, which is a wider decision than the
// one row an exact revoke was about — the x on a roster row.
//
// server is the remote server the revoke was sent to, empty for this
// machine's own store, and the command names it: still is that server's
// grant, and the revoke handed on without it took back a grant of the same
// name on this machine, or none, while the one covering the target there
// stood.
func stillArgs(spec operatorid.RevokeSpec, server string, still core.Grant) []plugin.Arg {
	args := []plugin.Arg{{Name: "target", Value: still.Target, Positional: true}}
	if spec.Exact {
		if still.Scope != "" {
			args = append(args, plugin.Arg{Name: "scope", Value: still.Scope, Positional: true})
		}
		if still.Profile != "" {
			args = append(args, plugin.Arg{Name: "profile", Value: still.Profile})
		}
		if still.Agent != "" {
			args = append(args, plugin.Arg{Name: "agent", Value: still.Agent})
		}
		args = append(args, plugin.Arg{Name: "exact", Value: true})
	}
	if server != "" {
		args = append(args, plugin.Arg{Name: "server", Value: server})
	}
	return args
}

// revokeSelector is the selector a revoke's spec names.
func revokeSelector(spec operatorid.RevokeSpec) selector {
	return selector{all: spec.All, target: spec.Target, scope: spec.Scope, profile: spec.Profile,
		agent: spec.Agent, role: spec.Role, exact: spec.Exact}
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
//
// An exact revoke asks it of the agent and connection it named, the ones
// left out included: the grant it took back was the one for no named agent,
// or on the base connection, and a wider grant covering that holder is the
// one worth naming.
func stillCovering(live []core.Grant, spec operatorid.RevokeSpec) *core.Grant {
	for i := range live {
		g := live[i]
		if (spec.Exact || spec.Agent != "") && g.Agent != spec.Agent {
			continue
		}
		if (spec.Exact || spec.Profile != "") && g.Profile != spec.Profile {
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
//
// sf is the surface asking, for the name its refusals give the agent input.
func resolveAgent(sf plugin.Surface, asked string) (string, *view.Error) {
	if agent := strings.TrimSpace(asked); agent != "" {
		return agent, nil
	}
	known := knownAgents()
	switch len(known) {
	case 1:
		return known[0], nil
	case 0:
		// Not "no agent has connected": the log shows the ones that did, and
		// told they never had, an operator whose agent called an hour ago and
		// whose grant has since expired was sent looking for a name they had
		// already used. The guess above stops at what is connected or holds a
		// grant, since a name from an old session may be a client long gone;
		// the person asking can be shown them.
		hint := "no agent is connected right now and none holds a grant"
		if seen := loggedAgents(); len(seen) > 0 {
			hint += " — the log has seen " + strings.Join(seen, ", ")
		}
		return "", view.Errorf("grant.noagent", "name the agent this is for, with %s", sf.InputName("agent")).
			WithHint(hint + "; the name is the one from " +
				"`rta mcp serve --as`, which `rta mcp install <client>` sets to the client's name")
	default:
		return "", view.Errorf("grant.whichagent",
			"name the agent this is for, with %s — this machine knows %s",
			sf.InputName("agent"), strings.Join(known, ", ")).
			WithHint("a grant matches one agent exactly, so issuing it to the wrong one " +
				"is a grant that silently authorizes nothing")
	}
}

// loggedAgents is the agent names in the last stretch of the log, sorted.
func loggedAgents() []string {
	entries, err := agentlog.Read(500)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Agent != "" {
			seen[e.Agent] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
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

// agentsSeen is every name an agent has been seen under: connected now,
// holding a grant, or in the record. knownAgents is the first two, and what a
// grant fills in on its own; this is the three, and what a lock, a filter and
// a completion are checked against, because the agent an incident is about
// is as likely to have called an hour ago as to be connected now.
func agentsSeen() []string {
	seen := map[string]bool{}
	for _, n := range knownAgents() {
		seen[n] = true
	}
	for _, n := range loggedAgents() {
		seen[n] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// SuggestAgents completes an agent name from the ones this machine has seen,
// for every command that takes one to filter by.
func SuggestAgents(context.Context, plugin.Request) []string { return agentsSeen() }

// grantNeeded refuses a grant that could never be spent. Reads are free —
// core.Required is the one gate, and a Read on the base connection passes it
// without a row — so a grant on one is a row `grant list` shows as live that
// the gate never consults; and a capability reserved for the person at the
// terminal is never a tool, so no call could ever reach the grant. Both used
// to succeed, and read back as "done" — the same dead end a typo'd target
// is refused for above, reached by a correct spelling.
//
// A plugin name is asked the same question of everything in it: `grant allow
// sys` on a plugin of reads is the same nothing, spelled wider.
func grantNeeded(sf plugin.Surface, catalog func() []plugin.Capability, target, profile string) *view.Error {
	var caps []plugin.Capability
	for _, c := range catalog() {
		if c.ID == target || core.Namespace(c.ID) == target {
			caps = append(caps, c)
		}
	}
	human := 0
	for _, c := range caps {
		if c.HumanOnly {
			human++
			continue
		}
		if core.Required(c, profile) {
			return nil
		}
	}
	hint := sf.CapabilityWith("grant.list", "detail") + " shows what needs a grant, what needs none, and what is never a tool"
	switch {
	case human == len(caps) && len(caps) == 1:
		return view.Errorf("grant.needless",
			"%s is never a tool — only the person at the terminal runs it, so no call could spend a grant on it", target).
			WithHint(hint)
	case human == len(caps):
		return view.Errorf("grant.needless",
			"%s has nothing an agent can call — everything in it is for the person at the terminal", target).
			WithHint(hint)
	case len(caps) == 1:
		return view.Errorf("grant.needless",
			"%s needs no grant — it is a read, and agents can already call it", target).
			WithHint(hint)
	default:
		return view.Errorf("grant.needless",
			"%s needs no grant — everything in it is a read, and agents can already call those", target).
			WithHint(hint)
	}
}

// unknownAgentNote says when a name is one this machine has never seen an
// agent under, beside the names it has and the one the typed name is probably
// a slip for. Not a refusal: the first grant on a fresh machine is issued
// before the client has ever connected, and the name on it is the one `rta mcp
// install` just wrote. But a grant matches its agent exactly, so `--agent
// cluade` is a row that authorizes nothing and looks live, and a lock on it
// freezes nobody while the screen says "locked" — and the moment to say so is
// while the operator is still looking. Measured before the row is written,
// because afterwards the name on it is one this machine knows.
//
// seen is agentsSeen: a name from the record counts as one the machine has
// seen, since an agent that called last week and holds nothing now is the one
// an incident is about.
func unknownAgentNote(seen []string, agent string) string {
	if len(seen) == 0 || slices.Contains(seen, agent) {
		return ""
	}
	note := fmt.Sprintf("note: no agent named %q has connected, holds a grant or appears in the record — this machine knows %s",
		agent, strings.Join(seen, ", "))
	if near := nearestName(agent, seen); near != "" {
		note += " (did you mean " + near + "?)"
	}
	return note
}

// UnknownAgentNote is unknownAgentNote for a name typed into another command
// that names an agent, asked of what this machine has seen now.
func UnknownAgentNote(agent string) string { return unknownAgentNote(agentsSeen(), agent) }

// nearestName is the one name in names a slip of the keyboard could have made
// name from, or "" when none is near enough to be a guess worth printing: at
// most a third of the word away, one edit for a short name. A swap of two
// neighbouring letters is one edit, since that is the typo.
func nearestName(name string, names []string) string {
	name = strings.ToLower(name)
	allowed := max(1, len([]rune(name))/3)
	best, bestDistance := "", allowed+1
	for _, n := range names {
		if d := editDistance(name, strings.ToLower(n)); d < bestDistance {
			best, bestDistance = n, d
		}
	}
	return best
}

// editDistance is the optimal-string-alignment distance between two words:
// insertions, deletions and substitutions, and a swap of two neighbouring
// letters as one edit.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// olderServerNote warns when a server that is open right now is running a
// build this one is not, because that server is the one that will be asked
// to honour what was just issued.
//
// **A process keeps the code it started with**, and these stay open for
// days. A grant is stamped here from the config file as it is now; a server
// started before an upgrade decides by the rules it was built with, and when
// the two disagree the call is refused under the sentence an ungranted call
// gets — so nothing on screen connects the refusal to the grant that was
// just issued, and the remedy reached for is to issue it again.
//
// Beside the notes above and for their reason: the clock on a 15-minute
// grant is already running, and this is the moment the operator is still
// looking. Said for every open server on another build rather than only the
// ones named by this grant — a grant names one agent, a client can hold
// several servers open under it, and which of them takes the next call is
// not something this can know.
func olderServerNote() string {
	named, n := olderServersNamed()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("note: %s open on another build of rta — reconnect the client, or this grant "+
		"can be refused by a server deciding from the build it started with: %s",
		format.Count(n, "server is", "servers are"), named)
}

// olderServersNamed is "claude (pid 17188), cursor (pid 22451)" for every
// server open on a build this one is not, with how many of them there are.
//
// Two surfaces report this — `grant allow` at the moment of issue, `grant
// list` over the roster — and somebody who sees both has to be told about
// the same set in the same words, so neither composes the list itself.
func olderServersNamed() (string, int) {
	others := session.OtherBuilds(session.Self())
	named := make([]string, 0, len(others))
	for _, s := range others {
		who := s.Agent
		if who == "" {
			who = "unnamed"
		}
		named = append(named, fmt.Sprintf("%s (pid %d)", who, s.PID))
	}
	return strings.Join(named, ", "), len(others)
}

// olderServerWarning is that same fact on the roster, where a grant that will
// be refused is listed looking perfectly healthy.
//
// A warning rather than a row, because it is not about any one grant: the rows
// are a complete account of what is on disk, and this is the part the table
// cannot see — how a process that started on another build will decide.
// The refusal an agent gets says nothing about it on purpose (refuseMissing
// keeps it byte-identical to an ungranted call, so an agent cannot enumerate
// the operator's profiles), and that is exactly why the person reading the
// roster has to be able to find it here.
func olderServerWarning() *view.Error {
	named, n := olderServersNamed()
	if n == 0 {
		return nil
	}
	return &view.Error{
		Code: "core.grant.older.server",
		Message: fmt.Sprintf("%s open on another build of rta, and a server decides by the build it "+
			"started with, so a grant listed here can still be refused",
			format.Count(n, "server is", "servers are")),
		Hint: "reconnect the client: " + named,
		// Every row is there: this is about how a server will judge them,
		// so it heads the roster as a warning and never as partial.
		Advisory: true,
	}
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
			format.Duration(n.ttl), format.Duration(n.asked), n.capWhere)
	}
	return fmt.Sprintf("capped at the %s maximum (you asked for %s)", format.Duration(core.MaxTTL), format.Duration(n.asked))
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
		// No surface crosses the operator channel, so the draft's refusals
		// are worded for the command line most operators issue from — the
		// known limit pkg/plugin/naming.go records.
		g, notes, verr := buildGrant(plugin.SurfaceUnknown, catalog, artifact, spec, core.FromOperatorPrefix+label)
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
	if verr := revokeSelector(spec).check(plugin.SurfaceUnknown); verr != nil {
		return operatorid.RevokeOutcome{}, verr
	}
	// Role counts as a selector here as it does locally: `revoke --role dev`
	// takes one role back from every agent, and refusing it for naming
	// nothing sent an operator to --all, the widest revoke there is. Worded
	// for the command line, as PrepareRemote's refusals are.
	if !spec.All && spec.Target == "" && strings.TrimSpace(spec.Profile) == "" &&
		strings.TrimSpace(spec.Agent) == "" && strings.TrimSpace(spec.Role) == "" {
		sf := plugin.SurfaceUnknown
		return operatorid.RevokeOutcome{}, view.Errorf("grant.notarget", "name a capability, or give %s", sf.InputName("all")).
			WithHint("`" + sf.Call("grant.list", plugin.Arg{Name: "server", Value: "<name>"}) +
				"` shows what is currently allowed there")
	}
	// The record as sent, as `grant revoke` takes it here, and white space
	// alone refused for givenRecord's reason: this client refuses it before
	// sending, and one that did not must not be read as every record.
	if verr := givenRecord(plugin.SurfaceUnknown, spec.Scope); verr != nil {
		return operatorid.RevokeOutcome{}, verr
	}
	return revokeOutcome(spec, write)
}

// revokeOutcome runs one revocation under the store's lock. The count, the
// surviving-coverage answer and the rows written back are three answers to
// one question, so they are all decided from the snapshot the lock is held
// over — deriving them from an unlocked read let an operator be told
// "revoked 1 grant" while a Reserve running at that instant put the
// grant back.
func revokeOutcome(spec operatorid.RevokeSpec, write bool) (operatorid.RevokeOutcome, *view.Error) {
	out, _, verr := revokeDetailed(spec, write)
	return out, verr
}

// revokeDetailed is revokeOutcome with the grants it took back, or would, as
// they stood: the operator channel's answer is a count and stays one, and the
// person at this machine is told which grants went, from the same snapshot the
// count was taken from.
func revokeDetailed(spec operatorid.RevokeSpec, write bool) (operatorid.RevokeOutcome, []core.Grant, *view.Error) {
	// Said back, so a client that asked for exact can tell this revoke
	// matched it from one by a server that never heard of it.
	out := operatorid.RevokeOutcome{Exact: spec.Exact}
	var gone []core.Grant
	sel := revokeSelector(spec)
	verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
		now := time.Now()
		kept := make([]core.Grant, 0, len(stored))
		// live is the active subset of kept. The stored file also holds rows
		// that authorize nothing — expired, or spent and waiting on a refund
		// — and those must survive a revoke of some other target without ever
		// being counted or reported as covering anything.
		var live []core.Grant
		gone = nil
		for _, g := range stored {
			// Revoking a plugin takes back every grant inside it: the point of
			// `rta grant revoke kv` in a hurry is that nothing kv-shaped survives
			// it, not that grants naming a capability slip through. The rule,
			// and exact's reading of it, is selector's, shared with renew.
			match := sel.matches(g)
			active := g.Active(now)
			if match {
				if active {
					gone = append(gone, g)
				}
				continue
			}
			kept = append(kept, g)
			if active {
				live = append(live, g)
			}
		}
		if len(gone) == 0 && len(live) == 0 {
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
		out.Revoked = len(gone)
		if len(gone) == 0 || !write {
			return nil, false
		}
		return kept, true
	})
	return out, gone, verr
}

// revokeBody words one outcome, for the local flow and the remote one
// alike — the sentences an operator acts on must not depend on which
// machine computed them. sf is the surface asking, which is this machine's
// either way, for the call a leftover grant is named with, and server the
// remote server the outcome came from, empty for this machine's own.
func revokeBody(sf plugin.Surface, spec operatorid.RevokeSpec, server string, out operatorid.RevokeOutcome,
	gone []core.Grant, dry bool,
) string {
	target := spec.Target
	if out.NoneActive {
		return "Nothing to revoke — no grant is active."
	}
	stillCovered := func(line string) string {
		if out.Still == nil {
			return line
		}
		record := core.ShownRecord(out.Still.Scope, "any")
		return line + fmt.Sprintf("\nstill covered by an active grant on %s (record: %s) — revoke that too: `%s`",
			out.Still.Target, record, sf.Call("grant.revoke", stillArgs(spec, server, *out.Still)...))
	}
	if out.Revoked == 0 {
		msg := fmt.Sprintf("No active grant for %s.", target)
		switch {
		case spec.Exact:
			// Every part named: the ones left out are what exact reads as
			// none, and "no active grant for kv.get" beside a roster of
			// kv.get grants read as a revoke that failed.
			msg = "No active grant is exactly " + revokeSelector(spec).described() + "."
		case target == "":
			// A revoke by who or where alone — --agent, --profile, --role —
			// said "No active grant for ." with the target it did not have.
			msg = "No active grant matches."
			if named := revokeSelector(spec).narrowed(); named != "" {
				msg = "No active grant " + named + "."
			}
		case out.Still != nil:
			// "No active grant" would be a flat lie here: nothing named this
			// target exactly, but something else still authorizes it.
			msg = fmt.Sprintf("No grant named exactly %s to remove.", target)
		default:
			// Said with what it was narrowed to. "No active grant for
			// cert.expiry" after `--agent other` read as no grant on
			// cert.expiry at all, beside the one another agent holds: a
			// mistyped agent name, and an operator told access is gone that
			// is not.
			if named := revokeSelector(spec).narrowed(); named != "" {
				msg = fmt.Sprintf("No active grant for %s, %s.", target, named)
			}
		}
		return stillCovered(msg)
	}
	if dry {
		return stillCovered("would revoke " + format.Count(out.Revoked, "grant", "grants") + goneLines(sf, gone, true))
	}
	return stillCovered("revoked " + format.Count(out.Revoked, "grant", "grants") + goneLines(sf, gone, false))
}

// goneLines is the grants a revoke took back, or would, each in the sentence
// `grant allow` said it in, and — for one that is gone — the call that puts it
// back. The receipt of "revoked 5 grants" was a count, and a count cannot be
// checked against what was meant: the revoke that took the wrong five had to
// be retyped from memory.
//
// The call is the grant as it stood: what time and what uses it had left, not
// what it was first issued with, since putting back a spent grant with its
// first budget would be a widening nobody asked for. It is a line to read and
// to run by hand; rta runs nothing on a revoke's behalf.
//
// The remote flow has a count and nothing to list: the operator channel's
// answer carries one, and a server on an older rta could not send more.
func goneLines(sf plugin.Surface, gone []core.Grant, dry bool) string {
	if len(gone) == 0 {
		return ""
	}
	now := time.Now()
	var b strings.Builder
	b.WriteString(":")
	for _, g := range gone {
		b.WriteString("\n  " + subject(g) + " may " + describe(g))
		if !dry {
			b.WriteString("\n    re-issue: " + reissueCall(sf, g, now))
		}
	}
	return b.String()
}

// reissueCall is the `grant allow` that gives g back with what it had left.
func reissueCall(sf plugin.Surface, g core.Grant, now time.Time) string {
	args := []plugin.Arg{{Name: "target", Value: g.Target, Positional: true}}
	if g.Scope != "" {
		args = append(args, plugin.Arg{Name: "scope", Value: g.Scope, Positional: true})
	}
	if g.Profile != "" {
		args = append(args, plugin.Arg{Name: "profile", Value: g.Profile})
	}
	if g.Agent != "" {
		args = append(args, plugin.Arg{Name: "agent", Value: g.Agent})
	}
	left := max(g.Expires.Sub(now), time.Second)
	if left >= time.Minute {
		// Rounded down to the minute: a command somebody reads and runs by hand
		// is not improved by seconds, and never rounding up keeps it from
		// giving back more time than was left.
		left = left.Truncate(time.Minute)
	}
	args = append(args, plugin.Arg{Name: "ttl", Value: format.Duration(left)})
	if left := g.MaxUses - g.Uses; g.MaxUses > 0 && left > 0 {
		args = append(args, plugin.Arg{Name: "max-uses", Value: left})
	}
	if g.RateMax > 0 && g.RateWindow != "" {
		args = append(args, plugin.Arg{Name: "rate", Value: fmt.Sprintf("%d/%s", g.RateMax, g.RateWindow)})
	}
	return sf.Call("grant.allow", args...)
}

// stillConnected is what a revoke leaves running: the agents that have an rta
// server open and are not locked. Revoking takes back what grants gave and
// nothing else — the ungated reads an agent's server answers stay open — and
// the revoke someone types in a hurry is the one for which "the agent is
// stopped" is the thing they believe. Said once, last, with the command that
// does stop it. Empty when nothing is connected, or everything that is has
// been frozen already.
func stillConnected(sf plugin.Surface) string {
	open, err := session.List()
	if err != nil {
		return ""
	}
	frozen := map[string]bool{}
	if locks, verr := lockdown.Load(); verr == nil {
		for _, l := range locks {
			if l.Kind == lockdown.KindAgent {
				frozen[l.Name] = true
			}
		}
	}
	if frozen[lockdown.Everyone] {
		return ""
	}
	var running []string
	for _, srv := range open {
		if srv.Agent != "" && !frozen[srv.Agent] && !slices.Contains(running, srv.Agent) {
			running = append(running, srv.Agent)
		}
	}
	sort.Strings(running)
	switch len(running) {
	case 0:
		return ""
	case 1:
		return running[0] + " is still connected (reads stay open): `" +
			sf.Call("lock.add", plugin.Arg{Name: "name", Value: running[0], Positional: true}) + "`"
	}
	return strings.Join(running, ", ") + " are still connected (reads stay open): `" +
		sf.Call("lock.add", plugin.Arg{Name: "all", Value: true}) + "` freezes them all, or `" +
		sf.Call("lock.add", plugin.Arg{Name: "name", Value: "<name>", Positional: true}) + "` one"
}
