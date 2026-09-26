// Package app assembles the CLI: it loads built-in plugins into the registry
// and materializes every capability as a cobra command. This is the CLI
// renderer of the capability model — the TUI, MCP and web surfaces are
// siblings, not callers, of this package.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/tui"
	agentsession "github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// ExitCode maps an error returned by Execute to the fixed exit-code contract:
// 0 ok, 1 capability error, 2 usage error (or an error nothing coded), 3
// confirmation declined.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ve *view.Error
	if ok := asViewError(err, &ve); ok {
		switch ve.Code {
		case CodeConfirmRequired:
			return 3
		case CodeUsage, CodeOutputInvalid:
			return 2
		}
		return 1
	}
	return 2
}

// CodeUsage is the refusal of a command line rta could not use: an unknown
// command or flag, a missing or extra argument, a value the flag cannot take,
// a required flag left out, an --output nothing renders.
//
// Coded, where it used to be the one refusal left as a plain error for fang to
// style: under `-o json` a missing argument wrote a box of prose to stderr
// with no code in it, so a script parsing stderr as JSON got prose and one
// branching on the code had nothing to branch on. It still exits 2 — nothing
// ran, which is not the capability refusing — and it is rendered by the
// top-level handler in whatever format the command line asked for.
const CodeUsage = "core.usage"

// usageError codes a mistake on the command line as CodeUsage, with the
// command's own help as the hint. An error already coded passes through: a
// validator that knows better than "usage" has said so.
func usageError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	var ve *view.Error
	if errors.As(err, &ve) {
		return err
	}
	says := "says what it takes"
	if cmd.HasSubCommands() {
		says = "lists its commands"
	}
	return &view.Error{Code: CodeUsage, Message: err.Error(),
		Hint: "`" + cmd.CommandPath() + " --help` " + says}
}

// codeUsageErrors makes every argument check in the tree answer with
// usageError: cobra's own validators (NoArgs, MaximumNArgs…) return plain
// errors, and there is no hook for them the way there is for flags. After the
// tree is built, so a command added anywhere in it is covered without being
// remembered here.
func codeUsageErrors(cmd *cobra.Command) {
	if check := cmd.Args; check != nil {
		cmd.Args = func(c *cobra.Command, args []string) error { return usageError(c, check(c, args)) }
	}
	for _, sub := range cmd.Commands() {
		codeUsageErrors(sub)
	}
}

// checkCommandLine refuses, as CodeUsage, what cobra would otherwise refuse as
// a plain error after this point or what a command would find unusable only
// once it started: an --output nothing renders, a required flag left out, a
// flag group broken. cobra checks the flags itself, after this hook — a flag
// found missing here is simply found missing first, in the coded form.
//
// The --output only when it was typed. One that came from RTA_OUTPUT or the
// config file is not a mistake on this command line, and is refused beside
// this as what it is, by the commands it stops — see CodeOutputInvalid.
func checkCommandLine(cmd *cobra.Command, output string) error {
	if _, err := cli.ParseFormat(output); err != nil && cmd.Flags().Changed("output") {
		return &view.Error{Code: CodeUsage, Message: err.Error(),
			Hint: "--output takes " + formatNames()}
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return usageError(cmd, err)
	}
	return usageError(cmd, cmd.ValidateFlagGroups())
}

// CodeOutputInvalid is the refusal of an output format nobody typed: one from
// RTA_OUTPUT or the config file's output: key that names nothing rta renders.
//
// Not CodeUsage, because the command line is not what is wrong, and the plain
// error it replaces said only `unknown output format "jsno"` to somebody who
// had typed no format at all — nothing in it pointed at a variable exported in
// a shell rc or a key in a file. It exits 2 all the same: nothing ran.
//
// Nothing ran because it is refused before the command, beside
// checkCommandLine, and not where the command renders: the app's own commands
// render last, so `rta plugin install` or `rta profile set` had written what
// they were asked to by the time the format failed, and exited 2 over a write
// that landed. The refusal itself is drawn in pretty — the format asked for is
// the broken thing (topLevelRenderOptions).
const CodeOutputInvalid = "core.output.invalid"

// annotOutputExempt marks a command a broken default output format does not
// stop: one that writes no view in that format — plain text, a file's bytes,
// a server's protocol, an interactive form — and doctor, which is where
// somebody goes to find out what is broken and reports it as a row
// (doctorOutput). `mcp serve` among the first, which ran with a broken default
// before and has nothing to render it in.
//
// Marked on the exempt commands rather than on the ones that render, because
// forgetting the mark is then a refusal before anything runs, which the
// operator fixes by fixing the default, and not a write that lands and exits 2.
//
// A command that draws a view on one of its paths only is marked too, and asks
// format() itself at the top of that path, before it writes or builds
// anything: `plugin manifest` with --index, and `plugin dev` with no command
// after `--`. Stopping them here would refuse the manifest's bytes, which
// render nothing, and a command run under dev, which the root it runs in holds
// to this same check.
const annotOutputExempt = "rta.output.exempt"

// outputExempt is the Annotations a command carries to be marked so.
func outputExempt() map[string]string { return map[string]string{annotOutputExempt: "true"} }

// needsOutputFormat reports whether a broken default output format stops cmd.
// Beside the marked commands: the root, which opens the TUI or prints help; a
// group, which prints help or refuses its arguments as CodeUsage; and cobra's
// own help and completion commands, which rta does not build and so cannot
// mark.
func needsOutputFormat(cmd *cobra.Command) bool {
	if cmd.Annotations[annotOutputExempt] != "" || !cmd.HasParent() || cmd.HasSubCommands() {
		return false
	}
	for c := cmd; c.HasParent(); c = c.Parent() {
		if c.Parent() == c.Root() && (c.Name() == "help" || c.Name() == "completion") {
			return false
		}
	}
	return true
}

// format is the output format a command renders in, and the one place a
// default nothing renders is refused. A typed --output nothing renders never
// reaches here: checkCommandLine refused it as CodeUsage before the command
// ran.
func (o *globalOpts) format() (cli.Format, error) {
	f, err := cli.ParseFormat(o.output)
	if err != nil {
		return "", invalidOutputDefault(o.output)
	}
	return f, nil
}

// invalidOutputDefault names where the default came from, in the words
// somebody would search for it by: the variable, or the key and the file it is
// in. The precedence is config.Load's — the environment over the file.
func invalidOutputDefault(value string) *view.Error {
	if os.Getenv("RTA_OUTPUT") != "" {
		return &view.Error{Code: CodeOutputInvalid,
			Message: fmt.Sprintf("RTA_OUTPUT is %q, which is not an output format", value),
			Hint: "RTA_OUTPUT takes " + formatNames() +
				" — unset it for pretty, or pass -o for one command"}
	}
	return &view.Error{Code: CodeOutputInvalid,
		Message: fmt.Sprintf("output: %q in %s is not an output format", value, config.Path()),
		Hint: "output: takes " + formatNames() +
			" — remove the key for pretty, or pass -o for one command"}
}

// formatNames is cli.Formats without the descriptions, as a sentence, so a
// refusal lists the formats that are accepted rather than a copy of them.
func formatNames() string {
	all := cli.Formats()
	names := make([]string, len(all))
	for i, f := range all {
		names[i], _, _ = strings.Cut(f, "\t")
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// RenderTopLevelError writes a failure the way a success would have been
// written: in the format the caller asked for. It reports whether it handled
// the error, so the caller can fall back to fang's styling for an error
// nothing coded — a mistake on the command line is not one (see CodeUsage).
//
// The bug it fixes is narrow and bad. main printed an unrendered view.Error
// with fmt.Fprintf, ignoring --output entirely, so `rta plugin dev -o json`
// answered success with JSON and failure with prose — and a script parsing the
// first has nothing to do with the second. cli.RenderError's own doc records
// this being fixed once already, for -o yaml, inside the renderer; this is the
// same fault at the one call site that did not go through it.
//
// The options are read back off the root's parsed flags rather than kept in a
// package variable. That is not only less state: the flag carries the config
// file's default too, so what this reads is exactly what the command that
// failed would have rendered with.
func RenderTopLevelError(w io.Writer, root *cobra.Command, err error) bool {
	if err == nil {
		return true
	}
	// Already on the terminal — printing it again is how one problem reads as
	// two, in two slightly different layouts.
	//
	// errors.As rather than a type assertion, here and in asViewError: the
	// exit-code contract is documented as stable, and a direct assertion made
	// it hold only for as long as nobody wrapped a view.Error with %w on its
	// way up — a wrapped one exited 2 and was styled by fang as a usage
	// mistake. Nothing wraps one today; the contract should not depend on
	// that staying true.
	var rendered RenderedError
	if errors.As(err, &rendered) {
		return true
	}
	var ve *view.Error
	if !errors.As(err, &ve) {
		return false
	}
	return cli.RenderError(w, ve, topLevelRenderOptions(root)) == nil
}

// topLevelRenderOptions rebuilds what a command would have rendered with.
func topLevelRenderOptions(root *cobra.Command) cli.Options {
	flag := func(name string) string {
		if root == nil {
			return ""
		}
		if f := root.PersistentFlags().Lookup(name); f != nil {
			return f.Value.String()
		}
		return ""
	}
	output := flag("output")
	// A flag mistake stops pflag where it stands, so an --output after it was
	// never parsed: `rta net dns x --bogus -o json` refused the flag in the
	// default format, to a script that had asked for json. The command line
	// itself still says what was asked for.
	if root != nil {
		if f := root.PersistentFlags().Lookup("output"); f != nil && !f.Changed {
			if asked, ok := outputOnCommandLine(os.Args[1:]); ok {
				output = asked
			}
		}
	}
	// An unparseable --output has already failed the command that used it;
	// falling back to pretty here means the error still reaches the terminal
	// rather than disappearing into a second failure.
	format, ferr := cli.ParseFormat(output)
	if ferr != nil {
		format = cli.Pretty
	}
	return cli.Options{
		Format:  format,
		NoColor: flag("no-color") == "true" || !isTTY(),
		Width:   termWidth(),
	}
}

// outputOnCommandLine finds the --output a command line asks for without
// parsing the rest of it, in every spelling pflag accepts: -o json, -ojson,
// -o=json, --output json, --output=json. The last one wins, as it does for
// pflag, and nothing after a bare -- is a flag.
func outputOnCommandLine(args []string) (string, bool) {
	value, found := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		var v string
		switch {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				continue
			}
			i++
			v = args[i]
		case strings.HasPrefix(a, "--output="):
			v = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o") && !strings.HasPrefix(a, "--"):
			v = strings.TrimPrefix(strings.TrimPrefix(a, "-o"), "=")
		default:
			continue
		}
		value, found = v, true
	}
	return value, found
}

// CodeConfirmRequired is returned when a destructive capability runs on the
// CLI without --yes. There is no prompt on this surface, by design: a script
// says it means it with the flag and exits 3 otherwise, which is a question
// and not a failure (docs/20-using/10-cli.md). The TUI confirms on the
// capability's own dry run instead (internal/render/tui/confirm.go).
const CodeConfirmRequired = "core.confirm.required"

// RenderedError marks a *view.Error that has already been printed, so the
// top-level handler suppresses it instead of printing it twice.
//
// It replaces a rule that could not hold: the handler used to suppress *every*
// *view.Error on the assumption that runCapability was the only thing
// producing one. It was not — `rta plugin dev` returns view.Errors of its own,
// and every one of them vanished, so a failed build exited 1 with no output at
// all. Marking the rendered ones inverts the default: a new command that
// returns a view.Error gets it printed, which is the behaviour worth having by
// default because the failure mode is loud rather than silent.
type RenderedError struct{ Err *view.Error }

// Rendered marks err as already printed.
func Rendered(err *view.Error) error {
	if err == nil {
		return nil
	}
	return RenderedError{err}
}

func (e RenderedError) Error() string { return e.Err.Error() }
func (e RenderedError) Unwrap() error { return e.Err }

// asViewError unwraps to the view.Error inside err, through the Rendered
// marker and any %w wrapping on the way — so the exit-code contract does not
// depend on whether a command happened to print its own error first, or on
// what it wrapped it in.
func asViewError(err error, target **view.Error) bool {
	return errors.As(err, target)
}

// NewRegistry builds the registry of built-in plugins. The catalogue itself
// lives in builtin/all, where nothing downstream of it can be an import
// cycle away from asking what is in it.
func NewRegistry() (*registry.Registry, error) { return all.RegistryWith(PluginConfig, doctorReport) }

// LoadPlugins adds every SDK plugin found on $PATH to reg and returns the
// host that owns their processes, plus whatever went wrong.
//
// Problems are returned rather than raised. A third-party plugin that fails
// to launch, or whose namespace collides with a built-in, must not stop rta
// from starting: a tool where any installed plugin can brick the binary is a
// tool people stop installing plugins for. The caller prints them to stderr
// and carries on with what did load.
//
// The returned host must be closed when the process exits, or plugin
// subprocesses outlive the rta that started them.
func LoadPlugins(ctx context.Context, reg *registry.Registry, stderr io.Writer) (*pluginhost.Host, []error) {
	h := pluginhost.New(stderr)
	return h, h.LoadInto(ctx, reg)
}

type globalOpts struct {
	output  string
	noColor bool
	yes     bool
	dryRun  bool
}

// groupRunE is what a command that only groups other commands does with its
// arguments: nothing shows help, anything else is a usage error.
//
// Shared rather than repeated because the two hand-written groups did neither.
// Cobra's default for a parent with no Run is to print help and return nil, so
// `rta mcp serv` — one letter off `serve` — wrote help to stdout and exited 0.
// A human sees something that looks like an answer; a script sees success and
// an empty result, which is the version that survives into a pipeline. Every
// namespace built from the registry already got this right, which is exactly
// why it was invisible: fourteen commands behaved one way and two behaved
// another, and the two were the ones nobody types twice.
func groupRunE(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return usageError(cmd, unknownCommand(cmd, args[0]))
}

// unknownCommand is what rta says about a name that is not a command, at
// every depth of the tree.
//
// One function because there were two, and only one of them knew what its
// message would be rendered with. cobra's legacyArgs answers the root and
// this answers every group below it, and cobra's wording is a block: a blank
// line, "Did you mean this?", the candidates one per line, and a trailing
// newline. fang, which rendered usage errors before they were coded, added
// a full stop to err.Error(), so that block put the stop on a command name
// one level down —
//
//	Did you mean this?
//	    get
//	    set.
//
// — and on a line of its own at the root. The suggestion is the useful half
// of the message and it was the half the stray character landed on.
//
// So a near miss is one sentence, which is both what a renderer can put on
// one line and what a reader takes in at a glance. Unpunctuated, as every
// error message here is. It reaches the reader coded as CodeUsage.
func unknownCommand(cmd *cobra.Command, arg string) error {
	msg := fmt.Sprintf("unknown command %q for %q", arg, cmd.CommandPath())
	if cmd.DisableSuggestions {
		return errors.New(msg)
	}
	// cobra sets the distance on the root alone, inside Execute, and leaves
	// every subcommand at zero — where SuggestionsFor matches on prefix only
	// and `lst` earns no `list`. The root's own value, applied here.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	// The suggestion is what turns a typo into a one-keystroke fix instead of
	// a trip through --help: `rta sy cpu` suggested `sys` while `rta sys
	// cpuu` only said unknown, until every group came through here.
	near := cmd.SuggestionsFor(arg)
	if len(near) == 0 {
		return errors.New(msg)
	}
	quoted := make([]string, len(near))
	for i, n := range near {
		quoted[i] = strconv.Quote(n)
	}
	return fmt.Errorf("%s — the closest %s %s", msg,
		format.Plural(len(near), "match is", "matches are"), strings.Join(quoted, ", "))
}

// NewRoot builds the root cobra command over the given registry.
func NewRoot(reg *registry.Registry, version string) *cobra.Command {
	opts := &globalOpts{}
	// Recorded here rather than threaded: resolveProfile is reached from a
	// cobra RunE that has the capability and nothing else, and this is the
	// registry the whole tree is being built from.
	SetInstalled(withTrust{reg})
	// Same reasoning, one line along: the doctor capability and `grant allow`
	// both compare an open server's build against this one, and both are
	// reached with a request and nothing else.
	agentsession.SetSelf(version)
	// Config is optional; a broken file must not brick the CLI — doctor and
	// init both diagnose it, so they need the binary to still run.
	cfg, cfgErr := config.Load()
	defaultOutput := cfg.Output
	if defaultOutput == "" {
		defaultOutput = "pretty"
	}
	root := &cobra.Command{
		Use:   "rta",
		Short: "One capability model to rule them all",
		// The startup notice, here rather than in main, because this is the
		// first point at which the *format* is known. It used to print from
		// main on every run where stderr was a terminal, which put a sentence
		// of English above the JSON of every `rta … -o json` somebody ran at
		// a prompt — and the output a person copies off their screen is the
		// output they paste into a parser.
		//
		// Not on the completion command either. A banner appearing while
		// somebody is still typing is the noise the notice exists to avoid
		// being.
		//
		// The command line is checked here first, for the same reason the
		// notice is: this is the first point at which the format a refusal
		// should be written in is known. See checkCommandLine.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd {
				return nil
			}
			if err := checkCommandLine(cmd, opts.output); err != nil {
				return err
			}
			if needsOutputFormat(cmd) {
				if _, err := opts.format(); err != nil {
					return err
				}
			}
			if !stderrIsTerminal() {
				return nil
			}
			// Where am I, before the command rather than after it — and first,
			// because it frames whatever follows it. Silent unless the active
			// environment carries a `color:`; see WarnActiveProfile.
			WarnActiveProfile(cmd.ErrOrStderr(), cfg, opts.output != "pretty", opts.noColor)
			WarnUntrustedPlugins(cmd.ErrOrStderr(), opts.output != "pretty")
			return nil
		},
		Long:          "rta is a single extendable binary offering one consistent interface\nover the tools you juggle daily — scriptable CLI, TUI, and MCP for AI agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Rather than cobra's legacyArgs, which is the same refusal in a
		// shape the error renderer cannot punctuate — see unknownCommand.
		// Without this the root and every group below it answer an identical
		// mistake in two different wordings, which is how only one of them
		// got fixed the first time.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return unknownCommand(cmd, args[0])
		},
		// Bare `rta` on a TTY opens the interactive shell; in a pipe it
		// prints help so scripts never hang on an invisible TUI.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY() {
				return cmd.Help()
			}
			if cfgErr != nil {
				return cfgErr
			}
			// The untrusted artifacts go in too. The startup line naming them
			// is written to the primary buffer and the TUI opens on the
			// alternate one, so it is covered before it can be read and does
			// not come back until the session ends — which makes the pane the
			// only place a person in the TUI can learn a decision is pending.
			return tuiExit(tui.Run(cmd.Context(), reg, cfg.TrustedDashboard(), pluginConfig,
				tui.WithUntrusted(untrustedPluginsFound)))
		},
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&opts.output, "output", "o", defaultOutput, "output format: pretty|json|yaml|csv|md")
	pf.BoolVar(&opts.noColor, "no-color", false, "disable styled output")
	pf.BoolVarP(&opts.yes, "yes", "y", false, "skip confirmation prompts")
	pf.BoolVar(&opts.dryRun, "dry-run", false, "report what would happen without doing it")
	// A closed set of five, inherited by every command in the tree — the same
	// case Field.Options already gets for free on a plugin's own inputs, which
	// is what makes its absence here conspicuous rather than acceptable.
	completeFlag(root, "output",
		func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return cli.Formats(), cobra.ShellCompDirectiveNoFileComp
		})

	// Namespace commands host their capabilities as subcommands.
	nsCmds := map[string]*cobra.Command{}
	for _, p := range reg.Plugins() {
		nsCmd := &cobra.Command{
			Use:   p.Name,
			Short: p.Summary,
			RunE:  groupRunE,
		}
		nsCmds[p.Name] = nsCmd
		root.AddCommand(nsCmd)
	}
	for _, c := range reg.Capabilities() {
		attach(nsCmds[c.Words()[0]], c, opts)
	}
	root.AddCommand(newMCPCommand(reg, version, opts))
	root.AddCommand(newExplainCommand(reg, opts))
	root.AddCommand(newPluginCommand(reg, version, opts))
	root.AddCommand(newDoctorCommand(reg, opts))
	root.AddCommand(newInitCommand(reg))
	root.AddCommand(newUseCommand(opts))
	root.AddCommand(newPolicyCommand(opts))
	root.AddCommand(newProfileCommand(reg, opts))
	root.AddCommand(newDashboardCommand(reg, opts))
	root.AddCommand(newConfigCommand())
	groupRoot(root, reg)
	describeGroups(root)
	documentArguments(root)
	// Last, over the whole tree: see CodeUsage. A capability command sets a
	// flag-error function of its own (positionalFlagError), which codes its
	// answer itself; every other command inherits this one.
	root.SetFlagErrorFunc(usageError)
	codeUsageErrors(root)
	completeThroughOneRule(root)
	return root
}

// tuiExit is what the TUI ending means to the command that opened it.
//
// A signal — SIGTERM from a supervisor, SIGINT from outside the terminal — is
// how an interactive program is asked to stop, and stopping is not a failure:
// the same call `mcp serve` makes about a client hanging up. bubbletea
// reports it as "program was killed: context canceled", which reached the
// screen the TUI had just handed back as a box nothing coded. Anything else
// it returns — a terminal it could not take, a panic it recovered — is coded,
// so it is rendered like every other failure rather than styled by fang.
func tuiExit(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return view.AsError(err, "core.tui")
}

// The root help's three headings. `rta --help` used to be one alphabetical
// list of thirty-odd entries, `agent` beside `audit` beside `cert`, with
// nothing saying that a third of them are the product's second half — the
// commands that decide what an agent may reach — and another third are
// setup. The docs tell the story in three parts; the help now does too.
const (
	groupCapabilities = "capabilities"
	groupAgents       = "agents"
	groupSetup        = "setup"
)

// agentCommands are the root commands about agents and consent: the built-in
// namespaces whose every verb answers to the person at the terminal, the
// server that exposes everything else to an agent, and the ceiling over the
// grants. Named here rather than derived, because this is rta's own
// vocabulary — a plugin installed tomorrow is a capability by definition.
var agentCommands = map[string]bool{
	"mcp": true, "grant": true, "agent": true, "lock": true, "operator": true, "policy": true,
}

// groupRoot files every root command under one of the three headings:
// registry namespaces are capabilities unless they are about agents, and
// everything rta adds itself is setup. cobra's own help and completion
// commands are setup too; they are attached at Execute, so they are named by
// their group id here rather than by command.
func groupRoot(root *cobra.Command, reg *registry.Registry) {
	root.AddGroup(
		&cobra.Group{ID: groupCapabilities, Title: "capabilities"},
		&cobra.Group{ID: groupAgents, Title: "agents and consent"},
		&cobra.Group{ID: groupSetup, Title: "setup"},
	)
	namespaces := map[string]bool{}
	for _, p := range reg.Plugins() {
		namespaces[p.Name] = true
	}
	for _, c := range root.Commands() {
		switch name := c.Name(); {
		case agentCommands[name]:
			c.GroupID = groupAgents
		case namespaces[name]:
			c.GroupID = groupCapabilities
		default:
			c.GroupID = groupSetup
		}
	}
	root.SetHelpCommandGroupID(groupSetup)
	root.SetCompletionCommandGroupID(groupSetup)
}

// describeGroups gives every noun command the summary findOrCreate could
// not: a noun exists before its verbs do, so it is named after them here,
// once the tree is complete. `rta grant guard --help` used to open with
// "guard operations", which says the word twice and the verbs never.
func describeGroups(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		describeGroups(sub)
		if sub.Short != sub.Name()+" operations" || !sub.HasSubCommands() {
			continue
		}
		verbs := make([]string, 0, len(sub.Commands()))
		for _, v := range sub.Commands() {
			if !v.Hidden {
				verbs = append(verbs, v.Name())
			}
		}
		sub.Short = sub.Name() + ": " + strings.Join(verbs, ", ")
	}
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
		Long: withArguments(
			strings.TrimSpace(c.Summary+"\n\n"+c.Description),
			capabilityArgs(c, positionals),
		),
		Args: positionalArgsValidator(positionals),
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
	// Per capability rather than persistent on the root, for the same reason
	// --detail is: a flag that exists everywhere and does something nowhere
	// teaches people it is decoration. `rta sys cpu --profile x` should say
	// "unknown flag", which names the fact, instead of accepting a value that
	// no input could ever receive.
	if plugin.Profilable(c) && cmd.Flags().Lookup("profile") == nil {
		cmd.Flags().String("profile", "", "run against one of the connections in your config "+
			"(name, or name/instance when an environment holds several)")
		completeFlag(cmd, "profile",
			func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
				cfg, err := config.Load()
				if err != nil {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				// The refs a call would accept, not just the names: an
				// environment holding several connections to this plugin
				// completes as staging/analytics beside staging, because a
				// bare name over several labeled entries is exactly what
				// Lookup refuses. The description carries the connection's
				// own address where the note would repeat per instance.
				ns := plugin.Namespace(c.ID)
				var out []cobra.Completion
				for _, name := range cfg.ProfilesFor(ns) {
					p := cfg.Profiles[name]
					for _, ref := range profile.InstanceRefs(p, name, ns) {
						desc := p.Note
						if instance := config.RefInstance(ref); instance != "" {
							if _, conn, ok := p.ForInstance(ns, instance); ok {
								desc = connAddress(conn, p.Note)
							}
						}
						out = append(out, cobra.CompletionWithDesc(ref, desc))
					}
				}
				return out, cobra.ShellCompDirectiveNoFileComp
			})
	}
	cmd.SetFlagErrorFunc(positionalFlagError(c, positionals))
	declareCompletion(cmd, c, positionals)
	parent.AddCommand(cmd)
}

// connAddress names where one instance points, for a completion description —
// the one line that tells two instances of a plugin apart, which the
// profile's note cannot do since every instance shares it.
func connAddress(conn config.Connection, fallback string) string {
	if conn.Kube != "" {
		return conn.Kube
	}
	if conn.SSH != "" {
		return conn.SSH
	}
	// The most address-like set: keys, in the order a person would look.
	for _, k := range []string{"host", "endpoint", "url", "bucket", "database", "addr"} {
		if v, ok := conn.Set[k]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return fallback
}

// positionalFlagError improves the one flag mistake a declaration makes
// likely.
//
// An input declared Positional becomes an argument rather than a flag, so
// `rta hello greet --name you` gets pflag's "unknown flag: --name". That is
// accurate and no help at all to somebody looking straight at `input:name` in
// `rta explain` — the input exists, it is spelled the way they typed it, and
// nothing says why the flag does not. The declaration already knows which
// inputs are positional, so the error can say which one and show the form
// that works.
//
// The wording leads with a word rather than the input name on purpose: fang
// sentence-cases a plain error before printing it, so a message starting with
// an identifier renders it capitalised — "name" became "Name", which is a
// different field as far as the reader is concerned.
//
// Coded as CodeUsage like every other flag mistake: this is the capability
// command's flag-error function, so the root's, which codes the rest, never
// sees what reaches it.
func positionalFlagError(c plugin.Capability, positionals []plugin.Field) func(*cobra.Command, error) error {
	return func(cmd *cobra.Command, err error) error {
		name, ok := unknownFlagName(err)
		if !ok {
			return usageError(cmd, err)
		}
		for _, f := range positionals {
			if f.Name == name {
				return usageError(cmd, fmt.Errorf("this capability takes %q as an argument, not a flag — %s", f.Name, cliForm(c)))
			}
		}
		return usageError(cmd, err)
	}
}

// unknownFlagName pulls the flag out of pflag's unknown-flag error. Matching
// on the message is unpleasant and is the only option pflag offers: it
// formats this one with fmt.Errorf and exports no typed error to check for.
func unknownFlagName(err error) (string, bool) {
	const prefix = "unknown flag: --"
	if err == nil {
		return "", false
	}
	_, after, found := strings.Cut(err.Error(), prefix)
	if !found || after == "" {
		return "", false
	}
	return after, true
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
		//
		// And so is a field this operator has already answered. Without that
		// clause the gate excluded every plain String with no Suggest, which
		// is exactly the set internal/recent exists for — a bucket, a
		// database, a schema — so the values were recorded on every run and
		// offered on none of them.
		return len(f.Options) > 0 || f.Suggest != nil || f.Type == plugin.Path ||
			len(remembered().For(c.ID, f.Name)) > 0
	}

	if len(positionals) > 0 {
		cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			f, ok := positionalAt(positionals, len(args))
			if !ok || !completable(f) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return candidates(cmd, c, f, args)
		}
	} else {
		// A capability that takes no argument offers none, rather than
		// falling through to cobra's default of listing the working
		// directory. `rta sys cpu <tab>` printing your files is the shell
		// telling somebody this command takes a filename, which is a
		// confident answer to a question the declaration already answers the
		// other way.
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}
	for _, f := range c.Inputs {
		if f.Positional || !completable(f) {
			continue
		}
		f := f
		completeFlag(cmd, f.Name,
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
	if last := positionals[len(positionals)-1]; last.Type.Repeatable() {
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
	// Resolved, not raw. Field.Suggest is documented to receive "what the
	// caller has supplied so far, which is what lets a suggestion depend on
	// an earlier answer", and that was true here only by accident: cobra
	// baked every declared default into its flag set and collectValues read
	// them all back. Gating on Changed() made that accident stop working, so
	// a suggestion that reads a sibling would have started seeing nothing —
	// for every plugin, not only the ones that adopt config.
	//
	// SurfaceCompletion, not SurfaceCLI: this is a keystroke with nobody
	// waiting to answer anything, and a Suggest that would have prompted must
	// be able to tell the difference. And without any credential the resolve
	// pulled in from the environment fallback — see plugin.CompletionRequest.
	//
	// Through the environment as well, so a suggestion is computed against the
	// connection the command would actually reach. Bind, never Fill: this runs
	// on every keystroke and must not open a store — see bindProfile.
	name, bound := bindProfile(cmd, c)
	req := plugin.CompletionRequest(c, plugin.Resolve(c, plugin.Inputs{
		Caller: values, Profile: bound, ProfileName: name, Config: PluginConfig(c),
	}))

	// A path keeps file completion alongside whatever was declared: zsh offers
	// the suggestions first and falls back to the filesystem when none of them
	// match what is being typed, which is exactly the order you want —
	// `--identity <tab>` your keys, `--identity ~/proj/<tab>` your files.
	directive := cobra.ShellCompDirectiveNoFileComp
	if f.Type == plugin.Path {
		directive = cobra.ShellCompDirectiveDefault
	}
	// Held to shown's rule by the producer this is called from, like every
	// other completion (see completion).
	out := offering(f, c, f.Candidates(ctx, req))
	if len(out) == 0 {
		return nil, directive
	}
	return out, directive
}

// shown holds what a shell is about to print to the rule every other
// completion surface keeps. A Suggest answers with whatever exists — a note
// title an agent wrote, a key in somebody's bucket — and the shell prints it
// as it came: zsh lists the description after the tab verbatim, so an OSC 0
// and a right-to-left override in a title set over MCP reached the operator's
// terminal on `rta note show <tab>`.
//
// A value that would display as something other than itself is dropped, not
// cleaned, for the reason the TUI's candidateValues gives: it is inserted as
// offered, and a cleaned one is a different value. The description is only
// read, so it is cleaned like any text on its way to a terminal, and its
// whitespace folded to single spaces: a tab would start a column the shell
// does not have, and a newline a completion that does not exist.
func shown(entries []cobra.Completion) []cobra.Completion {
	out := entries[:0:0]
	for _, entry := range entries {
		value, desc, described := strings.Cut(entry, "\t")
		if textclean.Deceives(value) {
			continue
		}
		if described {
			desc = strings.Join(strings.Fields(textclean.Terminal(desc)), " ")
			entry = cobra.CompletionWithDesc(value, desc)
		}
		out = append(out, entry)
	}
	return out
}

// completion holds a completion producer to shown's rule, so the shell hears
// one rule from every producer rather than from a capability's fields alone.
//
// The app's own producers printed what they read as it came: a profile's
// note on `--profile <tab>` and `rta use <tab>`, a file name on `rta plugin
// trust <tab>`, an index name. A note is the operator's words, but the config
// file is also written by scripts and tools, and a terminal acts on an escape
// in it whoever wrote it. Wrapping the producer rather than cleaning inside
// each one is what keeps the next producer from being the one that forgot.
func completion(fn cobra.CompletionFunc) cobra.CompletionFunc {
	if fn == nil {
		return nil
	}
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		out, directive := fn(cmd, args, toComplete)
		return shown(out), directive
	}
}

// completeFlag registers a flag's completion through completion. cobra keeps
// flag completions in a table nothing can rewrite afterwards, so this is the
// one way the app registers one (TestEveryFlagCompletionIsRegisteredThroughOneRule).
func completeFlag(cmd *cobra.Command, name string, fn cobra.CompletionFunc) {
	_ = cmd.RegisterFlagCompletionFunc(name, completion(fn))
}

// completeThroughOneRule wraps every command's argument completion in
// completion, after the tree is built, so a command added anywhere in it is
// covered without being remembered here — the way codeUsageErrors covers
// argument checks.
func completeThroughOneRule(cmd *cobra.Command) {
	cmd.ValidArgsFunction = completion(cmd.ValidArgsFunction)
	for _, sub := range cmd.Commands() {
		completeThroughOneRule(sub)
	}
}

// remembered is this process's view of the shortlists. Read once: a shell
// completion is one process answering one question, and a file read per field
// would be work done to learn the same thing twice.
var remembered = sync.OnceValue(recent.Load)

// offering puts what the operator has actually used behind whatever the field
// declared for itself.
//
// Behind, not in front: a declared list is authoritative — those are the tags
// that exist — while a shortlist is a convenience, and burying the real answer
// under a habit would be the wrong way round. For the inputs this matters most
// for (a bucket, a database, a vault path) there is no declared list at all
// and the shortlist is the whole of it.
func offering(f plugin.Field, c plugin.Capability, declared []cobra.Completion) []cobra.Completion {
	used := remembered().For(c.ID, f.Name)
	if len(used) == 0 {
		return declared
	}
	seen := make(map[string]bool, len(declared))
	for _, d := range declared {
		seen[plugin.CandidateValue(d)] = true
	}
	out := declared
	for _, value := range used {
		if seen[value] {
			continue
		}
		out = append(out, cobra.CompletionWithDesc(value, "you used this"))
	}
	return out
}

func findOrCreate(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	// Nested nouns are groups too — `rta net hosts` holds net.hosts.list and
	// its siblings — so they get the same rule as every other group. Missed
	// on the first pass, which left `rta net hosts bogus` printing help and
	// exiting 0 one level below where that had just been fixed.
	cmd := &cobra.Command{Use: name, Short: name + " operations", RunE: groupRunE}
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
		if f.Type.Repeatable() {
			variadic = true // a slice positional swallows all remaining args
		}
	}
	// cobra's own sentence was "requires at least 1 arg(s), only received 0"
	// — the one refusal in rta that named neither the thing missing nor the
	// command to type. Every field here is positional, in order, so the
	// first required one at or past what was given is the one missing.
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < required {
			for i, f := range fields {
				if i >= len(args) && f.Required {
					return fmt.Errorf("missing <%s> — usage: %s", f.Name, cmd.UseLine())
				}
			}
		}
		// Every one of them named, not just the first. The sentence was
		// "one argument too many, %q" — a count spelled as a word, so three
		// extra arguments were reported as one, and the two the reader still
		// had to hunt for went unmentioned. What is useful here is which
		// words to delete, and the word/number agreement comes from the one
		// counting vocabulary rather than from this line's own idea of it.
		if !variadic && len(args) > len(fields) {
			extra := args[len(fields):]
			quoted := make([]string, len(extra))
			for i, a := range extra {
				quoted[i] = strconv.Quote(a)
			}
			return fmt.Errorf("unexpected %s %s — usage: %s",
				format.PluralOf(len(extra), "argument"), strings.Join(quoted, ", "), cmd.UseLine())
		}
		return nil
	}
}

func declareFlags(cmd *cobra.Command, c plugin.Capability) {
	for _, f := range c.Inputs {
		if f.Positional {
			continue
		}
		usage := flagUsage(c, f)
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
		case plugin.SecretSlice:
			// StringArray, never StringSlice, and this is the same ruling
			// profileset.go's `--set` already made: StringSlice splits its
			// argument on commas, so `--data 'password=a,b'` would silently
			// become two values. For a credential that is not a cosmetic
			// difference — it writes half a password under one key and a
			// fragment under another, with nothing said. StringSlice keeps
			// its splitting because callers of the existing type rely on it.
			def, _ := f.Default.([]string)
			cmd.Flags().StringArray(f.Name, def, usage)
		default:
			def, _ := f.Default.(string)
			cmd.Flags().String(f.Name, def, usage)
		}
		// A required input that config can fill is not required on the
		// command line: cobra enforces MarkFlagRequired during parsing, which
		// is before anything has looked at the operator's file, so marking it
		// would make `plugins.pg@abc.host` unusable for the one input it
		// exists to supply. rta checks the resolved value instead, in run(),
		// where config has already been applied — so the message can say
		// which of the two ways to supply it were tried.
		if f.Required && f.Config == "" {
			_ = cmd.MarkFlagRequired(f.Name)
		}
	}
}

// requireResolved reports a required input that nothing supplied — neither
// the caller nor the operator's configuration.
//
// Only for inputs that declare a config key: every other required input is
// still cobra's to enforce, at parse time, where the error arrives with the
// usage text beside it.
func requireResolved(c plugin.Capability, values map[string]any) *view.Error {
	for _, f := range c.Inputs {
		if !f.Required || f.Config == "" {
			continue
		}
		if v, ok := values[f.Name]; ok && v != "" && v != nil {
			continue
		}
		return view.Errorf("core.input.missing", "%s needs --%s", c.ID, f.Name).
			WithHint(fmt.Sprintf("pass --%s, or set %s in your rta config", f.Name, f.Config))
	}
	return nil
}

// flagUsage renders a field's help for `--help`, appending what the host adds
// rather than what each declaration remembers to write down.
//
// The closed set, so nobody keeps a copy of it in prose and it cannot drift
// from the completion or the MCP schema.
//
// And the environment variable a credential may arrive in, for the same
// reason and with a sharper one behind it: three built-in declarations wrote
// "(or set RTA_KV_PASSPHRASE)" into Help by hand, one of them by calling
// LocalEnvVar itself, and `rta explain` prints the same variable from the
// declaration two clauses earlier — so the page describing an input named it
// twice. Every credential field in the plugin catalogue writes a bare noun
// phrase and got no mention at all. Generated here, the CLI is the one
// surface that says it, it says it for every EnvFallback input including a
// plugin's, and it cannot disagree with what Resolve actually reads.
//
// The shell-history clause goes here too and only here. It is true of argv
// and of nothing else: a TUI form has no history and an MCP caller has no
// argv, and it was being read out under a masked box.
func flagUsage(c plugin.Capability, f plugin.Field) string {
	usage := f.Help
	if len(f.Options) > 0 {
		set := "one of: " + strings.Join(f.Options, "|")
		if usage == "" {
			usage = set
		} else {
			usage += " (" + set + ")"
		}
	}
	if f.Local && f.EnvFallback {
		env := "or set $" + plugin.LocalEnvVar(c.ID, f.Name) + ", which keeps it out of shell history"
		if usage == "" {
			return env
		}
		usage += " (" + env + ")"
	}
	return usage
}

func runCapability(ctx context.Context, cmd *cobra.Command, c plugin.Capability, args []string, opts *globalOpts) error {
	format, err := opts.format()
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

	// Safety gate: on this surface a destructive capability runs only with
	// --yes, and exits 3 otherwise — a question, not a failure, and a script
	// answers it with the flag. The TUI confirms on the dry run instead
	// (internal/render/tui/confirm.go).
	if c.Safety == plugin.Destructive && !opts.yes && !opts.dryRun {
		verr := &view.Error{
			Code:    CodeConfirmRequired,
			Message: fmt.Sprintf("%s is destructive and needs confirmation", c.ID),
			Hint:    "re-run with --yes to confirm, or --dry-run to preview",
		}
		// Rendered here, like every other capability error: main suppresses
		// view.Errors on the assumption the runner has already shown them, so
		// returning this one unrendered made `rta note rm 1` exit 3 in
		// silence — the right code, and not one word about how to proceed.
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}

	values, err := collectValues(cmd, c, args)
	if err != nil {
		return usageError(cmd, err)
	}
	// The profile, and the values it contributes. A person at a terminal
	// needs no grant for any of this: the gate is on the MCP surface, because
	// the operator writing the profile and the operator running the command
	// are the same person, and consent to yourself is not a thing.
	profileName, filled, closeTunnel, verr := resolveProfile(ctx, cmd, c, values)
	// Deferred before the error check on purpose: a `kube:` connection whose
	// forward came up and whose next step then failed still has a port open,
	// and closeTunnel is never nil.
	defer closeTunnel()
	if verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}
	req := plugin.ResolveRequest(c, plugin.Inputs{
		Caller:      values,
		Profile:     filled,
		ProfileName: profileName,
		Config:      PluginConfig(c),
		// The heading Config sits under, for a refusal to name the line.
		ConfigSection: PluginConfigSection(c),
	}, opts.dryRun, opts.yes).WithSurface(plugin.SurfaceCLI)
	// The required check for config-backed inputs, which cobra no longer
	// makes because making it would have run before config was consulted.
	// Named here rather than left as a handler's zero value: an input that is
	// required and empty is the one case where "you can also put this in your
	// config" is the sentence somebody needs.
	if verr := requireResolved(c, req.Values()); verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}
	v, runErr := c.Run(ctx, req)
	if runErr != nil {
		ve := view.AsError(runErr, c.ID+".failed")
		// A failure is the one moment an unhonoured config section explains
		// something, so it is said here and nowhere else — see
		// ConfigNotApplied. On the Hint rather than beside it, because Hint is
		// the field every output format already carries, and a note printed
		// only in pretty output is a note an agent never sees.
		if note := ConfigNotApplied(c); note != "" {
			ve = ve.WithHint(strings.TrimSpace(ve.Hint + " · " + note))
		}
		_ = cli.RenderError(cmd.ErrOrStderr(), ve, renderOpts)
		return Rendered(ve)
	}
	// Remembered after the run succeeded, and from `values` rather than from
	// `req`: a declared default is not a choice anybody made, and a host
	// the environment filled in is already offered by the environment. What
	// this keeps is what the person typed and it worked.
	if !opts.dryRun {
		recent.Record(plugin.SurfaceCLI, c, values)
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
			if f.Type.Repeatable() {
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
		// Only what the caller actually typed. cobra bakes every declared
		// default into its flag set, so reading them all back unconditionally
		// meant a flag nobody passed still arrived as a supplied value —
		// which config could never beat, and which made "the caller supplied
		// none" impossible to express on this surface. Changed() rather than
		// comparing against the zero value, because `--shout=false` is a
		// caller supplying false and is indistinguishable from the default by
		// value alone.
		if !cmd.Flags().Changed(f.Name) {
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
		case plugin.SecretSlice:
			// Declared as a StringArray above, so it must be read back as
			// one: GetStringSlice on an array flag returns an error rather
			// than the values.
			v, err := cmd.Flags().GetStringArray(f.Name)
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
	// The detail flag is a rendering depth, not a declared input, and no
	// config key can reach it — so it is read unconditionally and its absence
	// means false, which is what every renderer already assumes.
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
//
// COLUMNS, when it names a width a terminal could have (see columns), was
// asked for by name and wins over both, as it does for Python's shutil and
// the CLIs built on it. Without it `rta doctor | less` had no way to be
// shaped at all: natural width left rows several hundred columns wide in a
// pager that was eighty. Neither bash nor zsh exports it by default, so a
// pipe stays unshaped unless somebody wrote `COLUMNS=100` in front of the
// command, which is the point.
func termWidth() int {
	if w := columns(); w > 0 {
		return w
	}
	if !isTTY() {
		return 0
	}
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// columns is the width COLUMNS asks for, or 0 when it asks for none.
//
// Bounded at maxColumns, the widest a terminal can report: TIOCGWINSZ
// carries the column count in an unsigned 16-bit field, so while the width
// came only from the terminal it could not be larger. COLUMNS has no such
// limit, and the renderer spends width on padding — every section heading is
// a rule drawn out to the margin — so COLUMNS=1000000 turned a 174-byte
// codec.jwt answer into nine megabytes, and a value near 1e10 would ask for
// tens of gigabytes. A value past what any terminal could hold is no request
// at all, the same as one that is not a number.
func columns() int {
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 && w <= maxColumns {
		return w
	}
	return 0
}

const maxColumns = math.MaxUint16
