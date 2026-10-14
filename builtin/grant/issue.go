package grant

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/role"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A role is a day's grants under one word: a named list of grant lines in
// the grammar `grant allow` parses, issued whole to one agent by `rta grant
// issue <role>` under one passphrase. The issued bundle has no noun and no
// id of its own — it is "role dev, issued to claude", and the grant file
// already keeps one row per target, record, connection and agent, so that
// pair is what names it. `grant list` shows the role on each row, `grant
// revoke --role dev` takes the bundle back, `grant renew --role dev` moves
// its deadline.
//
// Nothing new is enforced. Every line is built by buildGrant exactly as a
// typed grant is, the ceiling caps each one, the guard signs each one, the
// gate never reads the role name. The agent still presents its --as name
// and still holds nothing an operator did not issue.
//
// A role from a repository's policy file is somebody else's list, and a
// bundle issues it whole. So a team's role is issued at the command line
// only, where its lines are printed before the passphrase asks for them —
// and where no passphrase stands in for that look, the guard off, it wants
// --yes after `rta grant roles <name>` has been read. An operator's own
// roles ask nothing extra: they wrote them.

// DefaultRoleTTL is how long a role's grants last when neither the role nor
// the operator says: a working day with the evening in it, under the 24h a
// grant may stand at most.
const DefaultRoleTTL = "12h"

func suggestRoles(context.Context, plugin.Request) []string {
	all, verr := role.Available()
	if verr != nil {
		return nil
	}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.Name+"\t"+fmt.Sprintf("%d %s, %s", len(s.Role.Grants),
			format.Plural(len(s.Role.Grants), "grant", "grants"), sourceWord(s)))
	}
	return out
}

// suggestStandingRoles completes --role on revoke and renew from the roles
// whose grants stand right now.
func suggestStandingRoles(context.Context, plugin.Request) []string {
	grants, verr := core.Load()
	if verr != nil {
		return nil
	}
	seen := map[string]map[string]bool{}
	for _, g := range grants {
		if g.Role == "" {
			continue
		}
		if seen[g.Role] == nil {
			seen[g.Role] = map[string]bool{}
		}
		seen[g.Role][g.Agent] = true
	}
	out := make([]string, 0, len(seen))
	for name, agents := range seen {
		names := make([]string, 0, len(agents))
		for a := range agents {
			names = append(names, a)
		}
		sort.Strings(names)
		out = append(out, name+"\tfor "+strings.Join(names, ", "))
	}
	sort.Strings(out)
	return out
}

func suggestRoleTTL(context.Context, plugin.Request) []string {
	return []string{"4h\tan afternoon", "8h\ta working day", DefaultRoleTTL + "\tthe default", "24h\tthe most a grant can last"}
}

func sourceWord(s role.Source) string {
	if s.Team {
		return "the team's (" + s.From + ")"
	}
	return "yours (" + s.From + ")"
}

// effectiveWindow is how long a role's grants will actually stand: the
// role's ttl (or the default), under the grant maximum, under the team
// ceiling — which is what `rta policy init`'s own starter file caps at one
// hour a few lines above its commented eight-hour role. Said before the
// role is issued rather than discovered after signing.
func effectiveWindow(r role.Source) (asked, effective time.Duration, cappedBy string, err error) {
	raw := strings.TrimSpace(r.Role.TTL)
	if raw == "" {
		raw = DefaultRoleTTL
	}
	asked, err = time.ParseDuration(raw)
	if err != nil || asked <= 0 {
		return 0, 0, "", fmt.Errorf("role %q in %s: ttl %q is not a duration", r.Name, r.From, raw)
	}
	effective = min(asked, core.MaxTTL)
	if d, capped, where := core.ClampTTL(effective); capped {
		effective, cappedBy = d, where
	}
	return asked, effective, cappedBy, nil
}

// windowWords is the ttl column of `grant roles` and doctor's row: the
// written window, and what it becomes when something caps it.
func windowWords(r role.Source) string {
	asked, effective, cappedBy, err := effectiveWindow(r)
	if err != nil {
		return "not a duration"
	}
	if effective == asked {
		return format.Duration(asked)
	}
	by := "the 24h maximum"
	if cappedBy != "" {
		by = cappedBy
	}
	return fmt.Sprintf("%s, capped to %s by %s", format.Duration(asked), format.Duration(effective), by)
}

// RolesInForce is one line per role and agent among the grants standing,
// for the overview and doctor — the same words the roster prints above
// its rows. Empty when no grant carries a role.
//
// Returns the error rather than folding it into "" the way an empty grants
// file also would: core.Load failing (a corrupted file, a Ceiling it cannot
// read) and core.Load succeeding with nothing active are not the same fact,
// and rolesLine's caller needs to tell an operator "none" from "unreadable".
func RolesInForce() (string, *view.Error) {
	grants, verr := core.Load()
	if verr != nil {
		return "", verr
	}
	return rolesInForce(grants), nil
}

// IssueRole is the issue flow for another built-in: agent.allow answers a
// parked call with the whole role its line belongs to. Same lines printed,
// same ceiling per line, same one passphrase, same team-role rule.
func IssueRole(req plugin.Request, catalog func() []plugin.Capability, artifact func(string) (string, bool)) (view.View, error) {
	return runIssue(req, catalog, artifact)
}

// SuggestRoles completes a role name, for agent.allow's --role.
func SuggestRoles(ctx context.Context, req plugin.Request) []string { return suggestRoles(ctx, req) }

// EffectiveWindow is windowWords for doctor's roles row.
func EffectiveWindow(r role.Source) string { return windowWords(r) }

// unlockPrompted is how the passphrase is asked for, a var so a test can count
// the times it is: once for the whole issuance, however many grants are signed
// with the one key it returns — the point of issuing a list at all.
var unlockPrompted = guard.UnlockPrompted

func runIssue(req plugin.Request, catalog func() []plugin.Capability, artifact func(string) (string, bool)) (view.View, error) {
	iss, verr := planIssue(req, catalog)
	if verr != nil {
		return nil, verr
	}
	sf := req.Surface()
	if verr := guard.RefuseArgv(req); verr != nil {
		return nil, verr
	}
	from := core.Origin(sf, term.IsTerminal(int(stdio.Real().Fd())))
	prepared, notes, verr := iss.build(sf, catalog, artifact, from)
	if verr != nil {
		return nil, verr
	}
	// What each line replaces, read before the lock so it can be shown
	// before the passphrase. Advisory for that reason: only what Issue
	// hands back from inside its locked callback is what actually went.
	standing, _ := core.Load()
	replaces := make([]string, len(prepared))
	var replacing []core.Grant
	for i, g := range prepared {
		if old := equivalentOf(standing, g); old != nil {
			replaces[i] = replacedWords(*old)
			replacing = append(replacing, *old)
		}
	}
	plan := planTable(prepared, replaces)
	if req.DryRun {
		return iss.dryRun(sf, prepared, plan, notes, replacing), nil
	}
	if guard.Enabled() {
		if sf == plugin.SurfaceCLI {
			iss.listBeforeSigning(os.Stderr, sf, prepared, replaces, notes, replacing)
		}
		signer, verr := unlockPrompted(req)
		if verr != nil {
			return nil, verr
		}
		for i := range prepared {
			core.SignWith(signer, &prepared[i])
		}
	} else if iss.role != nil && iss.role.Team && !req.Yes {
		src := iss.role
		return nil, view.Errorf("grant.role.unread", "role %q comes from %s, and no passphrase stands between its %d %s and the grant file",
			src.Name, src.From, len(prepared), format.Plural(len(prepared), "line", "lines")).
			WithHint("`" + sf.Call("grant.roles", roleArg(src.Name)) + "` shows the lines; run again with " +
				"--yes once read, or `" + sf.Call("grant.guard.on") + "` to be asked every time")
	}
	issued := 0
	var replaced []core.Grant
	for _, g := range prepared {
		old, verr := core.IssueReplacing(g, true)
		if verr != nil {
			if issued > 0 {
				return nil, view.Errorf(verr.Code, "%d of %d grants issued, then %q failed: %s",
					issued, len(prepared), g.Target, verr.Message).
					WithHint("`" + iss.takeBack(sf) + "` takes the issued ones back")
			}
			return nil, verr
		}
		issued++
		if old != nil {
			replaced = append(replaced, *old)
		}
	}
	var until time.Time
	for _, g := range prepared {
		if g.Expires.After(until) {
			until = g.Expires
		}
	}
	ends := view.Pair{Key: "until", Value: format.Clock(until) + " (" + format.Duration(time.Until(until)) + ")"}
	if iss.env != nil {
		// The bundle is named by what it is, so it has no "refreshed, not
		// doubled" to claim: a grant it replaces may be a hand-issued one as
		// readily as an earlier bundle, and the summary says which.
		pairs := append(iss.environmentFacts(sf, notes, replaced, false), ends,
			view.Pair{Key: "take back", Value: takeBackBundle(sf, iss.env.name, iss.agent)})
		return view.Sections{Items: []view.Section{
			{ID: "environment", Title: "Environment", View: view.KeyValue{Pairs: pairs}},
			{ID: "grants", Title: "Grants", View: plan},
		}}, nil
	}
	src, agent := *iss.role, iss.agent
	sameRole := true
	for _, old := range replaced {
		if old.Role != src.Name || old.Agent != agent {
			sameRole = false
		}
	}
	pairs := []view.Pair{
		{Key: "role", Value: src.Name + " — " + sourceWord(src)},
		{Key: "agent", Value: agent},
		{Key: "grants", Value: strconv.Itoa(issued)},
	}
	switch {
	case len(replaced) == issued && sameRole:
		pairs = append(pairs, view.Pair{Key: "replaced", Value: fmt.Sprintf("the %s already standing — %s is refreshed, not doubled", format.Plural(issued, "grant", "grants"), src.Name)})
	case len(replaced) > 0:
		pairs = append(pairs, view.Pair{Key: "replaced", Value: fmt.Sprintf("%d — the grants column says which", len(replaced))})
	}
	pairs = append(pairs, ends, view.Pair{Key: "take back", Value: takeBack(sf, src.Name, agent)})
	if len(notes) > 0 {
		pairs = append(pairs, view.Pair{Key: "note", Value: strings.Join(notes, "\n")})
	}
	return view.Sections{Items: []view.Section{
		{ID: "role", Title: "Role", View: view.KeyValue{Pairs: pairs}},
		{ID: "grants", Title: "Grants", View: plan},
	}}, nil
}

// issuance is what one `grant issue` hands the flow in runIssue: the lines to
// build grants from, who they are for and for how long. They are a role's, or
// an environment's (bundle.go); everything after the lines are built —
// listed, checked against the ceiling, signed once, written one by one and
// reported — is the one flow, so a bundle is held to what a role is held to.
type issuance struct {
	role   *role.Source
	env    *environmentBundle
	lines  []role.Line
	agent  string
	ttl    string
	window time.Duration
	run    bundleRun
}

// planIssue decides what is being issued — exactly one of a role and an
// environment, said as a refusal when it is both or neither — and reads it
// into lines.
func planIssue(req plugin.Request, catalog func() []plugin.Capability) (issuance, *view.Error) {
	sf := req.Surface()
	named, env := strings.TrimSpace(req.String("role")), strings.TrimSpace(req.String("profile"))
	switch {
	case named != "" && env != "":
		return issuance{}, view.Errorf("grant.issue.both", "give a role or %s, not both: a role is a list somebody wrote, and %s issues what an environment covers",
			sf.InputName("profile"), sf.InputName("profile")).
			WithHint("`" + sf.Call("grant.issue", roleArg(named)) + "` issues the role, `" +
				sf.Call("grant.issue", plugin.Arg{Name: "profile", Value: env}) + "` the environment")
	case named == "" && env == "":
		return issuance{}, view.Errorf("grant.issue.nothing", "name what to issue: a role, or %s for everything an environment covers",
			sf.InputName("profile")).
			WithHint(sf.CapabilityName("grant.roles") + " lists the roles, `rta profile list` the environments")
	case env != "":
		return planEnvironment(req, catalog, env)
	}
	return planRole(req, named)
}

func planRole(req plugin.Request, named string) (issuance, *view.Error) {
	sf := req.Surface()
	src, verr := role.Find(named)
	if verr != nil {
		return issuance{}, verr
	}
	// What a bundle of an environment takes, and a role's lines already say
	// for themselves: a bound given here would be read as every line's, and
	// a line has its own.
	for _, given := range []struct {
		name string
		set  bool
	}{
		{"safety", strings.TrimSpace(req.String("safety")) != ""}, {"max-uses", req.Int("max-uses") != 0},
		{"rate", strings.TrimSpace(req.String("rate")) != ""}, {"note", strings.TrimSpace(req.String("note")) != ""},
	} {
		if given.set {
			return issuance{}, bundleFlagRefusal(sf, src, given.name)
		}
	}
	lines, err := src.Lines()
	if err != nil {
		return issuance{}, view.Errorf("grant.role", "%v", err).
			WithHint("a role line is a target, an optional record, and --profile, --ttl, --max-uses, --rate or --note")
	}
	// The team's list is issued where its lines can be read first: on the
	// command line, printed before the passphrase or acknowledged with --yes.
	// A form that collected a masked passphrase before the run showed
	// nothing, and a checkbox is not a look at somebody else's list.
	if src.Team && sf != plugin.SurfaceCLI {
		return issuance{}, view.Errorf("grant.role.team", "role %q comes from %s, and a team's role is issued at the command line, where its lines are shown first", src.Name, src.From).
			// Spelled for the CLI on purpose: that is where this refusal
			// sends its reader.
			WithHint("at the command line, `" + plugin.SurfaceCLI.Call("grant.roles", roleArg(src.Name)) +
				"` shows the lines and `" + plugin.SurfaceCLI.Call("grant.issue", roleArg(src.Name)) + "` issues them")
	}
	// --agent, else the agent the operator's own role names, else the one
	// this machine knows. A default for a command a person still runs
	// under the guard: it picks who receives the list, never what is in it.
	asked := strings.TrimSpace(req.String("agent"))
	if asked == "" && !src.Team {
		asked = strings.TrimSpace(src.Role.Agent)
	}
	agent, verr := resolveAgent(sf, asked)
	if verr != nil {
		return issuance{}, verr
	}
	if verr := core.CheckAgent(agent); verr != nil {
		return issuance{}, verr
	}
	ttl := strings.TrimSpace(req.String("ttl"))
	if ttl == "" {
		ttl = strings.TrimSpace(src.Role.TTL)
	}
	if ttl == "" {
		ttl = DefaultRoleTTL
	}
	window, err := time.ParseDuration(ttl)
	if err != nil || window <= 0 {
		return issuance{}, view.Errorf("grant.role.ttl", "%q is not a duration a role can stand for", ttl).
			WithHint("4h, 12h — the most a grant can last is 24h")
	}
	return issuance{role: &src, lines: lines, agent: agent, ttl: ttl, window: window}, nil
}

// bundleFlagRefusal answers a flag only an environment's bundle takes, given
// beside a role. A role's lines name their capabilities, so --safety, which
// picks which classes of a plugin's capabilities an environment covers, has
// nowhere to apply and is no line flag either: the way to a class is the
// environment, and that is the whole hint. The others are a line's own, and
// the example is a line of this role, not a capability picked for the text.
func bundleFlagRefusal(sf plugin.Surface, src role.Source, name string) *view.Error {
	if name == "safety" {
		return view.Errorf("grant.issue.bundleflag", "%s is for an environment: a role names its capabilities itself, so there is no class for it to pick",
			sf.InputName(name)).
			WithHint("`" + sf.Call("grant.issue", plugin.Arg{Name: "profile", Value: "<env>"}, plugin.Arg{Name: "safety", Value: "write"}) +
				"` issues an environment's capabilities of that class")
	}
	stem := "<capability>"
	if lines, err := src.Lines(); err == nil {
		stem = strings.TrimSpace(lines[0].Target + " " + lines[0].Scope)
	}
	return view.Errorf("grant.issue.bundleflag", "%s is for an environment: a role's lines carry their own", sf.InputName(name)).
		WithHint("write it on the role's line, `" + stem + " --" + name + " …`, or give " + sf.InputName("profile") + " instead of the role")
}

func planEnvironment(req plugin.Request, catalog func() []plugin.Capability, env string) (issuance, *view.Error) {
	sf := req.Surface()
	class, verr := bundleClass(req.String("safety"))
	if verr != nil {
		return issuance{}, verr
	}
	agent, verr := resolveAgent(sf, req.String("agent"))
	if verr != nil {
		return issuance{}, verr
	}
	// The day a role's own default is: the command is the same one line for a
	// day's work, and the ceiling caps it as it caps a role.
	ttl := strings.TrimSpace(req.String("ttl"))
	if ttl == "" {
		ttl = DefaultRoleTTL
	}
	window, err := time.ParseDuration(ttl)
	if err != nil || window <= 0 {
		return issuance{}, view.Errorf("grant.issue.ttl", "%q is not a duration the grants can stand for", ttl).
			WithHint("4h, 12h — the most a grant can last is 24h")
	}
	run := bundleRun{agent: agent, window: window, maxUses: req.Int("max-uses"), rate: strings.TrimSpace(req.String("rate"))}
	if verr := checkShared(sf, run); verr != nil {
		return issuance{}, verr
	}
	bundle, verr := expandEnvironment(sf, catalog, env, class)
	if verr != nil {
		return issuance{}, verr
	}
	return issuance{
		env: &bundle, lines: bundle.lines(req.String("note"), run.rate, run.maxUses),
		agent: agent, ttl: ttl, window: window, run: run,
	}, nil
}

// checkShared holds the inputs every row of a bundle is built from to the
// rules a grant's own are held to, once, before any row exists. Left to the
// row, a bad --rate was reported against the first row generated — `line
// "kube.cert.list --profile staging"`, which nobody wrote — and the person
// went looking for a line that is not in any role or config.
func checkShared(sf plugin.Surface, run bundleRun) *view.Error {
	if verr := core.CheckAgent(run.agent); verr != nil {
		return verr
	}
	if verr := checkMaxUses(sf, run.maxUses); verr != nil {
		return verr
	}
	_, _, verr := parseRate(sf, run.rate)
	return verr
}

// where names a line in a refusal: the role and the line, or the environment
// and the row.
func (i issuance) where(l role.Line) string {
	if i.role != nil {
		return fmt.Sprintf("role %q, line %q", i.role.Name, l.Raw)
	}
	return fmt.Sprintf("environment %q, line %q", i.env.name, l.Raw)
}

// build makes the grants, each exactly as a typed one is made and checked.
func (i issuance) build(sf plugin.Surface, catalog func() []plugin.Capability, artifact func(string) (string, bool),
	from string,
) ([]core.Grant, []string, *view.Error) {
	var prepared []core.Grant
	var notes []string
	for _, l := range i.lines {
		lineTTL := i.ttl
		if l.TTL != "" {
			// The role author's bound on one line and the operator's on all
			// of them: the shorter stands, and a line never widens.
			d, err := time.ParseDuration(l.TTL)
			if err != nil || d <= 0 {
				return nil, nil, view.Errorf("grant.role", "%s: --ttl %q is not a duration", i.where(l), l.TTL)
			}
			if d < i.window {
				lineTTL = l.TTL
			}
		}
		g, n, verr := buildGrant(sf, catalog, artifact, operatorid.IssueSpec{
			Target: l.Target, Scope: l.Scope, Profile: l.Profile, Agent: i.agent,
			TTL: lineTTL, Note: l.Note, MaxUses: l.MaxUses, Rate: l.Rate,
		}, from)
		if verr != nil {
			return nil, nil, view.Errorf(verr.Code, "%s: %s", i.where(l), verr.Message).WithHint(verr.Hint)
		}
		// The ceiling before the passphrase, for every line: one forbidden
		// target refuses the whole role before anybody types anything. An
		// environment's rows the ceiling forbids were left out when it was
		// read, so one that reaches here is a ceiling that changed under the
		// command, and refuses the same way.
		if verr := core.CheckCeiling(g.Target, g.Scope, g.Profile); verr != nil {
			return nil, nil, view.Errorf(verr.Code, "%s: %s", i.where(l), verr.Message).WithHint(verr.Hint)
		}
		if i.role != nil {
			g.Role = i.role.Name
		}
		for _, s := range []string{cappedNote(n), inactiveProfileNote(g), unusableProfileNote(catalog, artifact, g)} {
			if s == "" {
				continue
			}
			switch {
			case i.role != nil:
				notes = append(notes, l.Raw+": "+s)
			// Said once for a bundle: the window, the ceiling and the
			// environment are the same on every row, and the same sentence
			// a hundred times is a sentence nobody reads.
			case !slices.Contains(notes, s):
				notes = append(notes, s)
			}
		}
		prepared = append(prepared, g)
	}
	return prepared, notes, nil
}

// takeBack is the call that takes the whole issuance back.
func (i issuance) takeBack(sf plugin.Surface) string {
	if i.role != nil {
		return takeBack(sf, i.role.Name, i.agent)
	}
	return sf.Call("grant.revoke", plugin.Arg{Name: "profile", Value: i.env.name}, plugin.Arg{Name: "agent", Value: i.agent})
}

// environmentFacts is the bundle's facts with what it replaces and what the
// ceiling or the environment's state says about it, for the listing, the dry
// run and the receipt alike. dry says nothing is issued yet.
func (i issuance) environmentFacts(sf plugin.Surface, notes []string, replaced []core.Grant, dry bool) []view.Pair {
	pairs := i.env.describe(sf, i.run, dry)
	if s := replacedSummary(i.agent, replaced); s != "" {
		key := "replaced"
		if dry {
			key = "replaces"
		}
		pairs = append(pairs, view.Pair{Key: key, Value: s})
	}
	if len(notes) > 0 {
		pairs = append(pairs, view.Pair{Key: "note", Value: strings.Join(notes, "\n")})
	}
	return pairs
}

// dryRun is what would be issued, as the receipt would say it.
func (i issuance) dryRun(sf plugin.Surface, prepared []core.Grant, plan view.Table, notes []string, replacing []core.Grant) view.View {
	if i.env != nil {
		return view.Sections{Items: []view.Section{
			{ID: "environment", Title: "Environment", View: view.KeyValue{Pairs: i.environmentFacts(sf, notes, replacing, true)}},
			{ID: "grants", Title: "Grants", View: plan},
		}}
	}
	return view.Sections{Items: []view.Section{
		{ID: "role", Title: "Role", View: view.Text{Body: fmt.Sprintf(
			"would issue %s to %s: %d %s for %s", i.role.Name, i.agent, len(prepared),
			format.Plural(len(prepared), "grant", "grants"), format.Duration(i.window)) + noteLines(notes)}},
		{ID: "grants", Title: "Grants", View: plan},
	}}
}

// listBeforeSigning is what a person reads before the passphrase is asked
// for: a role's lines one by one, or an environment's facts — which are what
// there is to decide on when the lines are a hundred of the same shape.
func (i issuance) listBeforeSigning(w io.Writer, sf plugin.Surface, prepared []core.Grant, replaces, notes []string, replacing []core.Grant) {
	if i.env != nil {
		fmt.Fprintf(w, "rta: environment %s would issue %s to %s:\n", i.env.name,
			format.Count(len(prepared), "grant", "grants"), i.agent)
		printPairs(w, i.environmentFacts(sf, notes, replacing, true))
		return
	}
	fmt.Fprintf(w, "rta: role %s would issue %s to %s:\n", i.role.Name,
		format.Plural(len(prepared), "one grant", fmt.Sprintf("%d grants", len(prepared))), i.agent)
	for n, g := range prepared {
		line := "  " + issueLine(g)
		if replaces[n] != "" {
			line += " — replaces " + replaces[n]
		}
		fmt.Fprintln(w, line)
	}
}

// equivalentOf is the standing grant a new one would replace: Issue's own
// key, target, record, connection and agent.
func equivalentOf(standing []core.Grant, g core.Grant) *core.Grant {
	now := time.Now()
	for i := range standing {
		s := standing[i]
		if s.Target == g.Target && s.Scope == g.Scope && s.Profile == g.Profile && s.Agent == g.Agent && s.Active(now) {
			return &s
		}
	}
	return nil
}

// replacedWords says what a replaced grant was, in the words the operator
// needs to decide whether the replacement is the one they meant: a grant
// issued by hand with time left is the one a role line silently widened.
func replacedWords(old core.Grant) string {
	left := format.Duration(time.Until(old.Expires))
	if old.Role != "" {
		return "role " + old.Role + ", issued " + format.Ago(old.Issued) + ", " + left + " left"
	}
	return "by hand, " + left + " left"
}

func noteLines(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return "\n" + strings.Join(notes, "\n")
}

func issueLine(g core.Grant) string {
	s := g.Target
	if g.Scope != "" {
		s += " " + textclean.Record(g.Scope)
	}
	if g.Profile != "" {
		s += " --profile " + g.Profile
	}
	return s + " for " + format.Duration(g.Expires.Sub(g.Issued))
}

func planTable(prepared []core.Grant, replaces []string) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Capability"}, {Name: "Record"}, {Name: "Profile"},
		{Name: "Expires In", Kind: view.KindDuration}, {Name: "Budget"}, {Name: "Replaces"},
	}}
	for i, g := range prepared {
		t.Rows = append(t.Rows, []string{
			g.Target, core.ShownRecord(g.Scope, "—"), dash(g.Profile), format.Duration(g.Expires.Sub(g.Issued)),
			budgetLeft(g, g.Issued), dash(replaces[i]),
		})
	}
	t.Total = len(t.Rows)
	return t
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func runRoles(_ context.Context, req plugin.Request) (view.View, error) {
	all, verr := role.Available()
	if verr != nil {
		return nil, verr
	}
	if want := strings.TrimSpace(req.String("role")); want != "" {
		var hits []role.Source
		for _, s := range all {
			if s.Name == want {
				hits = append(hits, s)
			}
		}
		if len(hits) == 0 {
			return nil, role.Unknown(want)
		}
		all = hits
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Role"}, {Name: "From"}, {Name: "Agent"}, {Name: "TTL"}, {Name: "Grants"},
	}}
	for _, s := range all {
		t.Rows = append(t.Rows, []string{s.Name, sourceWord(s), dash(s.Role.Agent), windowWords(s), strings.Join(s.Role.Grants, "\n")})
	}
	t.Total = len(t.Rows)
	// The table even when no role is defined, and the sentence beside it for
	// a screen: see view.Table.Empty.
	if len(t.Rows) == 0 {
		t.Empty = "no role is defined — a `roles:` block in your config, your policy file, or the team's " +
			".rta-policy.yaml names one:\n\n" +
			"roles:\n  dev:\n    ttl: 8h\n    grants:\n      - kv.get db-password\n      - pg.query --profile staging"
	}
	return t, nil
}

// roleArg is a role given to a grant call Surface.Call spells, by its place.
func roleArg(name string) plugin.Arg { return plugin.Arg{Name: "role", Value: name, Positional: true} }

// takeBack is the call that revokes every grant a role issued to agent, as
// sf, the surface asking, makes it.
func takeBack(sf plugin.Surface, role, agent string) string {
	return sf.Call("grant.revoke", plugin.Arg{Name: "role", Value: role}, plugin.Arg{Name: "agent", Value: agent})
}
