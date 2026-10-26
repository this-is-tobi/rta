package app

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/tui"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
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
		format, err := opts.format()
		if err != nil {
			return err
		}
		renderOpts := renderOptions(cmd, format, opts.noColor)
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
			v, verr := viewOf(cmd, cfg, false)
			if verr != nil {
				return render(cmd, nil, verr)
			}
			return render(cmd, dashboardTable(reg, cfg, v), nil)
		},
	}
	addViewFlag(list, "list the tiles of this view")
	sections := &cobra.Command{
		Use:   "sections",
		Short: "The dashboard's sections: which view draws them, how each starts, and how many tiles are in it",
		Long: "A section draws tiles together under one header that folds. This lists each one every view draws or" +
			" states — its name, how many tiles it holds as bare `rta` would draw them now, whether it starts folded" +
			" and its own column count — the way `rta dashboard views` lists the views. A group the panels of one" +
			" entry make, `pg · staging`, is counted for the environment that is switched on, since that is the one" +
			" it is named after; a section a view states and no tile is in is listed with none.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadFile()
			if err != nil {
				return render(cmd, nil, view.AsError(err, "core.dashboard.config"))
			}
			return render(cmd, sectionsTable(reg, cfg), nil)
		},
	}
	views := &cobra.Command{
		Use:               "views",
		Short:             "The dashboard's views, which profiles link them, and the one drawn now",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadFile()
			if err != nil {
				return render(cmd, nil, view.AsError(err, "core.dashboard.config"))
			}
			return render(cmd, viewsTable(cfg), nil)
		},
	}
	cmd.AddCommand(list, views, sections, dashboardAddCommand(reg, render, opts), dashboardRemoveCommand(render, opts),
		dashboardHideCommand(reg, render, opts, true), dashboardHideCommand(reg, render, opts, false))
	return cmd
}

// layoutNow is what bare `rta` would draw at this moment: this config's
// arrangement, expanded into the switched-on environment's connections.
func layoutNow(reg *registry.Registry, cfg config.Config, v string) []tui.Placement {
	return tui.Layout(reg, cfg.TrustedDashboardFor(v), tui.InstancesOf(cfg, profile.Active()))
}

func dashboardTable(reg *registry.Registry, cfg config.Config, v string) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Tile"},
		{Name: "Profile"},
		{Name: "Section"},
		{Name: "Source"},
		{Name: "Screen"},
		{Name: "Re-runs"},
		{Name: "With"},
	}}
	for _, p := range layoutNow(reg, cfg, v) {
		every := "every " + pace(p.Refresh)
		if p.PaceFrom != "" {
			every += " (" + p.PaceFrom + ")"
		}
		source := p.Source
		if p.Expanded {
			source += ", one of several"
		}
		screen := "on"
		if p.Hidden {
			screen = "hidden"
		}
		t.Rows = append(t.Rows, []string{p.ID, p.Profile, nameCell(p.Section), source, screen, every, withLine(p.With)})
	}
	t.Total = len(t.Rows)
	return t
}

// sectionsTable is every section the configuration draws or states, view by view:
// the default block first and then each view, in the order its sections are drawn,
// and after those the ones the view states and no tile is in, which are listed
// rather than dropped since a name that matches nothing is what a person looks
// this table up to find.
//
// What is counted is what bare `rta` would draw, the way `list` is: a hidden panel
// is not in its section, and a group named after an environment is the switched-on
// environment's. How a section starts and how wide it is are what the view states,
// read from the file whether or not this configuration is one that draws it.
func sectionsTable(reg *registry.Registry, cfg config.Config) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "View"}, {Name: "Section"}, {Name: "Tiles", Kind: view.KindNumber},
		{Name: "Starts"}, {Name: "Width", Kind: view.KindNumber}, {Name: "Height", Kind: view.KindNumber},
		{Name: "Refresh"}, {Name: "File"},
	}}
	t.Empty = "No section is stated or drawn — `rta dashboard add <capability> --section <name>` puts a tile in one."
	for _, name := range append([]string{config.DefaultView}, cfg.ViewNames()...) {
		v, file := name, config.Where("views", name)
		if name == config.DefaultView {
			v, file = "", config.Where("dashboard", "")
		}
		stated := blockOf(cfg, v).Sections
		counts := map[string]int{}
		var drawn []string
		for _, p := range layoutNow(reg, cfg, v) {
			if p.Section == "" || p.Hidden {
				continue
			}
			if counts[p.Section] == 0 {
				drawn = append(drawn, p.Section)
			}
			counts[p.Section]++
		}
		for _, section := range slices.Sorted(maps.Keys(stated)) {
			if counts[section] == 0 {
				drawn = append(drawn, section)
			}
		}
		for _, section := range drawn {
			starts, width, height := "open", "", ""
			if stated[section].Collapsed {
				starts = "folded"
			}
			if n := stated[section].Width; n > 0 {
				width = strconv.Itoa(n)
			}
			if n := stated[section].Height; n > 0 {
				height = strconv.Itoa(n)
			}
			t.Rows = append(t.Rows, []string{nameCell(name), nameCell(section), strconv.Itoa(counts[section]), starts, width, height,
				stated[section].Refresh, configFileName(file)})
		}
	}
	t.Total = len(t.Rows)
	return t
}

// nameCell is a name a file states — a section's, a view's — as a cell of a table
// shows it: as it is when it reads as itself, and quoted with what it holds written
// out otherwise. A section is a heading and a view an identifier, and a file can
// state either with a line break in it, which made a row of the table of one cell
// in csv and a second line in the pretty table. Nothing, for no name, so that a
// tile in no section has an empty cell and not a pair of quotation marks.
func nameCell(name string) string {
	if name == "" {
		return ""
	}
	return textclean.Name(name)
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
			" `--section` draws the tile in a named group under a header that folds, with the other tiles that" +
			" name it. `--refresh` sets how often it is re-run, such as 30s: a section and the screen can state the" +
			" same key as a default, and the nearest one wins." +
			" Adding the same tile again replaces it, so the command is safe to script." +
			" In the TUI, + on a catalogue row or a search match writes the same entry.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeReadCapabilities(reg),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runDashboardAdd(cmd, args[0], reg, opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("profile", "", "pin the tile to this connection: a profile name, or name/instance")
	// StringArray, as profile set's is: StringSlice split its argument on
	// commas, so net.port's own `--set ports=22,80` became "ports=22" and a
	// stray "80", refused as not a key=value pair, and no value with a comma
	// could be stated at all. A list-shaped input is one --set per element.
	cmd.Flags().StringArray("set", nil, "an input for the run, `key=value`; repeat for several")
	cmd.Flags().Int("width", 0, "how wide the tile is, in twelfths of the screen: 6 is half, 4 a third; 0 leaves it to its section, the screen and the terminal")
	cmd.Flags().Int("height", 0, "how tall the tile's box is, in lines; 0 leaves it to what the tile has to say")
	cmd.Flags().String("section", "", "draw the tile in this section, under a header that folds; left out, in none")
	cmd.Flags().String("refresh", "", "how often the tile is re-run, such as 30s or 5m; left out, its section's, then the screen's, then the capability's")
	addViewFlag(cmd, "put the tile on this view, creating it when it is new")
	completeFlag(cmd, "profile", completeProfiles)
	completeFlag(cmd, "set", completeTileInputs(reg))
	completeFlag(cmd, "section", completeSections)
	return cmd
}

// completeSections offers the sections of the view the command would write to:
// the ones its tiles name and the ones it states how to start, each with how many
// tiles name it, so a name is picked rather than spelled — a section is a string,
// and one letter off is a second section with one tile in it.
func completeSections(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, err := config.LoadFile()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	drawn, verr := viewOf(cmd, cfg, true)
	if verr != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	block := blockOf(cfg, drawn)
	named := map[string]int{}
	for _, t := range append(slices.Clone(block.Tiles), block.Add...) {
		if t.Section != "" {
			named[t.Section]++
		}
	}
	for name := range block.Sections {
		if _, seen := named[name]; !seen {
			named[name] = 0
		}
	}
	out := make([]cobra.Completion, 0, len(named))
	for _, name := range slices.Sorted(maps.Keys(named)) {
		// A name with a tab or a line break in it would end its candidate early
		// and begin a description nobody wrote: what cannot be a heading is not offered.
		if !config.ValidSectionName(name) {
			continue
		}
		desc := format.Count(named[name], "tile", "tiles")
		if block.Sections[name].Collapsed {
			desc += ", starts folded"
		}
		out = append(out, cobra.CompletionWithDesc(name, desc))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func dashboardRemoveCommand(render renderFn, opts *globalOpts) *cobra.Command {
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
	addViewFlag(cmd, "take the tile off this view")
	completeFlag(cmd, "profile", completeProfiles)
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
		hint := "`rta explain` lists every one"
		if missing, ok := missingPluginHint(reg.Capabilities(), id); ok {
			hint = missing
		}
		return nil, view.Errorf("core.dashboard.unknown", "no capability named %q", id).WithHint(hint)
	}
	if c.Safety != plugin.Read {
		return nil, view.Errorf("core.dashboard.notread",
			"%s is not a read, and a tile runs on a timer with no confirmation", id).
			WithHint("only a read can be a tile — `rta explain " + id + "` names its safety class")
	}
	if verr := refuseUnhonouredDashboard(); verr != nil {
		return nil, verr
	}
	cfg, err := config.LoadFile()
	if err != nil {
		return nil, view.AsError(err, "core.dashboard.config")
	}
	drawn, verr := viewOf(cmd, cfg, true)
	if verr != nil {
		return nil, verr
	}
	ref := strings.TrimSpace(mustString(cmd, "profile"))
	var expandsTo []string
	if ref != "" {
		if !plugin.Profilable(c) {
			return nil, view.Errorf("core.dashboard.noprofile",
				"%s takes no connection a profile could fill", id).
				WithHint("leave --profile off — `rta explain " + id + "` lists what it does take")
		}
		// Lookup's own refusals, in its own words: an unknown profile, one
		// from a file nobody named, one that says nothing about this
		// plugin, an instance it does not hold. Each is the exact thing
		// the tile would otherwise say on every refresh. The one refusal
		// a tile does not share is a bare name over several connections —
		// "your call", which the CLI is right to say of a single run and
		// the dashboard answers by showing every one (tui.expandTiles) —
		// so a profile that expands is checked connection by connection.
		refs := tui.InstancesOf(cfg, "")(ref, plugin.Namespace(id))
		if len(refs) == 0 {
			refs = []string{ref}
		}
		for _, r := range refs {
			if _, verr := profile.Lookup(cfg, c, r, withTrust{installed}); verr != nil {
				return nil, verr
			}
		}
		expandsTo = refs
	}
	pairs, _ := cmd.Flags().GetStringArray("set")
	with, verr := parseTileInputs(pairs, c)
	if verr != nil {
		return nil, verr
	}
	// The host holds every run to what the inputs declare, so a --set
	// outside an input's options or range would be written, then refused on
	// every refresh with nobody watching. Refused here instead, by the same
	// check, while the person who typed it is still looking.
	resolved := plugin.Resolve(c, plugin.Inputs{Caller: with})
	if verr := plugin.CheckInputs(c, plugin.NewRequest(resolved, false, false)); verr != nil {
		return nil, verr
	}
	// Written the way every run reads it, as profile set writes a value: an
	// option typed in another case is spelled as declared. The run rewrote
	// `--set proto=TCP` to tcp anyway, while the file and `dashboard list`
	// kept a spelling the declaration does not have.
	for k := range with {
		with[k] = resolved[k]
	}
	if verr := tileCanRunUnasked(c, with, ref != ""); verr != nil {
		return nil, verr
	}
	width, _ := cmd.Flags().GetInt("width")
	if width != 0 && (width < 1 || width > config.GridTracks) {
		return nil, view.Errorf("core.dashboard.width", "--width %d is not a width: the screen is %d wide", width, config.GridTracks).
			WithHint("a whole number of twelfths — 6 is half the screen, 4 a third, 12 the whole row; 0 leaves it to the terminal")
	}
	height, _ := cmd.Flags().GetInt("height")
	if height != 0 && (height < config.MinHeight || height > config.MaxHeight) {
		return nil, view.Errorf("core.dashboard.height", "--height %d is not a height: a tile is %d to %d lines tall",
			height, config.MinHeight, config.MaxHeight).
			WithHint("the lines of the tile's box, borders included; 0 leaves it to what the tile has to say")
	}
	section := strings.TrimSpace(mustString(cmd, "section"))
	if section != "" && !config.ValidSectionName(section) {
		return nil, view.Errorf("core.dashboard.section", "%q is not a name a section can have", section).
			WithHint(fmt.Sprintf("a section is a heading: up to %d characters, no control characters, no space at either end"+
				" — `databases` and `pg · staging` both are", config.MaxSectionName))
	}
	refresh, verr := refreshFlag(cmd, c)
	if verr != nil {
		return nil, verr
	}
	entry := config.Tile{ID: id, With: with, Profile: ref, Width: width, Height: height, Section: section, Refresh: refresh}
	// An entry with the key of an automatic tile is that tile (tui.joinTiles):
	// the one way to give the automatic sys tile a width, an input or a section.
	// One that says nothing the automatic tile does not already is refused
	// rather than written, because the person asking is usually asking for
	// a hidden one back, and the hint says so.
	var automatic bool
	if ref == "" {
		for _, p := range layoutNow(reg, cfg, drawn) {
			automatic = automatic || (p.ID == id && p.Source == "automatic")
		}
	}
	if automatic && len(with) == 0 && width == 0 && height == 0 && section == "" && refresh == "" {
		hint := "`--profile <name>` pins a second one to a connection; `--set`, `--width`, `--height`, `--section` and `--refresh` say how this one runs"
		if slices.Contains(blockOf(cfg, drawn).Hidden, id) {
			hint = "it is hidden — `rta dashboard unhide " + id + inViewFlag(drawn) + "` brings it back"
		}
		return nil, view.Errorf("core.dashboard.automatic",
			"%s is already on the automatic dashboard", id).WithHint(hint)
	}

	var existed, unchanged bool
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		block := blockOf(cfg, drawn)
		add := block.Add
		at := -1
		for i, t := range add {
			if t.Key() == entry.Key() {
				at = i
				break
			}
		}
		existed = at >= 0
		if existed {
			// Compared as the file would hold them, the way profile set
			// compares, and not as Go values: the tile read back holds a
			// number as the uint64 and a list as the []any YAML decodes, and
			// the entry the int and []string the flags typed, so a repeat of
			// either was never equal and was written as an update every time.
			before, beforeErr := yaml.Marshal(add[at])
			after, afterErr := yaml.Marshal(entry)
			unchanged = beforeErr == nil && afterErr == nil && bytes.Equal(before, after)
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
		block.Add = add
		cfg.SetBlock(drawn, block)
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.dashboard.write")
	}

	// A preview says what the run would do, in the words `config set` and
	// `profile set` use for theirs ("would set", "would create"); it said
	// "would have added", and the hide beside it "would have hid".
	verb := "added"
	switch {
	case existed && dryRun:
		verb = "would update"
	case existed:
		verb = "updated"
	case dryRun:
		verb = "would add"
	}
	pairsOut := []view.Pair{}
	switch {
	case unchanged:
		pairsOut = append(pairsOut, view.Pair{Key: "unchanged",
			Value: entry.Key() + " is already on the dashboard this way — nothing written to " + dashboardFile(drawn)})
	case dryRun:
		pairsOut = append(pairsOut, view.Pair{Key: "would write", Value: verb + " " + entry.Key() + " in " + dashboardFile(drawn)})
	default:
		pairsOut = append(pairsOut, view.Pair{Key: "wrote", Value: verb + " " + entry.Key() + " in " + dashboardFile(drawn)})
	}
	if drawn != "" {
		pairsOut = append(pairsOut, view.Pair{Key: "view", Value: drawnWhen(cfg, drawn)})
	}
	pairsOut = append(pairsOut, whichOfSeveral(cmd, cfg, drawn)...)
	pairsOut = append(pairsOut, view.Pair{Key: "tile", Value: id})
	if automatic {
		pairsOut = append(pairsOut, view.Pair{Key: "in place of",
			Value: "the automatic " + id + " tile — this entry says how it runs, and taking it down brings that one back"})
	}
	switch {
	case len(expandsTo) > 1:
		pairsOut = append(pairsOut, view.Pair{Key: "profile",
			Value: ref + " — one panel per " + plugin.Namespace(id) + " connection it holds: " +
				strings.Join(expandsTo, ", ") + "; a connection added to it later gets its panel too"})
	case ref != "":
		pairsOut = append(pairsOut, view.Pair{Key: "profile",
			Value: ref + " — the tile is about this connection whatever `rta use` switched on"})
	case hasConnection(c):
		// A connection, not merely an input a profile can fill: sys.cpu takes
		// a setting from one, and its receipt promised a panel per sys
		// connection the environment holds, of which there are none.
		pairsOut = append(pairsOut, view.Pair{Key: "profile",
			Value: "none — the tile follows whatever `rta use` switched on, one panel per " +
				plugin.Namespace(id) + " connection that environment holds; --profile <name> pins it"})
	}
	if len(with) > 0 {
		pairsOut = append(pairsOut, view.Pair{Key: "with", Value: withLine(with)})
	}
	if section != "" {
		pairsOut = append(pairsOut, view.Pair{Key: "section",
			Value: section + " — drawn with the other tiles that name it, under a header that folds (z in the TUI)"})
	}
	every, from := tui.PaceOf(c, entry, blockOf(cfg, drawn))
	paceText := "every " + pace(every)
	if from != "" {
		paceText += " (the " + from + "'s)"
	}
	pairsOut = append(pairsOut,
		view.Pair{Key: "re-runs", Value: paceText + ", for as long as the TUI is open"},
		view.Pair{Key: "on screen", Value: "bare `rta` opens on it; H on the tile or `rta dashboard rm " +
			removeLine(entry) + inViewFlag(drawn) + "` takes it down"})
	return view.KeyValue{Pairs: pairsOut}, nil
}

// drawnWhen says when a view is on screen, for the receipt of a command that
// has just put something on it: a profile's own screen is the one drawn while
// that environment is switched on, and any other view is the one a profile
// links, which is a thing `rta profile set` states for it.
func drawnWhen(cfg config.Config, v string) string {
	if cfg.InProfile(v) {
		return v + " — the screen written in profile " + v + "'s own entry, drawn while it is switched on " +
			"(`rta use " + v + "`), or with `--view " + v + "`"
	}
	return v + " — linked to a profile with `rta profile set <name> --dashboard " + v +
		"`: drawn first while that profile is switched on if it is the first, reached with V otherwise; `--view " + v + "` names it here"
}

// whichOfSeveral says, in the receipt of a command that was not told a view,
// which of several linked screens it acted on. The command line cannot see the
// screen a TUI session is showing, so it acts on the one the switched-on profile
// draws first, and a person who linked three and pressed V in the TUI would
// otherwise find the tile on a screen they were not looking at.
func whichOfSeveral(cmd *cobra.Command, cfg config.Config, acted string) []view.Pair {
	if strings.TrimSpace(mustString(cmd, "view")) != "" {
		return nil
	}
	active := profile.Active()
	names, linked := cfg.ScreensFor(active)
	if !linked || len(names) < 2 {
		return nil
	}
	label, other := acted, ""
	if label == "" {
		label = config.DefaultView
	}
	for _, n := range names {
		if n != label {
			other = n
			break
		}
	}
	return []view.Pair{{Key: "screens", Value: fmt.Sprintf("this went to %s, the first of %d screens %s links — `--view %s` names another",
		label, len(names), plugin.ListedName(active), other)}}
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
			verr := view.Errorf("core.dashboard.set.secret",
				"%q is a credential, and a tile's inputs are written into %s in plaintext",
				k, config.Path())
			// A profile's `secrets:` reaches only an input a profile may
			// fill. Offered for any other credential — codec.jwt's token —
			// the hint was a `profile set` refused in turn ("maps a secret
			// onto "token", which codec does not offer"), one refusal
			// leading to the next.
			if !plugin.ProfileFillable(c, f) {
				return nil, verr.WithHint("no profile fills " + k + " either, so a tile cannot be given it — run `" +
					strings.Join(append([]string{"rta"}, c.Words()...), " ") + "` when you have one")
			}
			return nil, verr.WithHint("give the tile a profile whose `secrets:` references it instead: " +
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
		v, verr := typedSetValue(f, k, raw[k])
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
	missing := tui.MissingInputs(c, with, pinned)
	if len(missing) == 0 {
		return nil
	}
	verr := view.Errorf("core.dashboard.needs",
		"%s needs %s, and a tile has no form to ask with", c.ID, strings.Join(missing, ", "))
	// `--set token=…` is itself refused for a credential, so pointing at it
	// sent the reader from one refusal to the next.
	if credential := tui.Untileable(c); len(credential) > 0 {
		return verr.WithHint(credential[0] + " is a credential, which a tile cannot be given — " +
			"`--set` would write it into the config in plaintext, and no profile fills it; " +
			"run `" + strings.Join(append([]string{"rta"}, c.Words()...), " ") + "` when you have one")
	}
	return verr.WithHint("`--set " + missing[0] + "=<value>` states it for every run")
}

func runDashboardRemove(cmd *cobra.Command, id string, dryRun bool) (view.View, *view.Error) {
	if verr := refuseUnhonouredDashboard(); verr != nil {
		return nil, verr
	}
	ref := strings.TrimSpace(mustString(cmd, "profile"))
	key := config.TileKey(id, ref)
	var (
		found   bool
		removed config.Tile
		keys    []string
	)
	now, err := config.LoadFile()
	if err != nil {
		return nil, view.AsError(err, "core.dashboard.config")
	}
	drawn, verr := viewOf(cmd, now, false)
	if verr != nil {
		return nil, verr
	}
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		block := blockOf(cfg, drawn)
		kept := make([]config.Tile, 0, len(block.Add))
		for _, t := range block.Add {
			if t.Key() == key {
				found, removed = true, t
				continue
			}
			keys = append(keys, t.Key())
			kept = append(kept, t)
		}
		if !found || dryRun {
			return cfg, false
		}
		block.Add = kept
		cfg.SetBlock(drawn, block)
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
		label, verb = "would write", "would remove"
	}
	return view.KeyValue{Pairs: append([]view.Pair{
		{Key: label, Value: verb + " " + key + " from the dashboard" + inView(drawn) + " in " + dashboardFile(drawn)},
		{Key: "back", Value: "`" + removed.AddCommand(drawn) + "`"},
	}, whichOfSeveral(cmd, now, drawn)...)}, nil
}

// dashboardHideCommand builds `hide` and `unhide`: what H does to an
// automatic tile or to one panel of an expanded entry, from a script, and
// the way back the TUI's inventory pane has only for automatic tiles.
func dashboardHideCommand(reg *registry.Registry, render renderFn, opts *globalOpts, hide bool) *cobra.Command {
	use, short := "hide <capability>", "Take an automatic tile, or one panel of an expanded entry, off the screen"
	long := "What H does in the TUI, from a script. An automatic tile is hidden by its capability;" +
		" one panel of an entry that expanded into a profile's several connections is hidden by" +
		" that connection, `--profile ohmlab/keycloak`, and its siblings stay. An added tile is" +
		" not hidden but withdrawn: `rta dashboard rm` is its command."
	if !hide {
		use, short = "unhide <capability>", "Bring a hidden tile or panel back"
		long = "The reverse of hide, and of H in the TUI: removes the `hidden:` line for a" +
			" capability, or for one connection's panel with `--profile`."
	}
	cmd := &cobra.Command{
		Use:               use,
		Short:             short,
		Long:              long,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeHideable(reg, hide),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runDashboardHide(cmd, args[0], reg, hide, opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("profile", "", "the connection whose panel to hide or bring back, for an entry that expanded into several")
	addViewFlag(cmd, "hide it on, or bring it back to, this view")
	completeFlag(cmd, "profile", completeProfiles)
	return cmd
}

func runDashboardHide(cmd *cobra.Command, id string, reg *registry.Registry, hide, dryRun bool) (view.View, *view.Error) {
	if verr := refuseUnhonouredDashboard(); verr != nil {
		return nil, verr
	}
	ref := strings.TrimSpace(mustString(cmd, "profile"))
	key := config.TileKey(id, ref)
	cfg, err := config.LoadFile()
	if err != nil {
		return nil, view.AsError(err, "core.dashboard.config")
	}
	drawn, verr := viewOf(cmd, cfg, false)
	if verr != nil {
		return nil, verr
	}
	if hide {
		// Only what H would hide: an automatic tile by its capability —
		// every panel of it, when the switched-on environment expanded it
		// into several — or one expanded panel by its connection. Anything
		// else on the screen came from an entry, and an entry is edited or
		// withdrawn rather than hidden; the refusal says how.
		var (
			found *tui.Placement
			there []string
		)
		for _, p := range layoutNow(reg, cfg, drawn) {
			if p.ID == id {
				there = append(there, config.TileKey(p.ID, p.Profile))
			}
			if found == nil && (config.TileKey(p.ID, p.Profile) == key ||
				(ref == "" && p.ID == id && p.Source == "automatic")) {
				p := p
				found = &p
			}
		}
		switch {
		case found == nil:
			hint := "`rta dashboard list` shows what is, and where each tile came from"
			if len(there) > 0 {
				hint = "it is there as " + strings.Join(there, ", ") + " — `--profile <ref>` names one " +
					"panel of an entry that expanded, and `rta dashboard rm` takes an added entry down"
			}
			return nil, view.Errorf("core.dashboard.absent", "%s is not on the dashboard", key).WithHint(hint)
		case found.Source == "stated" && !found.Expanded:
			return nil, view.Errorf("core.dashboard.notautomatic",
				"%s is stated in `tiles:`, and a stated list is edited rather than hidden", key).
				WithHint("take it out of `tiles:` in " + dashboardFile(drawn) + " — H on it in the TUI does that")
		case found.Source != "automatic" && !found.Expanded:
			return nil, view.Errorf("core.dashboard.notautomatic",
				"%s is an added tile, and an entry is withdrawn rather than hidden", key).
				WithHint("`rta dashboard rm " + removeLine(config.Tile{ID: id, Profile: ref}) + "` takes it down")
		case slices.Contains(blockOf(cfg, drawn).Hidden, key):
			return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
				Value: key + " is already hidden — nothing written to " + dashboardFile(drawn)}}}, nil
		}
	}
	var (
		changed      bool
		hiddenBefore []string
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		block := blockOf(cfg, drawn)
		hiddenBefore = block.Hidden
		kept := make([]string, 0, len(block.Hidden)+1)
		for _, h := range block.Hidden {
			if h == key {
				continue
			}
			kept = append(kept, h)
		}
		changed = len(kept) != len(block.Hidden)
		if hide {
			kept = append(kept, key)
			changed = true
		}
		if !changed || dryRun {
			return cfg, false
		}
		block.Hidden = kept
		cfg.SetBlock(drawn, block)
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.dashboard.write")
	}
	if !hide && !changed {
		hint := "nothing is hidden"
		if len(hiddenBefore) > 0 {
			hint = "hidden: " + strings.Join(hiddenBefore, ", ") + " — a panel is named with --profile"
		}
		return nil, view.Errorf("core.dashboard.nothidden", "%s is not hidden", key).WithHint(hint)
	}
	verb, back := "hid", "`rta dashboard unhide "+removeLine(config.Tile{ID: id, Profile: ref})+inViewFlag(drawn)+"` brings it back"
	if !hide {
		verb, back = "brought back", "`rta dashboard hide "+removeLine(config.Tile{ID: id, Profile: ref})+inViewFlag(drawn)+"` hides it again"
	}
	label := "wrote"
	switch {
	case dryRun && hide:
		label, verb = "would write", "would hide"
	case dryRun:
		label, verb = "would write", "would bring back"
	}
	return view.KeyValue{Pairs: append([]view.Pair{
		{Key: label, Value: verb + " " + key + inView(drawn) + " in " + dashboardFile(drawn)},
		{Key: "back", Value: back},
	}, whichOfSeveral(cmd, cfg, drawn)...)}, nil
}

// completeHideable offers what hide can take down — automatic tiles and
// expanded panels, described by their connection — or what unhide can
// bring back: the hidden lines.
func completeHideable(reg *registry.Registry, hide bool) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		cfg, err := config.LoadFile()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		now := drawnView(cfg)
		if !hide {
			for _, h := range blockOf(cfg, now).Hidden {
				id, ref, _ := strings.Cut(h, "@")
				desc := "hidden"
				if ref != "" {
					desc = "--profile " + ref
				}
				out = append(out, cobra.CompletionWithDesc(id, desc))
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}
		for _, p := range layoutNow(reg, cfg, now) {
			if p.Hidden || (p.Source != "automatic" && !p.Expanded) {
				continue
			}
			desc := "automatic"
			if p.Expanded {
				desc = "--profile " + p.Profile
			}
			out = append(out, cobra.CompletionWithDesc(p.ID, desc))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
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
	for _, t := range blockOf(cfg, drawnView(cfg)).Add {
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

// The views a dashboard command acts on.
//
// Without --view a command acts on the view that is drawn: the one the
// switched-on profile draws first, or the dashboard: block itself. Whatever a
// person adds, hides or lists is then what they are looking at in the TUI,
// and `--view default` names the block when that is not.

// drawnView is the view bare `rta` draws now, "" for the dashboard: block.
func drawnView(cfg config.Config) string { return cfg.ViewFor(profile.Active()) }

// blockOf is the arrangement of a view as a command reads it.
func blockOf(cfg config.Config, v string) config.Dashboard {
	d, _ := cfg.Block(v)
	return d
}

func addViewFlag(cmd *cobra.Command, help string) {
	cmd.Flags().String("view", "", help+" — one of `rta dashboard views`, or `default` for the dashboard: block; "+
		"left out, the view that is drawn now")
	completeFlag(cmd, "view", completeViews)
}

// viewOf is the view the command acts on: --view, else the one drawn now. A
// view that is not stated is refused unless the command may create it, which is
// `add`, so that a typo in a name does not hide the tile somewhere nobody looks.
func viewOf(cmd *cobra.Command, cfg config.Config, mayCreate bool) (string, *view.Error) {
	name := strings.TrimSpace(mustString(cmd, "view"))
	switch {
	case name == "":
		return drawnView(cfg), nil
	case name == config.DefaultView:
		return "", nil
	// Before the grammar of a view's name, which is for making one: a profile
	// that writes its screen in its entry has that screen under the profile's
	// name, and a profile may be called what no view could be.
	case cfg.HasView(name):
		return name, nil
	case !config.ValidViewName(name):
		return "", viewNameRefusal("core.dashboard.view", name)
	case !mayCreate:
		return "", missingViewRefusal(cfg, name)
	}
	return name, nil
}

// inView names a view in a receipt, and says nothing of the block itself.
func inView(v string) string {
	if v == "" {
		return ""
	}
	return " (view " + v + ")"
}

// inViewFlag is the flag that names the view in the command a receipt offers
// as the way back.
func inViewFlag(v string) string {
	if v == "" {
		return ""
	}
	return " --view " + v
}

// dashboardFile is the file that states a view's arrangement, or the dashboard:
// block's.
func dashboardFile(v string) string {
	if v == "" {
		return config.Where("dashboard", "")
	}
	return config.Where("views", v)
}

// completeViews offers the views there are, each with what it holds and who
// links it, so a long list of names is a list of things a person can choose
// between. A flag that is repeated is not offered what it was already given.
func completeViews(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cfg, err := config.LoadFile()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	given, _ := cmd.Flags().GetStringArray("dashboard")
	linkedBy := map[string][]string{}
	for _, profileName := range cfg.ProfileNames() {
		if names, linked := cfg.ScreensFor(profileName); linked {
			for _, v := range names {
				linkedBy[v] = append(linkedBy[v], profileName)
			}
		}
	}
	describe := func(desc, name string) string {
		if who := linkedBy[name]; len(who) > 0 {
			desc += ", linked to " + strings.Join(who, ", ")
		}
		return desc
	}
	var out []cobra.Completion
	if !slices.Contains(given, config.DefaultView) {
		out = append(out, cobra.CompletionWithDesc(config.DefaultView, describe("the dashboard: block", config.DefaultView)))
	}
	for _, name := range cfg.ViewNames() {
		if slices.Contains(given, name) {
			continue
		}
		block, _ := cfg.Block(name)
		desc := format.Count(len(block.Tiles)+len(block.Add), "entry", "entries")
		if cfg.InProfile(name) {
			desc += ", written in profile " + name
		}
		out = append(out, cobra.CompletionWithDesc(name, describe(desc, name)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// viewsTable is every view the configuration states: which profiles link it,
// what it holds, which file states it, and the one that is drawn now.
//
// What it holds is a count of entries, the tiles a view pins and the ones it
// adds, and it is called that: the number of tiles a view draws is not here (the
// automatic ones come with a view that states none, and one entry can draw
// several panels), and `dashboard list` and `dashboard sections` count what is
// drawn.
func viewsTable(cfg config.Config) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "View"}, {Name: "Linked To"}, {Name: "Entries", Kind: view.KindNumber}, {Name: "File"}, {Name: "Drawn Now"},
	}}
	now := drawnView(cfg)
	// Who links a view is who walks it with V, so this asks the same question
	// the screen does: a name nothing states, and a block that cannot be had
	// under its profile's name, link nothing and are not listed against
	// anything. A profile that writes its screen in its entry links it by being
	// it, and `default` in a profile's list is the dashboard: block.
	selectors := map[string][]string{}
	for _, name := range cfg.ProfileNames() {
		linked, any := cfg.ScreensFor(name)
		if !any {
			continue
		}
		for _, v := range linked {
			who := name
			if v == name && cfg.InProfile(v) {
				who += " (its own)"
			}
			selectors[v] = append(selectors[v], who)
		}
	}
	row := func(name, file string, tiles int, selected []string) {
		drawn := ""
		if name == now || (name == config.DefaultView && now == "") {
			drawn = "yes"
			if active := profile.Active(); active != "" && name == now && now != "" {
				drawn = "yes — " + active + " is switched on"
			}
		}
		listed := make([]string, len(selected))
		for i, who := range selected {
			listed[i] = plugin.ListedName(who)
		}
		t.Rows = append(t.Rows, []string{plugin.ListedName(name), strings.Join(listed, ", "), strconv.Itoa(tiles), configFileName(file), drawn})
	}
	base := cfg.Dashboard
	row(config.DefaultView, config.Where("dashboard", ""), len(base.Tiles)+len(base.Add), selectors[config.DefaultView])
	for _, name := range cfg.ViewNames() {
		v, _ := cfg.Block(name)
		row(name, config.Where("views", name), len(v.Tiles)+len(v.Add), selectors[name])
	}
	t.Total = len(t.Rows)
	// A file found in the working directory arranges no screen (a cloned
	// repository must not), so its views are listed as what the file says and
	// not as screens to choose from: the table would otherwise read as a menu
	// of which every entry draws the automatic dashboard.
	if !config.TrustedPath() {
		t.Warnings = append(t.Warnings, *view.Errorf("core.dashboard.untrusted",
			"%s is not a config file rta was told to use, so it arranges no screen and none of them is drawn", config.Path()).
			WithHint("set $RTA_CONFIG to the file you mean — a config file found in the working " +
				"directory arranges nothing, because a cloned repository must not"))
	}
	return t
}

// viewNameRefusal is the refusal for a name no view can have.
func viewNameRefusal(code, name string) *view.Error {
	return view.Errorf(code, "%q is not a name a view can have", name).
		WithHint("a view is named in lower case, digits and hyphens, starting with a letter, at most 32 long — " +
			"and `default` is the dashboard: block, not a view")
}

// inlineScreenRefusal is the refusal for `rta profile set --dashboard` on a
// profile whose screen is written in its own entry: the flag names a view, and
// naming one would discard the block.
func inlineScreenRefusal(name string, block *config.View) *view.Error {
	return view.Errorf("core.profile.dashboard.inline",
		"profile %s writes its own dashboard in its entry (%s), and --dashboard would throw it away",
		name, format.Count(len(block.Tiles)+len(block.Add), "tile", "tiles")).
		WithHint("`rta dashboard add <capability> --view " + name + "` changes that screen; to show named views " +
			"instead, move the tiles under `dashboard: views:` and replace the block in the entry with the view's " +
			"name, or a list of them (`rta config edit`)")
}

// missingViewRefusal is the refusal for a view nothing states, naming the ones
// that are, or the command that states the first.
func missingViewRefusal(cfg config.Config, name string) *view.Error {
	hint := "no view is stated yet — `rta dashboard add <capability> --view " + name + "` creates one"
	if names := cfg.ViewNames(); len(names) > 0 {
		hint = "views: " + strings.Join(names, ", ") + " — `rta dashboard add <capability> --view " + name + "` creates a new one"
	}
	return view.Errorf("core.dashboard.noview", "there is no view named %q", name).WithHint(hint)
}

// refreshFlag is --refresh as the entry writes it: refused when it cannot be
// read, or asks for a pace the capability's own declaration forbids, while the
// person who typed it is looking — the same answer `rta config check` gives the
// same line in a file, and the one a tile would otherwise give by quietly running
// at the floor. Written in the form a duration prints in, so `90s` and `1m30s`
// are one entry.
func refreshFlag(cmd *cobra.Command, c plugin.Capability) (string, *view.Error) {
	text := strings.TrimSpace(mustString(cmd, "refresh"))
	if text == "" {
		return "", nil
	}
	want, err := config.ParseRefresh(text)
	if err != nil {
		return "", view.Errorf("core.dashboard.refresh", "--refresh %s", err.Error()).
			WithHint("`--refresh 30s` re-runs the tile every thirty seconds; left out, it takes its section's, then the screen's, then the capability's")
	}
	if floor, cut := plugin.ClampPace(c, want); cut {
		return "", view.Errorf("core.dashboard.refresh.floor", "%s re-runs no more often than every %s, and --refresh %s asks for more",
			c.ID, pace(floor), text).
			WithHint("a pace a capability declared is what a run costs it, and a configuration can only make it slower")
	}
	return config.FormatRefresh(want), nil
}
