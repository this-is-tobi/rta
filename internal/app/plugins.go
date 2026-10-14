package app

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// newPluginListCommand implements `rta plugin list`: what is installed.
//
// `rta explain` lists every capability across every plugin, which answers a
// different question — scrolling a capability list to work out which plugins
// you have is reading a phone book to learn which towns exist. `doctor`
// counts them and stops.
// This is the inventory: one line per plugin, what it is for, how much it
// offers, and what it can do to your machine.
//
// It was `rta plugins` until it moved here, and the move is the app applying
// its own rule to itself. Every listing in rta is `<namespace> list` —
// grant.list, kv.list, note.list, net.hosts.list — and sdktest
// warns a plugin author who invents a verb outside that vocabulary. A
// top-level bare plural was the one place rta did not follow the convention
// it enforces on everybody else, and it sat one letter from `rta plugin` in
// the command list, which is the kind of pair a person reads twice and still
// gets wrong.
func newPluginListCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the plugins rta has and what each one offers",
		Long: "One line per plugin: its purpose, how many capabilities it has and whether\n" +
			"any of them write or destroy.\n\n" +
			"Once any plugin is not built in to the rta binary, a Source column says where\n" +
			"each came from: built in, installed by rta from an index, in the system root\n" +
			"an image or a package filled, or found on your $PATH.\n\n" +
			"A plugin that is installed and not running has a row too, saying which\n" +
			"way: waiting for your approval, or approved and unable to start.\n\n" +
			"Use `rta explain` for the capabilities themselves, and the TUI's `p`\n" +
			"pane to choose which plugins appear on the dashboard.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format()
			if err != nil {
				return err
			}
			return cli.Render(cmd.OutOrStdout(), pluginsView(reg),
				renderOptions(cmd, format, opts.noColor))
		},
	}
}

func pluginsView(reg *registry.Registry) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Plugin"},
		{Name: "Capabilities", Kind: view.KindNumber},
		{Name: "Can", Kind: view.KindStatus},
		{Name: "Summary"},
	}}
	// Where each row's bytes came from, held beside the rows until the whole
	// list says whether there is a column to put it in.
	var sources []string
	add := func(source string, cells ...string) {
		t.Rows = append(t.Rows, cells)
		sources = append(sources, source)
	}
	for _, p := range reg.Plugins() {
		origin, _ := reg.Origin(p.Name)
		add(plugindist.SourceOf(origin.Path),
			p.Name,
			fmt.Sprintf("%d", len(p.Capabilities)),
			reach(p),
			p.Summary,
		)
	}
	// Approved and not started: the other way an installed plugin is absent
	// from the inventory, and the one the startup report says once and then
	// scrolls away. Above the untrusted rows because this is the plugin the
	// operator expects to be working.
	for _, f := range failedPluginsFound {
		add(plugindist.SourceOf(f.Path), f.Name, "—", failedToStartStatus, failedPluginDetail(f))
	}
	// Installed and not run, in the same table rather than a section of its
	// own. A trust gate's failure mode is silence: a plugin that is present,
	// approved by nobody, and simply absent from the inventory is
	// indistinguishable from one that was never installed — and the inventory
	// is the first place somebody looks when a plugin "does not work".
	//
	// It has no capability count and no summary, and that is not a gap to fill
	// in: both of those are things the plugin says about itself, and asking it
	// would mean running it, which is the decision that has not been made.
	for _, u := range untrustedPluginsFound {
		detail := "installed and not run (" + u.Short() + ") — `rta plugin trust " + u.Name +
			"` to load it"
		if u.Taken {
			// The row above already carries this name. Offering to trust this
			// artifact would be offering a namespace collision on the next
			// start, and the plugin the operator can see is not the one that
			// was refused.
			detail = "found on $PATH (" + u.Short() + ") and not run — the name above is " +
				"already taken, so trusting it would collide; remove or rename the file"
		}
		add(plugindist.SourceOf(u.Path), u.Name, "—", "untrusted", detail)
	}
	// Where the bytes came from, in the TUI inventory's own words, and only
	// when it tells the rows apart. A stock install is built in all the way down,
	// and a column that says so twenty times separates nothing — the pane draws
	// no band for the same reason — so the table is then the one it always was.
	// What it answers is "which of these did I install", and the answer is in the
	// column only once there is something to tell from the rest.
	//
	// Last, and not beside the plugin it qualifies, so that the columns a script
	// reads by position keep their places whether the column is there or not.
	if slices.ContainsFunc(sources, func(source string) bool { return source != plugindist.SourceBuiltIn }) {
		t.Columns = append(t.Columns, view.Column{Name: "Source"})
		for i := range t.Rows {
			t.Rows[i] = append(t.Rows[i], sources[i])
		}
	}
	t.Total = len(t.Rows)
	return t
}

// reach summarises what a plugin is able to do, in the safety vocabulary the
// rest of the system uses. It is the column worth having: "which of these can
// change my machine?" is the question you ask before handing an agent a
// server, and counting capability classes by hand is not an answer.
func reach(p plugin.Plugin) string {
	worst := "read"
	for _, c := range p.Capabilities {
		switch c.Safety {
		case plugin.Destructive:
			return "destructive"
		case plugin.Write:
			worst = "write"
		}
	}
	return worst
}
