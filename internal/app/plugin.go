package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
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
			"Anything named " + pluginhost.Prefix + "* on your $PATH is loaded.\n\n" +
			"`rta plugin list` is the inventory; `new` and `dev` are for writing one.",
		RunE: groupRunE,
	}
	root.AddCommand(newPluginListCommand(reg, opts))
	root.AddCommand(newPluginNewCommand())
	root.AddCommand(newPluginDevCommand(reg, version, opts))
	return root
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
