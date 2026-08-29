package app

import (
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

	"github.com/this-is-tobi/rule-them-all/internal/grant"
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
	// file is where this client keeps its MCP configuration, and block is
	// what to put in it. Both are used when there is no command, and when
	// there is one but it is not installed.
	file  string
	block func(self, as string) string
	// note is anything the operator needs beyond the block itself.
	note string
}

// serveArgs is the argv every client ends up launching. `--as` is not
// decoration: without it every MCP client on the machine is one principal, so
// a grant issued while talking to one authorizes all the others
// decision 1). The operator typed the client's name, so the name is theirs.
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
			file:  "VS Code's user mcp.json",
			block: func(self, as string) string { return jsonBlock("servers", self, as) },
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
			file:  "~/.cursor/mcp.json (or .cursor/mcp.json for one project)",
			block: func(self, as string) string { return jsonBlock("mcpServers", self, as) },
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

func newMCPInstallCommand() *cobra.Command {
	var as string
	var show bool

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
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: valid,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ok := findClient(args[0])
			if !ok {
				return fmt.Errorf("unknown client %q — try one of: %s",
					args[0], strings.Join(names, ", "))
			}
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating rta binary: %w", err)
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

			out := cmd.OutOrStdout()
			if !show && client.bin != "" {
				if bin, err := exec.LookPath(client.bin); err == nil {
					run := exec.CommandContext(cmd.Context(), bin, client.args(self, name)...)
					run.Stdout, run.Stderr = out, cmd.ErrOrStderr()
					if err := run.Run(); err != nil {
						// Not fatal. A client whose command moved on is
						// exactly when somebody needs the block instead, and
						// failing here would leave them with nothing.
						fmt.Fprintf(cmd.ErrOrStderr(),
							"rta: %s could not register it (%v) — here is what to add instead\n\n",
							client.bin, err)
						describeClient(out, client, self, name)
						return nil
					}
					fmt.Fprintf(out, "✓ registered with %s as %q\n", client.label, name)
					fmt.Fprintf(out, "  Agents start read-only. `rta grant allow <capability> --agent %s`\n"+
						"  is how anything else gets through, and it expires on its own.\n", name)
					return nil
				}
			}
			describeClient(out, client, self, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&as, "as", "",
		"name to register this agent under (default: the client's name)")
	cmd.Flags().BoolVar(&show, "show", false,
		"print what to add without running the client's own command")
	return cmd
}

// describeClient prints what to add and where, which is all rta does for a
// client that cannot configure itself.
func describeClient(out io.Writer, c mcpClient, self, as string) {
	fmt.Fprintf(out, "Add this to %s:\n\n%s\n", c.file, c.block(self, as))
	if c.note != "" {
		fmt.Fprintf(out, "\n%s\n", c.note)
	}
	fmt.Fprintf(out, "\nThe `--as %s` is what keeps this agent's grants its own: without a name,\n"+
		"every MCP client on this machine shares one set of permissions.\n", as)
}
