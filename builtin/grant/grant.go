// Package grant is the human half of agent consent: the commands a person
// runs to allow, review and withdraw what an AI agent may do.
//
// The mechanism lives in internal/grant and is enforced in the MCP bridge,
// once, for every plugin. This package is only its face: three capabilities
// that read as a sentence — allow, list, revoke.
package grant

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Plugin returns the grant plugin declaration.
//
// It takes the catalogue it governs, because this is the one plugin that is
// about the others: what may be granted is whatever the registry holds that
// needs a grant, and a hand-maintained list would be wrong the day a plugin
// is added. The accessor is lazy — the registry is still being filled when
// this is called.
func Plugin(catalog func() []plugin.Capability) plugin.Plugin {
	suggestGatedTargets := func(context.Context, plugin.Request) []string {
		var out []string
		seen := map[string]bool{}
		for _, c := range catalog() {
			if !core.Required(c) {
				continue
			}
			out = append(out, c.ID+"\t"+c.Summary)
			if ns := core.Namespace(c.ID); !seen[ns] {
				seen[ns] = true
				out = append(out, ns+"\tevery gated capability in "+ns)
			}
		}
		sort.Strings(out)
		return out
	}
	// The record a grant narrows to is whatever the target itself completes:
	// `rta grant allow kv.get <tab>` offers your key names because kv.get
	// says its scope is a key and how to complete one. Nothing here knows
	// what a key is.
	suggestTargetScope := func(ctx context.Context, req plugin.Request) []string {
		target := core.Normalize(req.String("target"))
		for _, c := range catalog() {
			if c.ID != target || c.Scope == "" {
				continue
			}
			for _, f := range c.Inputs {
				if f.Name == c.Scope {
					return f.Candidates(ctx, req)
				}
			}
		}
		return nil
	}
	return plugin.Plugin{
		Name:    "grant",
		Summary: "Time-boxed permissions for AI agents",
		Capabilities: []plugin.Capability{
			{
				ID: "grant.allow", Summary: "Allow AI agents to use one capability, temporarily",
				Safety: plugin.Write, Idempotent: true,
				Description: "Grants expire (15m by default, 24h maximum) and can only be issued by " +
					"a person at a terminal — an agent that could grant itself access would be no " +
					"gate at all. The target is a capability ID (kv.get) or a plugin name (kv), " +
					"which covers all of it. A second argument narrows the grant to one record: " +
					"`rta grant allow kv.get db-password` allows that key and no other. " +
					"--max-uses expires the grant after that many successful calls, on top of " +
					"--ttl, whichever comes first — `--max-uses 1` for a value that should be " +
					"read exactly once.",
				Inputs: []plugin.Field{
					{Name: "target", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestGatedTargets,
						Help:    "capability to allow, e.g. kv.get — or a plugin name for all of it"},
					{Name: "scope", Type: plugin.String, Positional: true, Suggest: suggestTargetScope,
						Help: "narrow it to one record: a key, a task id, a hostname"},
					{Name: "ttl", Type: plugin.String, Default: "15m", Help: "how long it lasts: 30s, 15m, 2h"},
					{Name: "max-uses", Type: plugin.Int, Help: "expire after this many successful calls (0 = unlimited)"},
					{Name: "note", Type: plugin.String, Help: "why — shown by grant list"},
				},
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return runAllow(ctx, req, catalog)
				},
			},
			{
				ID: "grant.list", Summary: "List what AI agents are currently allowed to do",
				Safety: plugin.Read, Idempotent: true,
				Detailed: true,
				Description: "Readable without unlocking anything, so the question stays answerable " +
					"in a hurry. Expired grants are dropped on read. With --detail: what is currently " +
					"allowed, then everything an agent can reach with no grant at all, and everything " +
					"that would need one — because \"what did I allow\" is only half of \"what can it do\".",
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return runList(ctx, req, catalog)
				},
			},
			{
				ID: "grant.revoke", Summary: "Take an agent's access back", Safety: plugin.Write, Idempotent: true,
				Description: "Can only be run by a person at a terminal, the same as grant.allow and " +
					"for the same reason: consent state belongs to whoever is deciding it, not to " +
					"whoever is currently being granted or denied.",
				Inputs: []plugin.Field{
					{Name: "target", Type: plugin.String, Positional: true, Suggest: suggestHeldTargets,
						Help: "capability or plugin to revoke"},
					{Name: "scope", Type: plugin.String, Positional: true, Suggest: suggestHeldScopes,
						Help: "only the grant for this record"},
					{Name: "all", Type: plugin.Bool, Help: "revoke every grant"},
				},
				Run: runRevoke,
			},
		},
	}
}

// suggestHeldTargets completes from the grants that exist: revoking is
// something you do to a grant you already have.
func suggestHeldTargets(context.Context, plugin.Request) []string {
	grants, verr := core.Load()
	if verr != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range grants {
		if seen[g.Target] {
			continue
		}
		seen[g.Target] = true
		out = append(out, g.Target)
	}
	sort.Strings(out)
	return out
}

// suggestHeldScopes narrows to the records actually granted on that target.
func suggestHeldScopes(_ context.Context, req plugin.Request) []string {
	grants, verr := core.Load()
	if verr != nil {
		return nil
	}
	target := core.Normalize(req.String("target"))
	var out []string
	for _, g := range grants {
		if g.Scope == "" || (target != "" && g.Target != target) {
			continue
		}
		out = append(out, g.Scope)
	}
	sort.Strings(out)
	return out
}

func runAllow(_ context.Context, req plugin.Request, catalog func() []plugin.Capability) (view.View, error) {
	// An agent granting itself access would be no gate at all.
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("grant.human", "grants can only be issued by a person").
			WithHint("ask the operator to run: rta grant allow <capability> --ttl 15m")
	}
	target := core.Normalize(req.String("target"))
	if target == "" {
		return nil, view.Errorf("grant.notarget", "name what to allow").
			WithHint("rta grant allow kv.get db-password --ttl 15m")
	}
	// A grant that authorizes nothing is worse than an error: `grant list`
	// shows it looking exactly like a working one, so a typo — kv.gett for
	// kv.get — reads back as "done" right up until the agent tries the call
	// it was supposedly just allowed to make, and is refused anyway.
	if !targetExists(catalog, target) {
		return nil, view.Errorf("grant.unknowntarget", "%q does not name a registered capability or plugin", target).
			WithHint("rta explain lists capability IDs, rta plugins lists plugin names — check for a typo")
	}
	ttl, asked, verr := parseTTL(req.String("ttl"), target)
	if verr != nil {
		return nil, verr
	}
	maxUses := req.Int("max-uses")
	if maxUses < 0 {
		return nil, view.Errorf("grant.badmaxuses", "--max-uses cannot be negative").
			WithHint("0 means unlimited within the TTL, which is also the default")
	}

	now := time.Now()
	g := core.Grant{
		Target:  target,
		Scope:   strings.TrimSpace(req.String("scope")),
		Issued:  now,
		Expires: now.Add(ttl),
		Note:    req.String("note"),
		MaxUses: maxUses,
	}
	// Reading the file and replacing it used to be two unlocked steps here,
	// which is how a revoke issued in between got written back out.
	if verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
		// One grant per target+scope: re-allowing extends the deadline rather
		// than stacking two grants whose earlier expiry means nothing.
		kept := stored[:0]
		for _, existing := range stored {
			if existing.Target != g.Target || existing.Scope != g.Scope {
				kept = append(kept, existing)
			}
		}
		// A preview declines the write from inside the lock rather than
		// returning before it, so --dry-run still reads the file and still
		// reports an unreadable one instead of promising a grant that would
		// have failed.
		return append(kept, g), !req.DryRun
	}); verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would allow agents to %s for %s%s", describe(g), ttl, usesSuffix(maxUses))}, nil
	}
	msg := fmt.Sprintf("agents may %s for %s (until %s)%s", describe(g), ttl, g.Expires.Format("15:04:05"), usesSuffix(maxUses))
	if asked > core.MaxTTL {
		msg += fmt.Sprintf("\ncapped at the %s maximum (you asked for %s)", core.MaxTTL, asked)
	}
	return view.Text{Body: msg}, nil
}

// usesSuffix renders the use-count half of a grant's lifetime, when there is
// one to mention — most grants have no limit, and saying nothing beats
// saying "unlimited" on every single one.
func usesSuffix(maxUses int) string {
	if maxUses <= 0 {
		return ""
	}
	if maxUses == 1 {
		return ", or once, whichever comes first"
	}
	return fmt.Sprintf(", or %d uses, whichever comes first", maxUses)
}

// targetExists reports whether target names a real capability ID or a
// plugin namespace with at least one capability registered under it — the
// two forms grant.allow's own target field accepts.
func targetExists(catalog func() []plugin.Capability, target string) bool {
	for _, c := range catalog() {
		if c.ID == target || core.Namespace(c.ID) == target {
			return true
		}
	}
	return false
}

// parseTTL reads the requested lifetime and caps it.
func parseTTL(raw, target string) (ttl, asked time.Duration, verr *view.Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.DefaultTTL, core.DefaultTTL, nil
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, 0, view.Errorf("grant.badttl", "%q is not a duration: %v", raw, err).
			WithHint("use a Go duration: 30s, 15m, 2h")
	}
	if parsed <= 0 {
		return 0, 0, view.Errorf("grant.badttl", "a grant must last longer than zero").
			WithHint("to take access away, use: rta grant revoke " + target)
	}
	return min(parsed, core.MaxTTL), parsed, nil
}

// describe says what a grant allows, in the words the person used.
func describe(g core.Grant) string {
	if g.Scope == "" {
		return "call " + g.Target
	}
	return fmt.Sprintf("call %s on %s", g.Target, g.Scope)
}

func runList(ctx context.Context, req plugin.Request, catalog func() []plugin.Capability) (view.View, error) {
	held, verr := heldTable()
	if verr != nil {
		return nil, verr
	}
	if !req.Bool("detail") {
		return held, nil
	}
	// "What did I allow" is only half of "what can an agent do here". The
	// other half is what needs no allowing, and the page is only useful if
	// both are derived from the catalogue rather than written down again —
	// a list maintained by hand goes stale exactly when a capability is
	// added, which is the moment somebody most wants to read it.
	p := plugin.NewPage(ctx, req)
	p.Put("granted", held)
	for _, tier := range reachTiers {
		p.Put(tier.title, reachTable(catalog(), tier.holds))
	}
	return p.View(), nil
}

// reachTiers name the three ways an agent's access is decided, in widening
// order of what it takes to get there. They are distinct gates, not degrees
// of one: --allow-write is an operator's decision made once when the server
// is launched, a grant is a person's decision made per record and per
// quarter-hour. Folding them into one "not granted" bucket would read as if
// a write were as freely reachable as a read.
var reachTiers = []struct {
	title string
	holds func(plugin.Capability) bool
}{
	{"reachable by default", func(c plugin.Capability) bool {
		return !core.Required(c) && c.Safety == plugin.Read
	}},
	{"needs --allow-write on the server", func(c plugin.Capability) bool {
		return !core.Required(c) && c.Safety != plugin.Read
	}},
	{"needs a grant a person issues", core.Required},
}

// reachTable lists the capabilities in one tier.
func reachTable(caps []plugin.Capability, holds func(plugin.Capability) bool) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Capability"},
		{Name: "Safety", Kind: view.KindStatus},
		{Name: "Per-record"},
		{Name: "Summary"},
	}}
	for _, c := range caps {
		if !holds(c) {
			continue
		}
		record := "no"
		if c.Scope != "" {
			record = c.Scope
		}
		t.Rows = append(t.Rows, []string{c.ID, string(c.Safety), record, c.Summary})
	}
	sort.Slice(t.Rows, func(i, j int) bool { return t.Rows[i][0] < t.Rows[j][0] })
	t.Total = len(t.Rows)
	return t
}

func heldTable() (view.View, *view.Error) {
	grants, verr := core.Load()
	if verr != nil {
		return nil, verr
	}
	if len(grants) == 0 {
		return view.Text{Body: "No active grants — AI agents can only read.\n" +
			"Allow one with: rta grant allow <capability> --ttl 15m"}, nil
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Capability"},
		{Name: "Record"},
		{Name: "Expires In", Kind: view.KindDuration},
		{Name: "Uses Left"},
		{Name: "Note"},
	}}
	now := time.Now()
	for _, g := range grants {
		record := g.Scope
		if record == "" {
			record = "any"
		}
		usesLeft := "unlimited"
		if g.MaxUses > 0 {
			usesLeft = fmt.Sprintf("%d", g.MaxUses-g.Uses)
		}
		t.Rows = append(t.Rows, []string{
			g.Target,
			record,
			g.Expires.Sub(now).Round(time.Second).String(),
			usesLeft,
			g.Note,
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func runRevoke(_ context.Context, req plugin.Request) (view.View, error) {
	// The same reasoning as runAllow's, the other direction: an agent that
	// can freely erase grants can silently take back its own restriction, or
	// somebody else's mid-task — and the operator's `grant list` is supposed
	// to be a reliable record of what they decided, not something an agent
	// can rewrite. Consent state belongs to the person at the terminal in
	// both directions, not to whoever is currently being granted or denied.
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("grant.human", "grants can only be revoked by a person").
			WithHint("ask the operator to run: rta grant revoke <capability>")
	}
	all := req.Bool("all")
	target := core.Normalize(req.String("target"))
	scope := strings.TrimSpace(req.String("scope"))
	if !all && target == "" {
		return nil, view.Errorf("grant.notarget", "name a capability, or pass --all").
			WithHint("run `rta grant list` to see what is currently allowed")
	}
	// The count, the surviving-coverage warning and the rows that get written
	// back are three answers to one question, so they are all decided from the
	// snapshot the lock is held over. Deriving them from an unlocked read let
	// an operator be told "revoked 1 grant(s)" while a Reserve running at that
	// instant put the grant back.
	var body string
	if verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
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
			match := all || g.Target == target || core.Namespace(g.Target) == target
			if match && scope != "" && g.Scope != scope {
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
			body = "Nothing to revoke — no grant is active."
			return nil, false
		}
		// A row naming exactly this target can be gone while a wider grant still
		// authorizes every call it would ever make — "kv" covers "kv.get" the
		// same way in Check as it does here. Reporting only whether a matching
		// row existed, without asking whether the target is still reachable
		// through something wider, is how a namespace grant on kv survived
		// `rta grant revoke kv.get` while the operator was told there was
		// nothing to revoke — true of the row, false of the access.
		still := core.Covering(live, target, scope)
		stillCovered := func(line string) string {
			if still == nil {
				return line
			}
			record := still.Scope
			if record == "" {
				record = "any"
			}
			return line + fmt.Sprintf("\nstill covered by an active grant on %s (record: %s) — revoke that too: rta grant revoke %s",
				still.Target, record, still.Target)
		}

		if revoked == 0 {
			msg := fmt.Sprintf("No active grant for %s.", target)
			if still != nil {
				// "No active grant" would be a flat lie here: nothing named this
				// target exactly, but something else still authorizes it.
				msg = fmt.Sprintf("No grant named exactly %s to remove.", target)
			}
			body = stillCovered(msg)
			return nil, false
		}
		if req.DryRun {
			body = stillCovered(fmt.Sprintf("would revoke %d grant(s)", revoked))
			return nil, false
		}
		body = stillCovered(fmt.Sprintf("revoked %d grant(s)", revoked))
		return kept, true
	}); verr != nil {
		return nil, verr
	}
	return view.Text{Body: body}, nil
}
