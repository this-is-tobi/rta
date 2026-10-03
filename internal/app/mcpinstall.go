package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/render/cli"
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

// mcpClient is one client rta knows how to register with, or failing that,
// how to describe.
type mcpClient struct {
	// name is what the operator types, and — because they typed it — the
	// agent name the server is registered under. See asName.
	name  string
	label string
	// bin and args are the client's own configuration command. An empty bin
	// means rta has no verified way to register with this client and will
	// only ever show what to add.
	bin  string
	args func(self, as string) []string
	// globalArgs is args, but with the client's own flag for registering at
	// the user level rather than wherever rta happens to be run from — nil
	// for a client with no verified global-scope command. --global refuses
	// outright rather than guessing one, the same caution the "verified"
	// column above already states: a wrong flag here is a wrong command run
	// against a file that grants an agent access to secrets.
	globalArgs func(self, as string) []string
	// alwaysGlobal marks a client whose ordinary command already writes to a
	// single, user-level location: --global changes nothing about what
	// runs, only that it is accepted rather than refused as unsupported.
	alwaysGlobal bool
	// file is where this client keeps its MCP configuration, and block is
	// what to put in it. Both are used when there is no command, and when
	// there is one but it is not installed. globalFile is what --global
	// prints instead, "" when file already names the one relevant path.
	file, globalFile string
	block            func(self, as string) string
	// note is anything the operator needs beyond the block itself.
	note string
}

// serveArgs is the argv every client ends up launching. `--as` is not
// decoration: without it every MCP client on the machine is one principal, so
// a grant issued while talking to one authorizes all the others. The operator
// typed the client's name, so the name is theirs.
func serveArgs(as string) []string { return []string{"mcp", "serve", "--as", as} }

// stdioServer is one entry, as a struct rather than a map so the fields keep
// the order somebody reads them in: a map marshals its keys alphabetically,
// which puts "args" above the "command" they are arguments to.
type stdioServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// jsonBlock renders the mcpServers shape, which Claude Desktop established
// and Cursor and Gemini both adopted.
func jsonBlock(key, self, as string) string {
	body, _ := json.MarshalIndent(map[string]map[string]stdioServer{
		key: {"rta": {Command: self, Args: serveArgs(as)}},
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
			args: func(self, as string) []string {
				return append([]string{"mcp", "add", "rta", "--", self}, serveArgs(as)...)
			},
			// --scope user is claude's own flag, already documented in
			// docs/30-boundary/60-ai-clients.md and confirmed there against
			// the real CLI — the one client this file can say that about
			// rather than only claim it.
			globalArgs: func(self, as string) []string {
				return append([]string{"mcp", "add", "rta", "--scope", "user", "--", self}, serveArgs(as)...)
			},
			file:  ".mcp.json (or ~/.claude.json)",
			block: func(self, as string) string { return jsonBlock("mcpServers", self, as) },
		},
		{
			name: "vscode", label: "VS Code",
			// Verified against code 2026-08-29 by running it against a
			// throwaway --user-data-dir: it takes {"name","command","args"}
			// and writes servers.<name>, which is VS Code's own key and not
			// the mcpServers everything else uses.
			bin: "code",
			args: func(self, as string) []string {
				spec, _ := json.Marshal(struct {
					Name string `json:"name"`
					stdioServer
				}{Name: "rta", stdioServer: stdioServer{Command: self, Args: serveArgs(as)}})
				return []string{"--add-mcp", string(spec)}
			},
			// VS Code's own CLI exposes no project-scoped variant — every
			// run already lands in the user config --global would ask for.
			alwaysGlobal: true,
			file:         "VS Code's user mcp.json",
			block:        func(self, as string) string { return jsonBlock("servers", self, as) },
		},
		{
			name: "codex", label: "OpenAI Codex CLI",
			// Declared but NOT verified here — codex is not installed on the
			// machine this was written on. If the command is missing or fails,
			// the operator gets the block below instead, which is the whole
			// reason the fallback exists rather than an error.
			bin: "codex",
			args: func(self, as string) []string {
				return append([]string{"mcp", "add", "rta", "--", self}, serveArgs(as)...)
			},
			file: "~/.codex/config.toml",
			// TOML, alone among these. Rendered by hand because it is four
			// lines and pulling in an encoder to write four lines is worse.
			block: func(self, as string) string {
				quoted := make([]string, 0, 4)
				for _, a := range serveArgs(as) {
					quoted = append(quoted, strconv.Quote(a))
				}
				return fmt.Sprintf("[mcp_servers.rta]\ncommand = %s\nargs = [%s]\n",
					strconv.Quote(self), strings.Join(quoted, ", "))
			},
		},
		{
			name: "gemini", label: "Gemini CLI",
			// Declared but not verified here, as codex above.
			bin: "gemini",
			args: func(self, as string) []string {
				return append([]string{"mcp", "add", "rta", self}, serveArgs(as)...)
			},
			file:  "~/.gemini/settings.json",
			block: func(self, as string) string { return jsonBlock("mcpServers", self, as) },
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
			block:      func(self, as string) string { return jsonBlock("mcpServers", self, as) },
		},
		{
			name: "copilot", label: "GitHub Copilot CLI",
			// No command either — copilot manages MCP from an interactive
			// /mcp prompt, so there is nothing for rta to run.
			file:  "~/.copilot/mcp-config.json",
			block: func(self, as string) string { return jsonBlock("mcpServers", self, as) },
			note:  "Copilot can also do this from its own prompt: run `copilot`, then `/mcp`.",
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

func newMCPInstallCommand(opts *globalOpts) *cobra.Command {
	var as string
	var show, global bool

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
			"secrets, and it is worth reading before it changes.",
		// ExactArgs alone. OnlyValidArgs refused a client it did not know in
		// cobra's words — `invalid argument "nope" for "rta mcp install"` — one
		// step ahead of the check in RunE that names every client rta does
		// know, which therefore never ran. ValidArgs stays for completion.
		Args:      cobra.ExactArgs(1),
		ValidArgs: valid,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ok := findClient(args[0])
			if !ok {
				return usageError(cmd, fmt.Errorf("unknown client %q — try one of: %s",
					args[0], strings.Join(names, ", ")))
			}
			self, err := os.Executable()
			if err != nil {
				return view.Errorf("core.mcp.install.self", "locating the rta binary: %v", err).
					WithHint("the client is registered with this binary's path, so it has to be " +
						"one rta can name; `rta mcp install " + client.name + " --show` prints the block to fill in")
			}
			// Resolved, because a client launches this path months from now
			// and a symlink into a build tree is the kind of thing that stops
			// being true quietly.
			if resolved, err := filepath.EvalSymlinks(self); err == nil {
				self = resolved
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

			if !show && client.bin != "" {
				if bin, err := exec.LookPath(client.bin); err == nil {
					// Resolved here, inside the one branch that runs the
					// client's own command, because that is the only thing
					// --global can be refused *about*. Asked with --show, or
					// for a client that is not installed, nothing is going to
					// run and the operator is getting the block to paste
					// either way — refusing there sent them to `--show` in a
					// message they had already passed `--show` to read.
					// What --global steers on that path is the block's own
					// path, which describeClient below reads.
					argsFn := client.args
					if global {
						switch {
						case client.globalArgs != nil:
							argsFn = client.globalArgs
						case client.alwaysGlobal:
							// Already what --global asked for: VS Code's own
							// command writes to its single user-level file.
						default:
							// codex and gemini's own base commands are
							// declared but not verified against the real CLI;
							// guessing a scope flag on top of that is a wrong
							// command run against a file that grants an agent
							// access to secrets, not a smaller version of the
							// right one.
							//
							// core.usage: this command line cannot work as
							// typed, and the fix is on it.
							return &view.Error{Code: CodeUsage,
								Message: fmt.Sprintf("rta does not know %s's flag for installing at the user level",
									client.label),
								Hint: fmt.Sprintf("try `rta mcp install %s --show` and add it yourself, "+
									"or check %s's own --help", client.name, client.bin)}
						}
					}
					line := bin + " " + strings.Join(argsFn(self, name), " ")
					if opts.dryRun {
						return renderView(cmd, opts, registeredAnswer(client, name, line, true))
					}
					run := exec.CommandContext(cmd.Context(), bin, argsFn(self, name)...)
					// The client's own words go to stderr, both streams of
					// them. Stdout is rta's answer, in the format -o asks
					// for, and `claude mcp add` printing its own sentence
					// into it put prose ahead of the json a script had asked
					// for. A person at a terminal still reads both.
					var clientSaid bytes.Buffer
					// One writer for both streams, the same value: exec copies
					// them on a single goroutine only then, and two writing into
					// the same destination at once is a race.
					said := io.MultiWriter(cmd.ErrOrStderr(), &clientSaid)
					run.Stdout, run.Stderr = said, said
					if err := run.Run(); err != nil {
						// A refusal because rta is already registered is not a
						// command that moved on, and the block it was answered
						// with is the one thing to add twice: the server is
						// there, and what is left to decide is whether the path
						// or the name in it is the one wanted.
						if strings.Contains(strings.ToLower(clientSaid.String()), "already exists") {
							return renderView(cmd, opts, view.KeyValue{Pairs: []view.Pair{
								{Key: "already registered", Value: client.label},
								{Key: "next", Value: "it keeps the path and the name it was registered with — to change either, " +
									"take it out with " + client.bin + "'s own `mcp remove`, then run this again"},
							}})
						}
						// Not fatal. A client whose command moved on is
						// exactly when somebody needs the block instead, and
						// failing here would leave them with nothing.
						fmt.Fprintf(cmd.ErrOrStderr(),
							"rta: %s could not register it (%v) — here is what to add instead\n",
							client.bin, err)
						return renderClientBlock(cmd, opts, describeClient(client, self, name, global))
					}
					return renderView(cmd, opts, registeredAnswer(client, name, line, false))
				}
			}
			return renderClientBlock(cmd, opts, describeClient(client, self, name, global))
		},
	}
	cmd.Flags().StringVar(&as, "as", "",
		"name to register this agent under (default: the client's name)")
	cmd.Flags().BoolVar(&show, "show", false,
		"print what to add without running the client's own command")
	cmd.Flags().BoolVar(&global, "global", false,
		"install for every project instead of just this one, where the client supports it")
	return cmd
}

// registeredAnswer is what mcp install answers when the client's own command
// registered rta, or would have under --dry-run: which client, the agent name
// it was registered under, the command line that did it, and what an agent
// registered this way can reach.
//
// The command line is part of the answer because it is the one thing rta did
// to a file it does not own, and the only record of which scope a client's
// own flag put it in.
func registeredAnswer(c mcpClient, as, line string, dryRun bool) view.KeyValue {
	registered, ran := "registered", "ran"
	next := "agents start read-only — `rta grant allow <capability> --agent " + as +
		"` is how anything else gets through, and it expires on its own"
	if dryRun {
		registered, ran = "would register", "would run"
		next = "run without --dry-run to register it"
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: registered, Value: c.label},
		{Key: "as", Value: as},
		{Key: ran, Value: line},
		{Key: "next", Value: next},
	}}
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
// --show, or when that client's own command is missing or failed.
//
// The block is a value of its own rather than lines of prose around it, so a
// script provisioning a machine lifts it out of -o json whole, and a terminal
// lays its lines out as they were written rather than reflowing them as a
// sentence. "as" is the same pair registeredAnswer carries, so whichever
// answer came back, the agent name is read from one place.
func describeClient(c mcpClient, self, as string, global bool) view.KeyValue {
	file := c.file
	if global && c.globalFile != "" {
		file = c.globalFile
	}
	pairs := []view.Pair{
		{Key: "client", Value: c.label},
		{Key: "add to", Value: file},
		{Key: "block", Value: strings.TrimRight(c.block(self, as), "\n")},
		{Key: "as", Value: as},
	}
	if c.note != "" {
		pairs = append(pairs, view.Pair{Key: "note", Value: c.note})
	}
	return view.KeyValue{Pairs: append(pairs, view.Pair{Key: "next",
		Value: "add the block to that file yourself — rta writes nothing there. The `--as " + as +
			"` in it names this agent: grants are issued to that name, so one issued for another " +
			"client does not reach it, and `rta lock add " + as + "` freezes it"})}
}
