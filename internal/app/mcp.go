package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	"github.com/this-is-tobi/rule-them-all/internal/pathguard"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/stdio"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// newMCPCommand wires the MCP surface: serve (stdio) and install helpers.
func newMCPCommand(reg *registry.Registry, version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "mcp",
		Short: "Expose capabilities to AI agents over the Model Context Protocol",
		RunE:  groupRunE,
	}
	root.AddCommand(newMCPServeCommand(reg, version))
	root.AddCommand(newMCPInstallCommand())
	return root
}

func newMCPServeCommand(reg *registry.Registry, version string) *cobra.Command {
	var (
		allowWrite       []string
		allowDestructive []string
		roots            []string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve capabilities as MCP tools on stdio",
		Long: "Serve every registered capability as an MCP tool on stdio.\n\n" +
			"Safety gate: only read capabilities are exposed by default.\n" +
			"Use --allow-write for write capabilities, and --allow-destructive\n" +
			"with explicit capability IDs for destructive ones.\n\n" +
			"Path gate: every path argument must be under a root, including a\n" +
			"capability's own declared default. The default root is the directory\n" +
			"the server was started in; widen it with --root, which is repeatable.\n" +
			"The gate governs path arguments only: a capability that opens a fixed\n" +
			"file of its own — `net hosts list` and /etc/hosts — is unaffected,\n" +
			"because that path is never an argument for anyone to send.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(roots) == 0 {
				// The directory the operator started the server in. It is what
				// an MCP client passes as cwd, so it is the project the agent
				// was pointed at — and choosing it by default means the gate
				// is on for everybody rather than for whoever read the flag.
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolving the default root: %w", err)
				}
				roots = []string{wd}
			}
			guard, err := pathguard.New(roots...)
			if err != nil {
				return err
			}
			// The operator's connections, for the tool schema. Loaded through
			// config.Load — the same file every other surface reads — so a
			// profile an agent may name is one the operator wrote. The schema
			// is sent once, so this is a snapshot; what a call actually
			// resolves through is Reload, below.
			//
			// A config that will not parse is not fatal here. It costs the
			// agent every profile, which is the fail-closed direction: the
			// server still serves the base connection, and `rta doctor` is
			// where the operator finds out why nothing else worked.
			profileCfg, cfgErr := config.Load()
			if cfgErr != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "rta: no profiles are available:", cfgErr)
				profileCfg = config.Config{}
			}
			opts := mcp.Options{
				AllowWrite:       allowWrite,
				AllowDestructive: allowDestructive,
				Origin:           reg.Origin,
				Config:           pluginConfig.For,
				Profiles:         profileCfg,
				// The schema above is a snapshot; what a call resolves through
				// is the file as it is now, so an environment the operator
				// edits takes effect without a restart — and the grant they
				// issue against it is compared to the same connection it will
				// reach. A read that fails costs the agent every profile,
				// which is the fail-closed direction and the same call the
				// snapshot above makes.
				Reload: func() config.Config {
					live, err := config.Load()
					if err != nil {
						return config.Config{}
					}
					return live
				},
				// The store, opened from this server's own environment and
				// never by prompting. Wired here and not inside internal/mcp so
				// that "this server may read the operator's store" is a line
				// somebody typed rather than a transitive import.
				Secrets:   kv.Reveal,
				Untrusted: untrustedNames(),
				Paths:     guard,
			}
			server := mcp.NewServer(reg, version, opts)
			// Logs must go to stderr: stdout is the protocol channel.
			fmt.Fprintln(cmd.ErrOrStderr(), "rta mcp server listening on stdio")
			// Said out loud rather than left to be discovered from a refusal:
			// an operator who needs a wider root should learn it here, not
			// from an agent reporting that a file it can see does not exist.
			fmt.Fprintf(cmd.ErrOrStderr(), "path arguments confined to: %s\n",
				strings.Join(guard.Roots(), ", "))
			// An allowlist entry that authorizes nothing is indistinguishable
			// from one the operator chose not to write: the capability is
			// simply absent, and the agent reports only that the tool does not
			// exist. Said here because the operator is present here, and is
			// the only one who can act on it.
			for _, p := range opts.Problems(reg) {
				fmt.Fprintln(cmd.ErrOrStderr(), "rta:", p)
			}
			// fd 0 here is the agent's request stream. main() has already
			// taken it away from anything this process launches — it had to,
			// since plugins are spawned during startup, long before this runs
			// — so what is left to do is ask for it back.
			err = server.Run(cmd.Context(), &sdk.IOTransport{
				Reader: stdio.Real(),
				Writer: stdio.Writer(cmd.OutOrStdout()),
			})
			// Client hang-up and ctrl-c are clean shutdowns, not failures.
			// The SDK does not expose a sentinel for the session-closing error
			// (it wraps EOF in a plain fmt error), hence the string match.
			if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
				strings.Contains(err.Error(), "server is closing") {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringSliceVar(&allowWrite, "allow-write", nil,
		"plugins whose write capabilities are exposed (repeatable, e.g. todo)")
	cmd.Flags().StringSliceVar(&allowDestructive, "allow-destructive", nil,
		"destructive capabilities to allow; external plugins must be pinned "+
			"to their digest (e.g. todo.rm, hello.wipe@5dae737f8845)")
	cmd.Flags().StringSliceVar(&roots, "root", nil,
		"directory a caller may name in a path argument (repeatable; default: the working directory)")
	// The two flags whose values nobody can be expected to type. A pinned
	// capability ID is a digest an operator would otherwise have to go and
	// look up, and a control that costs a lookup is one that gets left off.
	allowing := func(safety plugin.Safety) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return mcp.Options{Origin: reg.Origin}.AllowValues(reg, safety), cobra.ShellCompDirectiveNoFileComp
		}
	}
	_ = cmd.RegisterFlagCompletionFunc("allow-write", allowing(plugin.Write))
	_ = cmd.RegisterFlagCompletionFunc("allow-destructive", allowing(plugin.Destructive))
	// A root is a directory, and the shell has the list.
	_ = cmd.RegisterFlagCompletionFunc("root",
		func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveFilterDirs
		})
	return cmd
}

func newMCPInstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "install claude",
		Short: "Register rta as an MCP server in a client (claude)",
		Args:  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []cobra.Completion{
			cobra.CompletionWithDesc("claude", "Claude Code"),
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			self, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locating rta binary: %w", err)
			}
			claude, err := exec.LookPath("claude")
			if err != nil {
				// No claude CLI: print the manual config instead of failing.
				fmt.Fprintf(cmd.OutOrStdout(),
					"claude CLI not found. Add this to your .mcp.json:\n\n"+
						"{\n  \"mcpServers\": {\n    \"rta\": {\n      \"command\": %q,\n      \"args\": [\"mcp\", \"serve\"]\n    }\n  }\n}\n", self)
				return nil
			}
			install := exec.CommandContext(cmd.Context(), claude, "mcp", "add", "rta", "--", self, "mcp", "serve")
			install.Stdout = cmd.OutOrStdout()
			install.Stderr = cmd.ErrOrStderr()
			if err := install.Run(); err != nil {
				return fmt.Errorf("claude mcp add failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "✓ registered — try asking Claude Code about your system: “what's eating my CPU?”")
			return nil
		},
	}
}
