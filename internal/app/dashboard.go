package app

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/tui"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta dashboard` — the TUI's landing screen without a terminal.
//
// # Why this exists
//
// The dashboard is arranged from inside the dashboard: H hides a tile, `[`
// and `]` move one, the inventory pane brings a hidden one back. What none
// of those could do was put a capability on it that the automatic set
// leaves out — every kube, pg, s3 and vault capability declines to run
// unasked — or put one on it twice, once per cluster. That took a `tiles:`
// list written by hand, which freezes the whole dashboard, and a hand is
// also what a dotfiles repository or a provisioning script does not have.
// This is the same gap `rta profile set` closed for environments, closed
// the same way: flags, a refusal for every shape the file would have
// accepted and quietly got wrong, and a write under the config lock.
//
// # `add`, and still idempotent
//
// A second `add` of the same tile — the same capability against the same
// profile — replaces the entry rather than adding a twin, so a script that
// runs on every boot leaves one tile, not one per boot. It is still called
// add rather than set because that is what it does to the dashboard: the
// automatic set stays, and this joins it.
//
// # What it refuses
//
// A capability that is not a read: a tile runs on load and again on a
// timer, with no form and no confirmation, and buildTiles already drops
// such an entry from the file — this refuses it before it is written,
// where the person can still see the answer. A `--set` naming a credential,
// for the reason `rta profile set` gives: the file is plaintext. A required
// input nothing fills: the tile would draw the same "missing input" error
// on every refresh forever, with nobody to ask. A profile the capability
// cannot use, or that does not cover its plugin: the tile would name prod
// while its numbers came from localhost.
func newDashboardCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
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
		Use:   "dashboard",
		Short: "The TUI's landing screen: what is on it, and what to add",
		Long: "Bare `rta` opens on a dashboard of tiles, one per plugin that can show something" +
			" unasked. Anything that reaches off the box is left off it deliberately; `rta dashboard" +
			" add` is you asking for one, and `--profile` pins it to one connection so the same" +
			" capability can watch two clusters side by side.",
		RunE: groupRunE,
	}
	list := &cobra.Command{
		Use:               "list",
		Short:             "Every tile bare `rta` would draw, and where each came from",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadFile()
			if err != nil {
				return render(cmd, nil, view.AsError(err, "core.dashboard.config"))
			}
			return render(cmd, dashboardTable(reg, cfg.TrustedDashboard()), nil)
		},
	}
	cmd.AddCommand(list, dashboardAddCommand(reg, render, opts), dashboardRemoveCommand(reg, render, opts))
	return cmd
}

func dashboardTable(reg *registry.Registry, dash config.Dashboard) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Tile"},
		{Name: "Profile"},
		{Name: "Source"},
		{Name: "Re-runs"},
		{Name: "With"},
	}}
	for _, p := range tui.Layout(reg, dash) {
		every := "every few seconds"
		if p.Refresh > 0 {
			every = "every " + pace(p.Refresh)
		}
		t.Rows = append(t.Rows, []string{p.ID, p.Profile, p.Source, every, withLine(p.With)})
	}
	t.Total = len(t.Rows)
	return t
}

// withLine spells a tile's inputs the way `--set` states them.
func withLine(with map[string]any) string {
	if len(with) == 0 {
		return ""
	}
	keys := make([]string, 0, len(with))
	for k := range with {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, with[k]))
	}
	return strings.Join(parts, " ")
}

func dashboardAddCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <capability>",
		Short: "Put a capability on the landing screen, pinned to a profile or following the switch",
		Long: "Joins a tile to the automatic dashboard without freezing it: plugins installed later" +
			" still appear. `--profile` pins the tile to one connection, so `rta dashboard add" +
			" kube.overview --profile prod` and the same with `--profile staging` are two tiles," +
			" each named on its own panel. Without it the tile follows whatever `rta use` switched on." +
			" `--set` fills the capability's inputs, `key=value`, typed the way it declares them." +
			" Adding the same tile again replaces it, so the command is safe to script.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeReadCapabilities(reg),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runDashboardAdd(cmd, args[0], reg, opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("profile", "", "pin the tile to this connection: a profile name, or name/instance")
	cmd.Flags().StringSlice("set", nil, "an input for the run, `key=value`; repeat for several")
	cmd.Flags().Int("span", 0, "grid columns the tile occupies; 0 leaves it to the capability")
	_ = cmd.RegisterFlagCompletionFunc("profile", completeProfiles)
	_ = cmd.RegisterFlagCompletionFunc("set", completeTileInputs(reg))
	return cmd
}

func dashboardRemoveCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <capability>",
		Aliases: []string{"remove"},
		Short:   "Take an added tile off the landing screen",
		Long: "Withdraws the entry `rta dashboard add` wrote. `--profile` names which one when the" +
			" capability was added against several. An automatic tile is not added and is not" +
			" removed here: H on it in the TUI hides it, and the inventory pane brings it back.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeAddedTiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runDashboardRemove(cmd, args[0], opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("profile", "", "the pinned tile to take down, when the capability was added against several")
	_ = cmd.RegisterFlagCompletionFunc("profile", completeProfiles)
	_ = reg
	return cmd
}

// refuseUnhonouredDashboard is refuseUnhonouredConfig for the dashboard
// block, which TrustedDashboard ignores on the same paths for the same
// reason: a cloned repository's ./.rta.yaml must not arrange a screen.
func refuseUnhonouredDashboard() *view.Error {
	if config.TrustedPath() {
		return nil
	}
	return view.Errorf("core.dashboard.untrusted",
		"the dashboard block in %s is not honoured, so writing to it would do nothing", config.Path()).
		WithHint("set $RTA_CONFIG to the file you mean — a config file found in the working " +
			"directory arranges nothing, because a cloned repository must not")
}

func runDashboardAdd(cmd *cobra.Command, id string, reg *registry.Registry, dryRun bool) (view.View, *view.Error) {
	c, ok := reg.Capability(id)
	if !ok {
		return nil, view.Errorf("core.dashboard.unknown", "no capability named %q", id).
			WithHint("`rta explain` lists every one")
	}
	if c.Safety != plugin.Read {
		return nil, view.Errorf("core.dashboard.notread",
			"%s is not a read, and a tile runs on a timer with no confirmation", id).
			WithHint("only a read can be a tile — `rta explain " + id + "` names its safety class")
	}
	if verr := refuseUnhonouredDashboard(); verr != nil {
		return nil, verr
	}
	ref := strings.TrimSpace(mustString(cmd, "profile"))
	if ref != "" {
		if !plugin.Profilable(c) {
			return nil, view.Errorf("core.dashboard.noprofile",
				"%s takes no connection a profile could fill", id).
				WithHint("leave --profile off — `rta explain " + id + "` lists what it does take")
		}
		cfg, err := config.LoadFile()
		if err != nil {
			return nil, view.AsError(err, "core.dashboard.config")
		}
		// Lookup's own refusals, in its own words: an unknown profile, one
		// from a file nobody named, one that says nothing about this
		// plugin, an instance it does not hold. Each is the exact thing
		// the tile would otherwise say on every refresh.
		if _, verr := profile.Lookup(cfg, c, ref, withTrust{installed}); verr != nil {
			return nil, verr
		}
	}
	pairs, _ := cmd.Flags().GetStringSlice("set")
	with, verr := parseTileInputs(pairs, c)
	if verr != nil {
		return nil, verr
	}
	if verr := tileCanRunUnasked(c, with, ref != ""); verr != nil {
		return nil, verr
	}
	span, _ := cmd.Flags().GetInt("span")
	if span < 0 {
		return nil, view.Errorf("core.dashboard.span", "--span %d is not a number of columns", span).
			WithHint("0 leaves the width to the capability")
	}
	entry := config.Tile{ID: id, With: with, Profile: ref, Span: span}

	var existed, unchanged bool
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		add := cfg.Dashboard.Add
		at := -1
		for i, t := range add {
			if t.Key() == entry.Key() {
				at = i
				break
			}
		}
		existed = at >= 0
		if existed {
			unchanged = reflect.DeepEqual(add[at], entry)
			if unchanged || dryRun {
				return cfg, false
			}
			add[at] = entry
		} else {
			if dryRun {
				return cfg, false
			}
			add = append(add, entry)
		}
		cfg.Dashboard.Add = add
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.dashboard.write")
	}

	verb := "added"
	if existed {
		verb = "updated"
	}
	if dryRun {
		verb = "would have " + verb
	}
	pairsOut := []view.Pair{}
	switch {
	case unchanged:
		pairsOut = append(pairsOut, view.Pair{Key: "unchanged",
			Value: entry.Key() + " is already on the dashboard this way — nothing written to " + config.Path()})
	case dryRun:
		pairsOut = append(pairsOut, view.Pair{Key: "would write", Value: verb + " " + entry.Key() + " in " + config.Path()})
	default:
		pairsOut = append(pairsOut, view.Pair{Key: "wrote", Value: verb + " " + entry.Key() + " in " + config.Path()})
	}
	pairsOut = append(pairsOut, view.Pair{Key: "tile", Value: id})
	if ref != "" {
		pairsOut = append(pairsOut, view.Pair{Key: "profile",
			Value: ref + " — the tile is about this connection whatever `rta use` switched on"})
	} else if plugin.Profilable(c) {
		pairsOut = append(pairsOut, view.Pair{Key: "profile",
			Value: "none — the tile follows whatever `rta use` switched on; --profile <name> pins it"})
	}
	if len(with) > 0 {
		pairsOut = append(pairsOut, view.Pair{Key: "with", Value: withLine(with)})
	}
	every := "every few seconds"
	if c.Refresh > 0 {
		every = "every " + pace(c.Refresh)
	}
	pairsOut = append(pairsOut,
		view.Pair{Key: "re-runs", Value: every + ", for as long as the TUI is open"},
		view.Pair{Key: "on screen", Value: "bare `rta` opens on it; H on the tile or `rta dashboard rm " +
			removeLine(entry) + "` takes it down"})
	return view.KeyValue{Pairs: pairsOut}, nil
}

// removeLine is the `rta dashboard rm` arguments that name this entry.
func removeLine(t config.Tile) string {
	if t.Profile == "" {
		return t.ID
	}
	return t.ID + " --profile " + t.Profile
}

// parseTileInputs turns `--set key=value` into the tile's `with:` block,
// typed against the capability's own declaration — the same reasons
// parseSetFlags gives for a profile's `set:`, applied to a tile's inputs,
// which are keyed by input name rather than by config key because a tile
// fills the run directly.
func parseTileInputs(pairs []string, c plugin.Capability) (map[string]any, *view.Error) {
	byName := make(map[string]plugin.Field, len(c.Inputs))
	for _, f := range c.Inputs {
		byName[f.Name] = f
	}
	order := make([]string, 0, len(pairs))
	raw := map[string][]string{}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, view.Errorf("core.dashboard.set.shape",
				"one --set argument is not a `key=value` pair").
				WithHint("write it as `--set namespace=platform`")
		}
		if k == "profile" {
			return nil, view.Errorf("core.dashboard.set.profile",
				"the profile is not an input of %s", c.ID).
				WithHint("`--profile " + v + "` is how a tile is pinned to one")
		}
		f, declared := byName[k]
		if !declared {
			return nil, view.Errorf("core.dashboard.set.unknown",
				"%s has no input named %q", c.ID, k).
				WithHint("`rta explain " + c.ID + "` lists the inputs it declares")
		}
		if f.Type == plugin.Secret || f.Type == plugin.SecretSlice {
			return nil, view.Errorf("core.dashboard.set.secret",
				"%q is a credential, and a tile's inputs are written into %s in plaintext",
				k, config.Path()).
				WithHint("give the tile a profile whose `secrets:` references it instead: " +
					"`rta profile set <name> --plugin " + plugin.Namespace(c.ID) + " --secret " + k + "=kv:<entry>`")
		}
		if v == "" {
			return nil, view.Errorf("core.dashboard.set.empty", "`--set %s=` states no value", k).
				WithHint("leave the flag out to let the input default — and check the variable this came from expanded")
		}
		if _, seen := raw[k]; !seen {
			order = append(order, k)
		}
		raw[k] = append(raw[k], v)
	}
	if len(order) == 0 {
		return nil, nil
	}
	with := make(map[string]any, len(order))
	for _, k := range order {
		f := byName[k]
		if !f.Type.Repeatable() && len(raw[k]) > 1 {
			return nil, view.Errorf("core.dashboard.set.repeated",
				"`--set %s` is given more than once, and %s takes one value", k, k).
				WithHint("repeating a key states a list, and only a list-shaped input takes one")
		}
		v, verr := typedSetValue(f, raw[k])
		if verr != nil {
			// typedSetValue's refusal is worded for a profile's `set:`,
			// whose keys are config keys; a tile's are input names, and an
			// input with no config key would be told to write `--set =`.
			out := *verr
			out.Code = "core.dashboard.set.type"
			out.Hint = "`rta explain " + c.ID + "` says what " + k + " takes"
			return nil, &out
		}
		with[k] = v
	}
	return with, nil
}

// tileCanRunUnasked refuses a tile that would draw the same "missing input"
// error on every refresh: a required input with no default, no config key
// a file could fill, nothing under --set, and — when the tile is pinned —
// nothing a profile could fill either.
func tileCanRunUnasked(c plugin.Capability, with map[string]any, pinned bool) *view.Error {
	var missing []string
	for _, f := range c.Inputs {
		if !f.Required || f.Default != nil || f.Config != "" {
			continue
		}
		if _, given := with[f.Name]; given {
			continue
		}
		if pinned && plugin.ProfileFillable(c, f) {
			continue
		}
		missing = append(missing, f.Name)
	}
	if len(missing) == 0 {
		return nil
	}
	return view.Errorf("core.dashboard.needs",
		"%s needs %s, and a tile has no form to ask with", c.ID, strings.Join(missing, ", ")).
		WithHint("`--set " + missing[0] + "=<value>` states it for every run")
}

func runDashboardRemove(cmd *cobra.Command, id string, dryRun bool) (view.View, *view.Error) {
	if verr := refuseUnhonouredDashboard(); verr != nil {
		return nil, verr
	}
	key := config.TileKey(id, strings.TrimSpace(mustString(cmd, "profile")))
	var (
		found bool
		keys  []string
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		kept := make([]config.Tile, 0, len(cfg.Dashboard.Add))
		for _, t := range cfg.Dashboard.Add {
			if t.Key() == key {
				found = true
				continue
			}
			keys = append(keys, t.Key())
			kept = append(kept, t)
		}
		if !found || dryRun {
			return cfg, false
		}
		cfg.Dashboard.Add = kept
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.dashboard.write")
	}
	if !found {
		hint := "nothing is added — `rta dashboard list` shows what is on screen and where each tile came from"
		if len(keys) > 0 {
			hint = "added: " + strings.Join(keys, ", ") + " — a pinned one is named with --profile"
		}
		return nil, view.Errorf("core.dashboard.absent", "nothing added as %s", key).WithHint(hint)
	}
	label, verb := "wrote", "removed"
	if dryRun {
		label, verb = "would write", "would have removed"
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: label, Value: verb + " " + key + " from the dashboard in " + config.Path()},
		{Key: "back", Value: "`rta dashboard add " + removeLine(config.Tile{ID: id, Profile: config.RefName(key[len(id):])}) + "`"},
	}}, nil
}

// completeReadCapabilities offers the capabilities a tile can run.
func completeReadCapabilities(reg *registry.Registry) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var out []cobra.Completion
		for _, c := range reg.Capabilities() {
			if c.Safety == plugin.Read {
				out = append(out, cobra.CompletionWithDesc(c.ID, c.Summary))
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeAddedTiles offers what `rm` can take down: the added entries,
// described by their profile so a pinned one is told apart.
func completeAddedTiles(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, err := config.LoadFile()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, t := range cfg.Dashboard.Add {
		desc := "follows the switch"
		if t.Profile != "" {
			desc = "--profile " + t.Profile
		}
		out = append(out, cobra.CompletionWithDesc(t.ID, desc))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeTileInputs offers the named capability's inputs as `--set` keys,
// credentials left out since `--set` refuses them.
func completeTileInputs(reg *registry.Registry) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 || strings.Contains(prefix, "=") {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		c, ok := reg.Capability(args[0])
		if !ok {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, f := range c.Inputs {
			if f.Type == plugin.Secret || f.Type == plugin.SecretSlice {
				continue
			}
			out = append(out, cobra.CompletionWithDesc(f.Name+"=", f.Help))
		}
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
}
