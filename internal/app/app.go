// Package app assembles the CLI: it loads built-in plugins into the registry
// and materializes every capability as a cobra command. This is the CLI
// renderer of the capability model — the TUI, MCP and web surfaces are
// siblings, not callers, of this package.
package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rule-them-all/builtin/all"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/internal/render/tui"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// ExitCode maps an error returned by Execute to the fixed exit-code contract
// (PROJECT.md §4.3): 0 ok, 1 capability error, 2 usage error, 3 confirmation
// declined.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ve *view.Error
	if ok := asViewError(err, &ve); ok {
		if ve.Code == CodeConfirmRequired {
			return 3
		}
		return 1
	}
	return 2
}

// CodeConfirmRequired is returned when a destructive capability runs without
// confirmation. Interactive confirmation (huh) lands in M1; until then --yes
// is the only way through.
const CodeConfirmRequired = "core.confirm.required"

func asViewError(err error, target **view.Error) bool {
	ve, ok := err.(*view.Error)
	if ok {
		*target = ve
	}
	return ok
}

// NewRegistry builds the registry of built-in plugins. The catalogue itself
// lives in builtin/all, where nothing downstream of it can be an import
// cycle away from asking what is in it.
func NewRegistry() (*registry.Registry, error) { return all.Registry() }

type globalOpts struct {
	output  string
	noColor bool
	yes     bool
	dryRun  bool
}

// NewRoot builds the root cobra command over the given registry.
func NewRoot(reg *registry.Registry, version string) *cobra.Command {
	opts := &globalOpts{}
	// Config is optional; a broken file must not brick the CLI — doctor and
	// init both diagnose it, so they need the binary to still run.
	cfg, cfgErr := config.Load()
	defaultOutput := cfg.Output
	if defaultOutput == "" {
		defaultOutput = "pretty"
	}
	root := &cobra.Command{
		Use:           "rta",
		Short:         "One capability model to rule them all",
		Long:          "rta is a single extendable binary offering one consistent interface\nover the tools you juggle daily — scriptable CLI, TUI, and MCP for AI agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `rta` on a TTY opens the interactive shell; in a pipe it
		// prints help so scripts never hang on an invisible TUI.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY() {
				return cmd.Help()
			}
			if cfgErr != nil {
				return cfgErr
			}
			return tui.Run(cmd.Context(), reg, cfg.Dashboard)
		},
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&opts.output, "output", "o", defaultOutput, "output format: pretty|json|yaml|csv|md")
	pf.BoolVar(&opts.noColor, "no-color", false, "disable styled output")
	pf.BoolVarP(&opts.yes, "yes", "y", false, "skip confirmation prompts")
	pf.BoolVar(&opts.dryRun, "dry-run", false, "report what would happen without doing it")

	// Namespace commands host their capabilities as subcommands.
	nsCmds := map[string]*cobra.Command{}
	for _, p := range reg.Plugins() {
		nsCmd := &cobra.Command{
			Use:   p.Name,
			Short: p.Summary,
			// Bare namespace shows help; unknown subcommands are usage errors.
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return cmd.Help()
				}
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			},
		}
		nsCmds[p.Name] = nsCmd
		root.AddCommand(nsCmd)
	}
	for _, c := range reg.Capabilities() {
		attach(nsCmds[c.Words()[0]], c, opts)
	}
	root.AddCommand(newMCPCommand(reg, version))
	root.AddCommand(newExplainCommand(reg, opts))
	root.AddCommand(newPluginsCommand(reg, opts))
	root.AddCommand(newDoctorCommand(reg, opts))
	root.AddCommand(newInitCommand(reg))
	return root
}

// attach materializes one capability as a (possibly nested) cobra command.
func attach(parent *cobra.Command, c plugin.Capability, opts *globalOpts) {
	words := c.Words()
	// For 3-segment IDs (pg.table.list) create/reuse the middle noun command.
	if len(words) == 3 {
		noun := findOrCreate(parent, words[1])
		parent = noun
	}
	leaf := words[len(words)-1]

	var positionals []plugin.Field
	for _, f := range c.Inputs {
		if f.Positional {
			positionals = append(positionals, f)
		}
	}
	use := leaf
	for _, f := range positionals {
		if f.Required {
			use += fmt.Sprintf(" <%s>", f.Name)
		} else {
			use += fmt.Sprintf(" [%s]", f.Name)
		}
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: c.Summary,
		Long:  strings.TrimSpace(c.Summary + "\n\n" + c.Description),
		Args:  positionalArgsValidator(positionals),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCapability(cmd.Context(), cmd, c, args, opts)
		},
	}
	declareFlags(cmd, c)
	// Lookup first: pflag panics on a redefined flag, and a panic here aborts
	// the whole command tree — every rta invocation, doctor included. The name
	// is reserved in Capability.validate, so reaching this branch means
	// something bypassed registration; refusing to add a second flag is
	// cheaper than losing the binary.
	if c.Detailed && cmd.Flags().Lookup("detail") == nil {
		cmd.Flags().Bool("detail", false, "show the full detailed view")
	}
	declareCompletion(cmd, c, positionals)
	parent.AddCommand(cmd)
}

// completionTimeout bounds a suggestion lookup. Completion runs while
// somebody is holding the tab key: a shell that pauses is worse than a shell
// that offers nothing.
const completionTimeout = 2 * time.Second

// declareCompletion turns declared candidates into shell completion, for
// positionals and flags alike.
//
// This is where `Options` and `Suggest` stop being documentation: `rta kv get
// <tab>` lists your keys, `rta net dns x --type <tab>` lists the record types
// that exist. The values come from the capability, so a plugin gets it by
// declaring one field — there is no completion script to write and nothing to
// regenerate when a capability changes.
func declareCompletion(cmd *cobra.Command, c plugin.Capability, positionals []plugin.Field) {
	completable := func(f plugin.Field) bool {
		// A path is completable with nothing declared at all: the shell
		// already knows what is on the filesystem, and does it better than we
		// could — quoting, colours, and the directory you are actually in.
		return len(f.Options) > 0 || f.Suggest != nil || f.Type == plugin.Path
	}

	if len(positionals) > 0 {
		cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			f, ok := positionalAt(positionals, len(args))
			if !ok || !completable(f) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return candidates(cmd, c, f, args)
		}
	}
	for _, f := range c.Inputs {
		if f.Positional || !completable(f) {
			continue
		}
		f := f
		_ = cmd.RegisterFlagCompletionFunc(f.Name,
			func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
				return candidates(cmd, c, f, args)
			})
	}
}

// positionalAt resolves which positional field is being typed. A slice
// positional swallows every remaining argument, so once it starts it keeps
// completing — `rta net hosts rm a b <tab>` is still completing hostnames.
func positionalAt(positionals []plugin.Field, idx int) (plugin.Field, bool) {
	if idx < len(positionals) {
		return positionals[idx], true
	}
	if last := positionals[len(positionals)-1]; last.Type == plugin.StringSlice {
		return last, true
	}
	return plugin.Field{}, false
}

// candidates asks the field what it can be completed to, with everything the
// caller has already typed available to it — so a suggestion can depend on an
// earlier answer.
func candidates(cmd *cobra.Command, c plugin.Capability, f plugin.Field, args []string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()
	// Best-effort: a half-typed command line does not parse, and a completion
	// that refuses to answer until the command is valid is a completion
	// nobody sees.
	values, _ := collectValues(cmd, c, args)
	// SurfaceCompletion, not SurfaceCLI: this is a keystroke with nobody
	// waiting to answer anything, and a Suggest that would have prompted must
	// be able to tell the difference.
	req := plugin.NewRequest(values, false, false).WithSurface(plugin.SurfaceCompletion)

	// A path keeps file completion alongside whatever was declared: zsh offers
	// the suggestions first and falls back to the filesystem when none of them
	// match what is being typed, which is exactly the order you want —
	// `--identity <tab>` your keys, `--identity ~/proj/<tab>` your files.
	directive := cobra.ShellCompDirectiveNoFileComp
	if f.Type == plugin.Path {
		directive = cobra.ShellCompDirectiveDefault
	}
	out := f.Candidates(ctx, req)
	if len(out) == 0 {
		return nil, directive
	}
	return out, directive
}

func findOrCreate(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	cmd := &cobra.Command{Use: name, Short: name + " operations"}
	parent.AddCommand(cmd)
	return cmd
}

func positionalArgsValidator(fields []plugin.Field) cobra.PositionalArgs {
	required := 0
	variadic := false
	for _, f := range fields {
		if f.Required {
			required++
		}
		if f.Type == plugin.StringSlice {
			variadic = true // a slice positional swallows all remaining args
		}
	}
	if variadic {
		return cobra.MinimumNArgs(required)
	}
	return cobra.MatchAll(cobra.MinimumNArgs(required), cobra.MaximumNArgs(len(fields)))
}

func declareFlags(cmd *cobra.Command, c plugin.Capability) {
	for _, f := range c.Inputs {
		if f.Positional {
			continue
		}
		usage := flagUsage(f)
		switch f.Type {
		case plugin.Int:
			def, _ := f.Default.(int)
			cmd.Flags().Int(f.Name, def, usage)
		case plugin.Bool:
			def, _ := f.Default.(bool)
			cmd.Flags().Bool(f.Name, def, usage)
		case plugin.Float:
			def, _ := f.Default.(float64)
			cmd.Flags().Float64(f.Name, def, usage)
		case plugin.StringSlice:
			def, _ := f.Default.([]string)
			cmd.Flags().StringSlice(f.Name, def, usage)
		default:
			def, _ := f.Default.(string)
			cmd.Flags().String(f.Name, def, usage)
		}
		if f.Required {
			_ = cmd.MarkFlagRequired(f.Name)
		}
	}
}

// flagUsage renders a field's help for `--help`, appending its closed set.
// The list is generated from the declaration so it cannot drift from what the
// completion and the MCP schema offer, and so nobody has to keep a copy of it
// in prose.
func flagUsage(f plugin.Field) string {
	if len(f.Options) == 0 {
		return f.Help
	}
	set := "one of: " + strings.Join(f.Options, "|")
	if f.Help == "" {
		return set
	}
	return f.Help + " (" + set + ")"
}

func runCapability(ctx context.Context, cmd *cobra.Command, c plugin.Capability, args []string, opts *globalOpts) error {
	format, err := cli.ParseFormat(opts.output)
	if err != nil {
		return err
	}
	// Notes goes to stderr, so `rta ... -o csv > out.csv` keeps stdout pure
	// csv and still tells the person at the terminal that they got 3 of 744
	// rows. Without it a truncated result is byte-indistinguishable from a
	// complete one, which is the one thing a machine-readable format must
	// never be ambiguous about.
	renderOpts := cli.Options{
		Format: format, NoColor: opts.noColor || !isTTY(),
		Width: termWidth(), Notes: cmd.ErrOrStderr(),
	}

	// Safety gate (PROJECT.md §4.7). Interactive confirmation lands in M1;
	// in M0 destructive capabilities require an explicit --yes.
	if c.Safety == plugin.Destructive && !opts.yes && !opts.dryRun {
		verr := &view.Error{
			Code:    CodeConfirmRequired,
			Message: fmt.Sprintf("%s is destructive and needs confirmation", c.ID),
			Hint:    "re-run with --yes to confirm, or --dry-run to preview",
		}
		// Rendered here, like every other capability error: main suppresses
		// view.Errors on the assumption the runner has already shown them, so
		// returning this one unrendered made `rta todo rm 1` exit 3 in
		// silence — the right code, and not one word about how to proceed.
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return verr
	}

	values, err := collectValues(cmd, c, args)
	if err != nil {
		return err
	}
	v, runErr := c.Run(ctx, plugin.NewRequest(plugin.Resolve(c, values), opts.dryRun, opts.yes).WithSurface(plugin.SurfaceCLI))
	if runErr != nil {
		ve := view.AsError(runErr, c.ID+".failed")
		_ = cli.RenderError(cmd.ErrOrStderr(), ve, renderOpts)
		return ve
	}
	if v == nil {
		return nil
	}
	return cli.Render(cmd.OutOrStdout(), v, renderOpts)
}

func collectValues(cmd *cobra.Command, c plugin.Capability, args []string) (map[string]any, error) {
	values := map[string]any{}
	argIdx := 0
	for _, f := range c.Inputs {
		if f.Positional {
			if f.Type == plugin.StringSlice {
				// A slice positional consumes every remaining argument.
				values[f.Name] = args[argIdx:]
				argIdx = len(args)
			} else if argIdx < len(args) {
				v, err := convertArg(f, args[argIdx])
				if err != nil {
					return nil, err
				}
				values[f.Name] = v
				argIdx++
			} else if f.Default != nil {
				values[f.Name] = f.Default
			}
			continue
		}
		switch f.Type {
		case plugin.Int:
			v, err := cmd.Flags().GetInt(f.Name)
			if err != nil {
				return nil, err
			}
			values[f.Name] = v
		case plugin.Bool:
			v, err := cmd.Flags().GetBool(f.Name)
			if err != nil {
				return nil, err
			}
			values[f.Name] = v
		case plugin.Float:
			v, err := cmd.Flags().GetFloat64(f.Name)
			if err != nil {
				return nil, err
			}
			values[f.Name] = v
		case plugin.StringSlice:
			v, err := cmd.Flags().GetStringSlice(f.Name)
			if err != nil {
				return nil, err
			}
			values[f.Name] = v
		default:
			v, err := cmd.Flags().GetString(f.Name)
			if err != nil {
				return nil, err
			}
			values[f.Name] = v
		}
	}
	// The detail flag is a rendering depth, not a declared input.
	if c.Detailed {
		if v, err := cmd.Flags().GetBool("detail"); err == nil {
			values["detail"] = v
		}
	}
	return values, nil
}

// convertArg parses a positional argument according to its declared type,
// so handlers always receive properly typed values.
func convertArg(f plugin.Field, raw string) (any, error) {
	switch f.Type {
	case plugin.Int:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("argument %q must be an integer, got %q", f.Name, raw)
		}
		return v, nil
	case plugin.Float:
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("argument %q must be a number, got %q", f.Name, raw)
		}
		return v, nil
	case plugin.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("argument %q must be true or false, got %q", f.Name, raw)
		}
		return v, nil
	default:
		return raw, nil
	}
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// termWidth reports the width pretty output should be shaped to: the terminal's
// own width when stdout is a terminal, and 0 — "natural width, wrap nothing" —
// otherwise. It is the same rule the renderer already applies to colour
// (NoColor: ... || !isTTY()): a human at a terminal gets terminal-shaped
// output, a pipe or a file gets bytes that do not depend on who ran the
// command or how wide their window happened to be.
func termWidth() int {
	if !isTTY() {
		return 0
	}
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 0
	}
	return w
}
