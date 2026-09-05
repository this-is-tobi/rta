// Package app assembles the CLI: it loads built-in plugins into the registry
// and materializes every capability as a cobra command. This is the CLI
// renderer of the capability model — the TUI, MCP and web surfaces are
// siblings, not callers, of this package.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

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
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// ExitCode maps an error returned by Execute to the fixed exit-code contract:
// 0 ok, 1 capability error, 2 usage error, 3 confirmation
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

// RenderTopLevelError writes a failure the way a success would have been
// written: in the format the caller asked for. It reports whether it handled
// the error, so the caller can fall back to cobra's own usage styling for the
// ones that are not rta's to format.
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
	if _, ok := err.(RenderedError); ok {
		return true
	}
	ve, ok := err.(*view.Error)
	if !ok {
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
	// An unparseable --output has already failed the command that used it;
	// falling back to pretty here means the error still reaches the terminal
	// rather than disappearing into a second failure.
	format, ferr := cli.ParseFormat(flag("output"))
	if ferr != nil {
		format = cli.Pretty
	}
	return cli.Options{
		Format:  format,
		NoColor: flag("no-color") == "true" || !isTTY(),
		Width:   termWidth(),
	}
}

// CodeConfirmRequired is returned when a destructive capability runs without
// confirmation. Interactive confirmation (huh) lands in M1; until then --yes
// is the only way through.
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
// marker if there is one — so the exit-code contract does not depend on
// whether a command happened to print its own error first.
func asViewError(err error, target **view.Error) bool {
	if r, ok := err.(RenderedError); ok {
		*target = r.Err
		return true
	}
	ve, ok := err.(*view.Error)
	if ok {
		*target = ve
	}
	return ok
}

// NewRegistry builds the registry of built-in plugins. The catalogue itself
// lives in builtin/all, where nothing downstream of it can be an import
// cycle away from asking what is in it.
func NewRegistry() (*registry.Registry, error) { return all.Registry(PluginConfig) }

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

// yieldsToPlugin marks a root command that deliberately steps aside when a
// plugin claims its namespace, which is what makes it safe for that name to
// be unreserved.
//
// Exactly one command wears it: the `rta ai` explainer a binary built
// without the AI engine registers, and only when no plugin holds the name.
// The reservation rule exists so that a hostile plugin cannot mask a
// command whose absence matters — `rta doctor` is the case it was written
// for. A message saying "this feature is not in this build" is the
// opposite: a real ai plugin taking that word is the feature arriving, and
// it should win. TestTheCLIReservesEveryTopLevelCommandItOwns reads this
// annotation, so the exemption is stated here rather than special-cased
// there.
const yieldsToPlugin = "rta.yields-to-plugin"

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
	return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
}

// NewRoot builds the root cobra command over the given registry.
func NewRoot(reg *registry.Registry, version string) *cobra.Command {
	opts := &globalOpts{}
	// Recorded here rather than threaded: resolveProfile is reached from a
	// cobra RunE that has the capability and nothing else, and this is the
	// registry the whole tree is being built from.
	SetInstalled(withTrust{reg})
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
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd {
				return
			}
			if !stderrIsTerminal() {
				return
			}
			// Where am I, before the command rather than after it — and first,
			// because it frames whatever follows it. Silent unless the active
			// environment carries a `color:`; see WarnActiveProfile.
			WarnActiveProfile(cmd.ErrOrStderr(), cfg, opts.output != "pretty", opts.noColor)
			WarnUntrustedPlugins(cmd.ErrOrStderr(), opts.output != "pretty")
		},
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
			// The untrusted artifacts go in too. The startup line naming them
			// is written to the primary buffer and the TUI opens on the
			// alternate one, so it is covered before it can be read and does
			// not come back until the session ends — which makes the pane the
			// only place a person in the TUI can learn a decision is pending.
			return tui.Run(cmd.Context(), reg, cfg.TrustedDashboard(), pluginConfig.For,
				tui.WithUntrusted(untrustedPluginsFound))
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
	_ = root.RegisterFlagCompletionFunc("output",
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
	// The one namespace with a bare form of its own: `rta ai "question"`
	// streams, while its subcommands stay ordinary capability commands. In
	// a binary built without the AI engine there is no such namespace, and
	// this is where `rta ai` still gets an answer worth reading.
	root.AddCommand(newMCPCommand(reg, version))
	root.AddCommand(newExplainCommand(reg, opts))
	root.AddCommand(newPluginCommand(reg, version, opts))
	root.AddCommand(newDoctorCommand(reg, opts))
	root.AddCommand(newInitCommand(reg))
	root.AddCommand(newUseCommand(opts))
	root.AddCommand(newPolicyCommand(opts))
	root.AddCommand(newProfileCommand(reg, opts))
	root.AddCommand(newConfigCommand())
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
	// Per capability rather than persistent on the root, for the same reason
	// --detail is: a flag that exists everywhere and does something nowhere
	// teaches people it is decoration. `rta sys cpu --profile x` should say
	// "unknown flag", which names the fact, instead of accepting a value that
	// no input could ever receive.
	if plugin.Profilable(c) && cmd.Flags().Lookup("profile") == nil {
		cmd.Flags().String("profile", "", "run against one of the connections in your config "+
			"(name, or name/instance when an environment holds several)")
		_ = cmd.RegisterFlagCompletionFunc("profile",
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
// It stays a plain error rather than becoming a view.Error, because a usage
// mistake exits 2 and ExitCode maps every view.Error to 1. The renderer boxes
// plain errors the same way, so nothing is lost by it.
func positionalFlagError(c plugin.Capability, positionals []plugin.Field) func(*cobra.Command, error) error {
	return func(_ *cobra.Command, err error) error {
		name, ok := unknownFlagName(err)
		if !ok {
			return err
		}
		for _, f := range positionals {
			if f.Name == name {
				return fmt.Errorf("this capability takes %q as an argument, not a flag — %s", f.Name, cliForm(c))
			}
		}
		return err
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
	out := offering(f, c, f.Candidates(ctx, req))
	if len(out) == 0 {
		return nil, directive
	}
	return out, directive
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

	// Safety gate. Interactive confirmation lands in M1;
	// in M0 destructive capabilities require an explicit --yes.
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
		return err
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
	resolved := plugin.Resolve(c, plugin.Inputs{
		Caller:      values,
		Profile:     filled,
		ProfileName: profileName,
		Config:      PluginConfig(c),
	})
	// The required check for config-backed inputs, which cobra no longer
	// makes because making it would have run before config was consulted.
	// Named here rather than left as a handler's zero value: an input that is
	// required and empty is the one case where "you can also put this in your
	// config" is the sentence somebody needs.
	if verr := requireResolved(c, resolved); verr != nil {
		_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
		return Rendered(verr)
	}
	v, runErr := c.Run(ctx, plugin.NewRequest(
		resolved, opts.dryRun, opts.yes).WithSurface(plugin.SurfaceCLI))
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
	// `resolved`: a declared default is not a choice anybody made, and a host
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
