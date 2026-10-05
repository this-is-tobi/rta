package app

import (
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta config` — the config file, from the command line.
//
// # Why this exists
//
// Zero config is a valid config, so the file is optional, but the knobs it
// holds had no front door: the default output format was settable only through
// `rta init`, a plugin's key only by hand or from a form in the TUI, and a
// colour by editing ten hex values. Profiles and dashboard tiles had commands;
// the rest of the file was an editor and a guess at the spelling. A key the
// editor spelled wrong was ignored without a word.
//
// This is the same gap `rta profile set` closed for environments and
// `rta dashboard add` for tiles, closed the same way: the key is named as the
// `rta explain` card prints it, the value is held to what the thing it
// configures declares, and the write goes through config.Mutate, which keeps
// the comments a person wrote beside it. Bare `rta config` is the answer to
// "what is set, and where is it": the path, whether the file exists, and every
// key it states with where each value comes from.
//
// These are plain commands, never capabilities. The file decides what an agent
// is allowed to reach (the output a script parses, the profiles a grant names),
// so no tool on the MCP surface may write it: a tool that did would be a way
// for an agent to change its own limits.
func newConfigCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	render := configRender(opts)
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and change the config file",
		Long: "Bare `rta config` says where the config file is, whether it exists, and every key it\n" +
			"states with where each value comes from. rta works with no file at all: a key is\n" +
			"written only when you set it.\n\n" +
			"Keys are named the way `rta explain` prints them: `output`, `dashboard.columns`,\n" +
			"`theme.primary`, `plugins.http.timeout`. `rta config set` holds the value to what the\n" +
			"key takes and keeps the comments you wrote in the file; `rta config edit` is for\n" +
			"everything a key cannot say, and `rta config check` names what rta ignores.",
		Example: "  rta config set output json\n" +
			"  rta config set plugins.http.timeout 10\n" +
			"  rta config set theme.primary '#7aa2f7'\n" +
			"  rta config edit",
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(cmd, unknownCommand(cmd, args[0]))
			}
			v, verr := runConfigShow(reg)
			return render(cmd, v, verr)
		},
	}
	cmd.AddCommand(configShowCommand(reg, render), configPathCommand(render), configGetCommand(reg, render),
		configSetCommand(reg, render, opts), configUnsetCommand(reg, render, opts),
		configEditCommand(reg, render, opts), configCheckCommand(reg, render), configSchemaCommand(reg))
	return cmd
}

// configRender draws a config command's answer. Exempt from a broken default
// output format, since fixing one is what these commands are for, and drawn in
// pretty when it names nothing, as doctor does.
func configRender(opts *globalOpts) renderFn {
	return func(cmd *cobra.Command, v view.View, verr *view.Error) error {
		format, err := opts.format()
		if err != nil {
			format = cli.Pretty
		}
		renderOpts := renderOptions(cmd, format, opts.noColor)
		if verr != nil {
			_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
			return Rendered(verr)
		}
		return cli.Render(cmd.OutOrStdout(), v, renderOpts)
	}
}

func configShowCommand(reg *registry.Registry, render renderFn) *cobra.Command {
	return &cobra.Command{
		Use:               "show",
		Annotations:       outputExempt(),
		Short:             "Where the config file is, and every key it states",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, verr := runConfigShow(reg)
			return render(cmd, v, verr)
		},
	}
}

func configPathCommand(render renderFn) *cobra.Command {
	return &cobra.Command{
		Use:               "path",
		Annotations:       outputExempt(),
		Short:             "The config file's path, for a script or an editor",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return render(cmd, view.Text{Body: config.Path()}, nil)
		},
	}
}

func configGetCommand(reg *registry.Registry, render renderFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "get <key>",
		Annotations:       outputExempt(),
		Short:             "What a key is set to, as bare text for a script",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfigKeys(reg),
		Example: "  rta config get output\n" +
			"  rta config get plugins.http.timeout",
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runConfigGet(reg, args[0])
			return render(cmd, v, verr)
		},
	}
	documentArgs(cmd, configKeyDoc)
	return cmd
}

// configKeyDoc is the argument every config key command takes.
var configKeyDoc = argDoc{"key", "a key as `rta explain` prints it — `output`, `dashboard.columns`, " +
	"`theme.primary`, `plugins.http.timeout`"}

func configSetCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "set <key> <value>...",
		Annotations: outputExempt(),
		Short:       "Set a key, held to what it takes",
		Long: "Writes one key of the config file and leaves the rest of it, comments included, as it\n" +
			"was. The value is held to what the key takes: an output format for `output`, a\n" +
			"#rrggbb colour for a `theme.` slot, a whole number for `dashboard.columns`, and for a\n" +
			"plugin's key whatever that plugin declares, so a value no call would accept is\n" +
			"refused here instead of written and then refused on every call.\n\n" +
			"The keys are `output`, `dashboard.columns`, `dashboard.hidden`, `dashboard.order`,\n" +
			"`theme.<slot>` and `plugins.<plugin>.<key>`. `rta explain` prints a plugin's keys in\n" +
			"that form, and ends the card with the line that sets one. A key that takes a list\n" +
			"takes its elements as further arguments. Setting a key to what it already is\n" +
			"writes nothing, so it is safe in a script that runs on every boot.",
		Example: "  rta config set output json\n" +
			"  rta config set dashboard.columns 3\n" +
			"  rta config set plugins.net.timeout 20\n" +
			"  rta config set dashboard.hidden gen.overview git.overview",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeConfigSet(reg),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runConfigSet(reg, args[0], args[1:], opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	documentArgs(cmd, configKeyDoc, argDoc{"value", "what to set it to; a key that takes a list takes one " +
		"argument per element, and a value with spaces or a # is quoted for the shell"})
	return cmd
}

func configUnsetCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "unset <key>",
		Annotations: outputExempt(),
		Short:       "Take a key out of the file, so it is the default again",
		Long: "Removes a key from the config file, and the block around it when it was the last\n" +
			"key in it, and leaves the rest of the file as it was. A block can be named whole:\n" +
			"`theme`, or `plugins.http` for everything stated about a plugin. A key that is not\n" +
			"there is not an error, so this is safe in a script.",
		Example: "  rta config unset output\n" +
			"  rta config unset plugins.http",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeConfigKeys(reg),
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runConfigUnset(reg, args[0], opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	documentArgs(cmd, configKeyDoc)
	return cmd
}

// completeConfigKeys offers every key `config set` takes, with what each is
// for, and nothing after the first argument.
func completeConfigKeys(reg *registry.Registry) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []cobra.Completion
		for _, name := range configKeyNames(reg) {
			desc := ""
			if k, verr := resolveConfigKey(reg, name); verr == nil {
				desc = k.Help
			}
			out = append(out, cobra.CompletionWithDesc(name, desc))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeConfigSet is the key, and then the values the key takes when it has a
// closed set of them.
func completeConfigSet(reg *registry.Registry) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	keys := completeConfigKeys(reg)
	return func(cmd *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return keys(cmd, args, prefix)
		}
		key, verr := resolveConfigKey(reg, args[0])
		if verr != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return key.Options, cobra.ShellCompDirectiveNoFileComp
	}
}
