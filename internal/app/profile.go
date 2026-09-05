package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// resolveProfile decides which environment this CLI invocation runs against,
// and binds this capability's share of it.
//
// Two sources, and they are deliberately not interchangeable:
//
//   - --profile names one explicitly. A profile that says nothing about this
//     plugin is an error, because somebody asked for something that cannot
//     happen.
//   - whatever `rta use` switched on applies to every plugin the profile
//     covers, and is silent about the rest. Switching on must not cost you
//     every command the environment happens not to mention.
//
// The flag wins, so a single command can reach elsewhere without disturbing the
// selection — the same relationship a shell's `cd` has with an absolute path.
//
// **Neither source can grant anything to internal/mcp.** An agent's profile
// comes from the decoded tool argument and from nowhere else, so the string a
// person consented to and the connection a call touched are the same string by
// construction. The selection reaches that side only as a refusal (see
// internal/mcp's active-profile bound), which is a direction that cannot make
// the gate and the run disagree.
//
// # The closer
//
// The third return tears down anything this opened — today, the port-forward a
// `kube:` coordinate names. It is never nil, so `defer` it on the line after
// the call and before checking the error: a failure that happens after the
// forward came up closes it here, and a connection that names no cluster
// returns a no-op.
func resolveProfile(ctx context.Context, cmd *cobra.Command, c plugin.Capability,
	caller map[string]any,
) (string, map[string]any, func(), *view.Error) {
	noop := func() {}
	if !plugin.Profilable(c) {
		return "", nil, noop, nil
	}
	explicit := ""
	if f := cmd.Flags().Lookup("profile"); f != nil {
		explicit = strings.TrimSpace(f.Value.String())
	}
	active := profile.Active()
	if explicit == "" && active == "" {
		return "", nil, noop, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", nil, noop, view.AsError(err, "core.profile.config")
	}

	var (
		name string
		conn config.Connection
		verr *view.Error
	)
	if explicit != "" {
		name = explicit
		conn, verr = profile.Lookup(cfg, c, explicit, installed)
	} else {
		name, conn, verr = profile.Ambient(cfg, c, active, installed)
	}
	if verr != nil {
		return "", nil, noop, verr
	}
	if name == "" {
		return "", nil, noop, nil
	}
	// Fill, not Bind: this is a real run with a person waiting, so a `secrets:`
	// reference is fetched here. kv.Reveal is injected rather than imported by
	// internal/profile, which is reached from the completion path on every
	// keystroke and has no business knowing an encrypted store exists.
	filled, verr := profile.Fill(ctx, name, conn, c, caller, os.LookupEnv, kv.Reveal)
	if verr != nil {
		// noop, never nil: the caller defers this before checking the error,
		// so a nil here is a segfault on the *failure* path — which is the
		// path an operator with a broken credential reference reaches. It took
		// running the built binary to notice; TestResolveProfileNeverReturnsA
		// NilTeardown walks every return out of this function now.
		return "", nil, noop, verr
	}
	// And the forward, if this connection names a cluster. Separate from Fill
	// because their lifetimes are: what Fill produces is true for as long as
	// the environment stands, and a forward is per call, by decision.
	// closeTunnel is never nil, so the caller defers it unconditionally.
	dialled, closeTunnel, verr := profile.Dial(ctx, name, conn, c, caller)
	if verr != nil {
		return "", nil, closeTunnel, verr
	}
	for input, v := range dialled {
		filled[input] = v
	}
	return name, filled, closeTunnel, nil
}

// installed is what this machine has registered, for profile resolution.
//
// Package state, for the reason doctor.go's pluginConfig already documents: it
// is fixed once at startup, before any command runs, and threading a registry
// through every command constructor to reach one lookup would be a wide change
// for a narrow need. A nil value resolves nothing and therefore refuses every
// external plugin's profiles — the right zero for a gate.
var installed profile.Installed

// SetInstalled records the registry profile resolution reads.
func SetInstalled(inst profile.Installed) { installed = inst }

// withTrust pairs the registry with what discovery refused to launch, so a
// profile naming a plugin that is installed-but-unapproved is told so instead
// of being told the plugin is not installed.
//
// A wrapper rather than a field on the registry: the registry's job is what
// registered, and trust is decided before registration is even attempted. The
// two facts meet here, in the one place that already holds both.
type withTrust struct{ profile.Installed }

// Untrusted reports whether this namespace names an artifact discovery found
// and did not run.
func (w withTrust) Untrusted(namespace string) bool {
	for _, u := range untrustedPluginsFound {
		if u.Name == namespace {
			return true
		}
	}
	return false
}

// bindProfile is resolveProfile without the fetching: what the environment
// contributes, from the config file and the process environment only.
//
// For the completion path, which runs on every keystroke and must not open an
// encrypted store, prompt, or block — the exact line internal/profile draws
// between Bind and Fill. Without it a suggestion computed the connection from
// the base configuration while the command it was completing would have run
// against the switched-on environment, so `rta pg table <tab>` on staging
// offered production's tables.
//
// Silent on every failure. A completion that refuses to answer until the
// command line is valid is a completion nobody sees.
func bindProfile(cmd *cobra.Command, c plugin.Capability) (string, map[string]any) {
	if !plugin.Profilable(c) {
		return "", nil
	}
	explicit := ""
	if f := cmd.Flags().Lookup("profile"); f != nil {
		explicit = strings.TrimSpace(f.Value.String())
	}
	active := profile.Active()
	if explicit == "" && active == "" {
		return "", nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", nil
	}
	name, conn := explicit, config.Connection{}
	var verr *view.Error
	if explicit != "" {
		conn, verr = profile.Lookup(cfg, c, explicit, installed)
	} else {
		name, conn, verr = profile.Ambient(cfg, c, active, installed)
	}
	if verr != nil || name == "" {
		return "", nil
	}
	return name, profile.Bind(name, conn, c, os.LookupEnv)
}

// newUseCommand implements `rta use [profile]`: the switch.
//
// It exists because the alternative is --profile on every invocation, and a
// security feature people find tedious is one they route around. Switching to
// an environment points every plugin it covers at that environment at once,
// which is the thing somebody actually does at the start of a piece of work.
//
// What it is not is a second way to authorize anything. Switching on grants
// nothing and changes no grant. On the agent side it can only *narrow*: while a
// profile is on, `rta mcp serve` refuses every other one, so switching is also
// how somebody takes an environment away from an agent without revoking
// anything.
func newUseCommand(opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use [profile]",
		Short: "Switch this machine to a configured environment",
		Long: "With a profile name, switches to it: every later command for a plugin that\n" +
			"profile covers runs against it, with no --profile at all.\n\n" +
			"--for gives the switch a deadline, and a profile carrying `ttl:` brings its own.\n" +
			"When it lapses everything falls back to the base configuration on its own — a\n" +
			"deadline that depends on a process staying alive is not a deadline.\n\n" +
			"With no arguments, prints what is on. --off switches it back off.\n\n" +
			"Switching authorizes nothing. While a profile is on, `rta mcp serve` refuses\n" +
			"every profile but that one, so this is also the fastest way to take an\n" +
			"environment away from an agent — but it can only take away, never give.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			renderOpts := cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()}
			v, verr := runUse(cmd, args)
			if verr != nil {
				_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
				return Rendered(verr)
			}
			return cli.Render(cmd.OutOrStdout(), v, renderOpts)
		},
	}
	cmd.Flags().Bool("off", false, "switch off, back to the base configuration")
	cmd.Flags().Duration("for", 0, "switch off again after this long (overrides the profile's ttl)")
	_ = cmd.RegisterFlagCompletionFunc("for", completeWindow)
	return cmd
}

// completeWindow offers durations for `rta use --for`, the profile's own ttl
// first when the name is already on the line.
//
// A duration is free text and stays free text — this is a list to pick from,
// not a set to be held to. But the value somebody most often wants is the one
// the profile already declares, and asking them to go and read it is how a
// deadline ends up being typed wrong or left off.
//
// Every entry must parse as a Go duration, because the flag is a Duration and
// a suggestion the flag then rejects is worse than none.
func completeWindow(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	out := make([]cobra.Completion, 0, 5)
	seen := map[string]bool{}
	add := func(value, desc string) {
		d, err := time.ParseDuration(value)
		if err != nil || seen[profile.ShortDuration(d)] {
			return
		}
		seen[profile.ShortDuration(d)] = true
		out = append(out, cobra.CompletionWithDesc(profile.ShortDuration(d), desc))
	}
	if len(args) == 1 {
		if cfg, err := config.Load(); err == nil {
			if p, known := cfg.Profiles[args[0]]; known && p.TTL != "" {
				add(p.TTL, "what "+args[0]+" declares")
			}
		}
	}
	add("15m", "a quick look")
	add("1h", "a task")
	add("8h", "a working day")
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeProfiles(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, name := range cfg.ProfileNames() {
		desc := cfg.Profiles[name].Note
		if desc == "" {
			desc = strings.Join(cfg.Profiles[name].Namespaces(), ", ")
		}
		out = append(out, cobra.CompletionWithDesc(name, desc))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func runUse(cmd *cobra.Command, args []string) (view.View, *view.Error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, view.AsError(err, "core.profile.config")
	}
	off, _ := cmd.Flags().GetBool("off")
	window, _ := cmd.Flags().GetDuration("for")
	now := time.Now()

	switch {
	case off:
		if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
			return nil, verr
		}
	case len(args) == 0:
		return currentView(cfg, profile.LoadSelection(), now), nil
	default:
		name := strings.TrimSpace(args[0])
		// A switch is the whole environment; an instance is one connection
		// inside it. `rta use staging/analytics` is refused rather than
		// silently narrowed to `staging`, because the two would then be one
		// keystroke apart and mean the same thing — and the operator who
		// typed the instance believes something narrower happened.
		if instance := config.RefInstance(name); instance != "" {
			return nil, view.Errorf("core.profile.instance.unusable",
				"%q names one connection; a switch is the whole environment", name).
				WithHint("`rta use " + config.RefName(name) + "` switches it on — the instance is " +
					"picked per call, with `--profile " + name + "`, and per grant")
		}
		p, ok := cfg.Profiles[name]
		if !ok {
			return nil, unknownProfile(cfg, name)
		}
		if !p.Trusted() {
			return nil, view.Errorf("core.profile.untrusted",
				"profile %q comes from a working-directory config file, so nothing honours it", name).
				WithHint("set $RTA_CONFIG to name that file deliberately")
		}
		// An unreadable ttl is refused here too, not only by Lookup. Otherwise
		// `rta use prod` on a profile carrying `ttl: 1hour` reports success and
		// stores a switch with no deadline at all — the operator wrote the line
		// precisely so that production activations expire, and the failure is
		// silent in the direction that keeps them there.
		if p.BadTTL() {
			return nil, view.Errorf("core.profile.ttl",
				"profile %q has ttl %q, which is not a duration", name, p.TTL).
				WithHint("write it as `30m`, `2h`, `12h` — or remove it for no deadline")
		}
		// An environment that cannot resolve cannot be switched to. Allowing it
		// would leave every later command for every plugin it covers failing
		// with a message about a connection the operator believes they chose
		// deliberately — and `rta use` would have said nothing at the one moment
		// they were looking. The TUI already refuses; this is the same rule on
		// the surface where somebody is more likely to be scripting it.
		for _, problem := range profile.Check(cfg, installed) {
			if problem.Name == name {
				return nil, view.Errorf("core.profile.unusable",
					"%s cannot be used: %s", name, problem.Reason).
					WithHint(problem.Hint)
			}
		}
		s := profile.Selection{Active: name}
		// The flag beats the profile's own ttl, and the profile's ttl beats
		// nothing at all. A production environment declaring `ttl: 1h` is
		// temporary even when somebody switches to it in a hurry, which is the
		// only time it matters.
		if window <= 0 {
			if d, has := p.Window(); has {
				window = d
			}
		}
		if window > 0 {
			until := now.Add(window)
			s.Until = &until
		}
		if verr := profile.SaveSelection(s); verr != nil {
			return nil, verr
		}
	}
	return currentView(cfg, profile.LoadSelection(), now), nil
}

// newProfileCommand implements `rta profile list|show`.
//
// Read-only and human-only. It is the answer to "what environments do I have,
// and which one is a grant about" — a question the operator has to be able to
// ask before issuing consent, and which `rta grant list` can only half answer
// because it knows the names and not what they point at.
//
// Not a plugin, deliberately: a Read capability is exposed over MCP by
// default, and the operator's whole connection inventory is exactly what an
// ungranted agent must not be handed. Living here keeps it off that surface by
// construction rather than by a flag somebody has to remember.
// renderFn is the closure each command group builds over its global options:
// render this view, or render this refusal to stderr and fail.
//
// Named because the group is no longer one function — `set` and `rm` live in
// profileset.go — and passing it is what keeps `--output` meaning the same
// thing on every subcommand.
type renderFn func(*cobra.Command, view.View, *view.Error) error

func newProfileCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	render := func(cmd *cobra.Command, v view.View, verr *view.Error) error {
		format, err := cli.ParseFormat(opts.output)
		if err != nil {
			return err
		}
		renderOpts := cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()}
		if verr != nil {
			_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
			return Rendered(verr)
		}
		return cli.Render(cmd.OutOrStdout(), v, renderOpts)
	}
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "The environments you have configured",
		Long: "A profile is a named environment across every plugin that has something in it:\n" +
			"`rta use proj1-staging` points pg, s3 and vault at staging at once, and\n" +
			"`rta grant allow pg.query --profile proj1-staging --ttl 1h` lets an agent reach\n" +
			"that one for an hour. Both halves name the same thing on purpose.",
		RunE: groupRunE,
	}
	list := &cobra.Command{
		Use:               "list",
		Short:             "List configured environments and whether each one is usable",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return render(cmd, nil, view.AsError(err, "core.profile.config"))
			}
			return render(cmd, profileTable(cfg, reg), nil)
		},
	}
	show := &cobra.Command{
		Use:               "show <profile>",
		Short:             "What one environment sets, and where each value comes from",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return render(cmd, nil, view.AsError(err, "core.profile.config"))
			}
			p, ok := cfg.Profiles[args[0]]
			if !ok {
				return render(cmd, nil, unknownProfile(cfg, args[0]))
			}
			return render(cmd, profileCard(args[0], p, reg), nil)
		},
	}
	cmd.AddCommand(list, show, profileSetCommand(reg, render), profileRemoveCommand(reg, render))
	return cmd
}

func profileTable(cfg config.Config, reg *registry.Registry) view.View {
	problems := map[string]string{}
	for _, p := range profile.Check(cfg, withTrust{reg}) {
		if _, already := problems[p.Name]; !already {
			problems[p.Name] = p.Reason
		}
	}
	active := profile.Active()
	t := view.Table{Columns: []view.Column{
		{Name: "Profile"},
		{Name: "Plugins"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Note"},
	}}
	for _, name := range cfg.ProfileNames() {
		p := cfg.Profiles[name]
		status := "ok"
		note := p.Note
		if reason, bad := problems[name]; bad {
			// An invalid profile is unnameable everywhere else, so this is the
			// one screen that has to say why rather than pretend it is absent.
			status, note = "invalid", reason
		} else if name == active {
			status = "on"
		}
		t.Rows = append(t.Rows, []string{name, strings.Join(p.Namespaces(), " "), status, note})
	}
	t.Total = len(t.Rows)
	return t
}

func profileCard(name string, p config.Profile, reg *registry.Registry) view.KeyValue {
	pairs := []view.Pair{{Key: "profile", Value: name}}
	if p.Note != "" {
		pairs = append(pairs, view.Pair{Key: "note", Value: p.Note})
	}
	if p.Color != "" {
		what := p.Color + " — the badge every command prints while this is switched on"
		if p.BadColor() {
			// Shown rather than hidden. An operator who wrote a colour and
			// sees no badge is owed the reason here, where they are looking at
			// the profile, and not only from `rta doctor`.
			what = p.Color + " — not a colour, so nothing is painted; the form is #rrggbb"
		}
		pairs = append(pairs, view.Pair{Key: "color", Value: what})
	}
	if p.TTL != "" {
		pairs = append(pairs, view.Pair{Key: "ttl", Value: p.TTL + " per switch"})
	}
	for _, key := range p.PluginKeys() {
		conn := p.Plugins[key]
		pairs = append(pairs, view.Pair{Key: "plugin", Value: key})
		// The coordinate first, because it is the one line that answers
		// "where does this actually go?" — and because it changes what every
		// line under it means: a `set:` host beside a `kube:` coordinate is
		// two statements about the destination, and the forward is the one
		// that wins. A page describing a connection that did not mention the
		// tunnel would be describing somewhere else.
		if conn.Kube != "" {
			pairs = append(pairs, view.Pair{Key: "  kube", Value: conn.Kube + " (port-forward, per call)"})
			pairs = append(pairs, tunnelledPairs(config.PluginNamespace(key), reg)...)
		}
		if conn.SSH != "" {
			pairs = append(pairs, view.Pair{Key: "  ssh", Value: conn.SSH + " (ssh tunnel, per call)"})
			pairs = append(pairs, tunnelledPairs(config.PluginNamespace(key), reg)...)
		}
		set := make([]string, 0, len(conn.Set))
		for k := range conn.Set {
			set = append(set, k)
		}
		sort.Strings(set)
		// A `set:` key naming a declared secret is redacted, and the reason is
		// that this is a mistake somebody makes rather than a thing they do.
		//
		// `secrets:` holds a reference and `set:` holds a value, so a password
		// typed into `set:` is inert — nothing reads it, and Check says so
		// below. What it is not is harmless: the config file is written 0644
		// because it is documented to hold no secrets, and this command exists
		// to be read and pasted. Printing the literal back, in the pretty view
		// and in --output json, turns one wrong line in a file into a
		// credential on a screen and in a ticket.
		//
		// Redacted by the plugin's own declaration rather than by guessing at
		// names like "password": what counts as a secret is something the
		// capability already states, and this is the same list credentialPairs
		// reads two lines down.
		secretInputs := map[string]bool{}
		for _, f := range profileSecrets(config.PluginNamespace(key), reg) {
			secretInputs[f] = true
		}
		for _, k := range set {
			value := fmt.Sprint(conn.Set[k])
			if secretInputs[k] {
				value = "(redacted — a secret under `set:` is inert; map it with `secrets:` instead)"
			}
			pairs = append(pairs, view.Pair{Key: "  set:" + k, Value: value})
		}
		pairs = append(pairs, credentialPairs(name, key, conn, reg)...)
	}
	// withTrust rather than the bare registry, so this page distinguishes
	// "not installed" from "installed and not approved" exactly as `rta use`
	// does. The two are one command apart and the second is what a rebuild
	// produces every time; `rta use` was taught the difference and the page
	// beside it was not, which is this file's recorded failure mode.
	for _, problem := range profile.Check(
		config.Config{Profiles: map[string]config.Profile{name: p}}, withTrust{reg}) {
		pairs = append(pairs, view.Pair{Key: "problem", Value: problem.Reason + " — " + problem.Hint})
	}
	return view.KeyValue{Pairs: pairs}
}

// credentialPairs lists every credential this plugin can take and where this
// profile gets it — in the precedence order it is actually resolved in, because
// "which of these two wins" is the question somebody has when both are present.
//
// The credential half is the half people get wrong. A profile can never carry a
// secret in the file (Config is refused on a Secret), so the only ways one
// reaches a connection are the variable and the `secrets:` reference, and naming
// them here is the difference between a working environment and a connection
// that fails authentication for no visible reason.
func credentialPairs(name, key string, conn config.Connection, reg *registry.Registry) []view.Pair {
	refs := map[string]config.SecretRef{}
	for _, r := range conn.SecretRefs() {
		refs[r.Input] = r
	}
	var pairs []view.Pair
	seen := map[string]bool{}
	for _, f := range profileSecrets(config.PluginNamespace(key), reg) {
		seen[f] = true
		env := plugin.ProfileEnvVar(name, f)
		_, exported := os.LookupEnv(env)
		ref, referenced := refs[f]

		var value string
		switch {
		case exported && referenced:
			value = "$" + env + " (set — wins over " + ref.Scheme + ":" + ref.Ref + ")"
		case exported:
			value = "$" + env + " (set)"
		case referenced:
			value = ref.Scheme + ":" + ref.Ref
		default:
			value = "$" + env + " (not set) — or map one with `secrets:`"
		}
		pairs = append(pairs, view.Pair{Key: "  credential:" + f, Value: value})
	}
	// A reference onto an input no capability here declares would never take
	// effect, and Fill refuses it at run time. Saying so on the page that
	// describes the profile is where somebody can act on it.
	//
	// **Asked the way Fill asks it, which is not the way the rows above are
	// chosen.** Those list the namespace's *Secret* inputs, because a page
	// about credentials should offer the credentials. What a `secrets:` block
	// may target is wider: Fill gates on ProfileFillable, so any input an
	// operator could have configured can be filled from a store entry or a
	// cluster Secret — mapping `user` onto a Secret's `username` key is an
	// ordinary thing to want and it works. Reusing the narrower list here
	// called that a problem while the call it described ran perfectly well,
	// which is the report-versus-reality drift this repo has recorded three
	// times. The rule that decides is the one on the path that uses the value.
	fillable := profileFillableInputs(config.PluginNamespace(key), reg)
	known := registeredNamespace(config.PluginNamespace(key), reg)
	for _, r := range conn.SecretRefs() {
		switch {
		case seen[r.Input]:
			// Already printed as a credential row above.
		case fillable[r.Input]:
			// Filled, and not a credential — so it is not in the rows above
			// and would otherwise be invisible. It still decides what the
			// connection is, which is exactly what this page is for.
			pairs = append(pairs, view.Pair{
				Key: "  " + r.Input, Value: r.Scheme + ":" + r.Ref + " (from the connection)"})
		case !known:
			// Same guard as the coordinate row above: with nothing registered
			// under this namespace there is no declaration to hold the
			// mapping against, and the pin row already says why.
			pairs = append(pairs, view.Pair{
				Key: "  " + r.Input, Value: r.Scheme + ":" + r.Ref})
		default:
			pairs = append(pairs, view.Pair{
				Key:   "  problem",
				Value: "secrets." + r.Input + " names an input " + config.PluginNamespace(key) + " does not offer",
			})
		}
	}
	return pairs
}

// profileFillableInputs is the set a `secrets:` reference may target: every
// input of this namespace that plugin.ProfileFillable admits.
//
// The same question internal/profile.Fill asks, asked here so a report and a
// run cannot disagree about whether a mapping takes effect.
func profileFillableInputs(ns string, reg *registry.Registry) map[string]bool {
	out := map[string]bool{}
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if plugin.ProfileFillable(c, f) {
				out[f.Name] = true
			}
		}
	}
	return out
}

// profileSecrets lists the Secret inputs a namespace declares that a profile is
// allowed to fill, sorted and deduplicated across its capabilities.
func profileSecrets(ns string, reg *registry.Registry) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if !f.Type.Sensitive() || !plugin.ProfileFillable(c, f) || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// tunnelledPairs lists the inputs a resolved forward fills, so "why did that
// host appear?" stays answerable from the page describing the connection.
//
// The same property `Config` keys and `$RTA_*` variables already have, and it
// matters more here: those are values an operator typed somewhere, and this is
// one rta computes. An input silently overwritten by the host is the kind of
// thing somebody only discovers by wondering why their `set: host` did nothing.
//
// Sorted by input name so two runs print the same page.
func tunnelledPairs(ns string, reg *registry.Registry) []view.Pair {
	filled := map[string]plugin.EndpointRole{}
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Endpoint != plugin.EndpointNone && plugin.ProfileFillable(c, f) {
				filled[f.Name] = f.Endpoint
			}
		}
	}
	if !registeredNamespace(ns, reg) {
		// Nothing is said about a declaration nobody read; the card's pin row
		// already names the real problem.
		return nil
	}
	if len(filled) == 0 {
		// A coordinate the plugin cannot be pointed at. Said plainly, because
		// the profile looks complete and the forward would be opened and then
		// used by nothing — the call would reach the plugin's own default.
		return []view.Pair{{Key: "  problem", Value: ns +
			" declares no input a tunnel can fill, so the forward would be opened and ignored"}}
	}
	names := make([]string, 0, len(filled))
	for n := range filled {
		names = append(names, n)
	}
	sort.Strings(names)
	pairs := make([]view.Pair, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, view.Pair{Key: "  fills:" + n, Value: "the forward's " + string(filled[n])})
	}
	return pairs
}

func unknownProfile(cfg config.Config, name string) *view.Error {
	hint := "no profiles are configured — see `rta profile list`"
	if all := cfg.ProfileNames(); len(all) > 0 {
		hint = "configured: " + strings.Join(all, ", ")
	}
	return view.Errorf("core.profile.unknown", "no profile named %q", name).WithHint(hint)
}

func currentView(cfg config.Config, s profile.Selection, now time.Time) view.View {
	name := s.Name(now)
	if name == "" {
		body := "Nothing switched on — commands run against the base configuration."
		if s.Active != "" {
			// Distinguished from "never switched on", because the operator's
			// question after a command reached the wrong place is "was I still
			// on staging?" and "it lapsed" is the answer.
			body = fmt.Sprintf("%s lapsed — commands run against the base configuration.", s.Active)
		}
		return view.Text{Body: body}
	}
	p := cfg.Profiles[name]
	pairs := []view.Pair{{Key: "on", Value: name}}
	if p.Note != "" {
		pairs = append(pairs, view.Pair{Key: "note", Value: p.Note})
	}
	pairs = append(pairs, view.Pair{Key: "covers", Value: strings.Join(p.Namespaces(), ", ")})
	if left, deadline := s.Left(now); deadline {
		pairs = append(pairs, view.Pair{Key: "until", Value: s.Until.Format(time.RFC3339) +
			" (" + left.Round(time.Minute).String() + " left)"})
	} else {
		pairs = append(pairs, view.Pair{Key: "until", Value: "no deadline — `rta use --off` ends it"})
	}
	return view.KeyValue{Pairs: pairs}
}

// missingCredentials names the RTA_PROFILE_* variables an environment needs and
// has neither exported nor mapped with `secrets:`.
//
// Only Secret inputs, and only the ones a profile is allowed to fill. An
// environment whose plugins need no credential — or whose credential is
// genuinely optional, like vault's namespace — yields nothing and earns a plain
// "ok".
func missingCredentials(name string, p config.Profile, reg *registry.Registry) []string {
	var missing []string
	for _, key := range p.PluginKeys() {
		conn := p.Plugins[key]
		for _, f := range profileSecrets(config.PluginNamespace(key), reg) {
			if _, mapped := conn.Secrets[f]; mapped {
				continue
			}
			env := plugin.ProfileEnvVar(name, f)
			if _, ok := os.LookupEnv(env); !ok {
				missing = append(missing, env)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// registeredNamespace answers whether anything in ns was actually registered.
//
// The guard every declaration-dependent row on this card needs. An empty
// answer from a lookup against a namespace nobody registered is not the plugin
// saying no — it is nobody having asked it, and the two argue for opposite
// conclusions. Printing the first for the second tells an operator their
// configuration is wrong when what is wrong is that a rebuilt plugin has not
// been approved yet, which is the ordinary state after `make plugins-install`
// or any upgrade: trust is keyed on the digest, so new bytes register nothing
// until somebody says so.
//
// Both spellings of that mistake shipped here — a `kube:` coordinate reported
// as unusable, and a `secrets:` mapping reported as naming an input the plugin
// does not offer — each under a hint offering to delete the line that was
// right.
func registeredNamespace(ns string, reg *registry.Registry) bool {
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) == ns {
			return true
		}
	}
	return false
}
