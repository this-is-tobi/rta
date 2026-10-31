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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/boxlist"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/match"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/passkey"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/tui"
	agentsession "github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/internal/toolcall"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// ExitCode maps an error returned by Execute to the fixed exit-code contract:
// 0 ok, 1 capability error, 2 usage error (or an error nothing coded), 3
// confirmation declined. 130 and 143 are a signal's, and never an error's:
// they are what rta exits with when it stops without the command
// (WatchSignals), which then has returned nothing to map.
//
// A usage error is one nothing ran for and a retry cannot mend, which is what a
// script branches on: 2 says "fix the arguments", 1 says "the operation was
// refused or failed". The host's check of a call's inputs is the second kind of
// usage error. A flag parser refuses `--limit abc` as CodeUsage before the call
// exists, and the check refuses `--limit 0` or `--sort bogus` as core.input.*
// once it does — in the same sentence, for the same mistake, since the
// declaration's bounds and options are only known there. Exiting 1 for those
// let a script's retry loop treat a limit of 0 as a flaky capability, and made
// the exit code depend on how a value was wrong instead of on whether anything
// ran.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ve *view.Error
	if ok := asViewError(err, &ve); ok {
		switch {
		case ve.Code == CodeConfirmRequired:
			return 3
		case ve.Code == CodeUsage, ve.Code == CodeOutputInvalid, strings.HasPrefix(ve.Code, codeInputFamily):
			return 2
		}
		return 1
	}
	return 2
}

// codeInputFamily is what the codes of the host's check of a call's inputs
// begin with: core.input.type, .option, .range and .missing, refused before any
// handler runs. Every surface refuses with them, so one that arrives from the
// operator's config or a profile — worded as that layer's, by
// plugin.CheckInputs — exits 2 as well, as an --output nobody typed does
// (CodeOutputInvalid): nothing ran, and it is the arguments, or the values
// that fill them, that are to be fixed.
const codeInputFamily = "core.input."

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
	hint := "`" + cmd.CommandPath() + " --help` says what it takes"
	switch {
	case cmd.HasSubCommands() && cmd.HasParent():
		hint = verbsHint(cmd)
	case cmd.HasSubCommands():
		hint = "`" + cmd.CommandPath() + " --help` lists its commands"
	case strings.HasPrefix(err.Error(), "unexpected argument"):
		if word := flagWordHint(cmd); word != "" {
			hint = word
		} else if value := switchValueHint(cmd); value != "" {
			hint = value
		} else if flags := valueFlagsHint(cmd); flags != "" {
			hint = flags
		}
	}
	return &view.Error{Code: CodeUsage, Message: err.Error(), Hint: hint}
}

// usageErrorOn is usageError for a mistake about one of a capability's inputs,
// naming it (view.Error.Input), so -o json tells a script which flag or
// argument to change as the checks made once the command line has parsed do.
// input "" names none.
func usageErrorOn(cmd *cobra.Command, err error, input string) error {
	coded := usageError(cmd, err)
	var ve *view.Error
	if input != "" && errors.As(coded, &ve) && ve.Input == "" {
		return ve.WithInput(input)
	}
	return coded
}

// codeUsageErrors makes every argument check in the tree answer with
// usageError: cobra's own validators (NoArgs, MaximumNArgs…) return plain
// errors, and there is no hook for them the way there is for flags. After the
// tree is built, so a command added anywhere in it is covered without being
// remembered here.
func codeUsageErrors(cmd *cobra.Command) {
	if check := cmd.Args; check != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			err := check(c, args)
			if err != nil && isCobraArity(c, err) {
				if named := namedArity(c, args); named != nil {
					err = named
				}
			}
			return usageError(c, err)
		}
	}
	for _, sub := range cmd.Commands() {
		codeUsageErrors(sub)
	}
}

// isCobraArity says whether an argument check failed with cobra's own count
// sentence — "accepts 1 arg(s), received 0" — as opposed to a validator that
// already knows what it is refusing.
//
// NoArgs is one of them, and says it differently: on a command that takes
// nothing, cobra's refusal of `rta doctor x` is `unknown command "x" for "rta
// doctor"`, which calls an argument a command. Only on a command with no
// subcommands, where nothing could have been mistaken for one — a group's
// unknown command is exactly that (unknownCommand).
func isCobraArity(cmd *cobra.Command, err error) bool {
	msg := err.Error()
	if strings.HasPrefix(msg, "unknown command ") {
		return !cmd.HasSubCommands()
	}
	return strings.HasPrefix(msg, "accepts ") || strings.HasPrefix(msg, "requires at least ")
}

// useArgument matches the argument placeholders in a command's Use line:
// <required> and [optional], the way every command here spells them. A bracket
// that opens on a dash is a flag group (`[-- command args...]`), not an
// argument.
var useArgument = regexp.MustCompile(`<[^<>]+>|\[[^\[\]-][^\[\]]*\]`)

// namedArity restates a count refusal in the words the capability commands
// use — "missing <client> — usage: …", "unexpected argument "x" — usage: …" —
// read from the command's own Use line. cobra's validators (ExactArgs,
// MaximumNArgs, RangeArgs) know how many arguments a command takes and not
// what they are called, so `rta plugin install` said "accepts 1 arg(s),
// received 0" while `rta kv rm` said which argument it wanted: the
// capability commands got the better sentence when
// positionalArgsValidator was written, and the commands written by hand
// never did.
//
// nil when the Use line does not account for the refusal — more arguments
// refused than it has places for, or fewer than every placeholder it marks
// required — so a command whose Use line is shorthand keeps cobra's sentence
// rather than getting a wrong one.
func namedArity(cmd *cobra.Command, args []string) error {
	places := useArgument.FindAllString(cmd.Use, -1)
	for i := len(args); i < len(places); i++ {
		if strings.HasPrefix(places[i], "<") {
			return fmt.Errorf("missing %s — usage: %s", places[i], cmd.UseLine())
		}
	}
	if len(args) > len(places) && !strings.Contains(cmd.Use, "...") {
		extra := args[len(places):]
		quoted := make([]string, len(extra))
		for i, a := range extra {
			quoted[i] = strconv.Quote(a)
		}
		return fmt.Errorf("unexpected %s %s — usage: %s",
			format.PluralOf(len(extra), "argument"), strings.Join(quoted, ", "), cmd.UseLine())
	}
	return nil
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
		return usageErrorOn(cmd, err, missingFlagInput(cmd))
	}
	return usageError(cmd, cmd.ValidateFlagGroups())
}

// missingFlagInput is the input a capability's command was refused for leaving
// out, when it left out one: the one flag cobra holds required and was not
// given (requiredOnCommandLine). "" for two or more, which no one flag answers,
// and on a command that is not a capability's, whose flags are no inputs.
func missingFlagInput(cmd *cobra.Command) string {
	if _, capability := cmd.Annotations[annotSafety]; !capability {
		return ""
	}
	var missing []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if required := f.Annotations[cobra.BashCompOneRequiredFlag]; len(required) > 0 && required[0] == "true" && !f.Changed {
			missing = append(missing, f.Name)
		}
	})
	if len(missing) != 1 {
		return ""
	}
	return missing[0]
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
// a server's protocol — and the two that render but are how a broken default
// is dealt with, drawing their answer in pretty when it names nothing: doctor,
// which is where somebody goes to find out what is broken and reports it as a
// row (doctorOutput), and init, which rewrites the key. `mcp serve` among the
// first, which ran with a broken default before and has nothing to render it
// in.
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

// RenderTopLevelError writes a failure the way a success would have been
// written: in the format the caller asked for. It reports whether it handled
// the error, so the caller can draw an error nothing coded under a
// code of its own (see Execute) — a mistake on the command line is not one (see CodeUsage).
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
	// way up — a wrapped one exited 2 and was drawn as a failure nothing
	// coded. Nothing wraps one today; the contract should not depend on
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
	return renderOptionsFor(root, os.Args[1:])
}

// renderOptionsFor is topLevelRenderOptions for a root that ran args, which
// for the process's own root are its command line and for the one plugin dev
// runs a command in are what followed `--` (see renderNested).
func renderOptionsFor(root *cobra.Command, args []string) cli.Options {
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
			if asked, ok := outputOnCommandLine(args); ok {
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
	return renderOptions(root, format, flag("no-color") == "true")
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

// noColorOnCommandLine is outputOnCommandLine for --no-color, in the
// spellings pflag accepts for a boolean: --no-color, and --no-color= with
// anything strconv.ParseBool reads. The last one wins, a value it cannot read
// changes nothing, and nothing after a bare -- is a flag.
func noColorOnCommandLine(args []string) bool {
	on := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--no-color" {
			on = true
			continue
		}
		if v, ok := strings.CutPrefix(a, "--no-color="); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				on = b
			}
		}
	}
	return on
}

// CodeConfirmRequired is returned when a destructive capability runs on the
// CLI without --yes and nobody was asked, or the person asked said no. A
// person at a terminal is asked, after the target is found and shown
// (confirmprompt.go); a script is not, by design: it says it means it with the
// flag and exits 3 otherwise, which is a question and not a failure
// (docs/20-using/10-cli.md). The TUI confirms on the capability's own dry run
// instead (internal/render/tui/confirm.go).
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
func LoadPlugins(ctx context.Context, reg *registry.Registry) (*pluginhost.Host, []error) {
	h := pluginhost.New()
	return h, h.LoadInto(ctx, reg)
}

// ReportLoadProblems writes what LoadPlugins could not load: a coded refusal
// drawn as the CLI draws any other, hint and all, and each cause once however
// many plugins it stopped.
//
// **The hint.** The launch codes the refusals whose fix is the operator's —
// a TMPDIR too long for a plugin's socket, or one that cannot hold the
// socket's directory (pluginhost's socketDir) — and a call that finds the
// declaration cached passes one on as itself (liveError). The load printed
// the same refusal as "rta:" and its message, so whether the operator was
// told what to change depended on whether a declaration happened to be
// cached.
//
// **Once.** Most of what stops a plugin loading is the plugin's own — a
// binary that will not start, a namespace already taken — in words no other
// plugin's problem shares. What is the machine's — that TMPDIR, a macOS
// without sandbox-exec, a deny set that cannot be built — stops every plugin
// in the same words, and it was said once per plugin, so with a dozen
// installed a command opened with a dozen copies of one sentence. A cause is
// said once, naming every plugin it stopped.
//
// In pretty whatever -o asks for, as the lines it replaced were: this runs
// before the command line is parsed, and on standard error, where a script
// reads the refusal its own command ends with and not these. A --no-color in
// args, the command line main was given, is honoured all the same, read off
// it unparsed (noColorOnCommandLine): the lines this replaced were plain
// text, and a coloured badge heading a run whose command line says
// --no-color is the flag not doing what it says.
func ReportLoadProblems(w io.Writer, problems []error, args []string) {
	opts := renderOptions(nil, cli.Pretty, !stderrIsTerminal() || noColorOnCommandLine(args))
	for _, p := range groupLoadProblems(problems) {
		var verr *view.Error
		if errors.As(p.cause, &verr) {
			said := *verr
			said.Message = p.about(verr.Message)
			if cli.RenderError(w, &said, opts) == nil {
				continue
			}
		}
		// Through textclean, not as it came. A problem can quote what the
		// plugin wrote — go-plugin puts the first line that is not a
		// handshake in the error it returns — so a trusted plugin that wrote
		// an OSC had it acted on by the operator's terminal, before every
		// command. Its stderr reaches no terminal but as its last words at
		// the end of a problem like this one (pluginhost's lastWords), and
		// its answers and a coded refusal go through the renderer; this is
		// the same rule for what is said about it failing to load.
		fmt.Fprintln(w, "rta:", textclean.Terminal(p.about(p.cause.Error())))
	}
}

// ReportConfigError says, at startup, that the configuration did not load, in
// the same form and for the same reasons ReportLoadProblems says a plugin did
// not: drawn by the renderer, in pretty whatever -o asks for, honouring a
// --no-color in args.
//
// It was a line of its own words, the error's message as it came, which lost
// two things. The hint — `rta config edit` opens the file and says what is
// wrong — is not in the message, so the line said a file was broken and not
// how to find where. And the YAML parser's message quotes the lines around the
// fault as they are in the file, so an escape sequence on one of them was acted
// on by the terminal before every command. The file is the operator's, but a
// drop-in from a shared dotfiles repository, or ./.rta.yaml read for want of a
// config directory, is somebody else's bytes; the renderer cleans what it
// draws.
func ReportConfigError(w io.Writer, err error, args []string) {
	if err == nil {
		return
	}
	opts := renderOptions(nil, cli.Pretty, !stderrIsTerminal() || noColorOnCommandLine(args))
	if cli.RenderError(w, view.AsError(err, "config.invalid"), opts) != nil {
		fmt.Fprintln(w, "rta:", textclean.Terminal(err.Error()))
	}
}

// loadProblem is one cause LoadPlugins reported, and whom it stopped, each
// named as its problem named it: "plugin kv".
type loadProblem struct {
	subjects []string
	cause    error
}

// about is text led by whom the problem stopped: "plugin kv: …" for one, and
// "plugins kv, pg: …" for several.
func (p loadProblem) about(text string) string {
	switch len(p.subjects) {
	case 0:
		return text
	case 1:
		return p.subjects[0] + ": " + text
	}
	names := make([]string, len(p.subjects))
	for i, s := range p.subjects {
		name, ok := strings.CutPrefix(s, "plugin ")
		if !ok {
			return strings.Join(p.subjects, ", ") + ": " + text
		}
		names[i] = name
	}
	return "plugins " + strings.Join(names, ", ") + ": " + text
}

// groupLoadProblems splits each problem into whom it stopped and why, by the
// wrapping LoadInto gives it — "plugin kv" before the error it wraps — and
// gathers those whose causes are the same, in the order the first of each
// was found. The same means the same words, and for a coded refusal the same
// code and hint too: a cause can only be said once if what is said is true
// of every plugin it names. A problem that wraps nothing, or whose text does
// not end in what it wraps, is a cause of its own and is said as it is.
func groupLoadProblems(problems []error) []loadProblem {
	var out []loadProblem
	at := map[string]int{}
	for _, p := range problems {
		cause := errors.Unwrap(p)
		subject, wrapped := "", false
		if cause != nil {
			subject, wrapped = strings.CutSuffix(p.Error(), ": "+cause.Error())
		}
		if !wrapped || subject == "" {
			out = append(out, loadProblem{cause: p})
			continue
		}
		key := cause.Error()
		var verr *view.Error
		if errors.As(cause, &verr) {
			key = verr.Code + "\n" + key + "\n" + verr.Hint
		}
		if i, ok := at[key]; ok {
			out[i].subjects = append(out[i].subjects, subject)
			continue
		}
		at[key] = len(out)
		out = append(out, loadProblem{subjects: []string{subject}, cause: cause})
	}
	return out
}

type globalOpts struct {
	output  string
	noColor bool
	yes     bool
	dryRun  bool
	// external says whether a capability comes from a plugin outside this
	// binary, which the confirmation question does not run a preview of.
	external func(plugin.Capability) bool
	// nothingToDo is set by a sweep whose dry run came up empty, so the
	// question that precedes the real one is not asked about nothing
	// (confirmByPreview).
	nothingToDo bool
	// config is the configuration NewRoot built the tree from (WithReading).
	config func() (config.Config, error)
}

// loadedConfig is the configuration the command tree was built from, which a
// step of a command asks rather than reading the files again; a command
// attached outside NewRoot, which has none, reads them.
func (o *globalOpts) loadedConfig() (config.Config, error) {
	if o == nil || o.config == nil {
		return config.Load()
	}
	return o.config()
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
	return usageError(cmd, unknownCommand(cmd, args[0], args[1:]...))
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
func unknownCommand(cmd *cobra.Command, arg string, rest ...string) error {
	return unknownWord(cmd, arg, rest, false)
}

// unknownWord is unknownCommand for a word that is a command line, or — with
// topic — one that asks for help on a subject: `rta help security` is not a
// service to install, and it is not a typo; the commands that name it are the
// answer, and past them the documentation.
func unknownWord(cmd *cobra.Command, arg string, rest []string, topic bool) error {
	msg := fmt.Sprintf("unknown command %q for %q", arg, cmd.CommandPath())
	// An empty word is an unset variable in a script (`rta kv "$ACTION"`), and
	// every command begins with nothing: listing them all as the closest
	// matches answers a question nobody asked.
	if cmd.DisableSuggestions || strings.TrimSpace(arg) == "" {
		return errors.New(msg)
	}
	// `rta version` is the first thing a person types at a tool they have just
	// installed, since every tool they used before this one has the command, and
	// rta's answer is the flag. Before any match, as a help topic too: a plugin
	// that lists "version" among its keywords — an overview shows the server's —
	// is a whole-word match, which used to win, so somebody asking which rta
	// they run was sent to `rta etcd overview` and `rta mariadb status`.
	if arg == "version" && !cmd.HasParent() {
		return &view.Error{Code: CodeUsage, Message: msg, Hint: "`rta --version` prints the version"}
	}
	// cobra sets the distance on the root alone, inside Execute, and leaves
	// every subcommand at zero — where SuggestionsFor matches on prefix only
	// and `lst` earns no `list`. The root's own value, applied here.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	// What the word is called somewhere else in the tree comes first: `rta
	// revoke` is `rta grant revoke`, and `rta ps` is `rta sys ps`, not the
	// edit-distance neighbour `fs`. See suggestCommands.
	guess := suggestCommands(cmd, arg, !topic)
	if guess.whole {
		hint := unknownHint(guess, arg, rest, topic)
		// A word that names a group rather than a verb may as well be a
		// service: `audit kube` is a command and `kube` is also a plugin, and
		// the one thing a group's name does not rule out is the other reading.
		// A verb (`revoke`, `ps`) is never one, and is not told it might be;
		// nor is a synonym the operator's vocabulary gives a command (`theme`).
		if !topic && guess.namesAGroup() && !guess.byKeyword(arg) {
			if more := notACommandHint(cmd, arg, nil); more != "" {
				hint += " · " + more
			}
		}
		return &view.Error{Code: CodeUsage, Message: msg, Hint: hint}
	}
	// The suggestion is what turns a typo into a one-keystroke fix instead of
	// a trip through --help: `rta sy cpu` suggested `sys` while `rta sys
	// cpuu` only said unknown, until every group came through here.
	near := plausibleSuggestions(arg, cmd.SuggestionsFor(arg))
	// A word that is a plugin's name is not a typo for a command that shares
	// its length: `rta pg` is not "pkg", and `rta docker` is not "doctor".
	if _, isPlugin := pluginWordHint(commandsOf(cmd), arg); isPlugin && !cmd.HasParent() {
		near = nil
	}
	if len(near) > 0 {
		quoted := make([]string, len(near))
		for i, n := range near {
			quoted[i] = strconv.Quote(n)
		}
		msg = fmt.Sprintf("%s — the closest %s %s", msg,
			format.Plural(len(near), "match is", "matches are"), strings.Join(quoted, ", "))
	} else if len(guess.commands) > 0 {
		return &view.Error{Code: CodeUsage, Message: msg, Hint: unknownHint(guess, arg, rest, topic)}
	}
	if topic {
		return &view.Error{Code: CodeUsage, Message: msg, Hint: unknownHint(suggestion{}, arg, nil, true)}
	}
	if hint := notACommandHint(cmd, arg, near); hint != "" {
		return &view.Error{Code: CodeUsage, Message: msg, Hint: hint}
	}
	return errors.New(msg)
}

// unknownHint is what a refused word is told: the commands it probably meant,
// and for a subject asked of `help`, where to read about it.
func unknownHint(guess suggestion, word string, rest []string, topic bool) string {
	hint := suggestionHint(guess, word, rest)
	if !topic {
		return hint
	}
	if hint == "" {
		hint = "`rta explain` finds a capability by word"
	}
	return hint + " — documentation: " + docsURL
}

// helpRunE is `rta help <command…>`: the help of the command named, and a usage
// error for a name that is none. cobra's own prints the root's help and exits 0
// for a topic it does not know — `rta help nope` was an answer in a script — so
// this takes the same road a mistyped command does everywhere else in the tree,
// suggestions and pointers included.
func helpRunE(cmd *cobra.Command, args []string) error {
	target, rest, _ := cmd.Root().Find(args)
	if len(rest) > 0 {
		return usageError(target, unknownWord(target, rest[0], nil, true))
	}
	return target.Help()
}

// plausibleSuggestions drops the near matches that are not near.
//
// cobra offers anything within two edits, and two edits are the whole of a
// two-letter name and half of a four-letter one: `rta pg query` was told the
// closest matches were "fs", "kv" and "pkg", and `rta kube` that it meant
// "use" — words that share nothing with the one typed except by chance. A typo
// is a small fraction of the word, so a suggestion is kept when it is one edit
// away, or two for a word of six letters or more; a prefix is kept whatever its
// length, which is how `rta sy` finds `sys`. A swap of two neighbouring
// letters is one edit, since that is the typo (`lsit`). The other side of the
// word is never kept (opposite): `uninstall` is two letters from `install` and
// the reverse of it, so a closest match that offers one for the other is a
// wrong answer given with a straight face.
func plausibleSuggestions(arg string, near []string) []string {
	arg = strings.ToLower(arg)
	allowed := max(1, len([]rune(arg))/3)
	var kept []string
	for _, n := range near {
		lower := strings.ToLower(n)
		if opposite(arg, lower) {
			continue
		}
		if strings.HasPrefix(lower, arg) || match.Distance(arg, lower) <= allowed {
			kept = append(kept, n)
		}
	}
	return kept
}

// notACommandHint says where a word typed at the root may live when it is no
// command and resembles none: the name of a service — `rta pg`, `rta vault` —
// which is a plugin and is installed, not built in. (The other thing a person
// at a first prompt types that rta does not have, `rta version`, is answered
// before any of this, by unknownWord.) It reads only what is already on the
// machine: the first-party names rta ships, and an attached index's manifests,
// which are local files. A first-party name is answered with the install, which
// attaches the first-party index itself; any other word with nothing attached
// is not guessed to be a service.
//
// A plugin found on the machine and not yet approved comes first, since it is
// the one answer that is about this machine rather than a guess: the word is
// the plugin the person just built, and the step they skipped is `rta plugin
// trust`.
//
// A word that is plainly a half-typed command (`rta sy`, near is [sys]) keeps
// to the suggestion; so does a longer one with a neighbour. A short word with
// only a distant neighbour does not, since the service names are the short
// ones — pg, s3 — and a neighbour two letters from a two-letter word is chance.
//
// "" for a word that is neither, and for any group below the root, where the
// word can only be a verb that is not there.
func notACommandHint(cmd *cobra.Command, arg string, near []string) string {
	if cmd.HasParent() {
		return ""
	}
	if !plugin.ValidName(arg) {
		return ""
	}
	// Before the neighbours and before the index: a plugin that is on this
	// machine and waiting for approval is the one thing a person who just
	// built it and typed its name needs told. The startup notice that names
	// it is printed by the root command's pre-run, which an unknown word
	// never reaches, so without this the answer to `rta weather greet` right
	// after the build is a pointer at an index the plugin was never in. That
	// one, a first-party plugin that is not here and one here that did not
	// start are said as what they are, the answers about this machine that
	// are not a guess. The install attaches the first-party index itself when
	// it has to.
	if hint, ok := pluginWordHint(commandsOf(cmd), arg); ok {
		return hint
	}
	if len(near) > 0 && (len([]rune(arg)) > 3 || slices.ContainsFunc(near, func(n string) bool {
		return strings.HasPrefix(strings.ToLower(n), strings.ToLower(arg))
	})) {
		return ""
	}
	if listed, verr := plugindist.Resolve(arg); verr == nil {
		return arg + " is a plugin in the " + listed.Index + " index — `rta plugin install " + arg + "` installs it"
	}
	return ""
}

// RootOption adjusts how NewRoot builds the command tree.
type RootOption func(*rootSettings)

// readConfig is config.Read, the read NewRoot makes when it is handed none: a
// var so that a test can count the reads one command makes, which is the
// number WithReading exists to hold at one.
var readConfig = config.Read

type rootSettings struct {
	read func() (config.Reading, error)
}

// WithReading hands NewRoot the configuration the caller has already read, as
// config.Read returned it, so the tree is built from the same read the rest of
// startup used instead of from a second one, and so is everything the command
// then asks of the files: the notice about keys rta does not read, whether a
// read fans out, the environment a call resolves.
//
// Measured, not tidiness: the file was parsed once in main for the plugin
// sections and the palette and parsed again here for the default output
// format, and goccy/go-yaml parses a 350-line config with twenty-five
// profiles in about two milliseconds — a tenth of every command's startup,
// spent a second time on an identical file. With an environment switched on,
// a read at a terminal then parsed every file three times more, once for each
// of those questions. A single read is also the stronger statement of what
// main's comment already promises: nothing discovers its own configuration
// halfway through a run.
func WithReading(r config.Reading, err error) RootOption {
	return func(s *rootSettings) { s.read = func() (config.Reading, error) { return r, err } }
}

// NewRoot builds the root cobra command over the given registry.
func NewRoot(reg *registry.Registry, version string, options ...RootOption) *cobra.Command {
	settings := rootSettings{read: readConfig}
	for _, option := range options {
		option(&settings)
	}
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
	reading, cfgErr := settings.read()
	cfg := reading.Config()
	opts := &globalOpts{config: func() (config.Config, error) { return cfg, cfgErr }}
	defaultOutput := cfg.Output
	if defaultOutput == "" {
		defaultOutput = "pretty"
	}
	root := &cobra.Command{
		Use:   "rta",
		Short: "RTA: one capability model, rendered as a CLI, a TUI and an MCP server",
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
			// The colours of the environment this call is about, which only
			// its own flags can say — main painted the one that is switched
			// on, for help and for everything that never gets here. Before the
			// terminal check below: stdout can be a terminal when stderr is
			// not, and the result is drawn in whatever this sets.
			ApplyTheme(cfg, environmentOf(cmd))
			if !stderrIsTerminal() {
				return nil
			}
			// Where am I, before the command rather than after it — and first,
			// because it frames whatever follows it. Before every command for
			// an environment with a `color:`, and before the ones it can
			// change for any other; see WarnActiveProfile.
			badgeOut, plain := badgeStream(cmd.ErrOrStderr(), os.Environ())
			WarnActiveProfile(badgeOut, cmd, cfg, BadgeStyle{
				MachineReadable: opts.output != "pretty",
				NoColor:         opts.noColor || plain,
				ASCII:           cli.ASCIIOnly(os.Getenv),
			})
			WarnUntrustedPlugins(cmd.ErrOrStderr(), opts.output != "pretty")
			warnConfigProblems(cmd.ErrOrStderr(), cmd, opts.output != "pretty", reading.Check)
			return nil
		},
		Long: "rta is a single extendable binary offering one consistent interface over the tools you juggle daily — " +
			"scriptable CLI, TUI, and MCP for AI agents. What an agent may reach is yours to decide: it starts with reads only, " +
			"and every grant beyond that is narrowed, expires on its own and is written to a record.",
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
			return unknownCommand(cmd, args[0], args[1:]...)
		},
		// Bare `rta` on a TTY opens the interactive shell; in a pipe it
		// prints help so scripts never hang on an invisible TUI.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY() {
				return cmd.Help()
			}
			// A terminal that cannot move the cursor cannot show a screen
			// that redraws itself in place: Emacs's shell buffer and a few
			// serial consoles say so with TERM=dumb, and the TUI there is a
			// scroll of escape sequences with no way out spelled in any of
			// it. Help is what a pipe gets and what this can read.
			if os.Getenv("TERM") == "dumb" {
				fmt.Fprintln(cmd.ErrOrStderr(), "rta: TERM=dumb cannot draw the TUI, so here is the help instead")
				return cmd.Help()
			}
			if cfgErr != nil {
				// Not the file's own error again: main has just written it to
				// stderr, parse excerpt and all, and repeating that for ten
				// lines said one thing twice. What is new here is the
				// consequence, and where the fix is.
				verr := view.AsError(cfgErr, "config.invalid")
				return view.Errorf(verr.Code, "the dashboard does not open over a config file it cannot read; "+
					"the message above names the fault").WithHint(verr.Hint)
			}
			// bubbletea stops on the cancelled context itself and hands the
			// terminal back on the way out; a deadline cutting that short
			// would leave the shell in raw mode on the alternate screen.
			ownShutdown()
			// The untrusted artifacts go in too, and the plugins that were
			// approved and did not start. The startup line naming them is
			// written to the primary buffer and the TUI opens on the alternate
			// one, so it is covered before it can be read and does not come
			// back until the session ends — which makes the pane the only
			// place a person in the TUI can learn a decision is pending, or
			// that a plugin they installed is not being provided.
			// The screen of the switched-on profile, when it selects one.
			drawn := drawnView(cfg)
			session := []tui.Option{
				tui.WithView(drawn), tui.WithUntrusted(untrustedPluginsFound), tui.WithFailed(failedPluginsFound),
			}
			if opts.noColor {
				session = append(session, tui.WithNoColor())
			}
			return tuiExit(tui.Run(cmd.Context(), reg, cfg.TrustedDashboardFor(drawn), pluginConfig, session...))
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
	opts.external = func(c plugin.Capability) bool {
		o, ok := reg.Origin(plugin.Namespace(c.ID))
		return ok && o.External()
	}
	for _, c := range reg.Capabilities() {
		attach(nsCmds[c.Words()[0]], c, opts)
	}
	root.AddCommand(newMCPCommand(reg, version, opts))
	root.AddCommand(newExplainCommand(reg, opts))
	root.AddCommand(newPluginCommand(reg, version, opts))
	root.AddCommand(newDoctorCommand(reg, opts))
	root.AddCommand(newInitCommand(opts))
	root.AddCommand(newUseCommand(opts))
	root.AddCommand(newPolicyCommand(opts))
	root.AddCommand(newProfileCommand(reg, opts))
	root.AddCommand(newDashboardCommand(reg, opts))
	root.AddCommand(newConfigCommand(reg, opts))
	groupRoot(root, reg)
	root.SetHelpFunc(helpFunc(opts))
	describeGroups(root)
	documentArguments(root)
	attachExamples(root)
	// Last, over the whole tree: see CodeUsage. A capability command sets a
	// flag-error function of its own (positionalFlagError), which codes its
	// answer itself; every other command inherits this one.
	root.SetFlagErrorFunc(flagError)
	// cobra's own help and completion commands, which it would otherwise add
	// inside Execute, after this point: a command the walk below never saw
	// refused `rta completion zsh extra` as a plain, uncoded error under
	// any -o. Added here they are in the tree the walk codes. completion is
	// a group like any other, so it refuses a shell it does not know rather
	// than printing its help and exiting 0 (see groupRunE).
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, sub := range root.Commands() {
		if sub.Name() == "completion" && sub.Run == nil && sub.RunE == nil {
			sub.RunE = groupRunE
		}
		if sub.Name() == "help" {
			sub.Run, sub.RunE = nil, helpRunE
		}
	}
	shapeCompletion(root)
	shapeVerbs(root, reg.Capabilities)
	// After cobra's own commands are added, whose long text is hand-wrapped too.
	reflowHelp(root)
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
// so it is rendered like every other failure rather than as one nothing coded.
func tuiExit(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return view.AsError(err, "core.tui")
}

// The root help's four headings. `rta --help` used to be one alphabetical
// list of thirty-odd entries, `agent` beside `audit` beside `cert`, with
// nothing saying that a third of them are the product's second half — the
// commands that decide what an agent may reach — and another third are
// setup. The docs tell the story in three parts; the help does too, and keeps
// what comes later under a fourth.
const (
	groupCapabilities = "capabilities"
	groupAgents       = "agents"
	groupSetup        = "setup"
	groupAdvanced     = "advanced"
)

// agentCommands are the root commands about agents and consent: the built-in
// namespaces whose every verb answers to the person at the terminal, and the
// server that exposes everything else to an agent. Named here rather than
// derived, because this is rta's own vocabulary — a plugin installed tomorrow
// is a capability by definition.
var agentCommands = map[string]bool{"mcp": true, "grant": true, "agent": true}

// advancedCommands are the ones a person reaches for later: the incident
// control, the team ceiling over every grant, and the identity that manages
// remote servers. Beside the three above they were half of one heading, and
// the heading is what a newcomer reads to learn what consent is.
var advancedCommands = map[string]bool{"lock": true, "policy": true, "operator": true}

// groupRoot files every root command under one of the four headings:
// registry namespaces are capabilities unless they are about agents, and
// everything rta adds itself is setup unless it is advanced. cobra's own help and completion
// commands are setup too; they are attached at the end of NewRoot, after
// this, so they are named by their group id here rather than by command.
func groupRoot(root *cobra.Command, reg *registry.Registry) {
	root.AddGroup(
		&cobra.Group{ID: groupCapabilities, Title: "capabilities"},
		&cobra.Group{ID: groupAgents, Title: "agents and consent"},
		&cobra.Group{ID: groupSetup, Title: "setup"},
		&cobra.Group{ID: groupAdvanced, Title: "advanced"},
	)
	namespaces := map[string]bool{}
	for _, p := range reg.Plugins() {
		namespaces[p.Name] = true
	}
	for _, c := range root.Commands() {
		switch name := c.Name(); {
		case agentCommands[name]:
			c.GroupID = groupAgents
		case advancedCommands[name]:
			c.GroupID = groupAdvanced
		case namespaces[name]:
			c.GroupID = groupCapabilities
		default:
			c.GroupID = groupSetup
		}
	}
	root.SetHelpCommandGroupID(groupSetup)
	root.SetCompletionCommandGroupID(groupSetup)
}

// groupSummaries are the sentences for the nouns that are worth one, keyed by
// the command's path. A noun without an entry is named after its verbs, which
// is the right answer for a group a plugin brings and none of this knows.
//
// `rta grant --help` listed `guard: off, on, remote, status` beside real
// sentences: for the switch that puts a passphrase in front of issuing grants,
// the line that says what it is for was its own name and its verbs.
var groupSummaries = map[string]string{
	"rta audit kube":   "Grade a cluster: end-of-life software, network policies, pod security, quotas and RBAC",
	"rta grant guard":  "A passphrase in front of issuing grants: turn it on or off, see its state, or trust remote operators to sign",
	"rta net hosts":    "Read and change the hosts file: add, remove or switch off an entry",
	"rta net resolver": "Show the system resolver and set the nameservers",
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
		if summary, ok := groupSummaries[sub.CommandPath()]; ok {
			sub.Short = summary
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

	positionals := c.Arguments()
	use := leaf
	for _, f := range positionals {
		use += " " + argumentSlot(f)
	}

	validateArgs := positionalArgsValidator(positionals)
	cmd := &cobra.Command{
		Use:   use,
		Short: c.Summary,
		Long: withArguments(
			cliProse(c, strings.TrimSpace(c.Summary+"\n\n"+c.Description)),
			capabilityArgs(c, positionals),
		),
		Example: capabilityExamples(c),
		// The swallowed flag first: it is what makes the word after it
		// unexpected, and with nothing after it there is no other check to
		// say it (refuseSwallowedFlag).
		Args: func(cmd *cobra.Command, args []string) error {
			if err := refuseSwallowedFlag(cmd, c); err != nil {
				return err
			}
			return validateArgs(cmd, args)
		},
		Annotations: capabilityAnnotations(c),
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
	addAdHocFlags(cmd, c)
	if plugin.Profilable(c) && cmd.Flags().Lookup("profile") == nil {
		cmd.Flags().String("profile", "", "run against one of the connections in your config "+
			"(name, or name/instance when an environment holds several)")
		if !hasConnection(c) {
			_ = cmd.Flags().SetAnnotation("profile", annotUnlisted, []string{"true"})
		}
		// The way one call leaves the environment `rta use` has switched on: the
		// per-call twin of `rta use --off`, and of the TUI picker's base entry.
		// Added exactly where --profile is, and listed where it is. Looked up
		// first, as every flag here is: pflag panics on a redefined one.
		if cmd.Flags().Lookup("no-profile") == nil {
			cmd.Flags().Bool("no-profile", false, "ignore the environment that is switched on, for this call: "+
				"it runs on the flags you type and the base configuration")
			if !hasConnection(c) {
				_ = cmd.Flags().SetAnnotation("no-profile", annotUnlisted, []string{"true"})
			}
		}
		completeFlag(cmd, "profile",
			func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
				cfg, err := config.Load()
				if err != nil {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				return profileFlagCompletions(cfg, c, profile.Active(), toComplete)
			})
	}
	cmd.SetFlagErrorFunc(positionalFlagError(c, positionals))
	declareCompletion(cmd, c, positionals)
	parent.AddCommand(cmd)
}

// connAddress names where one instance points, for a completion description —
// the one line that tells two instances of a plugin apart, which the
// profile's note cannot do since every instance shares it.
//
// The keys are first the plugin's own: the config key of each input that the
// capabilities of ns among caps declare as where a connection goes, a URL
// before an address before a host, since each says more than the next. A
// fixed list of likely names was all there was, and it did not hold
// `address`, which redis, etcd, qdrant and s3 declare, so two instances of
// any of them were both described by the note. The list stays after the
// declared keys, for a plugin that declares no endpoint and for a caller
// with no registry to ask.
func connAddress(ns string, caps []plugin.Capability, conn config.Connection, fallback string) string {
	if conn.Kube != "" {
		return conn.Kube
	}
	if conn.SSH != "" {
		return conn.SSH
	}
	var keys []string
	for _, role := range []plugin.EndpointRole{plugin.EndpointURL, plugin.EndpointAddress, plugin.EndpointHost} {
		for _, c := range caps {
			if plugin.Namespace(c.ID) != ns {
				continue
			}
			for _, f := range c.Inputs {
				if f.Endpoint == role && f.Config != "" && !slices.Contains(keys, f.Config) {
					keys = append(keys, f.Config)
				}
			}
		}
	}
	// The most address-like set: keys, in the order a person would look.
	keys = append(keys, "host", "endpoint", "url", "bucket", "database", "addr")
	for _, k := range keys {
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
// The wording leads with a word rather than the input name on purpose: a message
// that starts with an identifier reads as a sentence about a different field the
// moment anything capitalises it — "name" became "Name" once — and a word that
// is already a word cannot be misread.
//
// Coded as CodeUsage like every other flag mistake: this is the capability
// command's flag-error function, so the root's, which codes the rest, never
// sees what reaches it.
func positionalFlagError(c plugin.Capability, positionals []plugin.Field) func(*cobra.Command, error) error {
	return func(cmd *cobra.Command, err error) error {
		name, ok := unknownFlagName(err)
		if !ok {
			return flagValueError(cmd, err, &c)
		}
		for _, f := range positionals {
			if f.Name == name {
				return usageError(cmd, fmt.Errorf("this capability takes %q as an argument, not a flag — %s", f.Name, cliForm(c)))
			}
		}
		return flagValueError(cmd, err, &c)
	}
}

// refusedFlagValue is pflag's sentence for a value its flag cannot hold:
// `invalid argument "abc" for "--count" flag: strconv.ParseInt: parsing "abc":
// invalid syntax`, with the shorthand in front of the name when there is one.
var refusedFlagValue = regexp.MustCompile(`^invalid argument ("(?:[^"\\]|\\.)*") for "(?:-\w, )?--([^"]+)" flag: `)

// flagError is the flag-error function of every command that is not a
// capability: the value refusal below, and everything else as the usage
// mistake it is.
func flagError(cmd *cobra.Command, err error) error { return flagValueError(cmd, err, nil) }

// flagValueError is flagError for a command that may know its inputs' bounds.
func flagValueError(cmd *cobra.Command, err error, c *plugin.Capability) error {
	if refused := refuseFlagValue(cmd, err, c); refused != nil {
		return refused
	}
	if refused := refuseDashedValue(cmd, err); refused != nil {
		return refused
	}
	if refused := refuseMissingOption(err, c); refused != nil {
		return refused
	}
	if typed, unknown := unknownFlagName(err); unknown {
		if refused := unknownBeforeFlag(cmd); refused != nil {
			return refused
		}
		// A flag another command has taken over (toolcall.MovedTo), which is the
		// answer to "unknown flag" for whoever still types it.
		if c != nil {
			if to, moved := toolcall.MovedTo(c.ID, typed); moved {
				return &view.Error{Code: CodeUsage, Message: err.Error(),
					Hint: "--" + typed + " moved to " + plugin.SurfaceCLI.CapabilityName(to)}
			}
		}
		// Before the nearest spelling, which for `--json` is whichever flag of this
		// command is a letter or two away, and for a flag that says TLS is the
		// name of one that does nothing without it (tlsFlagHint).
		if hint := outputFlagHint(typed); hint != "" {
			return &view.Error{Code: CodeUsage, Message: err.Error(),
				Hint: hint + " — `" + cmd.CommandPath() + " --help` lists what it takes"}
		}
		if c != nil {
			if hint := tlsFlagHint(*c, typed); hint != "" {
				return &view.Error{Code: CodeUsage, Message: err.Error(),
					Hint: hint + " — `" + cmd.CommandPath() + " --help` lists what it takes"}
			}
		}
		if hint := oppositeFlagHint(cmd, typed); hint != "" {
			return &view.Error{Code: CodeUsage, Message: err.Error(),
				Hint: hint + " — `" + cmd.CommandPath() + " --help` lists what it takes"}
		}
		if near := nearestFlags(cmd, typed); len(near) > 0 {
			return &view.Error{Code: CodeUsage, Message: err.Error(),
				Hint: "did you mean " + strings.Join(near, " or ") + "? `" + cmd.CommandPath() + " --help` lists what it takes"}
		}
		if named := helpWordFlag(cmd, typed); named != "" {
			// The help up to its first aside: "hash algorithm (one of: …)" in
			// parentheses of its own read as a sentence that lost its way.
			usage, _, _ := strings.Cut(cmd.Flags().Lookup(strings.TrimPrefix(named, "--")).Usage, " (")
			return &view.Error{Code: CodeUsage, Message: err.Error(),
				Hint: "did you mean " + named + " (" + strings.ToLower(usage[:1]) + usage[1:] + ")? `" + cmd.CommandPath() + " --help` lists what it takes"}
		}
	}
	return usageError(cmd, err)
}

// missingFlagValue is pflag's refusal of a flag given with nothing after it.
var missingFlagValue = regexp.MustCompile(`^flag needs an argument: --([a-z0-9-]+)$`)

// refuseMissingOption says which values a flag takes when it was given none and
// takes one of a declared few: `rta mysql status --tls` was told "flag needs an
// argument" under a hint to read --help, for an input whose answer is five
// words long and already declared. nil for any other flag, which keeps pflag's
// sentence and the usage hint.
func refuseMissingOption(err error, c *plugin.Capability) *view.Error {
	m := missingFlagValue.FindStringSubmatch(err.Error())
	if m == nil || c == nil {
		return nil
	}
	for _, f := range c.Inputs {
		if f.Name == m[1] && len(f.Options) > 0 {
			return &view.Error{Code: CodeUsage, Message: err.Error(),
				Hint: "--" + f.Name + " takes one of " + strings.Join(f.Options, ", "), Input: f.Name}
		}
	}
	return nil
}

// shorthandRun is pflag's refusal of a word that opens on a dash and is not a
// flag: `unknown shorthand flag: '9' in -90m`.
var shorthandRun = regexp.MustCompile(`^unknown shorthand flag: '.' in (-[0-9]\S*)$`)

// refuseDashedValue says what to do about a value that starts with a dash and a
// digit — a negative number, a signed duration, an epoch before 1970 — which a
// flag parser reads as the flags `-9`, `-0`, `-m`. `rta time at -90m` answered
// `unknown shorthand flag: '9' in -90m` under a hint to read --help, which does
// not mention the one thing that fixes it: a `--` before the value. The time
// capability's own description says so; the error did not.
func refuseDashedValue(cmd *cobra.Command, err error) *view.Error {
	m := shorthandRun.FindStringSubmatch(err.Error())
	if m == nil {
		return nil
	}
	return &view.Error{Code: CodeUsage, Message: err.Error(),
		Hint: "a value that starts with a dash is read as a flag — put `--` before it: `" +
			cmd.CommandPath() + " -- " + m[1] + "`"}
}

// refuseFlagValue restates pflag's refusal of a value in the words the same
// mistake gets from a capability's input check — "`rta net ping` takes a whole
// number from 1 to 100 for --count, not "abc"" — instead of the Go parser's own
// report, which names strconv.ParseInt and says the syntax is invalid.
//
// The comment in plugin.checkNumber has the flag parser refusing text before
// that check runs, and this is what it refused with: every number, switch and
// duration flag in the tree answered a typo with the name of a function in the
// standard library. A capability's declared bounds are named when there are
// any, as they are for the same value arriving by any other route.
//
// nil for a refusal this does not recognise, or a flag of a kind it has no
// noun for, which keeps pflag's sentence.
func refuseFlagValue(cmd *cobra.Command, err error, c *plugin.Capability) *view.Error {
	m := refusedFlagValue.FindStringSubmatch(err.Error())
	if m == nil {
		return nil
	}
	quoted, name := m[1], m[2]
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		return nil
	}
	var want, input string
	switch flag.Value.Type() {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		want = "a whole number"
	case "float32", "float64":
		want = "a number"
	case "bool":
		want = "true or false"
	case "duration":
		want = "a duration such as 30s, 15m or 2h"
		if _, declared := flag.Value.(*durationValue); declared {
			want = "a duration such as 30s, 15m, 2h or 1d"
		}
	default:
		return nil
	}
	if c != nil {
		for _, f := range c.Inputs {
			if f.Name != name {
				continue
			}
			// Named only for a flag the capability declares: --detail and the
			// other flags the host adds are no input of the capability's.
			input = f.Name
			if f.Bounds() != "" {
				want += " " + f.Bounds()
			}
		}
	}
	hint := "`" + cmd.CommandPath() + " --help` says what it takes"
	if flag.Value.Type() == "bool" {
		// The one mistake a switch invites: `--cores yes`, or `--cores=yes`, as
		// though it were a setting with a value to choose.
		hint = "a switch needs no value: `--" + name + "` alone turns it on, `--" + name + "=false` off"
	}
	return &view.Error{Code: CodeUsage,
		Message: fmt.Sprintf("`%s` takes %s for --%s, not %s", cmd.CommandPath(), want, name, quoted),
		Hint:    hint, Input: input}
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
		//
		// And so is an input a profile can fence. Whether one does is a fact
		// about the command line — which profile it names, or which one is
		// switched on — and the tree is built before any line exists, so an
		// input is completable when it *could* be fenced and says nothing when
		// no fence applies, rather than the allowlist of the one profile that
		// has one never being offered to a shell that was told there was
		// nothing to ask.
		return len(f.Options) > 0 || f.Suggest != nil || f.Type == plugin.Path ||
			remembered().Used(c.ID, f.Name) || fenceable(c, f)
	}

	if len(positionals) > 0 {
		cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			f, ok := positionalAt(positionals, len(args))
			if !ok || !completable(f) {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			out, directive := candidates(cmd, c, f, args)
			return withoutTyped(out, f, positionals, args), directive
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
				out, directive := candidates(cmd, c, f, args)
				if !f.Type.Repeatable() {
					return out, directive
				}
				// A list flag is one word to the shell, `recipe,it`, which it
				// matches candidates against whole: bare candidates completed
				// the first element and then nothing, where the TUI's box
				// completes every one. Each comes back as the word it would
				// make (boxlist.Extend, the box's own rule) and the shell keeps
				// its cursor against it, since a comma may follow. A
				// positional is not this: it takes one value per word, and
				// withoutTyped drops the ones already given.
				//
				// A flag given more than once is one list, so what an earlier
				// occurrence named is chosen as surely as what is ahead of the
				// comma, and is left out the same way: the TUI's box holds the
				// whole list in one place, and a shell holds it across words.
				out = without(out, givenElements(cmd, f))
				return boxlist.Extend(toComplete, out), directive | cobra.ShellCompDirectiveNoSpace
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

// argumentSlot is how a positional is written in a usage line: <name> when it
// is required and [name] when it is not, with the dots inside when it repeats.
// `note done <id>` read as one id, and it takes several, all or none.
func argumentSlot(f plugin.Field) string {
	name := f.Name
	if f.Type.Repeatable() {
		name += "..."
	}
	if f.Required {
		return "<" + name + ">"
	}
	return "[" + name + "]"
}

// withoutTyped drops, for a positional that takes several values, the ones
// already on the line: `rta note done 3 <tab>` offering 3 again is a choice that
// can only be a mistake.
func withoutTyped(out []cobra.Completion, f plugin.Field, positionals []plugin.Field, args []string) []cobra.Completion {
	if !f.Type.Repeatable() {
		return out
	}
	start := slices.IndexFunc(positionals, func(p plugin.Field) bool { return p.Name == f.Name })
	if start < 0 || start >= len(args) {
		return out
	}
	return without(out, args[start:])
}

// givenElements is what the line has already given a list flag, across every
// occurrence of it: the elements the flag holds once cobra has read the words
// before the one being completed. A flag nobody gave holds its default, which is
// not something the line chose.
func givenElements(cmd *cobra.Command, f plugin.Field) []string {
	flag := cmd.Flags().Lookup(f.Name)
	if flag == nil || !flag.Changed {
		return nil
	}
	if list, ok := flag.Value.(pflag.SliceValue); ok {
		return list.GetSlice()
	}
	return nil
}

// without compares on the value alone: a candidate may carry a description after
// a tab, and a name that was given is the same name whatever it is described as.
func without(out []cobra.Completion, given []string) []cobra.Completion {
	if len(given) == 0 {
		return out
	}
	kept := make([]cobra.Completion, 0, len(out))
	for _, o := range out {
		if !slices.Contains(given, plugin.CandidateValue(o)) {
			kept = append(kept, o)
		}
	}
	return kept
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
	name, bound, fence := bindProfile(cmd, c)
	req := plugin.CompletionRequest(c, plugin.Resolve(c, plugin.Inputs{
		Caller: values, Profile: bound, ProfileName: name, Config: PluginConfig(c), Fence: fence,
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
	//
	// Narrowed to the connection's fence before the shortlist joins it, and
	// the shortlist narrowed too: what a fence refuses is not offered, and an
	// input with nothing of its own to offer offers what the fence allows.
	// Shell completion never runs a Live Suggest, so for such an input that is
	// the whole of the answer here.
	out := offering(f, c, name, fence, fence.Narrow(f, f.Candidates(ctx, req)))
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
// declared for itself, when recent.Behind says it belongs there.
func offering(f plugin.Field, c plugin.Capability, profile string, fence plugin.Fence, declared []cobra.Completion) []cobra.Completion {
	used := fence.Filter(f, remembered().For(profile, c.ID, f.Name))
	if len(used) == 0 || !recent.Behind(f) {
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
					return usageErrorOn(cmd, fmt.Errorf("missing <%s> — usage: %s", f.Name, cmd.UseLine()), f.Name)
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
		// The one-letter form the capability declared, on every kind of flag:
		// the *P variants take it and an empty one is no shorthand at all.
		short := f.Short
		switch f.Type {
		case plugin.Int:
			cmd.Flags().IntP(f.Name, short, f.DefaultInt(), usage)
		case plugin.Bool:
			def, _ := f.Default.(bool)
			cmd.Flags().BoolP(f.Name, short, def, usage)
		case plugin.Float:
			def, _ := f.Default.(float64)
			cmd.Flags().Float64P(f.Name, short, def, usage)
		case plugin.StringSlice:
			def, _ := f.Default.([]string)
			cmd.Flags().VarP(newListFlag(def), f.Name, short, usage)
		case plugin.SecretSlice:
			// StringArray, never StringSlice, and this is the same ruling
			// profileset.go's `--set` already made: StringSlice splits its
			// argument on commas, so `--data 'password=a,b'` would silently
			// become two values. For a credential that is not a cosmetic
			// difference — it writes half a password under one key and a
			// fragment under another, with nothing said. StringSlice keeps
			// its splitting because callers of the existing type rely on it.
			def, _ := f.Default.([]string)
			cmd.Flags().StringArrayP(f.Name, short, def, usage)
		case plugin.Duration:
			def, _ := f.Default.(string)
			cmd.Flags().VarP(newDurationValue(def), f.Name, short, usage)
		default:
			def, _ := f.Default.(string)
			cmd.Flags().StringP(f.Name, short, def, usage)
		}
		// A required input that config can fill is not required on the
		// command line: cobra enforces MarkFlagRequired during parsing, which
		// is before anything has looked at the operator's file, so marking it
		// would make `plugins.pg@abc.host` unusable for the one input it
		// exists to supply. rta checks the resolved value instead, in run(),
		// where config has already been applied — so the message can say
		// which of the two ways to supply it were tried.
		if requiredOnCommandLine(f) {
			_ = cmd.MarkFlagRequired(f.Name)
		}
		if f.Type.Sensitive() {
			_ = cmd.Flags().SetAnnotation(f.Name, annotCredential, []string{"true"})
		}
		if argvRefused(f) {
			_ = cmd.Flags().MarkHidden(f.Name)
		}
		if f.Config != "" && plugin.ProfileFillable(c, f) {
			_ = cmd.Flags().SetAnnotation(f.Name, annotFills, []string{f.Config})
		}
	}
}

// argvRefused is whether an input is one of the passphrases the command line
// refuses outright (passkey.Argv): the guard's and the operator key's, which
// arrive at a prompt on the CLI and in the masked box of a form. The flag is
// still parsed, so a person who types it is told why and what to do instead
// (refuseArgvSecrets) rather than that it does not exist — but nothing lists
// it, because a flag that is refused on every run reads in `--help`, in
// completion and on the explain card as one to try.
//
// Told by the declaration itself, the way the TUI picks these two out for its
// masked box (answers.go, quick.go), and not by shape: keys.add's --passphrase
// is a Local secret with no environment variable too, and the CLI takes it.
func argvRefused(f plugin.Field) bool {
	_, refused := argvCodes(f)
	return refused
}

// argvCodes is the code prefix a refusal of f on the command line carries,
// the one its own prompt's refusals carry, when f is one argvRefused names.
func argvCodes(f plugin.Field) (string, bool) {
	for _, refused := range []struct {
		field plugin.Field
		codes string
	}{
		{guard.PassphraseField, guard.PassphraseCodes},
		{operatorid.PassphraseField, operatorid.PassphraseCodes},
	} {
		if f.Name == refused.field.Name && f.Help == refused.field.Help {
			return refused.codes, true
		}
	}
	return "", false
}

// refuseArgvSecrets refuses a passphrase argvRefused names when the command
// line carried one, before the capability runs and whether or not it would
// have read it.
//
// Here, once, and not in each capability. It was each capability's to ask
// (passkey.Argv, guard.RefuseArgv), and the ones that read the field only
// sometimes — renew and agent approve only with the guard on, grant list and
// revoke only with --server, guard off never once remote — never asked
// otherwise, so with the guard off they went on with the passphrase in shell
// history and the habit the refusal exists to break. A command that declares
// the field tomorrow is covered without remembering to be. The flag is the
// command line's alone, so the TUI's masked box, which is what the input is
// declared for, never meets this; passkey.Prompt keeps its own check for a
// request that did not come through here.
func refuseArgvSecrets(cmd *cobra.Command, c plugin.Capability) *view.Error {
	for _, f := range c.Inputs {
		codes, refused := argvCodes(f)
		if !refused || !cmd.Flags().Changed(f.Name) {
			continue
		}
		value, _ := cmd.Flags().GetString(f.Name)
		if verr := passkey.OnArgv(value, codes); verr != nil {
			return verr
		}
	}
	return nil
}

// requiredOnCommandLine is whether the line itself has to carry the input as a
// flag: a required input that config can fill, or that the environment fills
// (a Local credential with EnvFallback, which a profile's secrets fill too), is
// checked after those are applied instead, so it may be left off, and a
// positional one is required by its place, which the usage line already draws.
// What the usage line and --help promise has to follow the same rule as what
// cobra enforces, or a flag that cannot be left out reads as optional until the
// refusal says otherwise, and one that can be left out is refused before the
// variable the hint names is read.
func requiredOnCommandLine(f plugin.Field) bool {
	return f.Required && f.Config == "" && (!f.Local || !f.EnvFallback) && !f.Positional
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
//
// So does the pipe. Four descriptions ended on "On the CLI, a token left out
// is read from a pipe", which is true of one surface, was sent to every
// agent as part of the tool's description, and said nothing a Piped
// declaration does not already say: Field.Piped is what makes the CLI read
// standard input, so the sentence is generated from it here.
func flagUsage(c plugin.Capability, f plugin.Field) string {
	usage := cliProse(c, f.Help)
	if len(f.Options) > 0 {
		set := "one of: " + strings.Join(f.Options, "|")
		if usage == "" {
			usage = set
		} else {
			usage += " (" + set + ")"
		}
	}
	if f.Piped {
		const pipe = "read from standard input when left out"
		if usage == "" {
			usage = pipe
		} else {
			usage += " (" + pipe + ")"
		}
	}
	if f.Local && f.EnvFallback {
		env := "or set $" + plugin.LocalEnvVar(c.ID, f.Name) + ", which keeps it out of shell history"
		if usage == "" {
			return env
		}
		usage += " (" + env + ")"
	}
	if requiredOnCommandLine(f) {
		if usage == "" {
			return "required"
		}
		usage += " (required)"
	}
	return usage
}

func runCapability(ctx context.Context, cmd *cobra.Command, c plugin.Capability, args []string, opts *globalOpts) error {
	format, err := opts.format()
	if err != nil {
		return err
	}
	// Notes goes to stderr (renderOptions), so `rta ... -o csv > out.csv`
	// keeps stdout pure csv and still tells the person at the terminal that
	// they got 3 of 744 rows. Without it a truncated result is
	// byte-indistinguishable from a complete one, which is the one thing a
	// machine-readable format must never be ambiguous about.
	renderOpts := renderOptions(cmd, format, opts.noColor)

	// Before anything else, a dry run and the destructive gate included: a
	// passphrase on argv is already in shell history, and no answer to the
	// command is worth teaching that it was fine to type it there.
	if verr := refuseArgvSecrets(cmd, c); verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}
	// A plugin whose section rta could not apply does not run on its own
	// defaults instead (ConfigWithheld). Before the destructive gate, so nobody
	// is asked to confirm a call that cannot run, and before any profile is
	// bound or forward opened, a dry run included: what it would preview is a
	// call to a destination nobody configured.
	if verr := ConfigWithheld(c); verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}

	// Safety gate: a destructive capability runs only when somebody has said
	// so. A person at a terminal is asked, further down, once the target is
	// found and what would happen can be shown (confirmprompt.go); anywhere
	// else it runs only with --yes, and exits 3 otherwise — a question, not a
	// failure, and a script answers it with the flag. The TUI confirms on the
	// dry run as well (internal/render/tui/confirm.go).
	confirming := c.Safety == plugin.Destructive && !opts.yes && !opts.dryRun
	asking := confirming && confirmTerminal()
	if confirming && !asking {
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
	// A read that names an environment and none of its targets runs on each
	// (fanout.go); everything else resolves exactly one.
	if targets := fanOutTargets(cmd, c, values, opts); len(targets) > 0 {
		return runFanOut(ctx, cmd, c, values, opts, renderOpts, targets)
	}
	// The profile, and the values it contributes. A person at a terminal
	// needs no grant for any of this: the gate is on the MCP surface, because
	// the operator writing the profile and the operator running the command
	// are the same person, and consent to yourself is not a thing.
	bound, closeTunnel, verr := resolveProfile(ctx, cmd, c, values, opts)
	// Deferred before the error check on purpose: a `kube:` connection whose
	// forward came up and whose next step then failed still has a port open,
	// and closeTunnel is never nil.
	defer closeTunnel()
	if verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}
	inputs, req, verr := preparedRun(c, values, bound, opts)
	if verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}
	if asking {
		if err := confirmCapability(ctx, cmd, c, inputs, opts, renderOpts); err != nil {
			return err
		}
		req = plugin.ResolveRequest(c, inputs, false, true).WithSurface(plugin.SurfaceCLI)
	}
	// A handler shapes its Primary value for a screen unless it is told the
	// value is all a pipe will be handed (see PrintPrimary): `http get URL |
	// jq .` is the document, not the 4 KiB a terminal shows of it. A dry run's
	// view is the request that was not sent, and a body in it is the one that
	// would have been: never the answer a pipe is asking for.
	piping := !opts.dryRun && cli.PrimaryAlone(c.Primary, renderOpts)
	if piping {
		req = req.WithPipedPrimary()
	}
	show := func(v view.View) error {
		if piping && cli.PrintPrimary(cmd.OutOrStdout(), v, c.Primary, renderOpts) {
			return nil
		}
		return cli.Render(cmd.OutOrStdout(), v, renderOpts)
	}
	v, runErr := c.Run(ctx, req)
	v, ve := ranAs(c, v, runErr, values, bound, opts)
	if ve != nil {
		// A failure that carries its answer (plugin.AnsweredError) is drawn
		// first, whole, as doctor draws its report before it exits 1: stdout is
		// the table either way, and stderr and the status say it was not a clean
		// one.
		if v != nil {
			if err := show(v); err != nil {
				return err
			}
		}
		_ = cli.RenderError(cmd.ErrOrStderr(), ve, renderOpts)
		return Rendered(ve)
	}
	if v == nil {
		return nil
	}
	return show(v)
}

// preparedRun is the request a capability runs with once its environment is
// resolved: what was typed and what the environment fills, with the plugin's
// configuration, held to the inputs it must carry.
//
// It and ranAs are the run of a single call (runCapability) and of each target
// of a fan-out (runOnTarget) alike. Fan-out began as a copy of the single
// path, and the rules the single path gained after it — a warning's undeclared
// input cut, an answered failure's table kept, what was typed remembered —
// did not reach the copy; one run in two places is how the next one does.
func preparedRun(c plugin.Capability, values map[string]any, bound boundProfile,
	opts *globalOpts,
) (plugin.Inputs, plugin.Request, *view.Error) {
	inputs := plugin.Inputs{
		Caller:      values,
		Profile:     bound.filled,
		ProfileName: bound.name,
		Tunnel:      bound.tunnel,
		Fence:       bound.fence,
		Config:      PluginConfig(c),
		// The heading Config sits under, for a refusal to name the line.
		ConfigSection: PluginConfigSection(c),
		HoldAmbient:   ConfigDisplaced(c),
	}
	req := plugin.ResolveRequest(c, inputs, opts.dryRun, opts.yes).WithSurface(plugin.SurfaceCLI)
	// The required checks cobra cannot make: of a config-backed input, since
	// making it at parse time would run before config was consulted, and of
	// any required input given empty — `--host ""`, a bare "" argument —
	// which cobra counts as given. One left off the command line cobra has
	// already refused, at parse time, with the usage beside it. The host's
	// own check (plugin.CheckRequired), asked here rather than left to the
	// guard inside c.Run: a refusal from there is a failure, and a failure
	// gets ConfigNotApplied's "so this ran without it" beside it, about a call
	// that did not run.
	return inputs, req, plugin.CheckRequired(c, req)
}

// ranAs makes what a run returned, v and runErr, what the command shows: the
// view held to the declaration, or the refusal with the view a failure carried
// beside it (plugin.AnsweredError), nil when it carried none.
func ranAs(c plugin.Capability, v view.View, runErr error, values map[string]any, bound boundProfile,
	opts *globalOpts,
) (view.View, *view.Error) {
	if runErr != nil {
		ve := view.AsError(runErr, c.ID+".failed")
		// An input this capability does not declare is no input of this call:
		// a plugin's error holds whatever its handler wrote (ErrorInput), and
		// -o json would hand a script a name no flag answers to.
		if _, ok := c.ErrorInput(ve); !ok && ve.Input != "" {
			ve = ve.WithInput("")
		}
		// A failure is the one moment an unhonoured config section explains
		// something, so it is said here and nowhere else — see
		// ConfigNotApplied. On the Hint rather than beside it, because Hint is
		// the field every output format already carries, and a note printed
		// only in pretty output is a note an agent never sees.
		if note := ConfigNotApplied(c); note != "" {
			ve = ve.WithHint(strings.TrimSpace(ve.Hint + " · " + note))
		}
		var answered *plugin.AnsweredError
		if errors.As(runErr, &answered) && answered.View != nil {
			return c.HeldWarnings(answered.View, anyInput), ve
		}
		return nil, ve
	}
	// Remembered after the run succeeded, and from `values` rather than from
	// the request: a declared default is not a choice anybody made, and a host
	// the environment filled in is already offered by the environment. What
	// this keeps is what the person typed and it worked.
	if !opts.dryRun {
		recent.Record(plugin.SurfaceCLI, c, bound.name, values)
	}
	if v == nil {
		return nil, nil
	}
	return c.HeldWarnings(v, anyInput), nil
}

// anyInput keeps every input a capability declares (Capability.HeldWarnings):
// what -o json tells a script of a warning is held to the declaration, as what
// it tells it of an error is.
func anyInput(plugin.Field) bool { return true }

func collectValues(cmd *cobra.Command, c plugin.Capability, args []string) (map[string]any, error) {
	values := map[string]any{}
	argIdx := 0
	// In the order the usage line shows them, which is not always the order
	// they are declared in: see plugin.Capability.Arguments.
	for _, f := range c.Arguments() {
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
	}
	for _, f := range c.Inputs {
		if f.Positional {
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
			list, ok := cmd.Flags().Lookup(f.Name).Value.(*listFlag)
			if !ok {
				return nil, fmt.Errorf("flag --%s is not a list", f.Name)
			}
			values[f.Name] = list.GetSlice()
		case plugin.Duration:
			d, ok := cmd.Flags().Lookup(f.Name).Value.(*durationValue)
			if !ok {
				return nil, fmt.Errorf("flag --%s is not a duration", f.Name)
			}
			values[f.Name] = d.value
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

// isTTY reports whether stdout is a terminal. A variable, as stderrIsTerminal
// is, so a test can stand in for one: an empty result's sentence is drawn only
// on a screen (cli.Options.Screen), and a test's stdout is a buffer.
var isTTY = func() bool {
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
