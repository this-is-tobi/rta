package app

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta profile set` and `rta profile rm` — an environment without a terminal.
//
// # Why this exists
//
// Every way to configure rta went through a TTY. The TUI has a form per
// profile and a form per plugin inside it, both generated from the plugin's
// own declarations, and both of them know which inputs are credentials. What
// there was no way to do was state one from a script, a Dockerfile, a
// provisioning run or a dotfiles repository — the places setup actually
// happens for a team.
//
// So a team that cannot script it writes the YAML by hand instead, and that
// is the path with the weaker guarantee: nothing checks a hand-written block
// until something tries to use it, `secrets:` holds a reference while `set:`
// holds a value, and `set:` is the obvious-looking place to put a password.
// One of those ends up in a 0644 file, inert, doing nothing, and every later
// connection fails authentication for no visible reason.
//
// The point of a command is that it can refuse. `--set` cannot be given a
// declared credential, `--secret` cannot be given a value instead of a
// reference, and every value is typed against the declaration before it is
// written — so the shapes that used to be silently wrong are now the shapes
// that do not get written.
//
// # `set`, not `add`
//
// Idempotent on purpose. The thing this is for runs more than once — a
// provisioning script, a dotfiles bootstrap, a CI job — and a command that
// fails the second time is one people wrap in `|| true`, which throws away
// every refusal above along with the one they were silencing.
//
// # What each flag replaces
//
// A block is replaced by what the flags state, and a block no flag mentions
// is left exactly as it was. So `--set` states the whole `set:` block for
// that plugin (omit a key to remove it) while leaving its `secrets:` alone,
// and a run with neither touches neither. That way a script is the source of
// truth for what it states and silent about the rest, which is the only
// reading under which running it twice and running it once are the same.

// profileSetCommand implements `rta profile set`.
func profileSetCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <profile>",
		Short: "Create or update an environment without a terminal",
		Long: "States a profile from flags, so setting one up is a line in a script rather than\n" +
			"a form or a block of hand-written YAML.\n\n" +
			"Idempotent: running it twice is running it once. Each block is replaced by what\n" +
			"the flags state and a block no flag mentions is left alone, so `--set` restates\n" +
			"one plugin's configuration without disturbing its credentials.\n\n" +
			"`--set` refuses a declared credential outright: a value belongs in the store and\n" +
			"a profile carries the reference. `--secret` is that reference — never a value.\n\n" +
			"`--set` states a default, which a caller can override. `--only` fences an input:\n" +
			"once per allowed value, it states what the connection reaches, and a call whose\n" +
			"value falls outside it is refused. `--unfence` takes the fences off.\n\n" +
			"`--color` marks the environment and is its primary colour while it is on. `--theme` states\n" +
			"the rest of its palette, one `slot=#rrggbb` per colour and the whole block as `--set` does,\n" +
			"and `--untheme` takes the block off. The status colours (good, warn, bad, inverse, ink)\n" +
			"are not an environment's to state: `rta config set theme.good` sets one in your own config.",
		// In the order a person would run them, because a test does: a profile that
		// configures nothing is refused at creation, so the line that states only
		// a note and a deadline comes after the one that gives it a plugin.
		Example: "  rta kv set staging-db-password --file /run/secrets/db\n" +
			"  rta profile set staging --plugin pg \\\n" +
			"      --set host=db.staging.internal --set port=5432 \\\n" +
			"      --secret password=kv:staging-db-password\n" +
			"  rta profile set staging --note 'shared staging' --ttl 8h\n" +
			"  rta profile set staging --color '#dd3333' --theme label='" + theme.Builtin("label") + "' --theme accent='" + theme.Builtin("accent") + "'\n" +
			"  rta profile set staging --plugin pg \\\n" +
			"      --only database=app --only database=app-jobs\n" +
			"  rta profile set staging --plugin pg/analytics \\\n" +
			"      --set host=analytics.staging.internal \\\n" +
			"      --secret password=kv:staging-analytics-password\n" +
			"  rta use staging",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runProfileSet(cmd, args[0], reg, opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("note", "", "why this environment exists")
	cmd.Flags().String("ttl", "", "how long a switch to it lasts (`30m`, `8h`, or `none`)")
	cmd.Flags().String("color", "", "badge colour every command prints while it is on, and the screen's primary (`#rrggbb`, or `none`)")
	cmd.Flags().StringArray("theme", nil,
		"a `slot=#rrggbb` of the palette while it is on, over yours, never a status colour; repeat for more — the whole theme: block")
	cmd.Flags().Bool("untheme", false, "remove the profile's theme: block")
	cmd.Flags().StringArray("dashboard", nil, "a dashboard view linked to it, repeated for several — the first is drawn while it is on, V walks the rest and v walks back; one of `rta dashboard views` (or `none`)")
	cmd.Flags().String("plugin", "", "which plugin the connection flags below are about — "+
		"`pg`, or `pg/analytics` to state one of several connections to it")
	// StringArray, never StringSlice: StringSlice splits its argument on
	// commas, so `--set search-path=public,app` would silently become two
	// malformed pairs. A configuration value is not a list of flags.
	cmd.Flags().StringArray("set", nil, "a `key=value` this plugin reads; repeat for more")
	cmd.Flags().StringArray("only", nil,
		"fence an input to the values this connection reaches: a `key=value`, once per allowed value")
	cmd.Flags().Bool("unfence", false, "remove every `only:` fence from this plugin's entry")
	cmd.Flags().StringArray("secret", nil,
		"an `input=kv:entry` reference — never the value itself; repeat for more")
	cmd.Flags().String("kube", "", "reach it through a port-forward: `context/namespace/kind/name:port`")
	cmd.Flags().String("ssh", "", "or through an ssh host: `[user@]host[:port]/desthost:destport`")
	cmd.Flags().Bool("direct", false, "connect directly: remove any `kube:` or `ssh:` forward")
	// Named apart from a plugin's own --set tls=... (etcd, qdrant, s3) or
	// --set sslmode=... (pg) — those are that plugin's on/off toggle in its
	// own library's vocabulary, forced off by the host over a tunnel; this
	// is a different fact at a different layer, about the coordinate itself
	// rather than about any plugin's configuration. config.Connection.
	// TunnelTLS's doc comment has the full reasoning.
	cmd.Flags().Bool("tunnel-tls", false,
		"the far side of the forward speaks TLS on its own — fill the endpoint as https, not http")
	completeFlag(cmd, "plugin", completeInstalledPlugins)
	completeFlag(cmd, "ttl", completeWindow)
	completeFlag(cmd, "dashboard", completeViews)
	completeFlag(cmd, "theme", completeThemeSlots)
	completeFlag(cmd, "set", completeSetKeys)
	completeFlag(cmd, "only", completeSetKeys)
	completeFlag(cmd, "secret", completeSecretInputs)
	return cmd
}

// completeThemeSlots offers the palette's slots for `--theme`, each with the
// colour it has when nothing states one — the ones a profile can state, which
// are not the status colours: offering a slot the next step refuses is a
// completion that teaches the mistake.
//
// Silent past the `=`, as completeSetKeys is: a colour is the operator's to
// choose, and the one thing rta could offer there, the built-in, is in the
// description beside the slot.
func completeThemeSlots(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	direction := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	if strings.Contains(prefix, "=") {
		return nil, direction
	}
	builtin := theme.Effective(nil)
	slots := theme.Fields()
	out := make([]cobra.Completion, 0, len(slots))
	for _, slot := range slots {
		if theme.IsStatus(slot) {
			continue
		}
		out = append(out, cobra.CompletionWithDesc(slot+"=", "built-in "+builtin[slot]))
	}
	return out, direction
}

// parseThemeFlags turns `--theme slot=#rrggbb` into a profile's `theme:` block,
// each slot one the palette has and each colour one it can use.
//
// Refused here, where `--set` and `--only` leave a key nothing reads for the
// check that follows to name, because the palette's slots are a closed list of
// ten and a typo among them is one the file would carry as a line nothing
// paints — the case `rta doctor` reports, and the one a command that can refuse
// should not write. So is a status colour, which a profile cannot state
// (theme.Resolve): the line would be there and the palette would leave it out. A slot named twice is refused as `--set` refuses a scalar
// given twice: two answers, and no way to say which a reader of the command
// would expect.
func parseThemeFlags(pairs []string) (map[string]string, *view.Error) {
	slots := theme.Fields()
	// What a refusal lists is what the flag takes, which is not every slot the
	// palette has: a hint that offered the status colours would offer the next
	// refusal.
	offered := slices.DeleteFunc(slices.Clone(slots), theme.IsStatus)
	order, raw, verr := collectPairs(pairs, nil, flagWords{
		flag: "theme", example: "primary=" + theme.Builtin("primary"), shape: "core.profile.theme.shape",
		empty: func(k string) *view.Error {
			return view.Errorf("core.profile.theme.empty", "`--theme %s=` states no colour", k).
				WithHint("`--theme` states the whole block, so leave a slot out to take it from yours — " +
					"and check the variable this came from expanded")
		},
	})
	if verr != nil {
		return nil, verr
	}
	block := make(map[string]string, len(order))
	for _, slot := range order {
		if slot == theme.GradesKey {
			return nil, view.Errorf("core.profile.theme.status",
				"%s sets how status is drawn, which only your own config file does", slot).
				WithHint("an environment cannot decide how failures are drawn on your screen; " +
					"`rta config set theme." + slot + " alerts` sets it in your own, for every environment")
		}
		if !slices.Contains(slots, slot) {
			hint := "one of: " + strings.Join(offered, ", ")
			if guess := theme.NearestSlot(slot); guess != "" {
				hint = "did you mean `" + guess + "`? — " + hint
			}
			return nil, view.Errorf("core.profile.theme.slot", "%q is not a colour of the palette", slot).WithHint(hint)
		}
		if theme.IsStatus(slot) {
			return nil, view.Errorf("core.profile.theme.status",
				"%s is a status colour, which only your own config file sets", slot).
				WithHint("an environment cannot state the colours that mean ok, warn and failed; " +
					"`rta config set theme." + slot + " '#3ED598'` sets it in your own, for every environment")
		}
		values := raw[slot]
		if len(values) > 1 {
			return nil, view.Errorf("core.profile.theme.repeated",
				"`--theme %s` is given more than once, and a slot takes one colour", slot).
				WithHint("state it once")
		}
		colour := strings.TrimSpace(values[0])
		if !theme.HexColor.MatchString(colour) {
			return nil, view.Errorf("core.profile.theme.color", "%s is not a colour: %q", slot, colour).
				WithHint("write it as `#rrggbb`, quoted since # starts a comment in a shell: `--theme " + slot + "='" + theme.Builtin(slot) + "'`")
		}
		block[slot] = colour
	}
	return block, nil
}

// completeSetKeys offers the config keys the plugin already on the command
// line declares, with their help text.
//
// The other half of not needing a form. A flag you have to look up is a flag
// people guess at, and a guessed key is the `hsot: typo.internal` this command
// spends a refusal on — better not to have been typed. The Options an input
// declares ride along in the description, since "one of disable|require|…" is
// the thing you would otherwise go and read.
//
// Silent whenever it cannot answer, like every completion here: one that
// refuses until the command line is valid is one nobody ever sees.
func completeSetKeys(cmd *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ns := config.PluginNamespace(strings.TrimSpace(mustString(cmd, "plugin")))
	if ns == "" || installed == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// Past the `=` there is a value to suggest, not a key, and rta has no
	// business guessing at one — a host or a database name is not something
	// it knows. NoSpace so `host=` stays open for typing.
	if strings.Contains(prefix, "=") {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
	var out []cobra.Completion
	seen := map[string]bool{}
	for _, c := range installed.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Config == "" || !plugin.ProfileFillable(c, f) || seen[f.Config] {
				continue
			}
			seen[f.Config] = true
			desc := f.Help
			if len(f.Options) > 0 {
				desc = strings.Join(f.Options, "|")
			}
			// The key is offered because something a profile may fill reads it;
			// what the rest of the namespace does with it is the part a person
			// choosing a key to state would otherwise find out from a read that
			// ignores it.
			if note := profile.ScopeNote(ns, installed, f.Config); note != "" {
				desc += " — a value stated here " + note
			}
			out = append(out, cobra.CompletionWithDesc(f.Config+"=", desc))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// completeSecretInputs offers the inputs a `secrets:` mapping may target.
//
// Wider than the credentials, and that is deliberate: Fill gates on
// ProfileFillable, so mapping `user` onto a Secret's `username` key is an
// ordinary thing to want and it works. The credentials are marked, because
// they are the ones that have no other way in.
func completeSecretInputs(cmd *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ns := config.PluginNamespace(strings.TrimSpace(mustString(cmd, "plugin")))
	if ns == "" || installed == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if strings.Contains(prefix, "=") {
		// A reference, and rta will not enumerate the store to complete one:
		// this runs on a keystroke and the entry names in an operator's store
		// are not something to unlock a store for. The scheme is the useful
		// part anyway — it is what makes the value a reference.
		return []cobra.Completion{cobra.CompletionWithDesc(prefix, "kv:<entry> or kube:<secret>/<key>")},
			cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
	var out []cobra.Completion
	seen := map[string]bool{}
	for _, c := range installed.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if !plugin.ProfileFillable(c, f) || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			desc := f.Help
			if f.Type.Sensitive() {
				// The same test profileSecrets applies, asked here because
				// this loop already has the declaration in hand.
				desc = "credential — " + desc
			}
			out = append(out, cobra.CompletionWithDesc(f.Name+"=", desc))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// profileRemoveCommand implements `rta profile rm`.
func profileRemoveCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <profile>",
		Aliases: []string{"remove"},
		Short:   "Remove an environment, or one plugin from it",
		Long: "Without --plugin, removes the environment: it is switched off if it was on, and\n" +
			"every grant naming it is revoked.\n\n" +
			"That last part is not tidiness. A grant naming a profile nothing can look up\n" +
			"authorizes nothing, so leaving it behind is a row in `rta grant list` that reads\n" +
			"like access and is not — which is the one thing a record of consent must never\n" +
			"contain.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runProfileRemove(cmd, args[0], reg, opts.dryRun, opts.yes)
			if verr != nil && verr.Code == CodeConfirmRequired {
				proceed, err := confirmByPreview(cmd.Context(), cmd, opts, func() error {
					preview, perr := runProfileRemove(cmd, args[0], reg, true, false)
					return render(cmd, preview, perr)
				})
				if err != nil {
					return err
				}
				if proceed {
					v, verr = runProfileRemove(cmd, args[0], reg, false, true)
				}
			}
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("plugin", "", "remove only this plugin's entry, keeping the environment — "+
		"`pg`, or `pg/analytics` for one instance of it")
	completeFlag(cmd, "plugin", completeInstalledPlugins)
	return cmd
}

func runProfileSet(cmd *cobra.Command, name string, reg *registry.Registry, dryRun bool) (view.View, *view.Error) {
	if !config.ValidName(name) {
		return nil, view.Errorf("core.profile.name", "%q is not a valid profile name", name).
			WithHint("lowercase letters, digits and dashes, starting with a letter or digit — " +
				"the name reaches an environment variable and a grant record")
	}
	if verr := refuseUnhonouredConfig(); verr != nil {
		return nil, verr
	}
	// A profile and a plugin namespace share a command line, so a profile
	// called `pg` is one nothing can name. Check refuses it in the file; this
	// refuses it at the keystroke, which is where somebody can still choose a
	// different word.
	if installed != nil {
		if _, taken := installed.Origin(name); taken {
			return nil, view.Errorf("core.profile.namespace",
				"%q is the name of a registered plugin", name).
				WithHint("pick another — a profile name and a namespace share a command line")
		}
	}
	// Two profile names can make each other's RTA_PROFILE_* variables
	// ambiguous, and the moment to say so is the one where a different word is
	// still free: `staging-db`'s password variable is spelled exactly like
	// `staging`'s `db-password` one, so whichever arrives second would start
	// reading a credential exported for the other. Refused at the keystroke
	// rather than at the read, because at the read there is nothing left to
	// choose — see plugin.ProfileEnvAmbiguous for why it cannot simply be
	// encoded away.
	if cfg, err := config.LoadFile(); err == nil {
		if clash := cfg.AmbiguousEnvNames(name); len(clash) > 0 {
			return nil, view.Errorf("core.profile.envclash",
				"%q and %q derive the same RTA_PROFILE_* variables",
				name, clash[0]).
				WithHint("a dash in a profile name and a dash in an input name both become " +
					"`_`, so the boundary between them is not recoverable — pick a name that " +
					"is not " + clash[0] + " plus a dash, or carry the credential as a " +
					"`secrets:` reference, where nothing is derived from a string")
		}
	}

	// Under the lock, and the whole load-mutate-write inside it. This
	// command is built to be scripted, and a script that states four
	// environments states them in parallel as readily as in sequence — eight
	// at once lost up to three of them, with all eight reporting success.
	//
	// Every refusal below stops the write by returning false rather than by
	// returning early, so a run that decides not to write still releases the
	// lock on the ordinary path.
	var (
		key      string
		receipts []view.Pair
		verr     *view.Error
		existed  bool
		// unchanged is a run that asked for a state the profile is already
		// in: not a refusal, and not a write either.
		unchanged bool
		// finalProfile is what would have been written, captured for a dry
		// run to report against — the closure below returns false rather
		// than committing it, so there is nothing on disk to read back.
		finalProfile config.Profile
		// scope is the configuration the profile is read in, for the part of its
		// card that depends on what is around it: the colours it lies over, and
		// the views a profile's own screen is read against, since whether its name
		// is taken is a fact of the configuration and not of the profile.
		scope config.Config
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		scope = cfg
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]config.Profile{}
		}
		current, wasThere := cfg.Profiles[name]
		existed = wasThere
		// Snapshotted as bytes rather than kept as a value. Profile.Plugins is
		// a map, so a copy of the struct shares it with the copy about to be
		// mutated, and any struct comparison between the two is equal by
		// construction — which reads as "nothing changed" for every edit. It
		// is also the honest question: what is being asked is whether the file
		// would differ, and this is the file.
		before, beforeErr := yaml.Marshal(current)
		p := current

		if cmd.Flags().Changed("note") {
			note, _ := cmd.Flags().GetString("note")
			p.Note = strings.TrimSpace(note)
		}
		if cmd.Flags().Changed("ttl") {
			ttl := strings.TrimSpace(mustString(cmd, "ttl"))
			if ttl == "none" {
				ttl = ""
			}
			if ttl != "" && (config.Profile{TTL: ttl}).BadTTL() {
				verr = view.Errorf("core.profile.ttl", "%q is not a duration", ttl).
					WithHint("write it as `30m`, `2h`, `12h` — or `none` for no deadline")
				return cfg, false
			}
			p.TTL = ttl
		}
		if cmd.Flags().Changed("dashboard") {
			asked, _ := cmd.Flags().GetStringArray("dashboard")
			var names []string
			for _, n := range asked {
				if n = strings.TrimSpace(n); n != "" && !slices.Contains(names, n) {
					names = append(names, n)
				}
			}
			if slices.Contains(names, "none") {
				if len(names) > 1 {
					verr = view.Errorf("core.profile.dashboard.none", "`none` takes every screen off the profile, and other names put them on").
						WithHint("state `--dashboard none` alone, or the views without it")
					return cfg, false
				}
				names = nil
			}
			// The block is the screen, written out, and this flag names
			// another one: taking the flag at its word would throw away
			// tiles somebody arranged, with nothing in the receipt that a
			// script could undo. `none` is refused the same way, since it
			// means "back to the dashboard: block" and not "forget my tiles".
			if p.Dashboard.Inline != nil {
				verr = inlineScreenRefusal(name, p.Dashboard.Inline)
				return cfg, false
			}
			for _, named := range names {
				switch {
				case named == config.DefaultView || cfg.HasView(named):
				case !config.ValidViewName(named):
					verr = viewNameRefusal("core.profile.dashboard", named)
					return cfg, false
				default:
					verr = missingViewRefusal(cfg, named)
					return cfg, false
				}
			}
			p.Dashboard = config.ProfileDashboard{Names: names}
		}
		if cmd.Flags().Changed("color") {
			color := strings.TrimSpace(mustString(cmd, "color"))
			if color == "none" {
				color = ""
			}
			if (config.Profile{Color: color}).BadColor() {
				verr = view.Errorf("core.profile.color", "%q is not a colour", color).
					WithHint("write it as `#rrggbb` — `#dd3333` red for production is the usual one — or `none` to drop it")
				return cfg, false
			}
			p.Color = color
		}
		// The palette states the whole block as `--set` and `--only` do, for the
		// same reason: a script is the source of truth for what it states, and a
		// colour left behind by a restatement is one nobody remembers writing.
		// What is dropped is said, as theirs is. `--untheme` is `--unfence` for
		// it: the way to take the block off.
		var themeReceipts []view.Pair
		switch {
		case cmd.Flags().Changed("theme") && mustBool(cmd, "untheme"):
			verr = view.Errorf("core.profile.theme.both",
				"--untheme removes the block that --theme states").
				WithHint("state one or the other")
			return cfg, false
		case cmd.Flags().Changed("theme"):
			pairs, _ := cmd.Flags().GetStringArray("theme")
			block, themeVerr := parseThemeFlags(pairs)
			if themeVerr != nil {
				verr = themeVerr
				return cfg, false
			}
			if left := missingSlots(p.Theme, block); len(left) > 0 {
				themeReceipts = append(themeReceipts, view.Pair{Key: "dropped",
					Value: strings.Join(left, ", ") + " — the flags state the whole block, and these were not among them"})
			}
			p.Theme = block
		case mustBool(cmd, "untheme"):
			if left := missingSlots(p.Theme, nil); len(left) > 0 {
				themeReceipts = append(themeReceipts, view.Pair{Key: "removed",
					Value: strings.Join(left, ", ") + " — while " + name + " is switched on the screen is drawn in your own colours" +
						" again, and in its color: as the primary if it has one"})
			}
			p.Theme = nil
		}

		key, receipts, verr = applyConnectionFlags(cmd, name, &p, reg)
		if verr != nil {
			return cfg, false
		}
		receipts = append(themeReceipts, receipts...)
		if !existed && len(p.Plugins) == 0 {
			// A profile that configures nothing is one `rta use` refuses, so
			// creating one is creating something unusable. Updating an
			// existing empty one is left alone: the operator may be halfway
			// through.
			verr = view.Errorf("core.profile.empty",
				"a new profile has to configure at least one plugin").
				WithHint("add `--plugin <name>` and what it should connect to")
			return cfg, false
		}
		// A run asking for a state the profile is already in writes nothing
		// and says so. `--direct` on a plugin that never had a coordinate is
		// the case that matters: it is what somebody types after being told a
		// stray forward is their problem, and "updated me" over a
		// byte-identical file tells them they fixed something that was never
		// wrong. `rta policy` already answers "already off" rather than
		// reporting a write it did not need to make.
		after, afterErr := yaml.Marshal(p)
		if existed && beforeErr == nil && afterErr == nil && bytes.Equal(before, after) {
			unchanged = true
			return cfg, false
		}
		// Stamped with the file's provenance, the way LoadFile will stamp it
		// on the way back: a new profile starts as a zero value, which reads
		// as untrusted, and a dry run's card is built from this one.
		finalProfile = cfg.Stamp(p)
		if dryRun {
			return cfg, false
		}
		cfg.Profiles[name] = p
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.profile.write")
	}
	if verr != nil {
		return nil, verr
	}

	// A dry run has nothing on disk to read back — config.Mutate returned
	// false above precisely so nothing was written — so the card is built
	// from finalProfile, the same value that branch would have committed,
	// rather than from config.LoadFile's answer.
	written := finalProfile
	if !dryRun {
		// Read back what a later run will read, rather than reporting the
		// value this function built. The profile card is `rta profile
		// show`'s renderer, so what this prints on success and what that
		// prints afterwards are the same page — including its redaction
		// and its problem rows.
		after, err := config.LoadFile()
		if err != nil {
			return nil, view.AsError(err, "core.profile.config")
		}
		written, scope = after.Profiles[name], after
	}
	card := profileCard(scope, name, written, reg)
	verb := "updated"
	switch {
	case dryRun && !existed:
		verb = "would create"
	case dryRun:
		verb = "would update"
	case !existed:
		verb = "created"
	}
	what := verb + " " + name
	if key != "" {
		what += " — " + key
	}
	label := "wrote"
	if dryRun {
		label = "would write"
	}
	head := make([]view.Pair, 0, 1+len(receipts)+len(card.Pairs))
	head = append(head, view.Pair{Key: label, Value: what + " in " + config.Where("profiles", name)})
	if unchanged {
		already := name + " already reads this way"
		if key != "" {
			already = key + " in " + name + " already reads this way"
		}
		head = []view.Pair{{Key: "unchanged",
			Value: already + " — nothing written to " + config.Where("profiles", name)}}
	}
	head = append(head, receipts...)
	card.Pairs = append(head, card.Pairs...)
	return card, nil
}

// applyConnectionFlags folds --plugin and everything under it into p, and
// returns the plugins: key it wrote, "" when the run said nothing about a
// plugin.
func applyConnectionFlags(cmd *cobra.Command, name string, p *config.Profile,
	reg *registry.Registry,
) (key string, receipts []view.Pair, verr *view.Error) {
	stated := make([]string, 0, 5)
	for _, f := range []string{"set", "only", "unfence", "secret", "kube", "ssh", "direct", "tunnel-tls"} {
		if cmd.Flags().Changed(f) {
			stated = append(stated, "--"+f)
		}
	}
	want := strings.TrimSpace(mustString(cmd, "plugin"))
	if want == "" {
		if len(stated) > 0 {
			return "", nil, view.Errorf("core.profile.noplugin",
				"nothing says which plugin %s is about",
				strings.Join(stated, ", ")).
				WithHint("add `--plugin <name>` — a profile configures each plugin separately")
		}
		return "", nil, nil
	}
	if cmd.Flags().Changed("kube") && cmd.Flags().Changed("ssh") {
		return "", nil, view.Errorf("core.profile.tunnel.twice",
			"--kube and --ssh both name a forward, and a call opens one").
			WithHint("keep whichever names where this connection really goes")
	}
	if cmd.Flags().Changed("direct") && (cmd.Flags().Changed("kube") || cmd.Flags().Changed("ssh")) {
		return "", nil, view.Errorf("core.profile.tunnel.direct",
			"--direct removes the forward that --kube or --ssh states").
			WithHint("state one or the other")
	}
	if cmd.Flags().Changed("unfence") && cmd.Flags().Changed("only") {
		return "", nil, view.Errorf("core.profile.only.both",
			"--unfence removes the fence that --only states").
			WithHint("state one or the other")
	}

	key = pinKey(want, installed)
	if p.Plugins == nil {
		p.Plugins = map[string]config.Connection{}
	}
	ns, instance, _ := config.SplitKey(key)
	// The same refusal Check reports and Lookup enforces, made before the
	// key lands in the file — an operator scripting their setup is not
	// watching a report.
	if instance != "" && !config.ValidInstance(instance) {
		return "", nil, view.Errorf("core.profile.instance.invalid",
			"%q is not a valid instance label", instance).
			WithHint("lowercase letters, digits and dashes, starting with a letter — " +
				"`--plugin " + ns + "/analytics` is the shape")
	}
	// Whatever is already there, so a run that states one block leaves the
	// others exactly as they were.
	conn := p.Plugins[key]
	// **Re-pinned rather than added.** A profile configures a namespace once —
	// For() returns the first entry it finds and every reader downstream
	// assumes there is one — so a second key for the same namespace is not a
	// second configuration. It is the operator's blocks stranded on the old
	// pin while the new key resolves to nothing.
	//
	// It is also the ordinary consequence of the command somebody runs after
	// rebuilding a plugin, and rebuilding is routine here: trust is keyed on
	// the artifact digest, so every rebuild leaves every pin in every profile
	// stale at once, and `--plugin pg` is what pointing the profile at the new
	// artifact looks like. Adding a second entry made that command produce a
	// profile with the configuration on the dead half and nothing on the live
	// one, reported as neither.
	// Scoped to the same instance: repinning pg must not swallow
	// pg/analytics' blocks, and stating a new labeled instance must not
	// migrate the default's configuration onto it — ForInstance is the
	// entry this run is actually about, where For would resolve a sole
	// labeled entry as the namespace's answer.
	var repinnedFrom string
	if _, here := p.Plugins[key]; !here {
		if prevKey, prev, found := p.ForInstance(ns, instance); found && prevKey != key {
			conn, repinnedFrom = prev, prevKey
		}
	}

	if direct, _ := cmd.Flags().GetBool("direct"); direct {
		// tunnelTLS: true states something about the forward direct just
		// removed — left behind, it is not a stale value sitting quietly, it
		// is a connection CheckConnection now refuses to resolve at all,
		// over a flag that never mentioned it.
		conn.Kube, conn.SSH, conn.TunnelTLS = "", "", false
	}
	if cmd.Flags().Changed("kube") {
		conn.Kube, conn.SSH = strings.TrimSpace(mustString(cmd, "kube")), ""
	}
	if cmd.Flags().Changed("ssh") {
		conn.SSH, conn.Kube = strings.TrimSpace(mustString(cmd, "ssh")), ""
	}
	if cmd.Flags().Changed("tunnel-tls") {
		conn.TunnelTLS, _ = cmd.Flags().GetBool("tunnel-tls")
	}

	// What a restated block leaves behind. Stating the whole block is what
	// makes the command idempotent, and it is also how somebody changing a
	// host loses the `sslmode: require` line sitting beside it — a security
	// setting, gone, because they restated one key of four.
	//
	// Not a reason to merge instead: a merge makes a key impossible to remove
	// and stops a script being the source of truth for what it states. It is
	// a reason to say so, the same way the forward repair below does. A loss
	// an operator can see is a different thing from one they cannot.
	var dropped, removedFence []string
	if cmd.Flags().Changed("set") {
		pairs, _ := cmd.Flags().GetStringArray("set")
		set, verr := parseSetFlags(pairs, ns, reg)
		if verr != nil {
			return "", nil, verr
		}
		dropped = append(dropped, missingKeys(conn.Set, set, "set.")...)
		conn.Set = set
	}
	// The fence states the whole block as `--set` does, for the same reason: a
	// script is the source of truth for what it states, and a fence is the one
	// block where a key quietly left behind is a wall nobody remembers writing —
	// so what a restatement drops is said, as it is for `set:`.
	//
	// But not in the same words. A default that goes is a value nobody reads any
	// more; a fence that goes is a boundary somebody wrote, and a script that
	// adds a second key by restating only that one loosens the first. It is said
	// as `--unfence` says it, where a reader looks for a lock coming off.
	if cmd.Flags().Changed("only") {
		pairs, _ := cmd.Flags().GetStringArray("only")
		only, verr := parseOnlyFlags(pairs, ns, reg)
		if verr != nil {
			return "", nil, verr
		}
		removedFence = append(removedFence, missingKeys(conn.Only, only, "only.")...)
		conn.Only = only
	}
	if unfenced, _ := cmd.Flags().GetBool("unfence"); unfenced {
		removedFence = append(removedFence, missingKeys(conn.Only, nil, "only.")...)
		conn.Only = nil
	}
	if cmd.Flags().Changed("secret") {
		pairs, _ := cmd.Flags().GetStringArray("secret")
		secrets, verr := parseSecretFlags(pairs)
		if verr != nil {
			return "", nil, verr
		}
		was := make(map[string]any, len(conn.Secrets))
		for k := range conn.Secrets {
			was[k] = nil
		}
		now := make(map[string]any, len(secrets))
		for k := range secrets {
			now[k] = nil
		}
		dropped = append(dropped, missingKeys(was, now, "secrets.")...)
		conn.Secrets = secrets
	}
	sort.Strings(dropped)

	// A forward fills the endpoint inputs itself, laying them over whatever
	// `set:` said, so a stated host beside a coordinate is a line no run will
	// ever read — and CheckConnection refuses the pair rather than tolerate
	// it. Which leaves the question of what `--kube` alone should do to a key
	// the file already held.
	//
	// Removed, and said out loud. The alternative is refusing to add a
	// forward because of a key set in some earlier command, which is a
	// refusal about a line the operator is not looking at and cannot remove
	// with the flag they are holding — `--set` states the whole block, so
	// clearing one key means restating the others.
	//
	// **Only what was carried over.** A key this same invocation stated is
	// refused, not repaired: `--kube X --set host=y` is two contradictory
	// statements in one breath, and quietly dropping one of them would be
	// discarding a flag somebody just typed. The two cases are exactly
	// "did this run restate the block", which is what Changed answers.
	freed := freeEndpointKeys(&conn, ns, reg,
		!cmd.Flags().Changed("set"), !cmd.Flags().Changed("only"), !cmd.Flags().Changed("secret"))

	// **The same rules the report uses, before the write rather than after
	// it.** internal/profile.CheckConnection is what `rta profile list`,
	// `rta doctor` and the TUI all call; a second implementation here would
	// eventually disagree with them, and the disagreement would be a
	// connection this command called fine and every run refused.
	if problems := profile.CheckConnection(name, key, conn, installed); len(problems) > 0 {
		return "", nil, view.Errorf("core.profile.unusable", "%s", problems[0].Reason).
			WithHint(problems[0].Hint)
	}
	if repinnedFrom != "" {
		delete(p.Plugins, repinnedFrom)
	}
	p.Plugins[key] = conn
	if repinnedFrom != "" {
		receipts = append(receipts, view.Pair{Key: "re-pinned",
			Value: repinnedFrom + " → " + key +
				" — the artifact changed, and this profile's blocks moved with it"})
	}
	// Two different losses, so two different sentences. A key the forward
	// makes dead and a key a restatement left out are both the operator's
	// line going away, and telling them apart is the difference between "that
	// is fine" and "put it back".
	if len(dropped) > 0 {
		receipts = append(receipts, view.Pair{Key: "dropped",
			Value: strings.Join(dropped, ", ") + " — the flags state the whole block, " +
				"and these were not among them"})
	}
	// A fence a forward made dead is not a value nothing read: it was a
	// boundary, and "nothing read them" would say it cost nothing. It goes
	// where a fence taken off by hand goes, and says what to hold in its place.
	var freedValues []string
	for _, k := range freed {
		if strings.HasPrefix(k, "only.") {
			removedFence = append(removedFence, k)
		} else {
			freedValues = append(freedValues, k)
		}
	}
	if len(freedValues) > 0 {
		receipts = append(receipts, view.Pair{Key: "removed",
			Value: strings.Join(freedValues, ", ") + " — the forward fills those, so nothing read them"})
	}
	if len(removedFence) > 0 {
		sort.Strings(removedFence)
		receipts = append(receipts, view.Pair{Key: "unfenced",
			Value: strings.Join(removedFence, ", ") + " — calls through this connection are no longer held to those lists"})
	}
	return key, receipts, nil
}

// missingKeys names the keys of before that after does not have, prefixed.
func missingKeys(before, after map[string]any, prefix string) []string {
	var out []string
	for k := range before {
		if _, kept := after[k]; !kept {
			out = append(out, prefix+k)
		}
	}
	return out
}

// freeEndpointKeys drops the keys a forward makes dead, and reports them.
//
// Scoped by the two booleans to what this run did not state: see the comment
// at the call site. Nothing happens at all when the connection opens no
// forward, which is why `--direct` needs no counterpart — clearing the
// coordinate simply makes those keys live again.
func freeEndpointKeys(conn *config.Connection, ns string, reg *registry.Registry,
	setCarried, onlyCarried, secretsCarried bool,
) []string {
	if !conn.Tunnelled() {
		return nil
	}
	var freed []string
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Endpoint == plugin.EndpointNone || !plugin.ProfileFillable(c, f) {
				continue
			}
			// `set:` is keyed by Field.Config and `secrets:` by Field.Name.
			// Two keyspaces for the same input, which is why this walks the
			// declarations rather than either map.
			if setCarried && f.Config != "" {
				if _, stated := conn.Set[f.Config]; stated {
					delete(conn.Set, f.Config)
					freed = append(freed, "set."+f.Config)
				}
			}
			// A fence on a value the forward fills is dead the same way, and
			// repaired the same way when this run did not restate it.
			if onlyCarried && f.Config != "" {
				if _, fenced := conn.Only[f.Config]; fenced {
					delete(conn.Only, f.Config)
					freed = append(freed, "only."+f.Config)
				}
			}
			if secretsCarried {
				if _, mapped := conn.Secrets[f.Name]; mapped {
					delete(conn.Secrets, f.Name)
					freed = append(freed, "secrets."+f.Name)
				}
			}
		}
	}
	if len(conn.Set) == 0 {
		conn.Set = nil
	}
	if len(conn.Only) == 0 {
		conn.Only = nil
	}
	if len(conn.Secrets) == 0 {
		conn.Secrets = nil
	}
	sort.Strings(freed)
	return freed
}

// parseSetFlags turns `--set key=value` into the `set:` block, typed against
// what the namespace declares.
//
// Two things happen here that cannot happen in a file.
//
// A key naming a declared credential is refused outright. A `plugin.Secret`
// input can never be a config key — pkg/plugin.Validate refuses that
// declaration, because a config file is plaintext read on every invocation —
// so a value under one of those names is inert wherever it lands. The file
// path finds that out when the connection fails to authenticate; here it is
// the first thing checked, and the message names the flag that does work.
//
// And a value is written as the type the field declares. YAML types a bare
// word for you and every surface downstream reads with a type assertion, so
// `port: "5432"` reaches the handler as 0 and `tls: "true"` as false — the
// failure plugin.StatedTypeProblem now reports about a hand-written file.
// A flag argument is always text, so this is the one place that decides, and
// writing the string would produce that exact bug on every run.
func parseSetFlags(pairs []string, ns string, reg *registry.Registry) (map[string]any, *view.Error) {
	byConfig, secrets := declaredFor(ns, reg)

	order, raw, verr := collectPairs(pairs, secrets, flagWords{
		flag: "set", example: "host=db.internal", shape: "core.profile.set.shape",
		secret: func(k string) *view.Error {
			return view.Errorf("core.profile.set.secret",
				"%q is a credential, and `--set` writes values into %s in plaintext",
				k, config.Path()).
				WithHint("store it and reference it instead: `rta kv set " + ns + "-" + k +
					" --file <path>` then `--secret " + k + "=kv:" + ns + "-" + k + "`")
		},
		// Never silently dropped. `--set` states the whole block, so an empty
		// value is either a key meant to be removed — which is done by
		// omitting it — or a variable that expanded to nothing in the script
		// that called this, which is worth finding out about.
		empty: func(k string) *view.Error {
			return view.Errorf("core.profile.set.empty",
				"`--set %s=` states no value", k).
				WithHint("`--set` states the whole block, so leave a key out to remove it — " +
					"and check the variable this came from expanded")
		},
	})
	if verr != nil {
		return nil, verr
	}

	set := make(map[string]any, len(order))
	for _, k := range order {
		values := raw[k]
		f, declared := byConfig[k]
		if !declared {
			// Left as text for CheckConnection to refuse in its own words,
			// which name the capability and point at `rta explain`. Guessing
			// a type for a key nothing reads would be inventing a fact about
			// a field that does not exist.
			set[k] = values[len(values)-1]
			continue
		}
		if !f.Type.Repeatable() && len(values) > 1 {
			return nil, view.Errorf("core.profile.set.repeated",
				"`--set %s` is given more than once, and %s takes one value", k, k).
				WithHint("repeating a key states a list, and only a list-shaped input takes one")
		}
		v, verr := typedSetValue(f, k, values)
		if verr != nil {
			return nil, verr
		}
		if v, verr = heldToDeclaration(f, v); verr != nil {
			return nil, verr
		}
		set[k] = v
	}
	if len(set) == 0 {
		return nil, nil
	}
	return set, nil
}

// flagWords is what differs between the two flags that state a block of
// `key=value` pairs — `--set` and `--only`: how they name themselves, and the
// two refusals whose words are about what the block is.
type flagWords struct {
	flag, example, shape string
	secret, empty        func(key string) *view.Error
}

// collectPairs splits `key=value` arguments into the keys in the order they
// were first stated and the values each was given, refusing the shapes neither
// block can hold.
//
// Collected before any of it is typed, because a StringSlice input is stated by
// repeating the key and the whole list has to be in hand first — and an
// allowlist is stated the same way, one argument per allowed value.
func collectPairs(pairs []string, secrets map[string]bool, w flagWords) ([]string, map[string][]string, *view.Error) {
	order := make([]string, 0, len(pairs))
	raw := map[string][]string{}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			// The argument is deliberately not echoed. Somebody who typed a
			// value where a pair belongs has already put it on a command
			// line; rta will not put it anywhere else.
			return nil, nil, view.Errorf(w.shape,
				"one --%s argument is not a `key=value` pair", w.flag).
				WithHint("write it as `--" + w.flag + " " + w.example + "`")
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, nil, view.Errorf(w.shape, "one --%s argument names no key", w.flag).
				WithHint("write it as `--" + w.flag + " " + w.example + "`")
		}
		if secrets[k] {
			return nil, nil, w.secret(k)
		}
		if v == "" {
			return nil, nil, w.empty(k)
		}
		if _, seen := raw[k]; !seen {
			order = append(order, k)
		}
		raw[k] = append(raw[k], v)
	}
	return order, raw, nil
}

// parseOnlyFlags turns `--only key=value` into the `only:` block: for each key
// the list of values the connection reaches, each typed against the declaration
// and held to it as `--set` holds its value.
//
// A key is stated by repeating it, one argument per allowed value, which is the
// way a list-shaped `--set` is stated and for the same reason: a value is not a
// list of flags, and splitting on a comma would turn `--only name=a,b` into one
// value nobody listed. Every entry is typed as one element of the input — a
// list-shaped input is fenced a name at a time (plugin.FenceElement) — so
// `--only port=5432 --only port=5433` is two integers, not a list as a value.
func parseOnlyFlags(pairs []string, ns string, reg *registry.Registry) (map[string]any, *view.Error) {
	byConfig, secrets := declaredFor(ns, reg)
	order, raw, verr := collectPairs(pairs, secrets, flagWords{
		flag: "only", example: "namespace=staging", shape: "core.profile.only.shape",
		secret: func(k string) *view.Error {
			return view.Errorf("core.profile.only.secret",
				"%q is a credential, and an allowlist for it would be written into %s in plaintext",
				k, config.Path()).
				WithHint("a fence is for a value the profile states; a credential is mapped " +
					"with `--secret " + k + "=kv:<entry>`")
		},
		empty: func(k string) *view.Error {
			return view.Errorf("core.profile.only.empty",
				"`--only %s=` lists no value", k).
				WithHint("`--only` states the whole block, so leave a key out to remove its fence — " +
					"and check the variable this came from expanded")
		},
	})
	if verr != nil {
		return nil, verr
	}
	only := make(map[string]any, len(order))
	for _, k := range order {
		f, declared := byConfig[k]
		if !declared {
			// Left as text for CheckConnection to refuse in its own words, as
			// `--set` leaves a key nothing reads.
			entries := make([]any, len(raw[k]))
			for i, v := range raw[k] {
				entries[i] = v
			}
			only[k] = entries
			continue
		}
		ef := plugin.FenceElement(f)
		entries := make([]any, 0, len(raw[k]))
		for _, v := range raw[k] {
			typed, verr := typedSetValue(ef, k, []string{v})
			if verr != nil {
				return nil, asOnly(verr)
			}
			if typed, verr = heldToDeclaration(ef, typed); verr != nil {
				return nil, asOnly(verr)
			}
			entries = append(entries, typed)
		}
		only[k] = entries
	}
	if len(only) == 0 {
		return nil, nil
	}
	return only, nil
}

// asOnly rewords a refusal typedSetValue and heldToDeclaration worded for
// `--set`: the same rule about what a value of that type may be, said about the
// flag the person actually typed.
func asOnly(verr *view.Error) *view.Error {
	out := *verr
	if code, known := onlyCodes[out.Code]; known {
		out.Code = code
	}
	out.Hint = strings.ReplaceAll(out.Hint, "`--set ", "`--only ")
	return &out
}

// onlyCodes is the code each of `--set`'s typing refusals has when `--only`
// raises it — spelled out, because a code a script matches on is one somebody
// has to be able to find by searching for it.
var onlyCodes = map[string]string{
	"core.profile.set.type":   "core.profile.only.type",
	"core.profile.set.range":  "core.profile.only.range",
	"core.profile.set.option": "core.profile.only.option",
}

// heldToDeclaration refuses a value no capability reading the key would run
// with — outside every range, or naming none of the options — and writes an
// option typed in another case the way the field declares it. Written without
// one of those checks, the profile is saved and every call made through it is
// then refused, by which time nobody is looking at the command that caused it.
//
// f is the key's pluginconf.SharedField, not one reader's: a key serves every
// capability in the namespace that declares it, and each holds a number from
// the profile to its own range. Held to whichever field declaredFor kept last
// by ID, `--set timeout=90` for net was refused as net.trace's 1..60 though
// net.ping takes 300, and `--set limit=800` for fs was written against
// fs.usage's 1000 and then refused on every fs.tree call.
//
// Like badSetValue, never repeating the value, and naming the key as it was
// typed: f.Name is one reader's input, and `--set ping.count=101` was told
// "count takes a value from 1 to 100".
func heldToDeclaration(f plugin.Field, v any) (any, *view.Error) {
	if want, ok := f.Range(v); !ok {
		return nil, view.Errorf("core.profile.set.range", "%s takes a value %s", f.Config, want).
			WithHint("that is the widest range of any capability reading " + f.Config +
				", and each holds a value to its own inside it; `rta explain` on one names its range")
	}
	if len(f.Options) == 0 {
		return v, nil
	}
	refuse := view.Errorf("core.profile.set.option", "%s takes one of %s", f.Config, strings.Join(f.Options, ", ")).
		WithHint("the set is closed; write the value as one of those")
	switch t := v.(type) {
	case string:
		o, ok := f.CanonicalOption(t)
		if !ok {
			return nil, refuse
		}
		return o, nil
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			o, ok := f.CanonicalOption(s)
			if !ok {
				return nil, refuse
			}
			out[i] = o
		}
		return out, nil
	}
	return v, nil
}

// typedSetValue turns the text a flag carries into the type f declares. key
// is what was typed before the `=`: a config key for a profile, an input name
// for a tile.
func typedSetValue(f plugin.Field, key string, values []string) (any, *view.Error) {
	raw := values[len(values)-1]
	switch f.Type {
	case plugin.StringSlice, plugin.SecretSlice:
		return append([]string{}, values...), nil
	case plugin.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			want := "a whole number"
			switch bounds := f.Bounds(); {
			case bounds != "":
				want += " " + bounds
			case errors.Is(err, strconv.ErrRange):
				// Digits, and too many of them, on either side of zero:
				// "takes a whole number" is what the person already wrote.
				want += " from " + strconv.Itoa(math.MinInt) + " to " + strconv.Itoa(math.MaxInt)
			}
			return nil, badSetValue(f, key, want, setExample(f, "1"))
		}
		return n, nil
	case plugin.Float:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			want := "a number"
			if bounds := f.Bounds(); bounds != "" {
				want += " " + bounds
			}
			return nil, badSetValue(f, key, want, setExample(f, "1.5"))
		}
		return n, nil
	case plugin.Duration:
		if _, err := plugin.ParseDuration(raw); err != nil {
			want := "a duration"
			if bounds := f.Bounds(); bounds != "" {
				want += " " + bounds
			}
			return nil, badSetValue(f, key, want, setExample(f, "30s"))
		}
		return raw, nil
	case plugin.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			// `yes` and `on` land here, and that is the point: they are what
			// somebody writes when they mean true, and YAML 1.2 reads both as
			// text — so the file spelling of this mistake silently disables
			// whatever the field turns on.
			return nil, badSetValue(f, key, "`true` or `false`", "true")
		}
		return b, nil
	case plugin.Secret:
		// Unreachable: a Secret cannot be a config key, so it cannot be found
		// by config key here. Kept because "unreachable" is a claim about
		// another package, and the cost of being wrong is a credential in a
		// plaintext file.
		return nil, view.Errorf("core.profile.set.secret",
			"%q is a credential and cannot be stated with --set", f.Name).
			WithHint("store it and reference it with `--secret`")
	}
	return raw, nil
}

// badSetValue reports a value the declared type cannot hold, without ever
// repeating the value: this runs over something somebody typed on a command
// line, and the reason it did not parse is not a reason to print it again.
func badSetValue(f plugin.Field, key, want, example string) *view.Error {
	return view.Errorf("core.profile.set.type",
		"%s is declared %s, and takes %s", key, f.Type, want).
		WithHint("write it as `--set " + key + "=" + example + "`")
}

// setExample is a value for a hint to show, one heldToDeclaration then takes:
// the declared default, or the edge of the range, or fallback. The hint said
// `--set timeout=5432` for every integer, and following it for net's timeout
// (1 to 300) was refused as out of range.
func setExample(f plugin.Field, fallback string) string {
	for _, v := range []any{f.Default, f.Min, f.Max} {
		if _, ok := f.Range(v); v != nil && ok {
			return plugin.NumberText(v)
		}
	}
	return fallback
}

// parseSecretFlags turns `--secret input=ref` into the `secrets:` block.
//
// The scheme is what makes this a reference rather than a value, so a missing
// one is refused here rather than left to the loader — and refused **without
// echoing the argument**, because the single most likely thing to be sitting
// where a scheme should be is the credential itself.
func parseSecretFlags(pairs []string) (map[string]string, *view.Error) {
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		input, ref, ok := strings.Cut(pair, "=")
		input = strings.TrimSpace(input)
		if !ok || input == "" {
			return nil, view.Errorf("core.profile.secret.shape",
				"one --secret argument is not an `input=reference` pair").
				WithHint("write it as `--secret password=kv:staging-db-password`")
		}
		out[input] = strings.TrimSpace(ref)
	}
	// Asked the way the loader asks it, so the two cannot disagree about what
	// counts as a source.
	if bad := (config.Connection{Secrets: out}).BadSecretRefs(); len(bad) > 0 {
		named := make([]string, 0, len(bad))
		for _, b := range bad {
			named = append(named, strings.TrimPrefix(b, "secrets."))
		}
		return nil, view.Errorf("core.profile.secret.value",
			"--secret takes a reference to a stored credential, not the credential: %s",
			strings.Join(named, ", ")).
			WithHint("store it first — `rta kv set <entry> --file <path>` — then " +
				"`--secret <input>=kv:<entry>`. If what you passed was the credential " +
				"itself, it is in this shell's history now")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// declaredFor is what a namespace offers a profile: its config keys, each as
// what every capability reading it accepts (pluginconf.SharedField), and the
// names of the credentials that must never be one.
//
// The credential half is asked exactly as `rta profile show` asks it, which
// is the list that command redacts — so what this refuses to write and what
// that refuses to print are the same set. That is why this reads the registry
// the command was built over rather than the `installed` package variable
// used for the checks below: those two are the same object on every path
// (NewRoot sets one from the other), and where they could ever differ, the
// refusal should agree with the page that displays the result.
func declaredFor(ns string, reg *registry.Registry) (map[string]plugin.Field, map[string]bool) {
	readers := map[string][]plugin.Field{}
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Config != "" && plugin.ProfileFillable(c, f) {
				readers[f.Config] = append(readers[f.Config], f)
			}
		}
	}
	byConfig := make(map[string]plugin.Field, len(readers))
	for key, fields := range readers {
		byConfig[key] = pluginconf.SharedField(fields)
	}
	secrets := map[string]bool{}
	for _, f := range profileSecrets(ns, reg) {
		secrets[f] = true
	}
	return byConfig, secrets
}

// pinKey is the plugins: key a profile entry must carry for what the operator
// named: bare for a built-in, `<namespace>@<digest>` for anything on $PATH.
//
// Resolved from the registry rather than demanded, because a digest is not
// something anybody should be asked to type and typing one wrong is exactly
// the failure the pin exists to prevent. That is not a weakening: the pin
// stops a $PATH impostor from *later* claiming a namespace the operator
// configured, and rta reading the digest of the artifact registered right now
// is the same answer the operator would have copied out of `rta plugin list`.
//
// A key the operator pinned themselves is left alone, so a deliberate pin at
// an artifact that is not the installed one still reaches checkPin and is
// still refused by name.
func pinKey(want string, inst profile.Installed) string {
	if strings.Contains(want, "@") || inst == nil {
		return want
	}
	// `--plugin pg/analytics` names an instance; the pin belongs to the
	// namespace half, and the label rides through into the key unchanged.
	ns, _, _ := config.SplitKey(want)
	if o, known := inst.Origin(ns); known && o.External() {
		return want + "@" + o.Short()
	}
	return want
}

func runProfileRemove(cmd *cobra.Command, name string, reg *registry.Registry, dryRun, yes bool) (view.View, *view.Error) {
	if verr := refuseUnhonouredConfig(); verr != nil {
		return nil, verr
	}
	want := strings.TrimSpace(mustString(cmd, "plugin"))
	// Asked before the write: once the profile is gone nothing states it, and
	// the answer is config.yaml whichever drop-in it was removed from.
	from := config.Where("profiles", name)
	var (
		verr    *view.Error
		key     string
		current config.Profile
		scope   config.Config
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		scope = cfg
		p, known := cfg.Profiles[name]
		if !known {
			verr = unknownProfile(cfg, name)
			return cfg, false
		}
		if want == "" {
			current = p
			if dryRun {
				return cfg, false
			}
			// Removing the whole environment also switches it off if it was
			// on and revokes every grant naming it — a wider blast radius
			// than the --plugin form below, which only drops one map entry.
			if !yes {
				verr = &view.Error{
					Code:    CodeConfirmRequired,
					Message: "removing " + name + " also revokes every grant naming it and needs confirmation",
					Hint:    "re-run with --yes to confirm, or --dry-run to preview",
				}
				return cfg, false
			}
			delete(cfg.Profiles, name)
			return cfg, true
		}
		key = pinKey(want, installed)
		if _, has := p.Plugins[key]; !has {
			hint := name + " configures nothing"
			if keys := p.PluginKeys(); len(keys) > 0 {
				hint = name + " configures: " + strings.Join(keys, ", ")
			}
			verr = view.Errorf("core.profile.noentry",
				"%s has no entry for %q", name, want).WithHint(hint)
			return cfg, false
		}
		delete(p.Plugins, key)
		current = p
		if dryRun {
			return cfg, false
		}
		cfg.Profiles[name] = p
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.profile.write")
	}
	if verr != nil {
		return nil, verr
	}

	removeLabel := "removed"
	if dryRun {
		removeLabel = "would remove"
	}
	if want != "" {
		written := current
		if !dryRun {
			after, err := config.LoadFile()
			if err != nil {
				return nil, view.AsError(err, "core.profile.config")
			}
			written, scope = after.Profiles[name], after
		}
		card := profileCard(scope, name, written, reg)
		card.Pairs = append([]view.Pair{
			{Key: removeLabel, Value: key + " from " + name}}, card.Pairs...)
		return card, nil
	}

	pairs := []view.Pair{{Key: removeLabel, Value: "profile " + name + " from " + from}}
	// A screen written in the entry is part of it and is not kept anywhere
	// else: said, so removing an environment is not also quietly removing the
	// tiles somebody arranged for it.
	if current.Dashboard.Inline != nil {
		went := "went with it"
		if dryRun {
			went = "would go with it"
		}
		block := current.Dashboard.Inline
		pairs = append(pairs, view.Pair{Key: "dashboard", Value: "its own screen, " +
			format.Count(len(block.Tiles)+len(block.Add), "tile", "tiles") + ", " + went})
	}
	// The switch follows, or this machine stays switched on to a name nothing
	// can look up — and because the selection also bounds agents, every agent
	// call would then be refused against it.
	if sel := profile.LoadSelection(); sel.Active == name {
		if dryRun {
			pairs = append(pairs, view.Pair{Key: "would switch off",
				Value: "it is on — commands would fall back to the base configuration"})
		} else {
			if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
				return nil, verr
			}
			pairs = append(pairs, view.Pair{Key: "switched off",
				Value: "it was on — commands run against the base configuration"})
		}
	}
	if dryRun {
		if n := countActiveGrantsFor(name); n > 0 {
			pairs = append(pairs, view.Pair{Key: "would revoke",
				Value: format.Count(n, "grant", "grants") + " naming it, which would then authorize nothing"})
		}
	} else if n := grant.RevokeProfile(name, time.Now()); n > 0 {
		pairs = append(pairs, view.Pair{Key: "revoked",
			Value: format.Count(n, "grant", "grants") + " naming it, which would have authorized nothing"})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// countActiveGrantsFor previews grant.RevokeProfile's own count without
// writing anything — the same filter (by the name half: an instance like
// "staging/analytics" counts under "staging"), read-only.
func countActiveGrantsFor(name string) int {
	stored, verr := grant.Load()
	if verr != nil {
		return 0
	}
	now := time.Now()
	n := 0
	for _, g := range stored {
		if config.RefName(g.Profile) == name && g.Active(now) {
			n++
		}
	}
	return n
}

// refuseUnhonouredConfig stops a write that would land in a file nothing
// reads back.
//
// config.Path falls back to ./.rta.yaml when the operator has no config
// directory — ordinary under `env -i`, inside a container and in CI, which is
// precisely where a script is running. A profile written there is loaded and
// then ignored, because a working-directory config file could otherwise ship
// a profile called "prod" in a cloned repository. Succeeding silently would
// make this command's whole purpose — being scriptable — the case it gets
// wrong.
func refuseUnhonouredConfig() *view.Error {
	if config.TrustedPath() {
		return nil
	}
	return view.Errorf("core.profile.untrusted",
		"profiles in %s are not honoured, so writing one there would do nothing", config.Path()).
		WithHint("set $RTA_CONFIG to the file you mean — a config file found in the working " +
			"directory could belong to a repository you cloned, so its profiles are ignored")
}

// completeInstalledPlugins offers what a profile entry may name, pinned.
func completeInstalledPlugins(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if installed == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var out []cobra.Completion
	for _, c := range installed.Capabilities() {
		ns := plugin.Namespace(c.ID)
		if seen[ns] {
			continue
		}
		seen[ns] = true
		// The bare namespace, because pinKey resolves it. Offering a digest
		// to type would be offering the mistake back.
		out = append(out, ns)
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func mustBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

// missingSlots names the slots of before that after does not state, as the
// file spells them (`theme.good`), sorted.
func missingSlots(before, after map[string]string) []string {
	var out []string
	for slot := range before {
		if _, kept := after[slot]; !kept {
			out = append(out, "theme."+slot)
		}
	}
	sort.Strings(out)
	return out
}
