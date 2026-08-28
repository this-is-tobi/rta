package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
// grant.list, kv.list, note.list, todo.list, net.hosts.list — and sdktest
// warns a plugin author who invents a verb outside that vocabulary. A
// top-level bare plural was the one place rta did not follow the convention
// it enforces on everybody else, and it sat one letter from `rta plugin` in
// the command list, which is the kind of pair a person reads twice and still
// gets wrong.
func newPluginListCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed plugins and what each one offers",
		Long: "One line per plugin: its purpose, how many capabilities it has, and\n" +
			"whether any of them write or destroy.\n\n" +
			"Use `rta explain` for the capabilities themselves, and the TUI's `p`\n" +
			"pane to choose which plugins appear on the dashboard.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			return cli.Render(cmd.OutOrStdout(), pluginsView(reg),
				cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()})
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
	for _, p := range reg.Plugins() {
		t.Rows = append(t.Rows, []string{
			p.Name,
			fmt.Sprintf("%d", len(p.Capabilities)),
			reach(p),
			p.Summary,
		})
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
		t.Rows = append(t.Rows, []string{u.Name, "—", "untrusted", detail})
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
