package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// newPluginsCommand implements `rta plugins`: what is installed.
//
// `rta explain` lists every capability across every plugin, which answers a
// different question — scrolling a capability list to work out which plugins
// you have is reading a phone book to learn which towns exist. `doctor`
// counts them and stops.
// This is the inventory: one line per plugin, what it is for, how much it
// offers, and what it can do to your machine.
func newPluginsCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "List installed plugins and what each one offers",
		Long: "One line per plugin: its purpose, how many capabilities it has, and\n" +
			"whether any of them write or destroy.\n\n" +
			"Use `rta explain` for the capabilities themselves, and the TUI's `p`\n" +
			"pane to choose which plugins appear on the dashboard.",
		Args: cobra.NoArgs,
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
