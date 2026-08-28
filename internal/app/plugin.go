package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	root.AddCommand(newPluginNewCommand())
	root.AddCommand(newPluginDevCommand(reg, version, opts))
	root.AddCommand(newPluginInstallCommand(opts))
	root.AddCommand(newPluginUpgradeCommand(opts))
	root.AddCommand(newPluginRemoveCommand(opts))
	root.AddCommand(newPluginSearchCommand(opts))
	root.AddCommand(newPluginIndexCommand(opts))
	return root
}

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
			// Resolved before writing, so the replace directive names an
			// absolute path that still works when the plugin is built from
			// somewhere else.
			if rtaSource == "" {
				rtaSource = findRta()
			}
			if rtaSource != "" {
				abs, err := filepath.Abs(rtaSource)
				if err != nil {
					return view.Errorf("plugin.badsource", "resolving %q: %v", rtaSource, err)
				}
				s.RtaPath = abs
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
// The spawn is the ordinary one. ADR 0012 §6 is explicit that `rta plugin dev`
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
	// crash report, says what it is.
	binary := filepath.Join(out, pluginhost.Prefix+"dev")

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
		{Key: "confinement", Value: confinementLine()},
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
		pairs = append(pairs, view.Pair{Key: "dashboard tile", Value: "none — nothing here can run unasked"})
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

func confinementLine() string {
	if !pluginhost.Confined() {
		return "none on this platform — see `rta doctor`"
	}
	d, err := pluginhost.Resolve()
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return fmt.Sprintf("sandboxed: %d paths denied read+write, %d denied read (`rta doctor` lists them)",
		len(d.NoAccess), len(d.NoRead))
}
