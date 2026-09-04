package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/internal/render/tui"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// newPluginCommand is everything to do with plugins: the inventory, and the
// authoring commands.
//
// One namespace, not two. `rta plugins` was a separate top-level command
// until it became `rta plugin list`, because two entries one letter apart in
// the same list is a thing people get wrong every time — and because every
// other listing in rta is `<namespace> list`, a convention sdktest enforces
// on plugin authors and rta was not following itself.
//
// The ordering matters more than it looks: `list` is what almost everybody
// wants and `new`/`dev` are for the far smaller number of people writing a
// plugin. Keeping them in one namespace costs the reader one line of help and
// saves them the guess.
func newPluginCommand(reg *registry.Registry, version string, opts *globalOpts) *cobra.Command {
	root := &cobra.Command{
		Use:   "plugin",
		Short: "List, write and test plugins",
		Long: "A plugin is a program that returns a declaration and serves it over\n" +
			"gRPC; rta launches it and renders what it declares on every surface.\n" +
			"rta finds anything named " + pluginhost.Prefix + "* on your $PATH and runs it once\n" +
			"you have approved that artifact — `rta plugin trust`.\n\n" +
			"`rta plugin list` is the inventory; `new` and `dev` are for writing one.",
		RunE: groupRunE,
	}
	root.AddCommand(newPluginListCommand(reg, opts))
	root.AddCommand(newPluginTrustCommand(opts))
	root.AddCommand(newPluginUntrustCommand())
	root.AddCommand(newPluginAllowCommand(opts))
	root.AddCommand(newPluginDisallowCommand(opts))
	root.AddCommand(newPluginNewCommand())
	root.AddCommand(newPluginDevCommand(reg, version, opts))
	root.AddCommand(newPluginInstallCommand(opts))
	root.AddCommand(newPluginUpgradeCommand(opts))
	root.AddCommand(newPluginOutdatedCommand(opts))
	root.AddCommand(newPluginRemoveCommand(opts))
	root.AddCommand(newPluginSearchCommand(opts))
	root.AddCommand(newPluginManifestCommand(opts))
	root.AddCommand(newPluginIndexCommand(opts))
	return root
}

// newPluginAllowCommand implements `rta plugin allow`: the second decision,
// after trust and separate from it.
//
// On the platforms that confine plugins, a standard list of credential
// locations is denied to all of them — a weather plugin has no business
// reading a kubeconfig. (macOS confines; Linux and Windows do not, and say so
// through `rta doctor` and confinementLine below. The allow list still governs
// what a plugin *declares* it needs on every platform, which is what this
// command edits; what changes is whether anything enforces the denial.) Some
// plugins exist to use one, and for those the denial is not caution but
// breakage: `~/.kube` was denied from the first commit of the plugin host, and
// both plugins/kube and plugins/cnpg were unable to read a kubeconfig at all
// on macOS, which is to say unable to do anything.
//
// The plugin declares what it needs; this is where somebody decides. Two
// commands rather than one, because "these bytes may run" and "these bytes may
// read my cluster credentials" are different questions and an answer to the
// first must not stand in for the second. Both attach to the digest, so a
// rebuild asks both again.
//
// **Only what the plugin declared.** An operator cannot allow a location the
// artifact never asked for: that would be handing out access nobody requested,
// and the request is the only reason there is anything to weigh.
func newPluginAllowCommand(opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "allow [name] [location...]",
		Short: "Let a plugin read a credential location it declares it needs",
		Long: "Where plugins run confined, rta denies them a standard list of\n" +
			"credential locations — see `rta doctor` for whether this platform is one.\n" +
			"A plugin that exists to use one declares which; this is where you decide.\n\n" +
			"Separate from `rta plugin trust` on purpose: running an artifact and\n" +
			"letting it read your credentials are different decisions. Both attach to\n" +
			"the artifact's digest, so rebuilding a plugin asks again.\n\n" +
			"With no argument, lists what every loaded plugin asks for and what it has.\n" +
			"With a name and no locations, allows everything that plugin declares.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			render := func(v view.View) error {
				return cli.Render(cmd.OutOrStdout(), v,
					cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()})
			}
			if len(args) == 0 {
				return render(needsInventory())
			}
			c, verr := loadedByName(args[0])
			if verr != nil {
				return verr
			}
			declared := c.Declared.Needs
			if len(declared) == 0 {
				// Named by digest, because the declaration is the artifact's
				// and not the plugin's. Somebody whose installed copy is
				// older than the source they are reading gets this refusal,
				// and the digest is the thread that leads them to why.
				return view.Errorf("plugin.allow.none",
					"%s@%s declares no credential location it needs",
					args[0], c.Identity.Short()).
					WithHint("nothing to allow — a plugin only reaches a denied location " +
						"when its own declaration asks for one, and this is the " +
						"declaration of the artifact rta loaded")
			}
			want, verr := chosenNeeds(args[0], args[1:], declared)
			if verr != nil {
				return verr
			}
			union := mergedAllow(plugintrust.Load().Allowed(c.Identity.Digest), want)
			if verr := plugintrust.Allow(c.Identity.Digest, union); verr != nil {
				return verr
			}
			pairs := []view.Pair{
				{Key: "allowed", Value: c.Declared.Name},
				{Key: "digest", Value: c.Identity.Digest},
			}
			for _, loc := range union {
				pairs = append(pairs, view.Pair{Key: "may read", Value: needLine(plugin.Need(loc))})
			}
			pairs = append(pairs, view.Pair{Key: "next",
				Value: "it applies on your next `rta` command — the plugin is relaunched with " +
					"that location left out of its sandbox"})
			return render(view.KeyValue{Pairs: pairs})
		},
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
			// The plugins that ask for something first, then that plugin's own
			// declared locations — never the whole closed set, because a
			// location the artifact did not ask for is not allowable.
			var out []cobra.Completion
			if len(args) == 0 {
				for _, c := range loadedPlugins {
					if len(c.Declared.Needs) > 0 {
						out = append(out, cobra.CompletionWithDesc(c.Declared.Name,
							"asks for "+needNames(c.Declared.Needs)))
					}
				}
				return out, cobra.ShellCompDirectiveNoFileComp
			}
			c, verr := loadedByName(args[0])
			if verr != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			for _, n := range c.Declared.Needs {
				if !containsArg(args[1:], string(n)) {
					out = append(out, cobra.CompletionWithDesc(string(n), pluginhost.Tier2Path(n)))
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
	}
	return cmd
}

// newPluginDisallowCommand withdraws it.
//
// Its own command rather than `allow` with no locations, because taking access
// away has to be something somebody typed on purpose. `allow x` with an empty
// list would be one keystroke away from `allow x`, and the two would mean
// opposite things.
func newPluginDisallowCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "disallow <name>",
		Short: "Withdraw a plugin's access to the locations it was allowed",
		Long: "The plugin keeps running — this takes back only what `rta plugin allow`\n" +
			"gave it, so the next call that wanted the file fails and says so.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			c, verr := loadedByName(args[0])
			if verr != nil {
				return verr
			}
			had := plugintrust.Load().Allowed(c.Identity.Digest)
			if len(had) == 0 {
				return view.Errorf("plugin.disallow.none",
					"%s is not allowed any credential location", args[0]).
					WithHint("nothing to withdraw")
			}
			if verr := plugintrust.Allow(c.Identity.Digest, nil); verr != nil {
				return verr
			}
			return cli.Render(cmd.OutOrStdout(), view.KeyValue{Pairs: []view.Pair{
				{Key: "withdrawn", Value: c.Declared.Name},
				{Key: "digest", Value: c.Identity.Digest},
				{Key: "no longer reads", Value: strings.Join(had, ", ")},
				{Key: "next", Value: "it applies on your next `rta` command"},
			}}, cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()})
		},
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			var out []cobra.Completion
			set := plugintrust.Load()
			for _, c := range loadedPlugins {
				if len(set.Allowed(c.Identity.Digest)) > 0 {
					out = append(out, cobra.Completion(c.Declared.Name))
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
	}
}

// needsInventory is every loaded plugin that asks for a credential location,
// and what it has been given.
//
// Only the ones that ask. A table listing every installed plugin with an empty
// column is a table nobody reads, and the column's arrival is itself the news:
// a plugin appearing here is a plugin that wants something.
func needsInventory() view.View {
	set := plugintrust.Load()
	t := view.Table{Columns: []view.Column{
		{Name: "Plugin"}, {Name: "Asks for"}, {Name: "Status", Kind: view.KindStatus},
		{Name: "To allow"},
	}}
	for _, c := range loadedPlugins {
		if len(c.Declared.Needs) == 0 {
			continue
		}
		allowed := set.Allowed(c.Identity.Digest)
		status, action := "warn", "rta plugin allow "+c.Declared.Name
		if len(allowed) == len(c.Declared.Needs) {
			status, action = "ok", "—"
		}
		t.Rows = append(t.Rows, []string{
			c.Declared.Name, needNames(c.Declared.Needs), status, action,
		})
	}
	t.Total = len(t.Rows)
	if t.Total == 0 {
		return view.Text{Body: "No installed plugin asks for a credential location."}
	}
	return t
}

// loadedByName finds a loaded plugin, which is also the check that it is
// trusted: an untrusted artifact is never launched, so it has no declaration
// and nothing here could know what it asks for.
func loadedByName(name string) (*pluginhost.Client, *view.Error) {
	for _, c := range loadedPlugins {
		if c.Declared.Name == name {
			return c, nil
		}
	}
	return nil, view.Errorf("plugin.allow.notloaded", "no loaded plugin called %q", name).
		WithHint("`rta plugin list` shows what is loaded; an artifact that is not trusted " +
			"is never run, so rta cannot know what it declares")
}

// chosenNeeds is what the operator asked for, refusing anything the plugin did
// not declare.
func chosenNeeds(name string, asked []string, declared []plugin.Need) ([]plugin.Need, *view.Error) {
	if len(asked) == 0 {
		return declared, nil
	}
	var out []plugin.Need
	for _, a := range asked {
		n := plugin.Need(a)
		if !slices.Contains(declared, n) {
			return nil, view.Errorf("plugin.allow.undeclared",
				"%s does not ask for %q", name, a).
				WithHint("it declares " + needNames(declared) + " — a location the artifact " +
					"never asked for is access nobody requested")
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// mergedAllow unions want into had, so a second `allow` naming a different
// location adds to what an earlier one already granted rather than
// replacing it.
//
// plugintrust.Allow stores exactly the list it is given, and disallow is
// its own command "because taking access away has to be something somebody
// typed on purpose" (see its own doc comment). allow calling Allow with
// only the locations named in one invocation would silently withdraw
// whatever an earlier call had granted — the same removal disallow exists
// to make deliberate, reached by accident instead.
func mergedAllow(had []string, want []plugin.Need) []string {
	union := append([]string{}, had...)
	for _, n := range want {
		if !slices.Contains(union, string(n)) {
			union = append(union, string(n))
		}
	}
	return union
}

// needLine is a need and the path it names, because "kubeconfig" is the label
// and `~/.kube` is the thing being handed over.
func needLine(n plugin.Need) string {
	if p := pluginhost.Tier2Path(n); p != "" {
		return string(n) + " (" + p + ")"
	}
	return string(n)
}

// ungranted is what a plugin asked for and has not been given.
func ungranted(declared []plugin.Need, allowed []string) []plugin.Need {
	var out []plugin.Need
	for _, n := range declared {
		if !slices.Contains(allowed, string(n)) {
			out = append(out, n)
		}
	}
	return out
}

func needNames(ns []plugin.Need) string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = string(n)
	}
	return strings.Join(out, ", ")
}

func containsArg(args []string, want string) bool { return slices.Contains(args, want) }

// newPluginTrustCommand implements `rta plugin trust`: the decision that lets
// an artifact run at all.
//
// Bare, it lists what has been found and refused. With a name, it approves
// that artifact — and it prints what it is approving first, because the whole
// value of the step is that somebody looked.
//
// Nothing here executes the binary. That is the point: rta cannot show what an
// untrusted plugin declares, because asking it would be the thing being
// decided about. What it can show is what the filesystem says — where the file
// is, how big it is, when it was written, and the digest the approval attaches
// to — and the operator brings everything else they know about where it came
// from.
//
// Those facts are printed *with* the approval rather than before it behind a
// prompt, and that is a judgement rather than an omission: typing `rta plugin
// trust weather` is already the deliberate act, and a confirmation on top of
// an explicit command is the kind of friction people learn to answer without
// reading. What the output is for is the moment after — a size or a timestamp
// that is not what you expected is a thing to act on, and `rta plugin untrust`
// is one command away.
func newPluginTrustCommand(opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust [name]",
		Short: "Allow a discovered plugin artifact to run",
		Long: "A binary on your $PATH is not consent. rta loads a plugin by *running*\n" +
			"it, so an unapproved `" + pluginhost.Prefix + "*` would execute before anybody\n" +
			"typed a command naming it — including during shell completion.\n\n" +
			"Trust attaches to the artifact's content digest, not its name, so\n" +
			"rebuilding or replacing a plugin needs approving again. That is the\n" +
			"feature: a plugin's bytes changing under a name you already approved is\n" +
			"the event worth stopping for.\n\n" +
			"With no argument, lists what was found and not run.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			render := func(v view.View) error {
				return cli.Render(cmd.OutOrStdout(), v,
					cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()})
			}
			if len(args) == 0 {
				return render(trustInventory())
			}
			found, verr := discovered(args[0])
			if verr != nil {
				return verr
			}
			if verr := plugintrust.Add(found.Digest, found.Name, found.Path); verr != nil {
				return verr
			}
			pairs := []view.Pair{
				{Key: "trusted", Value: found.Name},
				{Key: "artifact", Value: found.Path},
				{Key: "digest", Value: found.Digest},
			}
			if info, err := os.Stat(found.Path); err == nil {
				pairs = append(pairs,
					view.Pair{Key: "size", Value: humanBytes(info.Size())},
					view.Pair{Key: "modified", Value: info.ModTime().Format(time.RFC3339)})
			}
			pairs = append(pairs, view.Pair{Key: "next",
				Value: "it loads on your next `rta` command — `rta plugin list` shows what it declares"})
			return render(view.KeyValue{Pairs: pairs})
		},
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			// Only the ones that are actually waiting: completing a plugin
			// that is already trusted offers a keystroke that does nothing.
			var out []cobra.Completion
			for _, u := range untrustedPlugins() {
				out = append(out, cobra.CompletionWithDesc(u.Name, u.Short()+" — "+u.Path))
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
	}
	return cmd
}

// newPluginUntrustCommand takes an approval back.
//
// By name as well as by digest, because that is how somebody reaches for it in
// the moment they want it: an operator taking a plugin back has a name in
// their head, and every digest that name ever had is a thing they want gone.
func newPluginUntrustCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "untrust <name|digest>",
		Short: "Withdraw approval from a plugin artifact",
		Long: "Removes every approval recorded under a name, or the one matching a\n" +
			"digest prefix. The binary is left exactly where it is, because deleting\n" +
			"somebody's file is not what \"I no longer trust this\" asked for.\n\n" +
			"Trust is checked once per process, before anything is launched, so a\n" +
			"session already running keeps the plugin it loaded — restart `rta mcp\n" +
			"serve` or the TUI to be rid of it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, verr := plugintrust.Remove(args[0])
			if verr != nil {
				return verr
			}
			if n == 0 {
				return view.Errorf("plugin.untrust.unknown",
					"nothing trusted is called %q", args[0]).
					WithHint("`rta plugin trust` with no argument lists what is waiting; " +
						"`rta doctor` lists what is trusted")
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"withdrew %s — it will not load again; a session already running keeps what it loaded\n",
				plural(n, "approval", "approvals"))
			return nil
		},
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			var out []cobra.Completion
			for _, e := range plugintrust.Load().Entries() {
				// Every name the artifact has been trusted under, because
				// untrust accepts every one of them and a completion that
				// offered fewer would be the shell disagreeing with the
				// command it completes.
				for _, name := range e.Names {
					out = append(out, cobra.CompletionWithDesc(name, e.Short()+" — "+e.Path))
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		},
	}
}

// humanBytes is a file size a person reads without counting digits. Local to
// this one report: nothing else in the app formats a size, and a shared helper
// for a single caller is a dependency without a reason.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// discovered finds one plugin by the name it is installed under, and hashes
// it. It refuses a name that is not on $PATH rather than trusting a digest
// nothing produced.
func discovered(name string) (pluginhost.Untrusted, *view.Error) {
	for _, f := range pluginhost.Discover() {
		if f.Name != name {
			continue
		}
		id, err := pluginhost.Identify(f.Path)
		if err != nil {
			return pluginhost.Untrusted{}, view.Errorf("plugin.trust.unreadable", "%v", err)
		}
		return pluginhost.Untrusted{Name: f.Name, Path: id.Path, Digest: id.Digest}, nil
	}
	return pluginhost.Untrusted{}, view.Errorf("plugin.trust.notfound",
		"no plugin called %q is installed", name).
		WithHint("rta finds a plugin as `" + pluginhost.Prefix + name + "` on your $PATH; " +
			"`rta plugin trust` with no argument lists what it found")
}

// untrustedPlugins is what discovery would refuse right now.
//
// Recomputed rather than read off the host, because this command runs *after*
// startup already skipped them and the operator may have installed one since —
// and because it is a filesystem question with a filesystem answer, needing no
// plugin to be running to ask it.
func untrustedPlugins() []pluginhost.Untrusted {
	trusted := plugintrust.Load()
	var out []pluginhost.Untrusted
	for _, f := range pluginhost.Discover() {
		id, err := pluginhost.Identify(f.Path)
		if err != nil || trusted.Trusts(id.Digest) {
			continue
		}
		out = append(out, pluginhost.Untrusted{Name: f.Name, Path: id.Path, Digest: id.Digest})
	}
	return out
}

// trustInventory is what `rta plugin trust` shows with no argument: what was
// found and not run, and what it would take to change that.
func trustInventory() view.View {
	waiting := untrustedPlugins()
	if len(waiting) == 0 {
		n := plugintrust.Load().Len()
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "waiting", Value: "nothing — every plugin found on $PATH is one you have approved"},
			{Key: "trusted", Value: plural(n, "artifact", "artifacts")},
		}}
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Plugin"},
		{Name: "Digest"},
		{Name: "Artifact"},
		{Name: "To load it"},
	}}
	for _, u := range waiting {
		t.Rows = append(t.Rows, []string{u.Name, u.Short(), u.Path, "rta plugin trust " + u.Name})
	}
	t.Total = len(t.Rows)
	return t
}

func newPluginNewCommand() *cobra.Command {
	var dir, module, rtaSource string
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a working plugin",
		Long: "Writes a plugin that builds and runs as it stands, rather than a\n" +
			"skeleton with TODOs in it — so the first run works, and every edit\n" +
			"after it is a change to something known-good.\n\n" +
			"<name> is the namespace: it becomes `rta <name> ...`, the prefix of\n" +
			"every capability ID, and part of the binary filename.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if verr := checkName(name); verr != nil {
				return verr
			}
			s := scaffold{
				Name:   name,
				Binary: pluginhost.Prefix + name,
				Module: module,
				RtaMod: rtaModule,
			}
			if s.Module == "" {
				s.Module = s.Binary
			}
			if dir == "" {
				dir = s.Binary
			}
			if rtaSource == "" {
				rtaSource = findRta()
			}
			if rtaSource != "" {
				abs, err := filepath.Abs(rtaSource)
				if err != nil {
					return view.Errorf("plugin.badsource", "resolving %q: %v", rtaSource, err)
				}
				s.RtaPath = replacePath(abs, dir)
			}
			if err := s.write(dir); err != nil {
				return err
			}
			// Resolve the module graph now, so the very first `go build` the
			// author runs succeeds. Without it the scaffold is a directory
			// that does not compile — "go: updates to go.mod needed" is the
			// first thing a stranger sees, which is the outcome writing a
			// working template was meant to avoid.
			tidy := exec.CommandContext(cmd.Context(), "go", "mod", "tidy")
			tidy.Dir = dir
			if out, err := tidy.CombinedOutput(); err != nil {
				// A warning, not a failure. The files are good; resolving may
				// need a network this machine does not have, and the author
				// can run one command. Deleting their new plugin over it
				// would be worse.
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: `go mod tidy` in %s did not succeed, so the first build may fail:\n%s\n",
					dir, strings.TrimSpace(string(out)))
			}
			fmt.Fprint(cmd.OutOrStdout(), nextSteps(s, dir))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "where to write it (default: the binary name)")
	cmd.Flags().StringVar(&module, "module", "", "go module path (default: the binary name)")
	cmd.Flags().StringVar(&rtaSource, "rta-source", "",
		"local rta checkout to `replace` with, since rta is not published yet "+
			"(default: found by walking up from here)")
	// Both name a directory, so the shell does the listing. --rta-source also
	// offers the answer this command would compute for itself, because on most
	// machines there is exactly one and it is already known.
	_ = cmd.RegisterFlagCompletionFunc("dir",
		func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveFilterDirs
		})
	_ = cmd.RegisterFlagCompletionFunc("rta-source",
		func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if found := findRta(); found != "" {
				return []cobra.Completion{
					cobra.CompletionWithDesc(found, "the checkout rta found for itself"),
				}, cobra.ShellCompDirectiveFilterDirs
			}
			return nil, cobra.ShellCompDirectiveFilterDirs
		})
	// A new namespace is a name being invented, so there is nothing to
	// complete it from — and the filesystem is the wrong answer.
	cmd.ValidArgsFunction = cobra.NoFileCompletions
	return cmd
}

// newPluginDevCommand builds a plugin from source and uses it without
// installing it.
//
// The spawn is the ordinary one: `rta plugin dev`
// gets no confinement exemption and shares one code path with installed mode,
// because "works in dev, breaks for users" is what kills sandbox adoption —
// and a separate dev path would mean the fifteen-minute gate exercises a path
// no user ever runs. What dev changes is where the binary comes from, and
// nothing else.
//
// **It does not need `rta plugin trust`, and that is not an exception to the
// rule but the rule applied.** The trust gate exists because being on $PATH is
// not consent: nobody named the artifact, and it runs anyway. Here the operator
// named a source directory in the command they just typed, rta compiled it in
// front of them, and the binary exists only for the length of this run. The act
// of approval that a digest records has already happened, in a stronger form
// than a digest could carry. Confinement is a different question and still
// applies, which is why it is the same spawn.
func newPluginDevCommand(reg *registry.Registry, version string, opts *globalOpts) *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use:   "dev [dir] [-- command args...]",
		Short: "Build a plugin from source and run it without installing",
		Long: "Compiles the plugin in [dir] (default: the current directory), loads it\n" +
			"exactly as an installed one is loaded, and reports what rta sees.\n\n" +
			"With arguments after `--`, runs that command with the plugin loaded:\n\n" +
			"    rta plugin dev -- hello greet world\n\n" +
			"The spawn path is the installed one, sandbox included. A dev mode that\n" +
			"skipped confinement would test something nobody ships.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, rest := ".", args
			// cobra hands everything after `--` in args too, so the first
			// element is a directory only when it is not part of the command
			// the author wants to run. A leading element that names an
			// existing directory is the directory; anything else is the
			// command.
			if len(args) > 0 && cmd.ArgsLenAtDash() > 0 {
				dir, rest = args[0], args[cmd.ArgsLenAtDash():]
			} else if cmd.ArgsLenAtDash() == 0 {
				rest = args
			} else if len(args) > 0 {
				dir, rest = args[0], nil
			}

			binary, cleanup, err := buildPlugin(cmd.Context(), dir, keep, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer cleanup()

			host := pluginhost.New(cmd.ErrOrStderr())
			defer host.CloseAll()
			client, err := host.Open(cmd.Context(), binary)
			if err != nil {
				return view.Errorf("plugin.dev.load", "%v", err).
					WithHint("rta loads a plugin by running it and asking what it declares; " +
						"a failure here is usually a panic in Plugin() or a declaration " +
						"rta refuses — the message above says which")
			}
			// A declaration that asks for a credential location is honoured
			// here, and here only, for the reason dev mode is already exempt
			// from trust: compiling from a directory named in the command just
			// typed is a stronger act of approval than a digest in a file.
			//
			// Twice on purpose. The declaration is what says which locations,
			// and running the plugin is how a declaration is read — so the
			// first launch is fully confined and answers the question, and the
			// second is the one that can act on the answer. Only for a plugin
			// that asks for something, so the ordinary inner loop is one
			// launch as before. The report says what was allowed, because a
			// relaxation nobody is told about is the kind that surprises
			// somebody later.
			if len(client.Declared.Needs) > 0 {
				relaxed, err := host.OpenAllowing(cmd.Context(), binary, client.Declared.Needs)
				if err != nil {
					return view.Errorf("plugin.dev.load", "%v", err).
						WithHint("the plugin declares a credential location it needs, and " +
							"relaunching it with that location readable failed")
				}
				client = relaxed
			}
			if err := reg.RegisterFrom(client.Declared, client.Origin()); err != nil {
				return view.Errorf("plugin.dev.register", "%v", err).
					WithHint("the namespace this plugin declares is already taken by a " +
						"built-in or another installed plugin; change Plugin().Name")
			}

			if len(rest) == 0 {
				format, err := cli.ParseFormat(opts.output)
				if err != nil {
					return err
				}
				return cli.Render(cmd.OutOrStdout(), devReport(reg, client),
					cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()})
			}

			// Run the requested command against a root that has this plugin
			// in it. A nested cobra execution rather than a bespoke dispatch,
			// so the flags, completion, confirmation prompts and exit codes
			// are the ones the author's users will get.
			root := NewRoot(reg, version)
			root.SetArgs(rest)
			root.SetOut(cmd.OutOrStdout())
			root.SetErr(cmd.ErrOrStderr())
			return root.Execute()
		},
	}
	cmd.Flags().BoolVar(&keep, "keep", false, "leave the compiled binary in place and print where")
	return cmd
}

// buildPlugin compiles dir into a temporary binary.
func buildPlugin(ctx context.Context, dir string, keep bool, stderr any) (string, func(), error) {
	noop := func() {}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", noop, view.Errorf("plugin.dev.dir", "resolving %q: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		return "", noop, view.Errorf("plugin.dev.dir", "%s is not a Go module", abs).
			WithHint("run this from a plugin directory, or `rta plugin new <name>` to make one")
	}

	out, err := os.MkdirTemp("", "rta-plugin-dev-*")
	if err != nil {
		return "", noop, view.Errorf("plugin.dev.build", "%v", err)
	}
	// Named with the plugin prefix so anything reading the process list, or a
	// crash report, says what it is — and with the platform's own executable
	// suffix, because `go build -o` writes the name it is given and Windows
	// will not run one without `.exe`.
	binary := filepath.Join(out, pluginhost.BinaryName("dev"))

	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = abs
	if combined, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(out)
		// The compiler's own output, verbatim and unwrapped. An author
		// looking at a build failure wants the file and line, not rta's
		// opinion about it.
		return "", noop, view.Errorf("plugin.dev.build", "building %s failed:\n%s", abs,
			strings.TrimSpace(string(combined)))
	}
	if keep {
		return binary, noop, nil
	}
	return binary, func() { os.RemoveAll(out) }, nil
}

// devReport is what an author sees when they run `rta plugin dev` with no
// command: everything rta now believes about their plugin.
//
// It is the declaration as *rta decoded it*, not as the source reads. That
// distinction is the whole value: a field rta did not understand, a summary
// that got refused, a capability whose safety class is not what the author
// thought are all invisible in the source and obvious here.
func devReport(reg *registry.Registry, c *pluginhost.Client) view.View {
	p := c.Declared
	pairs := []view.Pair{
		{Key: "name", Value: p.Name},
		{Key: "summary", Value: p.Summary},
		{Key: "binary", Value: c.Identity.Path + " (" + c.Identity.Short() + ")"},
		{Key: "confinement", Value: confinementLine(p.Needs...)},
	}
	// What this run allowed that an installed copy would not get for free.
	// An author who never sees this line learns the difference the first time
	// somebody installs their plugin and it stops working.
	if len(p.Needs) > 0 {
		pairs = append(pairs, view.Pair{Key: "credentials",
			Value: needNames(p.Needs) + " — readable here because you compiled it. " +
				"Installed, it needs `rta plugin allow " + p.Name + "`"})
	}
	if p.Version != "" {
		pairs = append(pairs, view.Pair{Key: "version", Value: p.Version})
	}
	// Which capability the dashboard picks is a consequence of safety
	// classes, defaults, NoPreview and declaration order all at once, so it
	// is invisible in the source and worth stating outright — an author who
	// wanted a different one learns here that `<plugin>.overview` is how to
	// say so.
	if id, ok := tui.TileFor(reg, p); ok {
		pairs = append(pairs, view.Pair{Key: "dashboard tile", Value: id})
	} else {
		pairs = append(pairs, view.Pair{Key: "dashboard tile", Value: "none — " + tui.NoTileReason(p)})
	}

	caps := view.Table{Columns: []view.Column{
		{Name: "Capability"},
		{Name: "Safety", Kind: view.KindStatus},
		{Name: "CLI"},
		{Name: "Agents"},
	}}
	for _, cap := range p.Capabilities {
		caps.Rows = append(caps.Rows, []string{
			cap.ID, string(cap.Safety), cliForm(cap), agentReach(reg, cap),
		})
	}
	caps.Total = len(caps.Rows)

	sections := []view.Section{
		{ID: "plugin", Title: "plugin", View: view.KeyValue{Pairs: pairs}},
		{ID: "capabilities", Title: "capabilities", View: caps},
	}

	var warnings []view.Error
	for id, fields := range c.Unknown {
		warnings = append(warnings, *view.Errorf("plugin.dev.unknown",
			"%s declares %s, which this rta does not understand", id, strings.Join(fields, ", ")).
			WithHint("the plugin may have been built against a newer rta"))
	}
	return view.Sections{Items: sections, Warnings: warnings}
}

// agentReach says what it takes for an MCP caller to reach a capability,
// which is the part authors most often get wrong — Safety is a claim about
// blast radius and it is easy to write Read for something that reveals a
// secret.
func agentReach(reg *registry.Registry, c plugin.Capability) string {
	flag := mcpOptionsForExplain(reg).AllowFlag(c)
	switch {
	case flag == "":
		return "yes, by default"
	case c.NeedsGrant || c.Safety == plugin.Destructive:
		return flag + ", plus a grant a person issues"
	default:
		return flag
	}
}

// confinementLine describes the sandbox a plugin is actually running under.
//
// `allowed` is what this particular run released, so the count is this run's
// and not the machine's. The two differ in dev mode, and a report that said
// "10 denied read" directly above a line explaining that one of them is
// readable would be contradicting itself on the same screen — the sort of
// small dishonesty that teaches people to stop reading the number.
func confinementLine(allowed ...plugin.Need) string {
	if !pluginhost.Confined() {
		return "none on this platform — see `rta doctor`"
	}
	d, err := pluginhost.ResolveAllowing(allowed)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return fmt.Sprintf("sandboxed: %d paths denied read+write, %d denied read (`rta doctor` lists them)",
		len(d.NoAccess), len(d.NoRead))
}
