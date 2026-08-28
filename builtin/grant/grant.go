// Package grant is the human half of agent consent: the commands a person
// runs to allow, review and withdraw what an AI agent may do.
//
// The mechanism lives in internal/grant and is enforced in the MCP bridge,
// once, for every plugin. This package is only its face: four capabilities
// that read as a sentence — allow, list, renew, revoke.
package grant

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	profiles "github.com/this-is-tobi/rule-them-all/internal/profile"
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
			if !core.Required(c, "") {
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
					{Name: "profile", Type: plugin.String, Suggest: suggestConfiguredProfiles,
						Help: "narrow it to one configured connection"},
					{Name: "ttl", Type: plugin.String, Default: "15m", Suggest: suggestTTL,
						Help: "how long it lasts: 30s, 15m, 2h"},
					{Name: "max-uses", Type: plugin.Int, Help: "expire after this many successful calls (0 = unlimited)"},
					{Name: "note", Type: plugin.String, Help: "why — shown by grant list"},
				},
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return runAllow(ctx, req, catalog)
				},
			},
			{
				ID: "grant.renew", Summary: "Push out the deadline on grants you already have",
				Safety: plugin.Write, Idempotent: true,
				Description: "Renew extends time and nothing else. Scope, profile, use limit, uses " +
					"already spent and note are all carried forward from the stored grant — so a " +
					"renewal can never turn a one-time grant into an unlimited one, which is what " +
					"re-running `grant allow` without retyping --max-uses quietly did. The moment " +
					"of first consent is not moved either, so a chain of renewals is still capped " +
					"at 24h from when a person first said yes. With no arguments it renews every " +
					"active grant, which is the common case: the work is still going, the clock is " +
					"not.",
				Inputs: []plugin.Field{
					{Name: "target", Type: plugin.String, Positional: true, Suggest: suggestHeldTargets,
						Help: "only grants on this capability or plugin"},
					{Name: "scope", Type: plugin.String, Positional: true, Suggest: suggestHeldScopes,
						Help: "only the grant for this record"},
					{Name: "profile", Type: plugin.String, Suggest: suggestHeldProfiles,
						Help: "only grants on this connection"},
					{Name: "ttl", Type: plugin.String, Suggest: suggestTTL,
						Help: "how much longer — defaults to the window the grant was issued with"},
				},
				Run: runRenew,
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
					{Name: "profile", Type: plugin.String, Suggest: suggestHeldProfiles,
						Help: "only the grant for this connection"},
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
//
// Filtered by profile as well as by target: a scope offered here is one the
// operator could act on, and a record granted on a connection this command is
// not about is not one of them.
func suggestHeldScopes(_ context.Context, req plugin.Request) []string {
	grants, verr := core.Load()
	if verr != nil {
		return nil
	}
	target := core.Normalize(req.String("target"))
	profile := strings.TrimSpace(req.String("profile"))
	var out []string
	for _, g := range grants {
		if g.Scope == "" || (target != "" && g.Target != target) {
			continue
		}
		if profile != "" && g.Profile != profile {
			continue
		}
		out = append(out, g.Scope)
	}
	sort.Strings(out)
	return out
}

// suggestHeldProfiles completes from the profiles that grants actually name —
// the set revoke and renew can act on.
func suggestHeldProfiles(context.Context, plugin.Request) []string {
	grants, verr := core.Load()
	if verr != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range grants {
		if g.Profile == "" || seen[g.Profile] {
			continue
		}
		seen[g.Profile] = true
		out = append(out, g.Profile)
	}
	sort.Strings(out)
	return out
}

// suggestConfiguredProfiles completes from the operator's own config, which is
// the set `grant allow` can issue against — unlike revoke and renew, which act
// on grants that already exist.
func suggestConfiguredProfiles(_ context.Context, req plugin.Request) []string {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	target := core.Normalize(req.String("target"))
	if ns := core.Namespace(target); target != "" && ns != "" {
		if named := cfg.ProfilesFor(ns); len(named) > 0 {
			return named
		}
	}
	return cfg.ProfileNames()
}

// checkProfile confirms a profile named on the command line exists and belongs
// to the plugin the target names.
//
// A grant that authorizes nothing is worse than an error — the same reasoning
// targetExists already applies to a mistyped capability. Here it is sharper: a
// grant naming a profile that does not exist looks identical in `grant list`
// to one that works, while every call it was meant to authorize is refused,
// and the refusal an agent reports says "needs a person's consent" for
// something the person believes they already consented to.
//
// This runs for a person at a terminal, so it names the profiles that would
// have worked. The MCP surface deliberately says nothing of the sort.
// It also returns the pin: the fingerprint of the connection being consented
// to, which is what the grant is bound to rather than the name. Taken here
// because this function has already loaded the config and already holds the
// profile, and because the pin has to describe the connection the operator is
// looking at when they decide.
func checkProfile(target, name string) (string, string, *view.Error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", "", view.AsError(err, "grant.config")
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return "", "", view.Errorf("grant.unknownprofile", "no profile named %q", name).
			WithHint(profileHint(cfg, core.Namespace(target)))
	}
	if !p.Trusted() {
		return "", "", view.Errorf("grant.untrustedprofile",
			"profile %q comes from a working-directory config file, so nothing honours it", name).
			WithHint("set $RTA_CONFIG to name that file deliberately, or move the profile to " +
				config.Path())
	}
	// A profile spans plugins now, so the question is whether this one says
	// anything about the target's — not whether the whole profile is that
	// plugin. Granting pg.query against an environment that has no pg entry
	// would issue a permission that can never be exercised.
	ns := core.Namespace(target)
	if ns != "" && !p.Covers(ns) {
		return "", "", view.Errorf("grant.profilemismatch",
			"profile %q says nothing about %s", name, ns).
			WithHint(profileCovers(p, name))
	}
	key, conn, ok := p.For(ns)
	if !ok {
		// Covers() just said otherwise, so this is a target with no namespace
		// at all — nothing a grant can name today, and a pin over the whole
		// profile would be the wrong answer rather than a safe one.
		return "", "", view.Errorf("grant.profilescope",
			"%q does not name a plugin, so there is no connection to grant against", target)
	}
	return name, profiles.ConnStamp(key, conn), nil
}

func profileCovers(p config.Profile, name string) string {
	if ns := p.Namespaces(); len(ns) > 0 {
		return name + " covers " + strings.Join(ns, ", ")
	}
	return name + " covers nothing — it has no `plugins:` block"
}

func profileHint(cfg config.Config, ns string) string {
	if named := cfg.ProfilesFor(ns); ns != "" && len(named) > 0 {
		return "configured for " + ns + ": " + strings.Join(named, ", ")
	}
	if all := cfg.ProfileNames(); len(all) > 0 {
		return "configured: " + strings.Join(all, ", ")
	}
	return "no profiles are configured — see `rta profile list`"
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
			WithHint("rta explain lists capability IDs, rta plugin list lists plugin names — check for a typo")
	}
	profile, pin, verr := checkProfile(target, req.String("profile"))
	if verr != nil {
		return nil, verr
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
		Target: target,
		Scope:  strings.TrimSpace(req.String("scope")),
		// The name and the connection behind it. The pin is what makes this a
		// grant against a place rather than against a label: edit the
		// environment afterwards and this stops covering calls on it, instead
		// of quietly following the name to wherever it now points.
		Profile:    profile,
		ProfilePin: pin,
		Issued:     now,
		Expires:    now.Add(ttl),
		Note:       req.String("note"),
		TTL:        strings.TrimSpace(req.String("ttl")),
		MaxUses:    maxUses,
	}
	// Reading the file and replacing it used to be two unlocked steps here,
	// which is how a revoke issued in between got written back out.
	if verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
		// One grant per target+scope+profile: re-allowing extends the deadline
		// rather than stacking two grants whose earlier expiry means nothing.
		//
		// Profile is part of the key, and leaving it out would have made this
		// destructive: `grant allow pg --profile b` would have deleted the
		// grant for profile a while reporting only that b had been allowed,
		// silently revoking access nobody asked to revoke. The key has to be
		// exactly what covers() distinguishes, or "replace the equivalent
		// grant" replaces one that is not equivalent.
		kept := stored[:0]
		for _, existing := range stored {
			if existing.Target != g.Target || existing.Scope != g.Scope || existing.Profile != g.Profile {
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
	// A grant for an environment that is not the one switched on cannot be
	// exercised until it is: the MCP bound refuses every profile but the active
	// one, in the same words an ungranted call gets. Said here because the
	// operator has just spent a command and would otherwise watch the agent be
	// refused with no way to connect the two — the clock is also running, so a
	// 15-minute grant issued and then noticed is most of a grant wasted.
	if on := profiles.Active(); on != "" && g.Profile != "" && g.Profile != on {
		msg += fmt.Sprintf("\nnote: %s is switched on, so this grant does nothing until "+
			"`rta use %s` — no agent can reach %s while you are working elsewhere",
			on, g.Profile, g.Profile)
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

// suggestTTL offers the windows a grant is usually given, up to the ceiling
// parseTTL enforces.
//
// Suggest rather than Options, because a duration stays free text: somebody
// who wants 90m should be able to type it. What this removes is the pause to
// work out what the spelling is and what the maximum is — on the surface where
// an operator is deciding how long an agent keeps something, which is the
// wrong moment to be guessing.
// Written as the strings somebody types, not as core.DefaultTTL.String(),
// which renders 15 minutes as "15m0s" and a day as "24h0m0s" — accepted by
// ParseDuration and typed by nobody. TestTheOfferedWindowsMatchTheRealBounds
// keeps them honest against the constants.
func suggestTTL(context.Context, plugin.Request) []string {
	return []string{
		"30s\tone call, more or less",
		"5m\ta quick job",
		"15m\tthe default",
		"1h\ta task",
		"4h\tan afternoon",
		"24h\tthe most a grant can last",
	}
}

// describe says what a grant allows, in the words the person used.
func describe(g core.Grant) string {
	s := "call " + g.Target
	if g.Scope != "" {
		s += " on " + g.Scope
	}
	if g.Profile != "" {
		s += " via profile " + g.Profile
	}
	return s
}

// runRenew extends the deadline on grants that already exist, and changes
// nothing else about them.
//
// It exists because "renewing" was re-running `grant allow`, and that is not a
// renewal — it builds a fresh grant from the flags of *this* invocation. A
// person extending a `--max-uses 1` grant without retyping the flag converted
// it to unlimited and reset the uses already spent, so the grant that was
// meant to reveal one secret once could reveal it again, and again. Nothing
// said so; `grant list` showed a healthy row.
//
// Issued is deliberately not moved. Active() already tests
// now.Before(Issued.Add(MaxTTL)) on every read, so leaving it alone caps any
// chain of renewals at 24h from the moment a person first said yes — the
// ceiling survives for free, and consent cannot be made perpetual one quarter
// hour at a time.
func runRenew(_ context.Context, req plugin.Request) (view.View, error) {
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Errorf("grant.human", "grants can only be renewed by a person").
			WithHint("ask the operator to run: rta grant renew")
	}
	target := core.Normalize(req.String("target"))
	scope := strings.TrimSpace(req.String("scope"))
	profile := strings.TrimSpace(req.String("profile"))
	askedTTL := strings.TrimSpace(req.String("ttl"))

	var ttl time.Duration
	if askedTTL != "" {
		var verr *view.Error
		ttl, _, verr = parseTTL(askedTTL, target)
		if verr != nil {
			return nil, verr
		}
	}

	now := time.Now()
	var renewed []string
	var stale []string
	var capped bool
	// Read once, outside the lock, for the staleness note below. A config that
	// cannot be read costs the note and nothing else.
	cfg, cfgErr := config.Load()
	seenStale := map[string]bool{}
	if verr := core.Mutate(func(stored []core.Grant) ([]core.Grant, bool) {
		renewed = nil
		stale = nil
		clear(seenStale)
		capped = false
		for i := range stored {
			g := stored[i]
			// Only what is still standing. A spent or expired grant is a fresh
			// decision, not an extension of one — that is `grant allow`.
			if !g.Active(now) {
				continue
			}
			if target != "" && g.Target != target {
				continue
			}
			if scope != "" && g.Scope != scope {
				continue
			}
			if profile != "" && g.Profile != profile {
				continue
			}
			window := ttl
			if window == 0 {
				window = g.Window()
			}
			expires := now.Add(window)
			// The absolute ceiling, applied here so the message can say it
			// happened. Active() would enforce it regardless; what it cannot do
			// is tell somebody why their grant died earlier than they asked.
			if ceiling := g.Issued.Add(core.MaxTTL); expires.After(ceiling) {
				expires, capped = ceiling, true
			}
			// The deadline, and nothing else. **ProfilePin is deliberately not
			// touched**, and its absence here is load-bearing: a renewal that
			// adopted the current connection would let an operator extend a
			// grant straight onto an environment somebody repointed in the
			// meantime, without ever seeing the connection they were agreeing
			// to. Re-consenting to a changed connection is a fresh decision,
			// which is `rta grant allow`.
			stored[i].Expires = expires
			if askedTTL != "" {
				stored[i].TTL = askedTTL
			}
			renewed = append(renewed, fmt.Sprintf("  %-24s until %s", describe(g), expires.Format("15:04:05")))
			// A renewal is the only person-facing surface that reports
			// per-grant success, and it was the only one silent about a grant
			// whose connection has been repointed: `grant list` marks it
			// (changed) and `doctor` warns, while this printed a fresh
			// deadline on a grant that authorizes nothing. The operator walks
			// away believing they re-confirmed consent and learns otherwise
			// from an agent's refusal — which is checkProfile's own named
			// failure, one command over.
			if cfgErr == nil && !seenStale[g.Profile] &&
				g.Stale(profiles.ConnStampFor(cfg, g.Profile, core.Namespace(g.Target))) {
				seenStale[g.Profile] = true
				stale = append(stale, g.Profile)
			}
		}
		return stored, len(renewed) > 0 && !req.DryRun
	}); verr != nil {
		return nil, verr
	}
	if len(renewed) == 0 {
		return view.Text{Body: "Nothing to renew — no matching grant is active."}, nil
	}
	verb := "renewed"
	if req.DryRun {
		verb = "would renew"
	}
	body := fmt.Sprintf("%s %d grant(s):\n%s", verb, len(renewed), strings.Join(renewed, "\n"))
	if len(stale) > 0 {
		body += fmt.Sprintf("\nnote: %d of these name a connection that has changed since it was "+
			"issued (%s), so the deadline moved and they still authorize nothing — "+
			"`rta grant allow` re-consents to the connection as it is now",
			len(stale), strings.Join(stale, ", "))
	}
	if capped {
		body += fmt.Sprintf("\ncapped at the %s maximum from first consent — "+
			"`rta grant allow` starts a new window, which is a fresh decision", core.MaxTTL)
	}
	return view.Text{Body: body}, nil
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
	p.PutAs("granted", "granted", held)
	for _, tier := range reachTiers {
		p.PutAs(tier.id, tier.title, reachTable(catalog(), tier.holds))
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
	// id is what a script or an agent addresses the section by; title is
	// what a person reads. One string doing both jobs made every wording
	// improvement a silent break for whoever had scripted the old one —
	// see view.Section.
	id, title string
	holds     func(plugin.Capability) bool
}{
	{"default", "reachable by default", func(c plugin.Capability) bool {
		return !core.Required(c, "") && c.Safety == plugin.Read
	}},
	{"allow-write", "needs --allow-write on the server", func(c plugin.Capability) bool {
		return !core.Required(c, "") && c.Safety != plugin.Read
	}},
	{"grant", "needs a grant a person issues", func(c plugin.Capability) bool {
		return core.Required(c, "")
	}},
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
		body := "No active grants — AI agents can only read.\n" +
			"Allow one with: rta grant allow <capability> --ttl 15m"
		// An empty list is the ordinary answer and a dropped file is not, so
		// the difference has to be visible here: this is the one screen where
		// somebody looking for a grant they issued will come looking for it.
		if core.Legacy() {
			body = "No active grants — AI agents can only read.\n\n" +
				"Grants are now sealed against tampering, and " + core.Path() + " predates\n" +
				"the seal, so nothing in it is honoured. Any grant it held is gone; re-issue\n" +
				"what you still need. Removing the file clears this notice:\n" +
				"  rm " + core.Path() + "\n\n" +
				"Allow one with: rta grant allow <capability> --ttl 15m"
		}
		return view.Text{Body: body}, nil
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Capability"},
		// Which connection this grant is about. Without it the operator cannot
		// see what they consented to: two grants on the same capability, one for
		// staging and one for production, render as identical rows — and the
		// screen whose entire job is "what is the agent allowed to do right
		// now?" answers a question narrower than the one it was asked.
		{Name: "Profile"},
		{Name: "Record"},
		{Name: "Expires In", Kind: view.KindDuration},
		{Name: "Uses Left"},
		{Name: "Note"},
	}}
	now := time.Now()
	cfg, cfgErr := config.Load()
	for _, g := range grants {
		record := g.Scope
		if record == "" {
			record = "any"
		}
		// An em dash rather than the word "any", deliberately: the Record column
		// one place over already uses "any" for the opposite meaning, and an
		// empty profile is not a wildcard — it is the base connection and
		// nothing else.
		connection := g.Profile
		if connection == "" {
			connection = "—"
		}
		// A grant whose connection has been repointed since it was issued is
		// still a row, and it is the row somebody most needs to see: it looks
		// live, it is listed, and every call it was issued for is refused.
		//
		// **This is where the remedy belongs.** The MCP refusal is deliberately
		// the same sentence an ungranted call gets — telling an agent "this
		// profile changed since you were granted" would disclose both that the
		// profile exists and that consent was once given for it — so the person
		// has to be able to find out here instead.
		if cfgErr == nil && g.Stale(profiles.ConnStampFor(cfg, g.Profile, core.Namespace(g.Target))) {
			connection += " (changed)"
		}
		usesLeft := "unlimited"
		if g.MaxUses > 0 {
			usesLeft = fmt.Sprintf("%d", g.MaxUses-g.Uses)
		}
		t.Rows = append(t.Rows, []string{
			g.Target,
			connection,
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
	profile := strings.TrimSpace(req.String("profile"))
	if !all && target == "" && profile == "" {
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
			match := all || target == "" || g.Target == target || core.Namespace(g.Target) == target
			if match && scope != "" && g.Scope != scope {
				match = false
			}
			// A revoke that names a profile takes back that connection and no
			// other. Without this, `rta grant revoke pg --profile staging`
			// would have removed the production grant too — the widest possible
			// reading of the narrowest possible request.
			if match && !all && profile != "" && g.Profile != profile {
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
		still := core.Covering(live, target, scope, profile)
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
