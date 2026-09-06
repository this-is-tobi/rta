package grant

import (
	"context"
	"fmt"
	"os"
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
			plural(len(s.Role.Grants), "grant", "grants"), sourceWord(s)))
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
func RolesInForce() string {
	grants, verr := core.Load()
	if verr != nil {
		return ""
	}
	return rolesInForce(grants)
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

func runIssue(req plugin.Request, catalog func() []plugin.Capability, artifact func(string) (string, bool)) (view.View, error) {
	src, verr := role.Find(strings.TrimSpace(req.String("role")))
	if verr != nil {
		return nil, verr
	}
	lines, err := src.Lines()
	if err != nil {
		return nil, view.Errorf("grant.role", "%v", err).
			WithHint("a role line is a target, an optional record, and --profile, --ttl, --max-uses, --rate or --note")
	}
	// The team's list is issued where its lines can be read first: on the
	// command line, printed before the passphrase or acknowledged with --yes.
	// A form that collected a masked passphrase before the run showed
	// nothing, and a checkbox is not a look at somebody else's list.
	if src.Team && req.Surface() != plugin.SurfaceCLI {
		return nil, view.Errorf("grant.role.team", "role %q comes from %s, and a team's role is issued at the command line, where its lines are shown first", src.Name, src.From).
			WithHint("`rta grant roles " + src.Name + "` shows the lines; `rta grant issue " + src.Name + "` issues them")
	}
	// --agent, else the agent the operator's own role names, else the one
	// this machine knows. A default for a command a person still runs
	// under the guard: it picks who receives the list, never what is in it.
	asked := strings.TrimSpace(req.String("agent"))
	if asked == "" && !src.Team {
		asked = strings.TrimSpace(src.Role.Agent)
	}
	agent, verr := resolveAgent(asked)
	if verr != nil {
		return nil, verr
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
		return nil, view.Errorf("grant.role.ttl", "%q is not a duration a role can stand for", ttl).
			WithHint("4h, 12h — the most a grant can last is 24h")
	}
	if verr := guard.RefuseArgv(req); verr != nil {
		return nil, verr
	}
	from := core.Origin(req.Surface(), term.IsTerminal(int(stdio.Real().Fd())))
	var prepared []core.Grant
	var notes []string
	for _, l := range lines {
		lineTTL := ttl
		if l.TTL != "" {
			// The role author's bound on one line and the operator's on all
			// of them: the shorter stands, and a line never widens.
			d, err := time.ParseDuration(l.TTL)
			if err != nil || d <= 0 {
				return nil, view.Errorf("grant.role", "role %q, line %q: --ttl %q is not a duration", src.Name, l.Raw, l.TTL)
			}
			if d < window {
				lineTTL = l.TTL
			}
		}
		g, n, verr := buildGrant(catalog, artifact, operatorid.IssueSpec{
			Target: l.Target, Scope: l.Scope, Profile: l.Profile, Agent: agent,
			TTL: lineTTL, Note: l.Note, MaxUses: l.MaxUses, Rate: l.Rate,
		}, from)
		if verr != nil {
			return nil, view.Errorf(verr.Code, "role %q, line %q: %s", src.Name, l.Raw, verr.Message).WithHint(verr.Hint)
		}
		// The ceiling before the passphrase, for every line: one forbidden
		// target refuses the whole role before anybody types anything.
		if verr := core.CheckCeiling(g.Target, g.Scope, g.Profile); verr != nil {
			return nil, view.Errorf(verr.Code, "role %q, line %q: %s", src.Name, l.Raw, verr.Message).WithHint(verr.Hint)
		}
		g.Role = src.Name
		for _, s := range []string{cappedNote(n), inactiveProfileNote(g)} {
			if s != "" {
				notes = append(notes, l.Raw+": "+s)
			}
		}
		prepared = append(prepared, g)
	}
	// What each line replaces, read before the lock so it can be shown
	// before the passphrase. Advisory for that reason: only what Issue
	// hands back from inside its locked callback is what actually went.
	standing, _ := core.Load()
	replaces := make([]string, len(prepared))
	for i, g := range prepared {
		if old := equivalentOf(standing, g); old != nil {
			replaces[i] = replacedWords(*old)
		}
	}
	plan := planTable(prepared, replaces)
	if req.DryRun {
		return view.Sections{Items: []view.Section{
			{ID: "role", Title: "Role", View: view.Text{Body: fmt.Sprintf(
				"would issue %s to %s: %d %s for %s", src.Name, agent, len(prepared),
				plural(len(prepared), "grant", "grants"), format.Duration(window)) + noteLines(notes)}},
			{ID: "grants", Title: "Grants", View: plan},
		}}, nil
	}
	if guard.Enabled() {
		if req.Surface() == plugin.SurfaceCLI {
			fmt.Fprintf(os.Stderr, "rta: role %s would issue %s to %s:\n", src.Name,
				plural(len(prepared), "one grant", fmt.Sprintf("%d grants", len(prepared))), agent)
			for i, g := range prepared {
				line := "  " + issueLine(g)
				if replaces[i] != "" {
					line += " — replaces " + replaces[i]
				}
				fmt.Fprintln(os.Stderr, line)
			}
		}
		signer, verr := guard.UnlockPrompted(req)
		if verr != nil {
			return nil, verr
		}
		for i := range prepared {
			core.SignWith(signer, &prepared[i])
		}
	} else if src.Team && !req.Yes {
		return nil, view.Errorf("grant.role.unread", "role %q comes from %s, and no passphrase stands between its %d %s and the grant file",
			src.Name, src.From, len(prepared), plural(len(prepared), "line", "lines")).
			WithHint("`rta grant roles " + src.Name + "` shows the lines; run again with --yes once read, " +
				"or `rta grant guard on` to be asked every time")
	}
	issued, replaced := 0, 0
	sameRole := true
	for _, g := range prepared {
		old, verr := core.IssueReplacing(g, true)
		if verr != nil {
			if issued > 0 {
				return nil, view.Errorf(verr.Code, "%d of %d grants issued, then %q failed: %s",
					issued, len(prepared), g.Target, verr.Message).
					WithHint("`rta grant revoke --role " + src.Name + " --agent " + agent + "` takes the issued ones back")
			}
			return nil, verr
		}
		issued++
		if old != nil {
			replaced++
			if old.Role != src.Name || old.Agent != agent {
				sameRole = false
			}
		}
	}
	var until time.Time
	for _, g := range prepared {
		if g.Expires.After(until) {
			until = g.Expires
		}
	}
	pairs := []view.Pair{
		{Key: "role", Value: src.Name + " — " + sourceWord(src)},
		{Key: "agent", Value: agent},
		{Key: "grants", Value: strconv.Itoa(issued)},
	}
	switch {
	case replaced == issued && sameRole:
		pairs = append(pairs, view.Pair{Key: "replaced", Value: fmt.Sprintf("the %s already standing — %s is refreshed, not doubled", plural(issued, "grant", "grants"), src.Name)})
	case replaced > 0:
		pairs = append(pairs, view.Pair{Key: "replaced", Value: fmt.Sprintf("%d — the grants column says which", replaced)})
	}
	pairs = append(pairs,
		view.Pair{Key: "until", Value: format.Clock(until) + " (" + format.Duration(time.Until(until)) + ")"},
		view.Pair{Key: "take back", Value: "rta grant revoke --role " + src.Name + " --agent " + agent},
	)
	if len(notes) > 0 {
		pairs = append(pairs, view.Pair{Key: "note", Value: strings.Join(notes, "\n")})
	}
	return view.Sections{Items: []view.Section{
		{ID: "role", Title: "Role", View: view.KeyValue{Pairs: pairs}},
		{ID: "grants", Title: "Grants", View: plan},
	}}, nil
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
		s += " " + g.Scope
	}
	if g.Profile != "" {
		s += " --profile " + g.Profile
	}
	return s + " for " + format.Duration(g.Expires.Sub(g.Issued))
}

func planTable(prepared []core.Grant, replaces []string) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "capability"}, {Name: "record"}, {Name: "profile"},
		{Name: "expires in", Kind: view.KindDuration}, {Name: "budget"}, {Name: "replaces"},
	}}
	for i, g := range prepared {
		t.Rows = append(t.Rows, []string{
			g.Target, dash(g.Scope), dash(g.Profile), format.Duration(g.Expires.Sub(g.Issued)),
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
			return nil, view.Errorf("role.unknown", "no role named %q", want).
				WithHint("`rta grant roles` lists them")
		}
		all = hits
	}
	if len(all) == 0 {
		return view.Text{Body: "no role is defined — a `roles:` block in your config, your policy file, or the team's " +
			".rta-policy.yaml names one:\n\n" +
			"roles:\n  dev:\n    ttl: 8h\n    grants:\n      - kv.get db-password\n      - pg.query --profile staging"}, nil
	}
	t := view.Table{Columns: []view.Column{
		{Name: "role"}, {Name: "from"}, {Name: "agent"}, {Name: "ttl"}, {Name: "grants"},
	}}
	for _, s := range all {
		t.Rows = append(t.Rows, []string{s.Name, sourceWord(s), dash(s.Role.Agent), windowWords(s), strings.Join(s.Role.Grants, "\n")})
	}
	t.Total = len(t.Rows)
	return t, nil
}
