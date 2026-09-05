package app

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// newPluginDocCommand implements `rta plugin doc <binary>`: the reference page
// for one plugin, generated from its declaration.
//
// The same idea as `rta plugin manifest`, pointed at readers instead of at an
// index: a page written by hand goes stale on the first commit that touches a
// capability, and nobody notices, because the commit is about something else.
// Read from the binary, the page cannot disagree with the plugin, and a
// repository can regenerate it in CI and fail when the committed copy drifts.
//
// Every capability section is `rta explain`'s card, unchanged. That is the
// point rather than a shortcut: the card is the one place that knows how to
// say what an input is, where its config key lives and what it takes to reach
// a capability over MCP, and a second renderer of the same facts is the way
// the two pages start disagreeing.
func newPluginDocCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "doc <binary>",
		Short: "Write a plugin's reference page from its own declaration",
		Long: "Runs the binary the way a load does — sandboxed — and prints one markdown\n" +
			"page from what it declares: the capability table, every config key the\n" +
			"plugin reads, and one section per capability holding the same card\n" +
			"`rta explain` prints. Nothing is typed, so nothing can drift from the\n" +
			"artifact.\n\n" +
			"    rta plugin doc bin/rta-plugin-pg > plugins/pg/README.md",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			declared, verr := plugindist.Describe(cmd.Context(), args[0], cmd.ErrOrStderr())
			if verr != nil {
				return verr
			}
			page, verr := pluginDocView(declared)
			if verr != nil {
				return verr
			}
			return cli.Render(cmd.OutOrStdout(), page, cli.Options{Format: cli.Markdown})
		},
	}
}

// pluginDocView assembles the page.
//
// The plugin is registered into a registry of its own, with no origin: the
// card names the config block and the MCP allow flag by consulting where a
// plugin came from, and a page read by strangers must not carry the digest of
// whatever build happened to produce it. So the page spells both bare, and
// the Configuration section says once that an installed plugin's section is
// pinned to its digest — `rta doctor` and `rta explain` on the reader's
// machine print the exact forms their install requires.
func pluginDocView(p plugin.Plugin) (view.View, *view.Error) {
	reg := registry.New()
	if err := reg.RegisterFrom(p, registry.Origin{}); err != nil {
		return nil, view.Errorf("plugin.doc.declaration", "%v", err)
	}

	head := "# " + p.Name + "\n"
	if p.Summary != "" {
		head += "\n" + p.Summary + "\n"
	}
	if len(p.Needs) > 0 {
		head += "\nAsks for " + needsLine(p.Needs) + " — granted, or not, with `rta plugin allow " + p.Name + "`.\n"
	}
	page := view.Sections{Items: []view.Section{
		{View: view.Text{Body: head, Markdown: true}},
		{Title: "Capabilities", View: catalogView(reg)},
	}}
	if cfg := configKeysView(reg, p); cfg != nil {
		page.Items = append(page.Items, view.Section{Title: "Configuration", View: cfg})
	}
	for _, c := range reg.Capabilities() {
		page.Items = append(page.Items, view.Section{Title: c.ID, View: docCard(reg, c)})
	}
	return page, nil
}

// configKeysView lists every key the plugin reads from its config block, with
// the capabilities that read it — the table an operator setting up a profile
// wants and no card can give, since a card is one capability wide.
func configKeysView(reg *registry.Registry, p plugin.Plugin) view.View {
	type use struct {
		help string
		caps []string
	}
	byKey := map[string]*use{}
	for _, c := range reg.Capabilities() {
		for _, f := range c.Inputs {
			if f.Config == "" {
				continue
			}
			u, ok := byKey[f.Config]
			if !ok {
				u = &use{help: f.Help}
				byKey[f.Config] = u
			}
			u.caps = append(u.caps, c.ID)
		}
	}
	if len(byKey) == 0 {
		return nil
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := view.Table{Columns: []view.Column{
		{Name: "Key"},
		{Name: "Read by"},
		{Name: "Help"},
	}}
	for _, k := range keys {
		u := byKey[k]
		t.Rows = append(t.Rows, []string{k, strings.Join(u.caps, ", "), u.help})
	}
	t.Total = len(t.Rows)
	note := "Under `plugins: " + p.Name + ":` in rta's configuration, or in a profile's `set:`. An installed " +
		"plugin's section is pinned to the artifact — `plugins: " + p.Name + "@<digest>:` — and " +
		"`rta doctor` prints the exact line. The caller always wins, so a configured value is a " +
		"default, never a lock."
	return view.Sections{Items: []view.Section{
		{View: view.Text{Body: note, Markdown: true}},
		{View: t},
	}}
}

// docCard is the explain card, with the description lifted out as prose
// above it — a paragraph in a table cell is a paragraph nobody reads — and
// without the one line that belongs to a machine rather than to the plugin:
// the path of the reader's config file.
func docCard(reg *registry.Registry, c plugin.Capability) view.View {
	card := cardView(reg, c).(view.KeyValue)
	kept := card.Pairs[:0]
	for _, pair := range card.Pairs {
		if pair.Key != "config file" && pair.Key != "description" {
			kept = append(kept, pair)
		}
	}
	card.Pairs = kept
	if c.Description == "" {
		return card
	}
	return view.Sections{Items: []view.Section{
		{View: view.Text{Body: c.Description}},
		{View: card},
	}}
}

func needsLine(needs []plugin.Need) string {
	names := make([]string, len(needs))
	for i, n := range needs {
		names[i] = "`" + string(n) + "`"
	}
	return strings.Join(names, ", ")
}
