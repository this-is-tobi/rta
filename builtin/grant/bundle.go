package grant

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	core "github.com/this-is-tobi/rta/internal/grant"
	profiles "github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/role"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An environment bundle is `rta grant issue --profile staging`: one grant for
// every capability of every plugin the environment configures, to one agent,
// under one passphrase. It is a role nobody had to write — the list a role
// would hold, read out of the environment and the catalogue at the moment the
// person asks, and issued by the flow a role is issued by.
//
// **Expanded when it is issued, into the ordinary rows a hand-typed list
// would have been, and never stored as a pattern.** A grant row carries the
// digest of one plugin build and the pin of one connection, and has no
// wildcard for Profile, Agent or Digest (internal/grant.Grant) for the reason
// each of them states: a stored "everything staging covers" would follow the
// name to whatever the environment holds next month, the plugin it gains and
// the connection it is repointed at, covering calls nobody agreed to. Read
// once, here, what is signed is the rows, each naming one capability on one
// connection, and a plugin the environment gains tomorrow is covered by
// nothing until somebody issues the bundle again — which is what consent is.
//
// **Reads by default, and the class is a ceiling.** `--safety` names how far
// the bundle may reach, never a target: read is the reads, write adds the
// writes, destructive adds what destroys. Narrowest by default, because the
// command is one short line and its reach is a whole environment: widening
// costs a word the person has to type and read back. A capability that reveals
// a stored value is classed a write by its own declaration (plugin.Reveals is
// refused on a Read), so the default bundle never hands over a secret.
//
// **A capability the team's ceiling forbids is skipped, and named.** A role is
// somebody's list, so one forbidden line refuses the whole role — the author
// meant that line. Here nobody meant any one row: the person asked for
// "everything staging covers", and the ceiling's answer is a smaller
// everything, said out loud (internal/grant.CheckCeiling's own sentence, and
// its own Forbids, not a second reading of never, neverProfile and
// requireScope). It is refused only when nothing is left to issue.
//
// **No role name on the grants.** The bundle is named by what it is, an
// agent's grants on an environment, and `grant revoke --profile staging
// --agent claude` takes it back as one. A pseudo-role would collide with a
// role of the same name, and `revoke --role` would take both.
//
// **--max-uses and --rate bound each row.** Every grant is a row of its own
// and spends its own budget; ten uses on a bundle of twenty is up to two
// hundred calls. The receipt says so beside the bound, because a person typing
// one number reads it as the bundle's.
//
// **Local only.** The operator channel signs one grant per envelope
// (operator.VerbGrantIssue), and `grant issue` takes no --server for the
// same reason a role has none: the machine whose config, catalogue and policy
// bind is the machine the bundle is read on, and a bundle read here and
// signed there would be the review-before-signing step skipped.

// bundleClasses are the classes an environment bundle may reach, narrowest
// first.
var bundleClasses = []plugin.Safety{plugin.Read, plugin.Write, plugin.Destructive}

// bundleClassOptions is --safety's closed set.
func bundleClassOptions() []string {
	out := make([]string, len(bundleClasses))
	for i, c := range bundleClasses {
		out[i] = string(c)
	}
	return out
}

// classRank orders a capability's class against the bundle's. A class nothing
// declares ranks past destructive, so it is never reached.
func classRank(s plugin.Safety) int {
	if i := slices.Index(bundleClasses, s); i >= 0 {
		return i
	}
	return len(bundleClasses)
}

// bundleClass reads --safety: read when it is left out.
func bundleClass(raw string) (plugin.Safety, *view.Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return plugin.Read, nil
	}
	if c := plugin.Safety(raw); slices.Contains(bundleClasses, c) {
		return c, nil
	}
	return "", view.Errorf("grant.issue.safety", "%q is not a class a bundle can reach", raw).
		WithHint("read, write or destructive — each takes in the ones before it, and read is the default").WithInput("safety")
}

// classWords is what a class reaches, as a sentence says it.
func classWords(c plugin.Safety) string {
	switch c {
	case plugin.Write:
		return "reads and writes"
	case plugin.Destructive:
		return "reads, writes and everything destructive"
	}
	return "reads"
}

// bundleRow is one grant of the bundle: a capability on a connection.
type bundleRow struct {
	capability plugin.Capability
	ref        string
}

// skippedRow is a row the ceiling forbade, with the sentence it said it in.
type skippedRow struct {
	id, ref, why string
}

// environmentBundle is what an environment expands into, and what it left out
// and why — every figure the receipt and the listing before the passphrase
// state is read off it, so neither can say what the other does not.
type environmentBundle struct {
	name  string
	class plugin.Safety
	// plugins are the plugins the environment configures that are installed
	// here, sorted, and refs the connections it holds for each, spelled as a
	// call names them: staging, staging/analytics.
	plugins []string
	refs    map[string][]string
	rows    []bundleRow
	skipped []skippedRow
	// where names the policy files in force, for the sentence a skipped row
	// is said in.
	where string
	// humanOnly are the capabilities for the person at the terminal, and
	// unprofiled the ones that take no profile — neither can be a grant on
	// a connection. uncovered are the unprofiled ones that need a grant all
	// the same, which no environment grant reaches.
	humanOnly, unprofiled, uncovered []string
	// wider are the capabilities a wider class would add, by class.
	wider map[plugin.Safety][]string
	// absent are plugins the environment configures that are not installed.
	absent []string
}

// expandEnvironment reads an environment into the rows a bundle issues.
//
// The refusals are the ones a grant on the environment would give
// (configuredEnvironment, the ceiling's own load), and a bare environment name
// only: `staging/analytics` is one connection, and one connection is a grant
// on it. A name that is no connection of the environment is not "one
// connection of" it — the wording belongs to a connection that exists — and is
// refused as `grant allow` refuses it, naming what the environment holds.
func expandEnvironment(sf plugin.Surface, catalog func() []plugin.Capability, env string, class plugin.Safety) (environmentBundle, *view.Error) {
	b := environmentBundle{name: env, class: class, refs: map[string][]string{}, wider: map[plugin.Safety][]string{}}
	cfg, err := config.Load()
	if err != nil {
		return b, view.AsError(err, "grant.config")
	}
	name, instance := config.SplitRef(env)
	p, verr := configuredEnvironment(cfg, name, "")
	if verr != nil {
		return b, verr
	}
	if instance != "" {
		held := connectionsIn(p, name)
		if !slices.Contains(held, env) {
			return b, view.Errorf("grant.unknowninstance", "profile %q has no connection called %q", name, instance).
				WithHint("it has: " + strings.Join(held, ", ")).WithInput("profile")
		}
		return b, view.Errorf("grant.issue.instance", "%q is one connection of %s, and a bundle covers every connection in it", env, name).
			WithHint("name the environment, `" + name + "`; one connection is a grant of its own: `" +
				sf.Call("grant.allow", plugin.Arg{Name: "target", Value: plugin.Placeholder("capability"), Positional: true},
					plugin.Arg{Name: "profile", Value: env}) + "`").WithInput("profile")
	}
	ceiling, verr := core.Ceiling()
	if verr != nil {
		return b, verr
	}
	b.where = ceiling.Where()
	byPlugin := map[string][]plugin.Capability{}
	for _, c := range catalog() {
		ns := core.Namespace(c.ID)
		byPlugin[ns] = append(byPlugin[ns], c)
	}
	for _, ns := range p.Namespaces() {
		caps := byPlugin[ns]
		if len(caps) == 0 {
			b.absent = append(b.absent, ns)
			continue
		}
		// Compacted: a stale pin left beside its replacement names one
		// connection twice (Profile.DuplicateNamespaces), and a bundle of
		// the same row twice would count a grant it issues once.
		refs := slices.Compact(profiles.InstanceRefs(p, env, ns))
		b.plugins = append(b.plugins, ns)
		b.refs[ns] = refs
		sort.Slice(caps, func(i, j int) bool { return caps[i].ID < caps[j].ID })
		for _, c := range caps {
			switch {
			case c.HumanOnly:
				b.humanOnly = append(b.humanOnly, c.ID)
			case !plugin.Profilable(c):
				b.unprofiled = append(b.unprofiled, c.ID)
				if core.Required(c, "") {
					b.uncovered = append(b.uncovered, c.ID)
				}
			case classRank(c.Safety) > classRank(class):
				b.wider[c.Safety] = append(b.wider[c.Safety], c.ID)
			default:
				for _, ref := range refs {
					if why := ceiling.Forbids(c.ID, "", ref); why != "" {
						b.skipped = append(b.skipped, skippedRow{id: c.ID, ref: ref, why: why})
						continue
					}
					b.rows = append(b.rows, bundleRow{capability: c, ref: ref})
				}
			}
		}
	}
	if len(b.rows) == 0 {
		return b, b.nothingToIssue(sf)
	}
	return b, nil
}

// connectionsIn is every connection an environment holds, across its plugins,
// spelled as a grant names them: staging, staging/analytics.
func connectionsIn(p config.Profile, name string) []string {
	namespaces := p.Namespaces()
	refs := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		refs = append(refs, profiles.InstanceRefs(p, name, ns)...)
	}
	sort.Strings(refs)
	return slices.Compact(refs)
}

// issuedAsABundle is the `grant issue --profile` call that issues exactly the
// grants given, or "" when none does. A bundle's grants carry no role or bundle
// id — the bookkeeping is the agent and the environment — so a revoke cannot
// know a set came from one; it can ask whether the set is what a call would
// issue, and say so only when it is, set for set: grants that were issued by
// hand, or a bundle some of whose grants have expired or been taken back one
// at a time, are not what the call would put back, and the per-grant lines
// stand.
//
// The call carries what issued it where every grant says the same — the
// class, and the window, the bound and the note — so that running it
// gives the set back as it was and not as a default would.
func issuedAsABundle(sf plugin.Surface, catalog func() []plugin.Capability, gone []core.Grant) string {
	if len(gone) < 2 || catalog == nil {
		return ""
	}
	first := gone[0]
	env := config.RefName(first.Profile)
	held := make(map[string]bool, len(gone))
	for _, g := range gone {
		if env == "" || config.RefName(g.Profile) != env || g.Agent != first.Agent || g.Role != "" || g.Scope != "" ||
			g.TTL != first.TTL || g.MaxUses != first.MaxUses || g.RateMax != first.RateMax ||
			g.RateWindow != first.RateWindow || g.Note != first.Note {
			return ""
		}
		held[g.Target+"@"+g.Profile] = true
	}
	for _, class := range bundleClasses {
		b, verr := expandEnvironment(sf, catalog, env, class)
		if verr != nil || len(b.rows) != len(held) {
			continue
		}
		if !slices.ContainsFunc(b.rows, func(r bundleRow) bool { return !held[r.capability.ID+"@"+r.ref] }) {
			return sf.Call("grant.issue", b.call(first)...)
		}
	}
	return ""
}

// call is the arguments that issue this bundle, as the grants of g say it was
// issued: the environment, the agent, the class when it is not the default
// one, and the window, bound and note where the grants carry them.
func (b environmentBundle) call(g core.Grant) []plugin.Arg {
	args := []plugin.Arg{{Name: "profile", Value: b.name}}
	if g.Agent != "" {
		args = append(args, plugin.Arg{Name: "agent", Value: g.Agent})
	}
	if b.class != plugin.Read {
		args = append(args, plugin.Arg{Name: "safety", Value: string(b.class)})
	}
	if g.TTL != "" {
		args = append(args, plugin.Arg{Name: "ttl", Value: g.TTL})
	}
	if g.MaxUses > 0 {
		args = append(args, plugin.Arg{Name: "max-uses", Value: g.MaxUses})
	}
	if g.RateMax > 0 && g.RateWindow != "" {
		args = append(args, plugin.Arg{Name: "rate", Value: fmt.Sprintf("%d/%s", g.RateMax, g.RateWindow)})
	}
	if g.Note != "" {
		args = append(args, plugin.Arg{Name: "note", Value: g.Note})
	}
	return args
}

// nothingToIssue is the refusal when the expansion is empty, and says why.
// The ceiling's reasons lead when it is what emptied it: that is the one a
// person can act on, and the rest is only a smaller everything.
func (b environmentBundle) nothingToIssue(sf plugin.Surface) *view.Error {
	if len(b.skipped) > 0 {
		var reasons []string
		for _, s := range b.skipped {
			why := s.why
			if b.where != "" {
				why += " — " + b.where
			}
			if !slices.Contains(reasons, why) {
				reasons = append(reasons, why)
			}
		}
		return view.Errorf("grant.policy.refused", "nothing on %s can be issued: %s", b.name, strings.Join(reasons, "; ")).
			WithHint(core.CeilingHint)
	}
	switch {
	case len(b.plugins) == 0 && len(b.absent) > 0:
		return view.Errorf("grant.issue.empty", "%s configures %s, and no plugin by that name is installed here",
			b.name, strings.Join(b.absent, ", ")).
			WithHint("`rta plugin list` says which plugins load")
	case len(b.plugins) == 0:
		return view.Errorf("grant.issue.empty", "%s configures no plugin, so there is nothing for a bundle to cover", b.name).
			WithHint("`rta profile show " + b.name + "` says what it holds")
	}
	for _, c := range bundleClasses[classRank(b.class)+1:] {
		if len(b.wider[c]) > 0 {
			return view.Errorf("grant.issue.empty", "%s has nothing to issue among %s", b.name, classWords(b.class)).
				WithHint(ask(sf, c) + " takes in " + format.Count(len(b.wider[c]), "capability", "capabilities") + " more").WithInput("safety")
		}
	}
	return view.Errorf("grant.issue.empty", "everything %s's plugins offer is for the person at the terminal, or takes no profile, so no grant on a connection covers it",
		b.name).WithHint("a capability that takes no profile is a grant of its own: " + sf.CapabilityName("grant.list") + " with `detail` shows which need one")
}

// ask is --safety given class, as the surface asking spells it.
func ask(sf plugin.Surface, c plugin.Safety) string {
	return "`" + sf.InputTo("safety", string(c)) + "`"
}

// lines are the rows as the lines `grant issue` builds from, each carrying the
// bound the person gave for it.
func (b environmentBundle) lines(note, rate string, maxUses int) []role.Line {
	out := make([]role.Line, len(b.rows))
	for i, r := range b.rows {
		out[i] = role.Line{
			Raw: r.capability.ID + " --profile " + r.ref, Target: r.capability.ID, Profile: r.ref,
			Note: note, Rate: rate, MaxUses: maxUses,
		}
	}
	return out
}

// bundleRun is what the person asked for beside the environment: who it is
// for, how long, and the bounds each grant carries.
type bundleRun struct {
	agent   string
	window  time.Duration
	maxUses int
	rate    string
}

// describe is the facts a person signs: the same pairs before the passphrase,
// in a dry run and in the receipt. dry says the grants are not issued yet.
func (b environmentBundle) describe(sf plugin.Surface, run bundleRun, dry bool) []view.Pair {
	grants := format.Count(len(b.rows), "grant", "grants") + ", each for " + format.Duration(run.window)
	if dry {
		grants = "would issue " + grants
	}
	pairs := []view.Pair{
		{Key: "environment", Value: b.name},
		{Key: "agent", Value: run.agent},
		{Key: "class", Value: b.classLine(sf)},
		{Key: "grants", Value: grants},
		{Key: "covers", Value: strings.Join(b.coverage(), "\n")},
		{Key: "reveals", Value: b.revealing()},
		{Key: "destructive", Value: b.destroying()},
	}
	if lines := b.skippedLines(); len(lines) > 0 {
		pairs = append(pairs, view.Pair{Key: "skipped by policy", Value: strings.Join(lines, "\n")})
	}
	if lines := b.leftOut(); len(lines) > 0 {
		pairs = append(pairs, view.Pair{Key: "left out", Value: strings.Join(lines, "\n")})
	}
	return append(pairs, view.Pair{Key: "budget", Value: b.budgetLine(sf, run)})
}

// classLine says how far the bundle reaches and what a wider class would add.
func (b environmentBundle) classLine(sf plugin.Surface) string {
	line := classWords(b.class)
	var adds []string
	for _, c := range bundleClasses[classRank(b.class)+1:] {
		if n := len(b.wider[c]); n > 0 {
			adds = append(adds, ask(sf, c)+" "+format.Count(n, "capability", "capabilities"))
		}
	}
	if len(adds) == 0 {
		return line
	}
	return line + " — " + strings.Join(adds, ", ") + " more"
}

// coverage is how many capabilities each plugin contributes, connection by
// connection.
func (b environmentBundle) coverage() []string {
	count := map[string]map[string]int{}
	for _, r := range b.rows {
		ns := core.Namespace(r.capability.ID)
		if count[ns] == nil {
			count[ns] = map[string]int{}
		}
		count[ns][r.ref]++
	}
	var out []string
	for _, ns := range b.plugins {
		var parts []string
		for _, ref := range b.refs[ns] {
			n := count[ns][ref]
			if n == 0 {
				continue
			}
			if len(parts) == 0 {
				parts = append(parts, format.Count(n, "capability", "capabilities")+" on "+ref)
				continue
			}
			parts = append(parts, strconv.Itoa(n)+" on "+ref)
		}
		if len(parts) > 0 {
			out = append(out, ns+" — "+strings.Join(parts, ", "))
		}
	}
	return out
}

// revealing names the capabilities that hand over a stored value, which a
// grant with no record covers for every record it reaches.
func (b environmentBundle) revealing() string {
	ids := b.rowsWhere(func(c plugin.Capability) bool { return c.Reveals })
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ") + " — each for every record it reaches"
}

// destroying names every destructive capability the bundle covers.
func (b environmentBundle) destroying() string {
	ids := b.rowsWhere(func(c plugin.Capability) bool { return c.Safety == plugin.Destructive })
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

// rowsWhere is the capability IDs among the rows that satisfy keep, sorted,
// once each however many connections they are issued on.
func (b environmentBundle) rowsWhere(keep func(plugin.Capability) bool) []string {
	var ids []string
	for _, r := range b.rows {
		if keep(r.capability) && !slices.Contains(ids, r.capability.ID) {
			ids = append(ids, r.capability.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// skippedLines names what the ceiling forbade, a line for each rule: the
// rule's sentence, the file it came from, and the capabilities it took out —
// each by its ID, and by the connections too when it took it out of some and
// not all of the ones the environment holds for that plugin.
func (b environmentBundle) skippedLines() []string {
	byWhy := map[string]map[string][]string{}
	var whys, ids []string
	for _, s := range b.skipped {
		if byWhy[s.why] == nil {
			byWhy[s.why] = map[string][]string{}
			whys = append(whys, s.why)
		}
		if !slices.Contains(ids, s.id) {
			ids = append(ids, s.id)
		}
		byWhy[s.why][s.id] = append(byWhy[s.why][s.id], s.ref)
	}
	out := make([]string, 0, len(whys))
	for _, why := range whys {
		var names []string
		for _, id := range ids {
			refs, ok := byWhy[why][id]
			if !ok {
				continue
			}
			if len(refs) == len(b.refs[core.Namespace(id)]) {
				names = append(names, id)
				continue
			}
			names = append(names, id+" via "+strings.Join(refs, ", "))
		}
		line := why
		if b.where != "" {
			line += " — " + b.where
		}
		out = append(out, line+": "+strings.Join(names, ", "))
	}
	return out
}

// leftOut counts what no grant on a connection can be: the capabilities for
// the person at the terminal, and the ones that take no profile — named the
// ones among them that need a grant of their own, since those are the ones
// the bundle does not give an agent and somebody may think it did.
func (b environmentBundle) leftOut() []string {
	var out []string
	if n := len(b.humanOnly); n > 0 {
		out = append(out, format.Count(n, "capability", "capabilities")+" for the person at the terminal, never a tool")
	}
	if n := len(b.unprofiled); n > 0 {
		line := format.Count(n, "capability that takes", "capabilities that take") + " no profile, so no grant on a connection names " +
			format.Plural(n, "it", "them")
		if len(b.uncovered) > 0 {
			line += " — " + namesOf(b.uncovered, 4) + format.Plural(len(b.uncovered), " needs", " need") + " a grant of its own"
		}
		out = append(out, line)
	}
	if len(b.absent) > 0 {
		out = append(out, b.name+" configures "+strings.Join(b.absent, ", ")+", which "+
			format.Plural(len(b.absent), "is", "are")+" not installed here")
	}
	return out
}

// namesOf is ids as a list of at most limit names and a count of the rest —
// never "and 1 more", whose name is shorter than saying so.
func namesOf(ids []string, limit int) string {
	if len(ids) <= limit+1 {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:limit], ", ") + " and " + strconv.Itoa(len(ids)-limit) + " more"
}

// budgetLine says what bounds each grant, and that it bounds each: the bound
// somebody types as one number reads as the bundle's.
func (b environmentBundle) budgetLine(sf plugin.Surface, run bundleRun) string {
	var bounds []string
	if run.maxUses > 0 {
		bounds = append(bounds, sf.InputTo("max-uses", run.maxUses))
	}
	if run.rate != "" {
		bounds = append(bounds, sf.InputTo("rate", run.rate))
	}
	if len(bounds) == 0 {
		return "neither is given — " + sf.InputName("max-uses") + " and " + sf.InputName("rate") +
			", when given, bound each grant on its own and never the bundle"
	}
	return strings.Join(bounds, " and ") + " bound each of the " + format.Count(len(b.rows), "grant", "grants") +
		" on its own, not the bundle"
}

// replacedSummary says what a bundle's grants replace, by where they came
// from, for the one sentence a person reads before signing: the rows
// themselves are the plan's.
func replacedSummary(agent string, replaced []core.Grant) string {
	if len(replaced) == 0 {
		return ""
	}
	byRole := map[string]int{}
	for _, g := range replaced {
		key := "under no role"
		if g.Role != "" {
			key = "under role " + g.Role
		}
		byRole[key]++
	}
	parts := make([]string, 0, len(byRole))
	for key, n := range byRole {
		parts = append(parts, strconv.Itoa(n)+" "+key)
	}
	sort.Strings(parts)
	return format.Count(len(replaced), "grant", "grants") + " already standing for " + agent + " — " + strings.Join(parts, ", ")
}

// takeBackBundle is the line a receipt ends on: how the whole bundle goes
// back, and how its deadline moves. Both select by the environment and the
// agent, so they take every grant that agent holds on it, however it was
// issued, which the sentence says.
func takeBackBundle(sf plugin.Surface, env, agent string) string {
	selectors := []plugin.Arg{{Name: "profile", Value: env}, {Name: "agent", Value: agent}}
	return "`" + sf.Call("grant.revoke", selectors...) + "` takes back every grant " + agent + " holds on " + env +
		", however it was issued; `" + sf.Call("grant.renew", selectors...) + "` moves their deadline"
}

// printPairs writes pairs as the listing a person reads before the
// passphrase: a key, and its value with continuation lines under it.
func printPairs(w io.Writer, pairs []view.Pair) {
	width := 0
	for _, p := range pairs {
		width = max(width, len(p.Key))
	}
	for _, p := range pairs {
		lines := strings.Split(p.Value, "\n")
		fmt.Fprintf(w, "  %-*s  %s\n", width, p.Key, lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(w, "  %-*s  %s\n", width, "", l)
		}
	}
}
