package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/plugindist"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Distribution commands (ADR 0017): install, upgrade, remove, search, and the
// index namespace. The design in one line — the index states claims, rta.lock
// records what rta computed — and the install output is a sequence of
// statements the operator can act on, ending with the two load-bearing lines:
// the pinned config heading and the credential variable, handed over at the
// one moment the operator has the digest in hand (ADR 0015: a control that
// requires homework is a control that gets turned off).

func renderView(cmd *cobra.Command, opts *globalOpts, v view.View) error {
	format, err := cli.ParseFormat(opts.output)
	if err != nil {
		return err
	}
	return cli.Render(cmd.OutOrStdout(), v,
		cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()})
}

func newPluginInstallCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "install <name | index/name>",
		Short: "Install a plugin from an attached index",
		Long: "Fetches the artifact an index claims, hashes it, launches it in the same\n" +
			"sandbox any load uses, and refuses if what it declares is not what the\n" +
			"index said — naming the index that made the claim. Only then does it\n" +
			"land: the managed store, the trust entry, and rta.lock, which records\n" +
			"what rta computed rather than what anybody claimed.\n\n" +
			"Installing is the trust decision: no separate `rta plugin trust` is\n" +
			"needed for a plugin installed this way.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rep, verr := plugindist.Install(cmd.Context(), args[0], cmd.ErrOrStderr())
			if verr != nil {
				return verr
			}
			return renderView(cmd, opts, installView(rep))
		},
	}
}

// installView is ADR 0017 §4's sequence of statements as a KeyValue page.
func installView(rep plugindist.Report) view.View {
	pin := rep.Name + "@" + shortDigest(rep.Digest)
	pairs := []view.Pair{
		{Key: "from", Value: rep.Index + " → " + rep.URL},
		{Key: "version", Value: rep.Version + " (the index's claim)"},
		{Key: "digest", Value: shortDigest(rep.Digest) + " (computed by rta from the bytes)"},
		{Key: "signature", Value: rep.Signature},
		{Key: "declares", Value: declaresLine(rep.Declared)},
		{Key: "installed", Value: rep.Path},
		{Key: "to configure it", Value: "plugins." + pin + ": — `rta explain " +
			firstCapability(rep.Declared) + "` lists its keys"},
	}
	if creds := credentialVars(rep.Declared); len(creds) > 0 {
		pairs = append(pairs, view.Pair{Key: "credentials",
			Value: "export " + strings.Join(creds, ", ") + " — the name, never in the config file"})
	}
	return view.KeyValue{Pairs: pairs}
}

func shortDigest(d string) string {
	if len(d) < 12 {
		return d
	}
	return d[:12]
}

func declaresLine(p plugin.Plugin) string {
	ids := make([]string, 0, len(p.Capabilities))
	counts := map[plugin.Safety]int{}
	grants := 0
	for _, c := range p.Capabilities {
		ids = append(ids, c.ID)
		counts[c.Safety]++
		if c.NeedsGrant {
			grants++
		}
	}
	sort.Strings(ids)
	var classes []string
	if counts[plugin.Read] == len(p.Capabilities) {
		classes = append(classes, "all read")
	} else {
		for _, s := range plugin.Safeties() {
			if counts[s] > 0 {
				classes = append(classes, fmt.Sprintf("%d %s", counts[s], s))
			}
		}
	}
	switch grants {
	case 0:
		classes = append(classes, "none needs a grant")
	case 1:
		classes = append(classes, "1 needs a grant")
	default:
		classes = append(classes, fmt.Sprintf("%d need a grant", grants))
	}
	return strings.Join(ids, ", ") + " — " + strings.Join(classes, " · ")
}

func firstCapability(p plugin.Plugin) string {
	if len(p.Capabilities) == 0 {
		return p.Name
	}
	ids := make([]string, 0, len(p.Capabilities))
	for _, c := range p.Capabilities {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids[0]
}

// credentialVars lists the environment variables the plugin's credential
// inputs read, deduped and sorted — the same Local+EnvFallback gate `rta
// explain` applies, for its reason (D94): a variable printed here that
// nothing reads is documentation of a channel that does not exist.
func credentialVars(p plugin.Plugin) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range p.Capabilities {
		for _, f := range c.Inputs {
			if !f.Local || !f.EnvFallback || f.Type != plugin.Secret {
				continue
			}
			name := "$" + plugin.LocalEnvVar(c.ID, f.Name)
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func newPluginRemoveCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Uninstall a managed plugin",
		Long: "Removes the store entry, the bin/ link, the trust for every stored\n" +
			"digest, and the rta.lock record — and names the config statements that\n" +
			"now point at nothing, without touching them: the config file is yours,\n" +
			"and `rta doctor` keeps reporting the orphans until you decide.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, verr := plugindist.Remove(args[0])
			if verr != nil {
				return verr
			}
			pairs := []view.Pair{
				{Key: "removed", Value: removed.Name},
				{Key: "artifacts", Value: fmt.Sprintf("%d (trust withdrawn from each)", len(removed.Digests))},
			}
			if len(removed.Orphans) > 0 {
				pairs = append(pairs, view.Pair{Key: "still states it",
					Value: strings.Join(removed.Orphans, ", ") + " — yours to keep or delete"})
			}
			return renderView(cmd, opts, view.KeyValue{Pairs: pairs})
		},
	}
}

func newPluginUpgradeCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade <name>",
		Short: "Move a managed plugin to what its index now claims",
		Long: "Re-verifies exactly like an install, then prints the declaration diff —\n" +
			"a capability changing safety class, or a destructive one appearing, is\n" +
			"the supply-chain event that matters, and precisely what a signature\n" +
			"does not tell you: the same publisher signing a worse plugin verifies\n" +
			"perfectly. The previous artifact stays in the store, so rolling back is\n" +
			"a re-install away, not a re-download.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			up, verr := plugindist.Upgrade(cmd.Context(), args[0], cmd.ErrOrStderr())
			if verr != nil {
				return verr
			}
			if up.UpToDate {
				return renderView(cmd, opts, view.KeyValue{Pairs: []view.Pair{
					{Key: "up to date", Value: up.Name + " " + up.Version + " (" +
						shortDigest(up.FromDigest) + ")"},
				}})
			}
			pairs := []view.Pair{
				{Key: "upgraded", Value: fmt.Sprintf("%s %s → %s", up.Name, up.FromVersion, up.Version)},
				{Key: "digest", Value: shortDigest(up.FromDigest) + " → " + shortDigest(up.Digest)},
				{Key: "signature", Value: up.Signature},
			}
			if len(up.Diff) == 0 {
				pairs = append(pairs, view.Pair{Key: "declaration", Value: "unchanged"})
			}
			for _, line := range up.Diff {
				pairs = append(pairs, view.Pair{Key: "declaration", Value: line})
			}
			pairs = append(pairs,
				view.Pair{Key: "your pin", Value: "plugins." + up.Name + "@" +
					shortDigest(up.FromDigest) + " no longer applies; the new pin is " +
					up.Name + "@" + shortDigest(up.Digest)})
			return renderView(cmd, opts, view.KeyValue{Pairs: pairs})
		},
	}
}

func newPluginSearchCommand(opts *globalOpts) *cobra.Command {
	var safety string
	cmd := &cobra.Command{
		Use:   "search [term]",
		Short: "List what the attached indexes claim to offer",
		Long: "Answers from the manifests alone — nothing is fetched or executed. Every\n" +
			"row is a claim, labelled with the index making it; install is where\n" +
			"claims meet evidence.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if safety != "" && !plugin.ValidSafety(safety) {
				return view.Errorf("plugin.search.safety",
					"%q is not a safety class", safety).
					WithHint("the classes are read, write, destructive")
			}
			term := ""
			if len(args) == 1 {
				term = args[0]
			}
			rows := plugindist.Search(term, safety)
			if len(rows) == 0 {
				if len(plugindist.Indexes()) == 0 {
					return view.Errorf("plugin.index.none", "no index is attached").
						WithHint("`rta plugin index add <name> <repository>` attaches one")
				}
				return renderView(cmd, opts, view.Text{Body: "nothing matches"})
			}
			t := view.Table{
				Columns: []view.Column{{Name: "Plugin"}, {Name: "Version"}, {Name: "Index"},
					{Name: "Installed"}, {Name: "Safety"}, {Name: "Summary"}},
			}
			for _, r := range rows {
				installed := ""
				if r.Installed != "" {
					installed = r.Installed
				}
				t.Rows = append(t.Rows, []string{r.Name, r.Version, r.Index, installed,
					r.Safety, r.Summary})
			}
			return renderView(cmd, opts, t)
		},
	}
	cmd.Flags().StringVar(&safety, "safety", "",
		"only plugins claiming a capability of this class (read|write|destructive)")
	return cmd
}

func newPluginIndexCommand(opts *globalOpts) *cobra.Command {
	root := &cobra.Command{
		Use:   "index",
		Short: "Attach, refresh and detach plugin indexes",
		Long: "An index is a git repository of plugins/<name>.yaml manifests — claims\n" +
			"about plugins, searchable without downloading anything. rta shells out\n" +
			"to your git, so your remotes, proxies and credentials keep working.\n" +
			"There is no default index: the first one attached is your decision.",
		RunE: groupRunE,
	}
	root.AddCommand(&cobra.Command{
		Use:   "add <name> <repository>",
		Short: "Attach an index by cloning it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if verr := plugindist.AddIndex(cmd.Context(), args[0], args[1]); verr != nil {
				return verr
			}
			ix, _ := plugindist.IndexByName(args[0])
			listed, bad := plugindist.Manifests(ix)
			pairs := []view.Pair{
				{Key: "attached", Value: args[0] + " (" + args[1] + ")"},
				{Key: "claims", Value: fmt.Sprintf("%d plugins", len(listed))},
			}
			for _, verr := range bad {
				pairs = append(pairs, view.Pair{Key: "problem", Value: verr.Message})
			}
			return renderView(cmd, opts, view.KeyValue{Pairs: pairs})
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List attached indexes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			indexes := plugindist.Indexes()
			if len(indexes) == 0 {
				return renderView(cmd, opts, view.Text{
					Body: "no index is attached — `rta plugin index add <name> <repository>`"})
			}
			t := view.Table{Columns: []view.Column{{Name: "Index"}, {Name: "Plugins"},
				{Name: "Problems"}}}
			for _, ix := range indexes {
				listed, bad := plugindist.Manifests(ix)
				t.Rows = append(t.Rows, []string{ix.Name,
					fmt.Sprint(len(listed)), fmt.Sprint(len(bad))})
			}
			return renderView(cmd, opts, t)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "update [name]",
		Short: "Fast-forward one index, or every one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if verr := plugindist.UpdateIndex(cmd.Context(), name); verr != nil {
				return verr
			}
			return renderView(cmd, opts, view.Text{Body: "updated"})
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Detach an index",
		Long: "Refused while an installed plugin records this index as its provenance:\n" +
			"rta.lock exists to answer where a binary came from, and detaching the\n" +
			"answer would leave the question standing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if verr := plugindist.RemoveIndex(args[0]); verr != nil {
				return verr
			}
			return renderView(cmd, opts, view.Text{Body: "detached " + args[0]})
		},
	})
	return root
}
