package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/builtin/audit"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/notify"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Registering rta with an MCP client, and the line it will not cross.
//
// **rta does not write another tool's config file.** Where a client ships its
// own command for editing its own configuration, rta runs that; where it does
// not, rta prints exactly what to add and where, and stops. Three reasons, and
// the first is the one that decides it:
//
//   - This is the file that grants an agent access to the operator's secrets.
//     A product whose entire argument is that consent should be visible and
//     deliberate has no business writing itself into five agents' permission
//     files unattended.
//   - These files hold things rta must not touch. VS Code's mcp.json is JSONC
//     — the one on the machine this was written on carries the operator's own
//     comments and an API key in a header — so a parse-and-rewrite would
//     destroy comments at best and mishandle a credential at worst.
//   - A config format changes when its client changes, not when rta does. The
//     same reasoning keeps `plugins/kube` shelling out to kubectl rather than
//     linking client-go: the tool that owns the format is the
//     one that stays correct.
//
// A client's own command has the same property in the other direction — it
// cannot go stale, because it ships with the thing it configures.
//
// Reading is not writing. The files are opened to find out what is registered
// (so a second install can say "nothing to do", or replace what differs through
// the client's own remove and add), and never to change them.

// mcpClient is one client rta knows how to register with, or failing that,
// how to describe.
type mcpClient struct {
	// name is what the operator types, and — because they typed it — the
	// agent name the server is registered under. See asName.
	name  string
	label string
	// bin and args are the client's own configuration command. An empty bin
	// means rta has no verified way to register with this client and will
	// only ever show what to add. serve is the whole of what the client
	// launches after the binary: `mcp serve --as <name>` and the options the
	// registration carries.
	bin  string
	args func(self string, serve []string) []string
	// globalArgs is args, but with the client's own flag for registering at
	// the user level rather than wherever rta happens to be run from — nil
	// for a client with no verified global-scope command. --global refuses
	// outright rather than guessing one, the same caution the "verified"
	// column above already states: a wrong flag here is a wrong command run
	// against a file that grants an agent access to secrets.
	globalArgs func(self string, serve []string) []string
	// alwaysGlobal marks a client whose ordinary command already writes to a
	// single, user-level location: --global changes nothing about what
	// runs, only that it is accepted rather than refused as unsupported.
	alwaysGlobal bool
	// file is where this client keeps its MCP configuration, and block is
	// what to put in it. Both are used when there is no command, and when
	// there is one but it is not installed. globalFile is what --global
	// prints instead, "" when file already names the one relevant path.
	file, globalFile string
	block            func(self string, serve []string) string
	// note is anything the operator needs beyond the block itself.
	note string
	// scope says in words where the client's own command registers rta, or ""
	// when rta does not know and would only be guessing.
	scope func(global bool, wd string) string
	// removeArgs is the client's own command for taking rta out of one scope,
	// nil where rta has not verified one. With a way to read what is
	// registered (registered) it is what lets a second install replace the
	// first rather than refuse beside it.
	removeArgs func(scope claudeScope) []string
	// auditLabel is how `rta audit clients` names this client's files, which
	// is the reader that finds out whether rta is in them. dir is where the
	// client leaves its own directory in the home directory once it has been
	// run, which is how it is told apart from one that is not on this
	// machine at all.
	auditLabel string
	dir        func(home string) string
}

// serveOptions are the options of `rta mcp serve` that a registration can
// carry, which is the only place an operator can put them: the client launches
// the server, so a flag that is not in the registered line is a flag that is
// not set.
//
// Every one is off by default, and the zero value is the registration that was
// always made. Nothing here widens what a registered agent can reach — consent
// asks where a refusal was, and a root narrows or moves a gate that is on.
type serveOptions struct {
	consent       bool
	consentNotify bool
	consentWait   time.Duration
	maxResultMiB  int
	maxResultSet  bool
	roots         []string
}

// serveArgs is the argv every client ends up launching. `--as` is not
// decoration: without it every MCP client on the machine is one principal, so
// a grant issued while talking to one authorizes all the others. The operator
// typed the client's name, so the name is theirs.
func serveArgs(as string, o serveOptions) []string {
	args := []string{"mcp", "serve", "--as", as}
	if o.consent {
		args = append(args, "--consent")
	}
	if o.consentNotify {
		args = append(args, "--consent-notify")
	}
	if o.consentWait > 0 {
		args = append(args, "--consent-wait", durationFlag(o.consentWait))
	}
	if o.maxResultSet {
		args = append(args, "--max-result", strconv.Itoa(o.maxResultMiB))
	}
	for _, r := range o.roots {
		args = append(args, "--root", r)
	}
	return args
}

// durationFlag spells a wait the way a person types one, which pflag reads
// back to the same value.
func durationFlag(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d%time.Second == 0:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return d.String()
}

// validate refuses the combinations `rta mcp serve` would only warn about, or
// fail on at the first call: this is typed once, and its answer is a line in
// another tool's configuration that is read months later, with no terminal
// to say what is wrong with it.
func (o serveOptions) validate() *view.Error {
	if o.maxResultSet && (o.maxResultMiB < 1 || o.maxResultMiB > maxResultCeilingMiB) {
		return serveUsage(fmt.Sprintf("--max-result is %d MiB, which is not between 1 and %d",
			o.maxResultMiB, maxResultCeilingMiB),
			"it is the most a result may be before it is withheld: what the server holds of an answer is "+
				"several times its size, so a ceiling is a memory limit, not only a courtesy to the model")
	}
	if o.consentNotify && !o.consent {
		return serveUsage("--consent-notify needs --consent",
			"nothing parks to ring about without it: a call that needs a grant is refused, not held")
	}
	if o.consentWait > 0 && !o.consent {
		return serveUsage("--consent-wait needs --consent",
			"it bounds how long a parked call waits, and without --consent no call is parked")
	}
	if o.consentWait > consent.MaxWait {
		return serveUsage("--consent-wait is above the ten-minute maximum",
			"past a couple of minutes the agent's own request has timed out; a parked call waits ten minutes at most")
	}
	return nil
}

// withRoots resolves each root to an absolute path and asks the same question
// `rta mcp serve` asks at start, so a root that would stop the server is
// refused here instead. Absolute because the client launches the server from
// wherever it was started, and a relative root registered today means a
// different directory in every other one.
func (o serveOptions) withRoots(roots []string) (serveOptions, *view.Error) {
	o.roots = nil
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			return o, view.Errorf("core.mcp.root", "--root %q: %v", r, err)
		}
		// A registration is typed once and read months later, by a server no
		// terminal is attached to: a root that is a typo is a gate that
		// silently refuses everything the operator meant it to reach.
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return o, view.Errorf("core.mcp.root", "--root %s is not a directory that exists", abs).
				WithHint("name the directory an agent may reach; the server resolves it when it starts")
		}
		o.roots = append(o.roots, abs)
	}
	if len(o.roots) == 0 {
		return o, nil
	}
	if _, err := pathguard.New(o.roots...); err != nil {
		return o, view.Errorf("core.mcp.root", "%v", err).
			WithHint("a --root is resolved through every symlink in it, and this one could not be")
	}
	return o, nil
}

// pairs describes the options that are set, for a receipt: what each one
// does, in the words the operator used to turn it on.
func (o serveOptions) pairs() []view.Pair {
	var out []view.Pair
	if o.consent {
		how := "a call that needs a grant waits for you instead of being refused — `rta agent pending` lists it, `rta agent allow <id>` answers"
		if o.consentWait > 0 {
			how += "; it is refused anyway after " + durationFlag(o.consentWait)
		}
		out = append(out, view.Pair{Key: "consent", Value: how})
		switch {
		case o.consentNotify && notify.Available():
			out = append(out, view.Pair{Key: "notify", Value: "this desktop rings when a call is parked"})
		case o.consentNotify:
			out = append(out, view.Pair{Key: "notify", Value: "no desktop notifier is installed here, so a parked call shows only in `rta agent pending`"})
		case notify.Available():
			out = append(out, view.Pair{Key: "notify", Value: "off — add --consent-notify to have this desktop ring when a call is parked"})
		}
	}
	if len(o.roots) > 0 {
		out = append(out, view.Pair{Key: "roots", Value: strings.Join(o.roots, ", ") +
			" — a path argument outside these is refused, and the directory the client starts in is not one unless it is listed"})
	}
	if o.maxResultSet {
		out = append(out, view.Pair{Key: "max result", Value: strconv.Itoa(o.maxResultMiB) + " MiB — a larger answer is withheld"})
	}
	return out
}

// stdioServer is one entry, as a struct rather than a map so the fields keep
// the order somebody reads them in: a map marshals its keys alphabetically,
// which puts "args" above the "command" they are arguments to.
type stdioServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// jsonBlock renders the mcpServers shape, which Claude Desktop established
// and Cursor and Gemini both adopted.
func jsonBlock(key, self string, serve []string) string {
	body, _ := json.MarshalIndent(map[string]map[string]stdioServer{
		key: {"rta": {Command: self, Args: serve}},
	}, "", "  ")
	return string(body)
}

func mcpClients() []mcpClient {
	return []mcpClient{
		{
			name: "claude", label: "Claude Code",
			// Verified against claude 2026-08-29: `claude mcp add <name>
			// <command> [args...]`, with `--` separating rta's own flags from
			// claude's own — without it `--as` is read by claude.
			bin: "claude",
			args: func(self string, serve []string) []string {
				return append([]string{"mcp", "add", "rta", "--", self}, serve...)
			},
			// --scope user is claude's own flag, already documented in
			// docs/30-boundary/60-ai-clients.md and confirmed there against
			// the real CLI — the one client this file can say that about
			// rather than only claim it.
			globalArgs: func(self string, serve []string) []string {
				return append([]string{"mcp", "add", "rta", "--scope", "user", "--", self}, serve...)
			},
			// Verified against claude's own `mcp remove --help`: <name> and
			// --scope local|user|project, which without it takes the entry
			// out of whichever scope holds it and refuses when two do.
			removeArgs: func(scope claudeScope) []string {
				return []string{"mcp", "remove", "rta", "--scope", string(scope)}
			},
			scope: func(global bool, wd string) string {
				if global {
					return "every project (Claude Code's user scope)"
				}
				return "this directory only (" + wd + ") — add --global for every project"
			},
			file:       ".mcp.json (or ~/.claude.json)",
			block:      func(self string, serve []string) string { return jsonBlock("mcpServers", self, serve) },
			auditLabel: "Claude Code",
			dir:        func(home string) string { return filepath.Join(home, ".claude") },
		},
		{
			name: "vscode", label: "VS Code",
			// Verified against code 2026-08-29 by running it against a
			// throwaway --user-data-dir: it takes {"name","command","args"}
			// and writes servers.<name>, which is VS Code's own key and not
			// the mcpServers everything else uses.
			bin: "code",
			args: func(self string, serve []string) []string {
				spec, _ := json.Marshal(struct {
					Name string `json:"name"`
					stdioServer
				}{Name: "rta", stdioServer: stdioServer{Command: self, Args: serve}})
				return []string{"--add-mcp", string(spec)}
			},
			// VS Code's own CLI exposes no project-scoped variant — every
			// run already lands in the user config --global would ask for.
			alwaysGlobal: true,
			scope: func(bool, string) string {
				return "every project — VS Code keeps one user-level mcp.json"
			},
			file:       "VS Code's user mcp.json",
			block:      func(self string, serve []string) string { return jsonBlock("servers", self, serve) },
			auditLabel: "VS Code",
			dir: func(home string) string {
				if runtime.GOOS == "darwin" {
					return filepath.Join(home, "Library", "Application Support", "Code")
				}
				return filepath.Join(home, ".config", "Code")
			},
		},
		{
			name: "codex", label: "OpenAI Codex CLI",
			// Declared but NOT verified here — codex is not installed on the
			// machine this was written on. If the command is missing or fails,
			// the operator gets the block below instead, which is the whole
			// reason the fallback exists rather than an error.
			bin: "codex",
			args: func(self string, serve []string) []string {
				return append([]string{"mcp", "add", "rta", "--", self}, serve...)
			},
			file: "~/.codex/config.toml",
			// TOML, alone among these. Rendered by hand because it is four
			// lines and pulling in an encoder to write four lines is worse.
			block: func(self string, serve []string) string {
				quoted := make([]string, 0, len(serve))
				for _, a := range serve {
					quoted = append(quoted, strconv.Quote(a))
				}
				return fmt.Sprintf("[mcp_servers.rta]\ncommand = %s\nargs = [%s]\n",
					strconv.Quote(self), strings.Join(quoted, ", "))
			},
			auditLabel: "Codex CLI",
			dir:        func(home string) string { return filepath.Join(home, ".codex") },
		},
		{
			name: "gemini", label: "Gemini CLI",
			// Declared but not verified here, as codex above.
			bin: "gemini",
			args: func(self string, serve []string) []string {
				return append([]string{"mcp", "add", "rta", self}, serve...)
			},
			file:       "~/.gemini/settings.json",
			block:      func(self string, serve []string) string { return jsonBlock("mcpServers", self, serve) },
			auditLabel: "Gemini CLI",
			dir:        func(home string) string { return filepath.Join(home, ".gemini") },
		},
		{
			name: "cursor", label: "Cursor",
			// No command: Cursor is configured by editing the file.
			file: "~/.cursor/mcp.json (or .cursor/mcp.json for one project)",
			// --global picks the one path rather than naming both — the
			// project file mentioned above is rta's own default
			// recommendation (docs/30-boundary/60-ai-clients.md: grants an
			// agent holds then scope to the repository), not this one.
			globalFile: "~/.cursor/mcp.json",
			block:      func(self string, serve []string) string { return jsonBlock("mcpServers", self, serve) },
			auditLabel: "Cursor",
			dir:        func(home string) string { return filepath.Join(home, ".cursor") },
		},
		{
			name: "copilot", label: "GitHub Copilot CLI",
			// No command either — copilot manages MCP from an interactive
			// /mcp prompt, so there is nothing for rta to run.
			file:       "~/.copilot/mcp-config.json",
			block:      func(self string, serve []string) string { return jsonBlock("mcpServers", self, serve) },
			note:       "Copilot can also do this from its own prompt: run `copilot`, then `/mcp`.",
			auditLabel: "GitHub Copilot CLI",
			dir:        func(home string) string { return filepath.Join(home, ".copilot") },
		},
	}
}

func findClient(name string) (mcpClient, bool) {
	for _, c := range mcpClients() {
		if c.name == name {
			return c, true
		}
	}
	return mcpClient{}, false
}

// otherClient is a client rta has never heard of, for the operator who typed
// its name anyway: Windsurf, Zed, Cline, Claude Desktop, JetBrains — anything
// that speaks MCP over stdio takes the standard mcpServers block. All rta can
// honestly say is the block and that it does not know where this one keeps
// it; it never runs anything for it.
func otherClient(name string) mcpClient {
	return mcpClient{
		name: name, label: name,
		file:  "wherever " + name + " keeps its MCP servers — rta does not know where",
		block: func(self string, serve []string) string { return jsonBlock("mcpServers", self, serve) },
		note: "this is the standard mcpServers shape, which most clients read; VS Code keeps its servers under " +
			"`servers` instead, and Codex reads TOML (`rta mcp install vscode --show`, `rta mcp install codex --show`)",
	}
}

// registeredBinary is the path a client is told to launch.
//
// The path the operator ran, when it is on PATH and is the same file as this
// process: a package manager's link (/opt/homebrew/bin/rta) stays true across
// an upgrade, where the versioned directory it points into disappears, and the
// client then fails to start rta with nothing naming the cause. Otherwise the
// file with every symlink resolved — a symlink into a build tree is the kind
// of thing that stops being true quietly, and nothing on PATH vouches for it.
//
// A link on PATH is only as trustworthy as PATH is, which is what a command
// typed by name has always been.
func registeredBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	return stableBinary(os.Args[0], self), nil
}

// stableBinary is registeredBinary for a given argv[0] and resolved path.
func stableBinary(argv0, resolved string) string {
	if argv0 == "" {
		return resolved
	}
	onPath, err := exec.LookPath(filepath.Base(argv0))
	if err != nil {
		return resolved
	}
	if abs, err := filepath.Abs(onPath); err == nil {
		onPath = abs
	}
	a, errA := os.Stat(onPath)
	b, errB := os.Stat(resolved)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		return resolved
	}
	return onPath
}

func newMCPInstallCommand(opts *globalOpts) *cobra.Command {
	var (
		as            string
		show, global  bool
		consentWait   time.Duration
		maxResultMiB  int
		roots         []string
		consentOn     bool
		consentNotify bool
	)

	all := mcpClients()
	valid := make([]cobra.Completion, 0, len(all))
	names := make([]string, 0, len(all))
	for _, c := range all {
		valid = append(valid, cobra.CompletionWithDesc(c.name, c.label))
		names = append(names, c.name)
	}
	sort.Strings(names)

	cmd := &cobra.Command{
		Use:   "install <client>",
		Short: "Register rta as an MCP server in a client (" + strings.Join(names, ", ") + ")",
		Long: "Registers rta with an MCP client, under a name, so that grants issued " +
			"while talking to one agent do not authorize every other client on this " +
			"machine.\n\n" +
			"Where a client ships its own command for editing its own configuration, " +
			"rta runs that. Where it does not, rta prints what to add and where, and " +
			"writes nothing: this is the file that gives an agent access to your " +
			"secrets, and it is worth reading before it changes. A client rta does not " +
			"list gets the standard block to add, and rta says it does not know where " +
			"that client keeps its configuration.\n\n" +
			"Run again, it compares what is registered with what you ask for: the same " +
			"says there is nothing to do, and a different name, path or option replaces " +
			"the old registration through the client's own commands.\n\n" +
			"The server's own options go into the registered line, because the client " +
			"is what launches the server: --consent asks you instead of refusing a call " +
			"that needs a grant, --root widens the path gate, --max-result bounds an " +
			"answer. All are off unless given.",
		// ExactArgs alone. OnlyValidArgs refused a client it did not know in
		// cobra's words — `invalid argument "nope" for "rta mcp install"` — one
		// step ahead of the check in RunE that names every client rta does
		// know, which therefore never ran. ValidArgs stays for completion.
		Args:      cobra.ExactArgs(1),
		ValidArgs: valid,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ok := findClient(args[0])
			if !ok {
				if near := plausibleSuggestions(args[0], names); len(near) > 0 {
					quoted := make([]string, len(near))
					for i, n := range near {
						quoted[i] = strconv.Quote(n)
					}
					return &view.Error{Code: CodeUsage,
						Message: fmt.Sprintf("unknown client %q — the closest %s %s", args[0],
							format.Plural(len(near), "match is", "matches are"), strings.Join(quoted, ", ")),
						Hint: "rta registers with " + strings.Join(names, ", ") +
							"; any other name prints the standard block for that client to add"}
				}
				client = otherClient(args[0])
			}

			serve := serveOptions{consent: consentOn, consentNotify: consentNotify}
			if cmd.Flags().Changed("consent-wait") {
				serve.consentWait = consentWait
			}
			if cmd.Flags().Changed("max-result") {
				serve.maxResultMiB, serve.maxResultSet = maxResultMiB, true
			}
			if verr := serve.validate(); verr != nil {
				return verr
			}
			serve, verr := serve.withRoots(roots)
			if verr != nil {
				return verr
			}

			name := strings.TrimSpace(as)
			if name == "" {
				name = client.name
			}
			// The same rule `mcp serve --as` and `grant allow --agent` use,
			// and the same function: a name registered here that the grant
			// command would refuse is a server nobody can ever grant anything.
			if verr := grant.CheckAgent(name); verr != nil {
				return verr
			}

			outcome, err := installClient(cmd.Context(), cmd.ErrOrStderr(), installRequest{
				client: client, as: name, global: global, show: show, dryRun: opts.dryRun, serve: serve,
			})
			if err != nil {
				return err
			}
			if outcome.block {
				return renderClientBlock(cmd, opts, outcome.answer)
			}
			return renderView(cmd, opts, outcome.answer)
		},
	}
	cmd.Flags().StringVar(&as, "as", "",
		"name to register this agent under (default: the client's name)")
	cmd.Flags().BoolVar(&show, "show", false,
		"print what to add without running the client's own command")
	cmd.Flags().BoolVar(&global, "global", false,
		"install for every project instead of just this one, where the client supports it")
	cmd.Flags().BoolVar(&consentOn, "consent", false,
		"register the server to ask you instead of refusing when a call needs a grant (off by default)")
	cmd.Flags().BoolVar(&consentNotify, "consent-notify", false,
		"with --consent, also ring this machine's desktop notification when a call is parked")
	cmd.Flags().DurationVar(&consentWait, "consent-wait", consent.DefaultWait,
		"with --consent, how long a parked call waits for your answer before it is refused")
	cmd.Flags().IntVar(&maxResultMiB, "max-result", plugin.DefaultResultLimit>>20,
		"the most a result may be, in MiB, before the server withholds it")
	cmd.Flags().StringSliceVar(&roots, "root", nil,
		"directory a caller may name in a path argument (repeatable); naming any replaces the default, "+
			"the directory the client starts the server in")
	completeFlag(cmd, "root",
		func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveFilterDirs
		})
	return cmd
}

// installRequest is everything one registration is decided from, whether it is
// asked for by `rta mcp install` or by `rta init`.
type installRequest struct {
	client mcpClient
	as     string
	global bool
	show   bool
	dryRun bool
	serve  serveOptions
}

// installOutcome is what a registration came to: the pairs that say so, and
// whether they carry a block to add by hand rather than a registration made.
type installOutcome struct {
	answer view.KeyValue
	block  bool
}

// installStep is one command run through the client's own CLI.
type installStep struct {
	args   []string
	remove bool
	scope  claudeScope
}

// installClient registers rta with one client through its own command, or says
// what to add when it has none, is not installed, or was asked not to run.
//
// said receives the client's own words, both streams of them. Stdout is rta's
// answer, in the format -o asks for, and `claude mcp add` printing its own
// sentence into it put prose ahead of the json a script had asked for. A
// person at a terminal still reads both.
func installClient(ctx context.Context, said io.Writer, req installRequest) (installOutcome, error) {
	c := req.client
	self, err := registeredBinary()
	if err != nil {
		return installOutcome{}, view.Errorf("core.mcp.install.self", "locating the rta binary: %v", err).
			WithHint("the client is registered with this binary's path, so it has to be " +
				"one rta can name; `rta mcp install " + c.name + " --show` prints the block to fill in")
	}
	serve := serveArgs(req.as, req.serve)
	wd, _ := os.Getwd()
	block := func(why string) installOutcome {
		return installOutcome{answer: describeClient(c, self, serve, req.as, req.global, req.serve, why), block: true}
	}

	if req.show || c.bin == "" {
		return block(""), nil
	}
	bin, err := exec.LookPath(c.bin)
	if err != nil {
		return block(c.bin + " is not on PATH, so rta ran nothing"), nil
	}
	// Resolved here, inside the one branch that runs the client's own
	// command, because that is the only thing --global can be refused
	// *about*. Asked with --show, or for a client that is not installed,
	// nothing is going to run and the operator is getting the block to paste
	// either way — refusing there sent them to `--show` in a message they had
	// already passed `--show` to read. What --global steers on that path is
	// the block's own path, which describeClient reads.
	argsFn := c.args
	if req.global {
		switch {
		case c.globalArgs != nil:
			argsFn = c.globalArgs
		case c.alwaysGlobal:
			// Already what --global asked for: VS Code's own command writes
			// to its single user-level file.
		default:
			// codex and gemini's own base commands are declared but not
			// verified against the real CLI; guessing a scope flag on top of
			// that is a wrong command run against a file that grants an agent
			// access to secrets, not a smaller version of the right one.
			//
			// core.usage: this command line cannot work as typed, and the
			// fix is on it.
			return installOutcome{}, &view.Error{Code: CodeUsage,
				Message: fmt.Sprintf("rta does not know %s's flag for installing at the user level", c.label),
				Hint: fmt.Sprintf("try `rta mcp install %s --show` and add it yourself, "+
					"or check %s's own --help", c.name, c.bin)}
		}
	}

	home, _ := os.UserHomeDir()
	plan := planInstall(c, req, self, serve, argsFn, home, wd)
	line := plan.commandLine(bin)
	receipt := installReceipt{client: c, req: req, wd: wd, plan: plan, line: line}

	if plan.nothingToDo() {
		receipt.verb = "already registered"
		return installOutcome{answer: receipt.answer()}, nil
	}
	if req.dryRun {
		receipt.verb = "would register"
		if plan.replaces != nil {
			receipt.verb = "would update"
		}
		return installOutcome{answer: receipt.answer()}, nil
	}

	run := &installRun{ctx: ctx, c: c, bin: bin, req: req, plan: plan, said: said, block: block}
	for _, step := range plan.steps {
		if err := run.do(step); err != nil {
			return run.failed(step, err)
		}
		run.done++
	}
	receipt.verb = "registered"
	if plan.replaces != nil {
		receipt.verb = "updated"
	}
	return installOutcome{answer: receipt.answer()}, nil
}

// installRun is the steps of one registration being run through the client's
// own command, and what it takes to answer for the one that failed.
type installRun struct {
	ctx   context.Context
	c     mcpClient
	bin   string
	req   installRequest
	plan  installPlan
	said  io.Writer
	block func(why string) installOutcome
	done  int
	words bytes.Buffer
}

// do runs one step, keeping what the client said of it.
func (r *installRun) do(step installStep) error {
	r.words.Reset()
	// One writer for both streams, the same value: exec copies them on a
	// single goroutine only then, and two writing into the same destination
	// at once is a race.
	both := io.MultiWriter(r.said, &r.words)
	run := exec.CommandContext(r.ctx, r.bin, step.args...)
	run.Stdout, run.Stderr = both, both
	return run.Run()
}

// failed is what a registration answers when the client's own command refused
// a step. Not fatal for the ordinary case: a client whose command moved on is
// exactly when somebody needs the block instead, and failing there would leave
// them with nothing. What is fatal is a registration that is not the one asked
// for, which is an error and not a receipt, and the state a half-done
// replacement leaves, which is put back when it can be.
func (r *installRun) failed(step installStep, err error) (installOutcome, error) {
	manual := manualRemove(r.c, step.scope == scopeUser || (step.scope == "" && r.req.global))
	switch {
	case step.remove && r.done == 0:
		return installOutcome{}, view.Errorf("core.mcp.install.remove",
			"%s would not take out the registration it holds: %v", r.c.label, err).
			WithHint("nothing was changed; take it out yourself with `" + manual + "`, then run this again")
	case step.remove:
		return installOutcome{}, view.Errorf("core.mcp.install.remove",
			"rta is registered for every project, but %s would not take out the directory-only registration "+
				"that overrides it here: %v", r.c.label, err).
			WithHint("take it out yourself with `" + manual + "`")
	case r.plan.replaces != nil && r.done > 0:
		hint := "the registration it replaced is back as it was"
		if !r.restore() {
			hint = "the registration it replaced could not be put back; `rta mcp install " + r.c.name +
				"` registers it again"
		}
		return installOutcome{}, view.Errorf("core.mcp.install.add",
			"%s refused the new registration: %v", r.c.label, err).WithHint(hint)
	case strings.Contains(strings.ToLower(r.words.String()), "already exists"):
		// A refusal because rta is already registered is not a command that
		// moved on, and the block it was answered with is the one thing to add
		// twice. It is not success either: the registration the operator asked
		// for is not the one that is there, and rta could not read enough of
		// the client's configuration to compare the two.
		return installOutcome{}, view.Errorf("core.mcp.install.exists",
			"%s already holds a registration for rta, and it is not the one asked for", r.c.label).
			WithHint("take it out with `" + manualRemove(r.c, r.req.global) + "`, then run this again")
	}
	fmt.Fprintf(r.said, "rta: %s could not register it (%v) — here is what to add instead\n", r.c.bin, err)
	return r.block(fmt.Sprintf("%s could not register it (%v); what it said is above", r.c.bin, err)), nil
}

// restore puts back the registration a replacement took out and could not
// replace, through the same command that made it.
func (r *installRun) restore() bool {
	run := exec.CommandContext(r.ctx, r.bin, r.plan.restore...)
	run.Stdout, run.Stderr = r.said, r.said
	return run.Run() == nil
}

// manualRemove is the exact line that takes rta out of a client by hand.
func manualRemove(c mcpClient, global bool) string {
	if c.removeArgs != nil {
		scope := scopeLocal
		if global {
			scope = scopeUser
		}
		return c.bin + " " + shellJoin(c.removeArgs(scope))
	}
	return c.bin + " mcp remove rta"
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellquote.Arg(a)
	}
	return strings.Join(quoted, " ")
}

// installPlan is what a registration will run, decided from what is there.
type installPlan struct {
	steps []installStep
	// replaces is the registration in the target scope that differs from the
	// one asked for, and restore the command that puts it back.
	replaces *claudeRegistration
	restore  []string
	// redundant is a directory-only registration that --global makes
	// pointless, and worse than pointless: it overrides the new one here.
	redundant *claudeRegistration
	// same is the registration already in place, identical to the one asked
	// for, when there is one.
	same bool
	// changed says how a replaced registration differs.
	changed string
}

func (p installPlan) nothingToDo() bool { return len(p.steps) == 0 }

// commandLine is every command the plan runs, in order.
func (p installPlan) commandLine(bin string) string {
	lines := make([]string, len(p.steps))
	for i, s := range p.steps {
		lines[i] = bin + " " + shellJoin(s.args)
	}
	return strings.Join(lines, " && ")
}

// planInstall decides what to run for one client from what is registered.
//
// For Claude Code, the only client whose configuration is read to scope, that
// is a comparison: the registration in the scope this install writes to is
// identical (nothing to run), different (taken out, then added again), or
// absent (added); and under --global a directory-only registration that would
// shadow the new one is taken out after it succeeds. Every other client is
// asked to add, and told nothing to do only when the file it keeps already
// holds exactly this.
func planInstall(c mcpClient, req installRequest, self string, serve []string,
	argsFn func(string, []string) []string, home, wd string,
) installPlan {
	add := installStep{args: argsFn(self, serve)}
	if c.removeArgs == nil {
		if alreadyDeclared(c, self, serve, home, wd) {
			return installPlan{same: true}
		}
		return installPlan{steps: []installStep{add}}
	}

	target := scopeLocal
	if req.global {
		target = scopeUser
	}
	var here, local *claudeRegistration
	for _, r := range claudeRegistrations(home, wd) {
		switch {
		case r.scope == target && r.managed():
			here = &r
		case r.scope == scopeLocal && r.managed():
			local = &r
		}
	}
	plan := installPlan{}
	switch {
	case here != nil && here.command == self && slices.Equal(here.args, serve):
		plan.same = true
	case here != nil:
		plan.replaces = here
		plan.changed = describeChange(*here, self, serve, req.as)
		plan.restore = restoreArgs(c, *here)
		plan.steps = append(plan.steps, installStep{args: c.removeArgs(target), remove: true, scope: target}, add)
	default:
		plan.steps = append(plan.steps, add)
	}
	if req.global && local != nil {
		plan.redundant = local
		plan.steps = append(plan.steps, installStep{args: c.removeArgs(scopeLocal), remove: true, scope: scopeLocal})
	}
	return plan
}

// restoreArgs is the command that registers an entry as it was.
func restoreArgs(c mcpClient, was claudeRegistration) []string {
	args := []string{"mcp", "add", "rta"}
	if was.scope != scopeLocal {
		args = append(args, "--scope", string(was.scope))
	}
	return append(append(args, "--", was.command), was.args...)
}

// alreadyDeclared reports whether the file a client keeps already launches
// exactly this server under the name rta gives it.
func alreadyDeclared(c mcpClient, self string, serve []string, home, wd string) bool {
	if c.auditLabel == "" || home == "" {
		return false
	}
	found, _ := audit.Registrations(home, wd)
	for _, r := range found {
		if strings.HasPrefix(r.Label, c.auditLabel) && r.Name == "rta" &&
			r.Command == self && slices.Equal(r.Args, serve) {
			return true
		}
	}
	return false
}

// describeChange says how a registration that is there differs from the one
// asked for, in the three things that can differ.
func describeChange(was claudeRegistration, self string, serve []string, as string) string {
	var parts []string
	if was.as != as {
		old := was.as
		if old == "" {
			old = "no name"
		}
		parts = append(parts, "name "+old+" → "+as)
	}
	if was.command != self {
		parts = append(parts, "binary "+was.command+" → "+self)
	}
	oldOpts, newOpts := optionTail(was.args), optionTail(serve)
	if !slices.Equal(oldOpts, newOpts) {
		parts = append(parts, "options "+orNone(oldOpts)+" → "+orNone(newOpts))
	}
	if len(parts) == 0 {
		parts = append(parts, "arguments "+shellJoin(was.args)+" → "+shellJoin(serve))
	}
	return strings.Join(parts, "; ")
}

// optionTail is what follows `mcp serve --as <name>` in a registered line.
func optionTail(args []string) []string {
	for i := 0; i+3 < len(args); i++ {
		if args[i] == "mcp" && args[i+1] == "serve" && args[i+2] == "--as" {
			return args[i+4:]
		}
	}
	return nil
}

func orNone(args []string) string {
	if len(args) == 0 {
		return "none"
	}
	return shellJoin(args)
}

// installReceipt is what a registration answers with, in every one of its
// verbs: registered, would register, updated, would update, already registered.
type installReceipt struct {
	client mcpClient
	req    installRequest
	wd     string
	plan   installPlan
	line   string
	verb   string
}

// answer is the receipt as pairs: which client, the agent name it was
// registered under, the command line that did it, which scope that put it in,
// and what an agent registered this way can reach.
//
// The command line is part of the answer because it is the one thing rta did
// to a file it does not own, and the only record of which scope a client's
// own flag put it in. The scope is a line of its own because the default of
// the client's command is the current directory, which is not where somebody
// who ran this from their downloads folder will ever start the client.
func (r installReceipt) answer() view.KeyValue {
	c := r.client
	pairs := []view.Pair{
		{Key: r.verb, Value: c.label},
		{Key: "as", Value: r.req.as},
	}
	if r.line != "" {
		ran := "ran"
		if strings.HasPrefix(r.verb, "would") {
			ran = "would run"
		}
		pairs = append(pairs, view.Pair{Key: ran, Value: r.line})
	}
	if scope := r.scope(); scope != "" {
		pairs = append(pairs, view.Pair{Key: "scope", Value: scope})
	}
	if r.plan.replaces != nil {
		pairs = append(pairs, view.Pair{Key: "changed", Value: r.plan.changed})
	}
	if red := r.plan.redundant; red != nil {
		drop := "removed"
		if strings.HasPrefix(r.verb, "would") {
			drop = "would remove"
		}
		what := "the directory-only registration for " + r.wd
		if tail := optionTail(red.args); len(tail) > 0 && !slices.Equal(tail, optionTail(serveArgs(r.req.as, r.req.serve))) {
			what += " (started with " + orNone(tail) + ", which this one does not carry)"
		}
		pairs = append(pairs, view.Pair{Key: drop, Value: what + ", which would have overridden this one there"})
	}
	pairs = append(pairs, r.req.serve.pairs()...)
	next, reach := r.nextAndReach()
	return view.KeyValue{Pairs: append(pairs,
		view.Pair{Key: "next", Value: next},
		view.Pair{Key: "reach", Value: reach})}
}

// scope says where the registration is, or what the client's own command will
// choose when rta does not know.
func (r installReceipt) scope() string {
	c := r.client
	switch {
	case c.scope != nil:
		return c.scope(r.req.global, r.wd)
	case c.bin != "":
		return "whichever scope " + c.bin + "'s own command defaults to — `" + c.bin + " mcp list` shows where"
	}
	return ""
}

func (r installReceipt) nextAndReach() (next, reach string) {
	c, as := r.client, r.req.as
	switch {
	case strings.HasPrefix(r.verb, "would"):
		next = "run without --dry-run to register it"
	case r.verb == "already registered":
		next = "nothing to do — it is registered exactly as asked; a " + c.label +
			" session opened before it was needs a restart to see it"
	default:
		where := ""
		if c.name == "claude" && !r.req.global {
			where = " in this directory"
		}
		next = "restart " + c.label + where + ", ask it to call sys_overview, and `rta agent overview` shows it connected"
	}
	reach = "read-only until you say otherwise — `rta grant allow <capability> --agent " + as +
		"` lets one more thing through, and it expires on its own"
	if r.req.serve.consent {
		reach = "a call that needs a grant waits for your answer — `rta agent pending` lists it; " +
			"`rta grant allow <capability> --agent " + as + "` grants ahead of time, and it expires on its own"
	}
	return next, reach
}

// renderClientBlock answers with describeClient's pairs, the block among them
// in every format but pretty, where it is printed after them as it is.
//
// The block is copied, not read. Each of its lines is a TOML key and its
// value or a JSON member, and the key/value renderer wraps a value to the
// terminal's width as the prose it takes it for: a path to the binary longer
// than the room beside the key put `command =` on one line and its quoted
// value on the next, and split a JSON string across two, so a narrow
// terminal handed somebody a block neither format parses. After the pairs it
// is drawn at its natural width, through the same renderer so it is cleaned
// of what a terminal would act on as every value is, and a line wider than
// the screen is the terminal's to fold on the screen and keep whole in a
// copy. -o json and the rest carry it as the value it is, which is where a
// script provisioning a machine lifts it from.
func renderClientBlock(cmd *cobra.Command, opts *globalOpts, answer view.KeyValue) error {
	format, err := opts.format()
	if err != nil {
		return err
	}
	if format != cli.Pretty {
		return renderView(cmd, opts, answer)
	}
	var block string
	pairs := make([]view.Pair, 0, len(answer.Pairs))
	for _, p := range answer.Pairs {
		if p.Key == "block" {
			block = p.Value
			continue
		}
		pairs = append(pairs, p)
	}
	out := cmd.OutOrStdout()
	o := renderOptions(cmd, format, opts.noColor)
	if err := cli.Render(out, view.KeyValue{Pairs: pairs}, o); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	o.Width = 0
	return cli.Render(out, view.Text{Body: block}, o)
}

// describeClient is what to add and where, which is all rta does for a client
// that cannot configure itself — and what it answers for one that can, under
// --show, or when that client's own command is missing or failed. why says
// which of those it is, when it is a failure the operator did not ask for.
//
// The block is a value of its own rather than lines of prose around it, so a
// script provisioning a machine lifts it out of -o json whole, and a terminal
// lays its lines out as they were written rather than reflowing them as a
// sentence. "as" is the same pair the receipt carries, so whichever answer
// came back, the agent name is read from one place.
func describeClient(c mcpClient, self string, serve []string, as string, global bool,
	o serveOptions, why string,
) view.KeyValue {
	file := c.file
	if global && c.globalFile != "" {
		file = c.globalFile
	}
	pairs := []view.Pair{{Key: "client", Value: c.label}}
	if why != "" {
		pairs = append(pairs, view.Pair{Key: "why", Value: why})
	}
	pairs = append(pairs,
		view.Pair{Key: "add to", Value: file},
		view.Pair{Key: "block", Value: strings.TrimRight(c.block(self, serve), "\n")},
		view.Pair{Key: "as", Value: as},
	)
	pairs = append(pairs, o.pairs()...)
	if c.note != "" {
		pairs = append(pairs, view.Pair{Key: "note", Value: c.note})
	}
	return view.KeyValue{Pairs: append(pairs,
		view.Pair{Key: "next",
			Value: "add the block to that file yourself — rta writes nothing there. The `--as " + as +
				"` in it names this agent: grants are issued to that name, so one issued for another " +
				"client does not reach it, and `rta lock add " + as + "` freezes it"},
		view.Pair{Key: "then",
			Value: "restart " + c.label + ", ask it to call sys_overview, and `rta agent overview` shows it connected"})}
}
