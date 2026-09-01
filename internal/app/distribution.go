package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/plugindist"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Distribution commands: install, upgrade, remove, search, and the
// index namespace. The design in one line — the index states claims, rta.lock
// records what rta computed — and the install output is a sequence of
// statements the operator can act on, ending with the two load-bearing lines:
// the pinned config heading and the credential variable, handed over at the
// one moment the operator has the digest in hand (a control that
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

// installView renders the install sequence as a KeyValue page: what was
// claimed, what was fetched, what it hashed to, and what it declared.
func installView(rep plugindist.Report) view.View {
	pin := rep.Name + "@" + shortDigest(rep.Digest)
	pairs := []view.Pair{
		{Key: "from", Value: rep.Index + " → " + rep.URL},
		{Key: "version", Value: rep.Version + " (the index's claim)"},
		{Key: "digest", Value: shortDigest(rep.Digest) + " (computed by rta from the bytes)"},
		{Key: "signature", Value: rep.Signature},
		{Key: "declares", Value: declaresLine(rep.Declared)},
	}
	// Named here and not only at `rta plugin search`, because this is the
	// moment it costs something to not know. The artifact is installed and
	// trusted, and a plugin whose need is ungranted runs and fails at the call
	// that wanted the file — so an operator who never sees this line meets it
	// later as somebody else's "operation not permitted".
	//
	// Install is deliberately not the place the grant happens. Letting an
	// artifact run and letting it read a credential location are separate
	// decisions, and this line is the first half of the second one.
	if len(rep.Declared.Needs) > 0 {
		asks := make([]string, len(rep.Declared.Needs))
		for i, n := range rep.Declared.Needs {
			asks[i] = needLine(n)
		}
		pairs = append(pairs, view.Pair{Key: "asks to read",
			Value: strings.Join(asks, ", ") + " — not granted by installing; " +
				"`rta plugin allow " + rep.Name + "` decides"})
	}
	pairs = append(pairs, []view.Pair{
		{Key: "installed", Value: rep.Path},
		{Key: "to configure it", Value: "plugins." + pin + ": — `rta explain " +
			firstCapability(rep.Declared) + "` lists its keys"},
	}...)
	if creds := credentialVars(rep.Declared); len(creds) > 0 {
		// Named, not spelled as a command. `export A, B` reads like a line to
		// paste and is not one — a shell takes the comma as part of the
		// identifier and refuses the whole thing — and a report that hands out
		// a broken command is worse than one that hands out none. Where rta
		// does write a real export line it writes one per variable with a
		// placeholder, which is the TUI's `y` (copyExportLine).
		pairs = append(pairs, view.Pair{Key: "credentials",
			Value: strings.Join(creds, ", ") +
				" — from the environment or a profile's `secrets:`, never the config file"})
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
// explain` applies, for its reason: a variable printed here that
// nothing reads is documentation of a channel that does not exist.
func credentialVars(p plugin.Plugin) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range p.Capabilities {
		for _, f := range c.Inputs {
			if !f.Local || !f.EnvFallback || !f.Type.Sensitive() {
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

// newPluginOutdatedCommand implements `rta plugin outdated`: search's own
// "answers from the manifests alone" restraint, scoped to what is installed
// and turned into a comparison instead of a listing.
func newPluginOutdatedCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "outdated",
		Short: "List installed plugins whose index no longer agrees with what is installed",
		Long: "Compares each installed plugin's recorded version against what its index\n" +
			"currently claims. Like search, nothing is fetched or executed — this is a\n" +
			"hint worth a look, not a verdict: `rta plugin upgrade <name>` is what\n" +
			"actually re-verifies against the bytes and reports whether anything a\n" +
			"grant hangs off changed. A respin under an unchanged version number is\n" +
			"invisible here for the same reason it is invisible to a signature.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(plugindist.ReadLock()) == 0 {
				return renderView(cmd, opts, view.Text{Body: "no plugin is installed"})
			}
			rows := plugindist.Outdated()
			if len(rows) == 0 {
				return renderView(cmd, opts, view.Text{
					Body: "every installed plugin matches what its index claims"})
			}
			t := view.Table{Columns: []view.Column{{Name: "Plugin"}, {Name: "Installed"},
				{Name: "Available"}, {Name: "Index"}}}
			for _, r := range rows {
				available := r.AvailableVersion
				if r.Problem != "" {
					available = r.Problem
				}
				t.Rows = append(t.Rows, []string{r.Name, r.InstalledVersion, available, r.Index})
			}
			return renderView(cmd, opts, t)
		},
	}
}

func newPluginIndexCommand(opts *globalOpts) *cobra.Command {
	root := &cobra.Command{
		Use:   "index",
		Short: "Attach, refresh and detach plugin indexes",
		Long: "An index is a git repository of plugins/<name>.yaml manifests — claims\n" +
			"about plugins, searchable without downloading anything. rta shells out\n" +
			"to your git, so your remotes, proxies and credentials keep working —\n" +
			"except that a repository may not name a remote helper (`ext::` runs a\n" +
			"command line) and may not be fetched in cleartext.\n" +
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
			// Origin is shown because it decides something: an index
			// attached from a path on this machine may name file://
			// artifacts and one attached from a network URL may not, so an
			// operator reading a refusal that says "attached from X" needs
			// somewhere to see what rta thinks X is. Until now nothing
			// showed where an attached index came from at all.
			//
			// Masked on the way out — an origin can carry a token, and a
			// table is also `--output json` and terminal scrollback.
			t := view.Table{Columns: []view.Column{{Name: "Index"}, {Name: "Origin"},
				{Name: "Plugins"}, {Name: "Problems"}}}
			for _, ix := range indexes {
				listed, bad := plugindist.Manifests(ix)
				origin, verr := plugindist.IndexOrigin(cmd.Context(), ix)
				shown := plugindist.OriginForDisplay(origin)
				if verr != nil {
					// Not fatal: a listing that refused to name the other
					// indexes because one of them is odd would be a worse
					// answer than the odd one.
					shown = "unknown"
				}
				t.Rows = append(t.Rows, []string{ix.Name, shown,
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

// newPluginManifestCommand implements `rta plugin manifest`: the producer side
// of an index, and the half that was missing.
//
// rta could read an index, search one, install from one and refuse an index
// that lied — and nothing anywhere helped anybody write one. So publishing a
// plugin meant hand-transcribing its declaration into YAML: every capability
// ID, its safety class, whether it needs a grant, and a sha256 per platform.
// That transcription is graded, and not by the person doing it — a slip
// surfaces at a stranger's `rta plugin install` as "index X claims Y and the
// binary disagrees", which is a true message pointing at somebody who is not
// reading it.
//
// Deriving it removes the step rather than checking it. rta already runs a
// plugin sandboxed to read its declaration on every load; doing it here and
// writing down what it says makes a manifest that disagrees with its artifact
// unrepresentable. What the author supplies is the one thing the artifact
// cannot know: where its bytes will be published.
func newPluginManifestCommand(opts *globalOpts) *cobra.Command {
	var (
		version   string
		homepage  string
		platforms []string
		checksums string
		binMember string
		indexDir  string
	)
	cmd := &cobra.Command{
		Use:   "manifest <binary>",
		Short: "Write an index manifest from a plugin binary's own declaration",
		Long: "Runs the binary the way a load does — sandboxed — and writes the index\n" +
			"entry its declaration implies: name, version, summary, every capability\n" +
			"with its safety class and grant flag, and every credential location it\n" +
			"asks for. None of that is typed, so none of it can drift from the\n" +
			"artifact.\n\n" +
			"You supply where the bytes will live:\n\n" +
			"    rta plugin manifest bin/rta-plugin-pg --version v1.2.0 \\\n" +
			"      --checksums dist/checksums.txt \\\n" +
			"      --platform linux/amd64=https://example.com/pg_linux_amd64.tar.gz\n\n" +
			"A platform whose artifact is a local file is hashed on the spot, and its\n" +
			"archive is opened to prove the `bin:` claim; anything else is looked up\n" +
			"in the checksums file by filename.\n\n" +
			"Prints the manifest, or writes it into an index with --index.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := plugindist.GenerateRequest{
				Binary:   args[0],
				Version:  version,
				Homepage: homepage,
			}
			for _, spec := range platforms {
				src, verr := parsePlatformSpec(spec, binMember)
				if verr != nil {
					return verr
				}
				req.Platforms = append(req.Platforms, src)
			}
			if checksums != "" {
				raw, err := os.ReadFile(checksums)
				if err != nil {
					return view.Errorf("plugin.manifest.checksums", "%v", err)
				}
				sums, verr := plugindist.ParseChecksums(raw)
				if verr != nil {
					return verr
				}
				req.Checksums = sums
			}
			doc, m, verr := plugindist.Generate(cmd.Context(), req, cmd.ErrOrStderr())
			if verr != nil {
				return verr
			}
			if indexDir == "" {
				// The file's exact bytes, not a rendering of them. This
				// command produces a document that goes into a git repository
				// and whose name is load-bearing; a caller redirecting it is
				// doing the ordinary thing, and a view wrapper would make
				// `> plugins/pg.yaml` write something an index refuses.
				_, err := cmd.OutOrStdout().Write(doc)
				return err
			}
			dest := filepath.Join(indexDir, plugindist.FileName(m))
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return view.Errorf("plugin.manifest.write", "%v", err)
			}
			// Atomically, because this lands in a git repository somebody
			// commits. A truncated manifest is a claim about a plugin that
			// is not the claim anybody made, and the window a plain write
			// opens is exactly the one where a regeneration is interrupted
			// half way through an index.
			if err := atomicfile.Write(dest, doc, 0o644); err != nil {
				return view.Errorf("plugin.manifest.write", "%v", err)
			}
			return renderView(cmd, opts, view.KeyValue{Pairs: []view.Pair{
				{Key: "wrote", Value: dest},
				{Key: "version", Value: m.Version},
				{Key: "claims", Value: plural(len(m.Capabilities), "capability", "capabilities") +
					" — " + m.SafetyLine()},
				{Key: "platforms", Value: m.Offered()},
			}})
		},
	}
	cmd.Flags().StringVar(&version, "version", "",
		"version to claim (default: what the binary declares)")
	cmd.Flags().StringVar(&homepage, "homepage", "", "where a person reads more, as an https URL")
	// Worded so the first word is not a literal: the help renderer
	// sentence-cases a flag's description, and `<Os>/<Arch>` is a platform
	// the manifest grammar refuses.
	cmd.Flags().StringArrayVar(&platforms, "platform", nil,
		"one <os>/<arch>=<url or local file>, repeated once per artifact")
	cmd.Flags().StringVar(&checksums, "checksums", "",
		"file of `<sha256>  <file>` lines covering the artifacts")
	cmd.Flags().StringVar(&binMember, "bin", "",
		"where the binary sits inside a .tar.gz artifact (default: rta-plugin-<name>)")
	cmd.Flags().StringVar(&indexDir, "index", "",
		"index directory to write plugins/<name>.yaml into, instead of printing")
	return cmd
}

// parsePlatformSpec reads `<os>/<arch>=<url>`.
//
// A value with no URL scheme is a path on this machine, and it is turned into
// the absolute file:// URL a manifest requires. That is the whole local
// rehearsal story: build the plugins, point at the files, and the index that
// comes out is one `rta plugin install` genuinely works against — the same
// fetch, the same hash, the same sandboxed declaration check as a published
// one, minus the publishing.
func parsePlatformSpec(spec, binMember string) (plugindist.PlatformSource, *view.Error) {
	bad := func(format string, args ...any) *view.Error {
		return view.Errorf("plugin.manifest.platform", format, args...).
			WithHint("`--platform <os>/<arch>=<url>`, for example " +
				"linux/amd64=https://example.com/pg_linux_amd64.tar.gz")
	}
	target, location, ok := strings.Cut(spec, "=")
	if !ok || location == "" {
		return plugindist.PlatformSource{}, bad("%q states no artifact", spec)
	}
	goos, goarch, ok := strings.Cut(target, "/")
	if !ok || goos == "" || goarch == "" {
		return plugindist.PlatformSource{}, bad("%q does not start with <os>/<arch>", spec)
	}
	if !strings.Contains(location, "://") {
		abs, err := filepath.Abs(location)
		if err != nil {
			return plugindist.PlatformSource{}, bad("%s: %v", location, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return plugindist.PlatformSource{}, view.Errorf("plugin.manifest.platform",
				"%s: %v", location, err).
				WithHint("a value with no scheme is a file on this machine; " +
					"a published artifact needs its https:// URL")
		}
		location = "file://" + filepath.ToSlash(abs)
	}
	return plugindist.PlatformSource{OS: goos, Arch: goarch, URL: location, Bin: binMember}, nil
}
