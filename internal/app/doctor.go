package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/pluginconf"
	"github.com/this-is-tobi/rule-them-all/internal/plugindist"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// newDoctorCommand implements `rta doctor`: one command that checks the
// environment and reports actionable state (PROJECT.md §5.1). Checks grow
// with the features that need them (config, plugins, keyring...).
func newDoctorCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:               "doctor",
		Short:             "Check rta's environment and report actionable findings",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			renderOpts := cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()}
			return cli.Render(cmd.OutOrStdout(), doctorReport(reg), renderOpts)
		},
	}
}

// loadedPlugins is what LoadPlugins registered, for the doctor rows above.
//
// A package-level value because doctor is built by NewRoot, which is handed a
// registry and not a host — and threading a host through every command
// constructor to reach one report would be a wide change for a narrow need.
// Written once at startup, before any command runs.
var loadedPlugins []*pluginhost.Client

// SetLoadedPlugins records the plugins a host loaded, for `rta doctor`.
func SetLoadedPlugins(cs []*pluginhost.Client) { loadedPlugins = cs }

// untrustedPluginsFound is what discovery refused to launch this run.
//
// A trust gate's failure mode is silence — a plugin installed, present, and
// doing nothing, with no way to tell that from a plugin that is broken. So
// what was refused is carried the same way what was loaded is, and every
// surface that lists plugins says it out loud.
var untrustedPluginsFound []pluginhost.Untrusted

// SetUntrustedPlugins records what a host found and did not run.
func SetUntrustedPlugins(us []pluginhost.Untrusted) { untrustedPluginsFound = us }

// untrustedNames is the same list as bare namespaces, for the surfaces that
// want to name them in a sentence rather than tabulate them.
func untrustedNames() []string {
	out := make([]string, 0, len(untrustedPluginsFound))
	for _, u := range untrustedPluginsFound {
		out = append(out, u.Name)
	}
	return out
}

// pluginConfig is the operator's per-plugin configuration, matched to the
// artifacts actually registered. Package state for the same reason
// loadedPlugins is: it is resolved once, at startup, before the command tree
// runs, and every surface reads the same answer.
//
// A nil resolver hands back nil for every namespace, so the zero state is
// "no plugin has any configuration" — which is both the truth on a machine
// with no config file and the safe answer everywhere else.
var (
	pluginConfig        *pluginconf.Resolver
	pluginConfigProblem []pluginconf.Problem
)

// SetPluginConfig records the resolved configuration and what could not be
// honoured. The problems are not fatal and are not printed here: they go to
// `rta doctor`, because a stale pin is a fact about an installation and
// printing it before every command is noise (the same argument LoadPlugins
// makes about everything else worth knowing).
func SetPluginConfig(r *pluginconf.Resolver, problems []pluginconf.Problem) {
	pluginConfig, pluginConfigProblem = r, problems
}

// themeProblems is what theme.Apply could not honour — an unknown field, a
// malformed hex — recorded the same way pluginConfigProblem is: not fatal,
// not printed before every command, reported by `rta doctor`. The palette
// itself already fell back to the built-in for anything wrong, in
// theme.Apply, before main ever calls SetThemeProblems; this is only the
// paper trail explaining why a color an operator wrote down is not the one
// on screen.
var themeProblems []theme.Problem

// SetThemeProblems records what theme.Apply reported.
func SetThemeProblems(problems []theme.Problem) { themeProblems = problems }

// PluginConfig returns the operator's stated values for the plugin a
// capability belongs to, or nil.
//
// By namespace off the capability ID, which is the plugin that declared it —
// the registry guarantees the prefix (pkg/plugin: "ID must start with plugin
// namespace"). Which section that namespace was allowed to read is
// internal/pluginconf's decision and was made once, against the artifact.
func PluginConfig(c plugin.Capability) map[string]any {
	words := c.Words()
	if len(words) == 0 {
		return nil
	}
	return pluginConfig.For(words[0])
}

// ConfigNotApplied names the operator's section that rta could not honour
// for this capability's plugin, or "".
//
// SetPluginConfig's argument for keeping these out of every command stands: a
// stale pin is a fact about an installation, and printing it before a command
// that worked is noise. It does not cover the case where the command *fails*.
// There the operator sees a message naming the wrong cause — rebuild a
// plugin, and `rta pg table list` reports "rejected the credentials for
// postgres", because the section was skipped and the declared defaults ran.
// The database really did refuse; the reason it was asked that question is
// three commands away in `rta doctor` and nothing connects them.
//
// So it is attached to failures only, where it explains something.
func ConfigNotApplied(c plugin.Capability) string {
	words := c.Words()
	if len(words) == 0 {
		return ""
	}
	for _, p := range pluginConfigProblem {
		// Problem.Section is the heading as written, pin and all, so it is
		// matched on the namespace it names rather than compared whole.
		if ns, _, _ := strings.Cut(p.Section, "@"); ns == words[0] {
			return "rta did not apply your `plugins." + p.Section + "` section (" +
				p.Reason + "), so this ran with " + words[0] + "'s declared defaults" +
				hintTail(p.Hint)
		}
	}
	return ""
}

func hintTail(hint string) string {
	if hint == "" {
		return ""
	}
	return " — " + hint
}

func doctorReport(reg *registry.Registry) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Check"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Detail"},
	}}
	add := func(check, status, detail string) {
		t.Rows = append(t.Rows, []string{check, status, detail})
	}

	// Registry health.
	//
	// External plugins register into the same registry as built-ins — that is
	// the design, and it is why no renderer knows the difference. It does
	// mean this row cannot call the total "built-ins" without lying the
	// moment somebody installs one, so the external share is named when there
	// is one.
	caps := reg.Capabilities()
	// Counted from the registry, which is where provenance lives now: a
	// plugin is external because its registration says so, not because the
	// host happens to still have it in a slice.
	external := 0
	for _, p := range reg.Plugins() {
		if o, ok := reg.Origin(p.Name); ok && o.External() {
			external++
		}
	}
	detail := fmt.Sprintf("%d plugins, %d capabilities", len(reg.Plugins()), len(caps))
	if external > 0 {
		detail += fmt.Sprintf(" (%d built in, %d from $PATH)", len(reg.Plugins())-external, external)
	}
	add("capabilities", "ok", detail)

	// Terminal.
	if isTTY() {
		add("terminal", "ok", "stdout is a TTY — styled output enabled")
	} else {
		add("terminal", "info", "stdout is piped — plain output")
	}

	// Config: zero-config is healthy; a broken file is an actionable error.
	cfgPath := config.Path()
	if _, statErr := os.Stat(cfgPath); statErr != nil {
		add("config", "info", "no config file — zero-config mode (`rta init` creates "+cfgPath+")")
	} else if cfg, err := config.Load(); err != nil {
		add("config", "error", err.Error())
	} else {
		detail := cfgPath
		if cfg.Output != "" {
			detail += fmt.Sprintf(" (output=%s)", cfg.Output)
		}
		switch n := len(cfg.Dashboard.Tiles); {
		case n > 0:
			detail += fmt.Sprintf(", %d dashboard tiles", n)
		default:
			detail += ", automatic dashboard (one tile per plugin)"
			if h := len(cfg.Dashboard.Hidden); h > 0 {
				detail += fmt.Sprintf(", %d hidden", h)
			}
		}
		add("config", "ok", detail)
	}

	// Per-plugin configuration, and specifically the ways it can silently
	// stop applying.
	//
	// A section is matched to the artifact it names, so upgrading a plugin
	// changes its digest and the operator's stated values quietly stop
	// reaching it — the capability still runs, with its declared defaults,
	// and nothing anywhere says why the host it used to connect to is gone.
	// That is the failure this row exists for, and it is why the message
	// hands over the pin to paste rather than making somebody look it up: a
	// control that costs a lookup is a control that gets turned off, which is
	// the argument Options.AllowFlag already makes for --allow-destructive.
	if cfg, err := config.LoadFile(); err == nil && len(cfg.Plugins) > 0 {
		resolver, problems := pluginconf.Resolve(cfg, reg.Origin)
		problems = append(problems, resolver.Check(reg)...)
		switch {
		case len(problems) == 0:
			add("plugin config", "ok", fmt.Sprintf("%d configured", len(cfg.Plugins)))
		default:
			for _, p := range problems {
				add("plugin config", "warn", p.String())
			}
		}
	}

	// Profiles: whether each configured connection is usable, and — for the
	// ones that are — whether the credential they need is actually present.
	//
	// The second half is the one people get wrong. A profile carries no secret
	// (ADR 0016 refuses Config on a Secret input), so the only way a
	// credential reaches a connection is $RTA_PROFILE_<NAME>_<INPUT>, and a
	// profile with the destination right and that variable unset fails as an
	// authentication error naming the role — three steps from the cause.
	// Saying it here means an operator finds out before the call rather than
	// from it.
	if cfg, err := config.Load(); err == nil && len(cfg.Profiles) > 0 {
		problems := profile.Check(cfg, reg)
		for _, p := range problems {
			add("profile", "warn", p.String())
		}
		bad := map[string]bool{}
		for _, p := range problems {
			bad[p.Name] = true
		}
		fromStore := 0
		for _, name := range cfg.ProfileNames() {
			if bad[name] {
				continue
			}
			p := cfg.Profiles[name]
			detail := fmt.Sprintf("%s → %s", name, strings.Join(p.PluginKeys(), ", "))
			var named []string
			for _, key := range p.PluginKeys() {
				for _, r := range p.Plugins[key].SecretRefs() {
					named = append(named, config.PluginNamespace(key)+"."+r.Input+
						" from "+r.Scheme+":"+r.Ref)
					if r.Scheme == "kv" {
						fromStore++
					}
				}
			}
			if len(named) > 0 {
				detail += " — " + strings.Join(named, ", ")
			}
			// Unmapped and unexported credentials are reported even when others
			// are mapped: an environment is several plugins now, and "the
			// database has a password" says nothing about the bucket.
			if missing := missingCredentials(name, p, reg); len(missing) > 0 {
				add("profile", "warn", detail+" — no credential: set $"+strings.Join(missing, ", $")+
					", or map one with `secrets:`")
				continue
			}
			add("profile", "ok", detail)
		}
		// Which environment is switched on, and for how long. It is the fact
		// that changes what every other row above means, and it is invisible
		// from the config file — so a report that omitted it would describe
		// somewhere the operator is not.
		if sel := profile.LoadSelection(); sel.Active != "" {
			now := time.Now()
			switch name := sel.Name(now); {
			case name == "":
				add("profile", "info", sel.Active+" has lapsed — commands run against the "+
					"base configuration, and agents may name any profile a grant covers")
			default:
				note := name + " is switched on with no deadline"
				if left, deadline := sel.Left(now); deadline {
					note = fmt.Sprintf("%s is switched on for another %s", name,
						left.Round(time.Minute))
				}
				add("profile", "info", note+" — while it is, `rta mcp serve` refuses "+
					"every other profile, whatever grants exist")
			}
		}
		// The widening, said out loud. A profile that maps a credential out of
		// the store means an agent holding a grant for that profile can cause
		// those entries to be read — it never sees them, and it cannot reach an
		// entry no profile maps, but the set of things a profile grant reaches
		// is larger than the connection alone and the operator should know it
		// from here rather than work it out.
		if unlockable, _ := kv.Unlockable(); fromStore > 0 && unlockable {
			add("profile", "info", fmt.Sprintf(
				"%d profile credential(s) come from the store, which unlocks from this "+
					"environment — an agent granted one of those profiles causes that entry to be "+
					"read (it never receives the value, and reaches no entry a profile does not map)",
				fromStore))
		}
		// R5 in the other direction: once a namespace has profiles, an MCP call
		// naming none is refused, so the base section stops being agent-reachable.
		// Said out loud because it is a change in what an already-running agent
		// can do, and the operator caused it by writing a profile.
	profiled:
		for _, name := range cfg.ProfileNames() {
			for _, ns := range cfg.Profiles[name].Namespaces() {
				// **Applied, not merely written.** RawSection answers whether a
				// heading names this namespace, which is a different question
				// from whether anything reads it: Resolve rejects a section
				// whose pin no longer matches the installed artifact — the
				// ordinary state after upgrading a plugin — and For then
				// applies none of it. Asked the weaker question, doctor stated
				// "`plugins.<ns>` still serves the CLI" about a section it had
				// itself just warned was dead, in the same table.
				if _, _, configured := pluginconf.RawSection(cfg, ns); !configured {
					continue
				}
				if pluginConfig == nil || len(pluginConfig.For(ns)) == 0 {
					continue
				}
				add("profile", "info", fmt.Sprintf(
					"agents must now name a profile for %s — `plugins.%s` still serves the CLI, "+
						"but an MCP call that names no profile is refused", ns, ns))
				break profiled
			}
		}
	}

	// Theme overrides that could not be honoured — an unknown field, a
	// malformed hex. theme.Apply already fell back to the built-in for each
	// one before this row is even reached; what this says is why the color
	// on screen is not the one written down. Nothing configured, or
	// everything applied, earns no row at all — the same quiet-by-default
	// the plugin config row above does not get, because that one also has
	// something to say when it is entirely healthy.
	for _, p := range themeProblems {
		add("theme", "warn", p.String())
	}

	// Which binary a plugin name actually resolved to, when more than one
	// answered to it. Not an error — a local build in front of a packaged one
	// is the ordinary reason, and $PATH order is what a shell would do too —
	// but ordinary and invisible is how "why is it still running the old one"
	// becomes an afternoon. Named here rather than on every command, for the
	// same reason everything else about an installation is.
	for _, f := range pluginhost.Discover() {
		if len(f.Shadowed) == 0 {
			continue
		}
		copies := "copies"
		if len(f.Shadowed) == 1 {
			copies = "copy"
		}
		add("plugin "+f.Name, "info", fmt.Sprintf("using %s; %d further %s on $PATH not used: %s",
			f.Path, len(f.Shadowed), copies, strings.Join(f.Shadowed, ", ")))
	}

	// Standing agent permissions. Worth a line of its own: a grant issued
	// yesterday and forgotten is exactly the thing a health check should
	// surface, and the answer is usually "none".
	if grants, verr := grant.Load(); verr != nil {
		add("agent grants", "error", verr.Message)
	} else if len(grants) == 0 && grant.Legacy() {
		add("agent grants", "info", "none active — "+grant.Path()+
			" predates the tamper seal, so nothing in it is honoured; `rm "+grant.Path()+
			"` clears this, and any grant you still need can be re-issued")
	} else if len(grants) == 0 {
		add("agent grants", "ok", "none active — agents cannot write or destroy anything")
	} else {
		named := make([]string, 0, len(grants))
		var stale []string
		// Loaded here rather than reused from the profile section above, which
		// scopes its own: an unreadable config leaves stale empty, and the row
		// below simply does not appear. Every other reader of the file has
		// already said so by the time this runs.
		grantCfg, grantCfgErr := config.Load()
		for _, g := range grants {
			named = append(named, strings.TrimSpace(g.Target+" "+g.Scope))
			// A grant issued against a connection that has since been
			// repointed. It is listed, it is inside its TTL, and every call it
			// was issued for is refused — which is the shape of thing that
			// costs an afternoon, because the row looks live.
			//
			// **The remedy lives here and not in the refusal.** An agent is
			// told only what an ungranted call is told: "this profile changed
			// since you were granted" would disclose that the profile exists
			// and that consent was once given for it, which is the oracle ADR
			// 0019 §6 exists to close. So a person has to be able to find it,
			// and this is where they look.
			if g.Profile == "" || grantCfgErr != nil {
				continue
			}
			if g.Stale(profile.ConnStampFor(grantCfg, g.Profile, grant.Namespace(g.Target))) {
				stale = append(stale, strings.TrimSpace(g.Target+" on "+g.Profile))
			}
		}
		add("agent grants", "info", fmt.Sprintf("%d active: %s (`rta grant list`)",
			len(grants), strings.Join(named, ", ")))
		if len(stale) > 0 {
			add("agent grants", "warn", fmt.Sprintf(
				"%d name a connection that has changed since it was issued, so they authorize "+
					"nothing: %s — `rta grant allow` re-consents to the connection as it is now "+
					"(`rta grant renew` moves the deadline and deliberately does not)",
				len(stale), strings.Join(stale, ", ")))
		}
	}

	// What an MCP server launched from this shell would inherit. The store is
	// encrypted, but encryption only helps while the key is somewhere the
	// reader is not — and a server started from here starts with this
	// environment.
	if unlockable, from := kv.Unlockable(); from == "no store" {
		add("kv store", "ok", "none yet — `rta kv init --generate` sets one up")
	} else if unlockable {
		add("kv store", "info", "unlocks from this environment ("+from+
			") — an MCP server started here can read secrets, bounded only by grants")
	} else if locked := kv.LockedIdentity(); locked != "" {
		add("kv store", "ok", "locked to "+locked+", which is itself passphrase-protected — "+
			"an MCP server started here would be asked for a passphrase it cannot answer")
	} else {
		add("kv store", "ok", "no key material here — an MCP server started from this shell could not open it")
	}

	// Plugin confinement. ONE row, naming the deny set and its scope.
	//
	// A count with a scope and an explicit "everything else is readable" is a
	// fact somebody can act on; a per-plugin green tick would be the same
	// information dressed as an assurance it does not carry. ADR 0012 §2 is
	// blunt about the bound, and so is this: every attack found in the pre-M2
	// review succeeds identically on a confined macOS host.
	deny, denyErr := pluginhost.Resolve()
	switch {
	case denyErr != nil:
		add("plugin confinement", "error", denyErr.Error())
	case !pluginhost.Confined():
		add("plugin confinement", "info", "none on "+runtime.GOOS+
			" — plugins run with this user's full access; process groups and the "+
			"environment allowlist still apply")
	default:
		add("plugin confinement", "ok", fmt.Sprintf(
			"sandbox-exec: %d paths denied read+write (rta's own state), %d denied read "+
				"(credential locations); everything else is readable",
			len(deny.NoAccess), len(deny.NoRead)))
	}

	// SDK plugins actually loaded, and anything about them worth knowing but
	// not worth printing before every command.
	if plugins := loadedPlugins; len(plugins) > 0 {
		for _, p := range plugins {
			detail := fmt.Sprintf("%s (%s, %s)",
				p.Identity.Path, plural(len(p.Declared.Capabilities), "capability", "capabilities"),
				p.Identity.Short())
			status := "ok"
			for id, fields := range p.Unknown {
				status = "info"
				detail += fmt.Sprintf(" — %s declares %s, which this rta does not understand; "+
					"it may have been built against a newer one", id, strings.Join(fields, ", "))
			}
			add("plugin "+p.Declared.Name, status, detail)
		}
	}

	// Found on $PATH and not run, because nothing said this artifact may.
	// A warning rather than info: it is a plugin the operator installed and
	// is not getting, and the fix is one command.
	for _, u := range untrustedPluginsFound {
		if u.Taken {
			add("plugin "+u.Name, "warn", fmt.Sprintf(
				"%s (%s) was found on $PATH and not run, and %q is already registered — "+
					"trusting it would collide rather than add anything; remove or rename the file",
				u.Path, u.Short(), u.Name))
			continue
		}
		add("plugin "+u.Name, "warn", fmt.Sprintf(
			"%s (%s) is installed and was not run — nothing has said this artifact may. "+
				"`rta plugin trust %s` after checking where it came from",
			u.Path, u.Short(), u.Name))
	}
	if n := plugintrust.Load().Len(); n > 0 {
		add("plugin trust", "ok", plural(n, "artifact", "artifacts")+
			" approved to run — `rta plugin untrust <name>` takes one back")
	}

	// Provenance for managed plugins (ADR 0017): what rta.lock recorded
	// against what the store and the loaded processes actually say. Every
	// mismatch here is a fact about drift, stated rather than repaired —
	// the lockfile records, it never authorizes.
	for _, e := range plugindist.ReadLock() {
		short := e.Digest
		if len(short) > 12 {
			short = short[:12]
		}
		name, status := "managed "+e.Name, "ok"
		detail := fmt.Sprintf("%s from index %q (%s) — signature: %s",
			e.Version, e.Index, short, e.Signature)
		current, linked := plugindist.CurrentDigest(e.Name)
		switch {
		case !linked:
			status, detail = "warn", detail+" — the store's bin link is gone; "+
				"`rta plugin install "+e.Name+"` after `rta plugin remove "+e.Name+"` restores it"
		case current != e.Digest:
			status, detail = "warn", detail+fmt.Sprintf(
				" — the store serves %.12s while rta.lock records %s: an upgrade that did not "+
					"finish, or a swap rta did not make", current, short)
		}
		if _, attached := plugindist.IndexByName(e.Index); !attached {
			if status == "ok" {
				status = "info"
			}
			detail += " — index " + e.Index + " is no longer attached, so upgrade has nowhere to ask"
		}
		// A $PATH copy outranking the managed one is ordinary shadowing,
		// deliberate or stale — said here because the managed row is where
		// somebody looks when the version they installed is not the one
		// answering.
		for _, p := range loadedPlugins {
			if p.Declared.Name == e.Name &&
				!strings.HasPrefix(p.Identity.Path, plugindist.StoreDir()+string(filepath.Separator)) {
				status = "warn"
				detail += " — shadowed: calls run " + p.Identity.Path + ", not the managed copy"
			}
		}
		add(name, status, detail)
	}

	// Exec-tier plugins on $PATH (rta-<name>), discovery convention only for
	// now — execution support lands with the plugin host (M3).
	// Named for what it scans, because the row above already said "1 from
	// $PATH" and this one used to answer "none found on $PATH" in the same
	// report. Both were true — they look for different filenames, one tier
	// apart — and nothing on the page said so, which makes a reader distrust
	// whichever they read second.
	if found := execPlugins(); len(found) > 0 {
		add("exec-tier (rta-*)", "info", strings.Join(found, ", ")+" (execution support lands in M3)")
	} else {
		add("exec-tier (rta-*)", "ok", "none found — this is the M3 tier, separate from the rta-plugin-* above")
	}

	// MCP clients we can install into.
	if _, err := exec.LookPath("claude"); err == nil {
		add("claude CLI", "ok", "found — `rta mcp install claude` available")
	} else {
		add("claude CLI", "info", "not found — `rta mcp serve` still works with any MCP client")
	}

	t.Total = len(t.Rows)
	return t
}

// execPlugins scans $PATH for rta-* binaries (kubectl/gh convention).
func execPlugins() []string {
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			// rta-plugin-* is the SDK tier, discovered and launched by
			// internal/pluginhost. Counting it here too would report one
			// binary under two tiers with two different sets of guarantees.
			if !strings.HasPrefix(name, "rta-") || strings.HasPrefix(name, pluginhost.Prefix) || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// plural formats a count with the right noun, so a report never says
// "1 capabilities" to somebody deciding whether to trust what it says.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
