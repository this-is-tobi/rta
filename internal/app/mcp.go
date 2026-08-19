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

	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
)

// newMCPCommand wires the MCP surface: serve (stdio) and install helpers.
func newMCPCommand(reg *registry.Registry, version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "mcp",
		Short: "Expose capabilities to AI agents over the Model Context Protocol",
	}
	root.AddCommand(newMCPServeCommand(reg, version))
	root.AddCommand(newMCPInstallCommand())
	return root
}

func newMCPServeCommand(reg *registry.Registry, version string) *cobra.Command {
	var (
		allowWrite       bool
		allowDestructive []string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve capabilities as MCP tools on stdio",
		Long: "Serve every registered capability as an MCP tool on stdio.\n\n" +
			"Safety gate: only read capabilities are exposed by default.\n" +
			"Use --allow-write for write capabilities, and --allow-destructive\n" +
			"with explicit capability IDs for destructive ones.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := mcp.NewServer(reg, version, mcp.Options{
				AllowWrite:       allowWrite,
				AllowDestructive: allowDestructive,
			})
			// Logs must go to stderr: stdout is the protocol channel.
			fmt.Fprintln(cmd.ErrOrStderr(), "rta mcp server listening on stdio")
			err := server.Run(cmd.Context(), &sdk.StdioTransport{})
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
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "expose write capabilities")
	cmd.Flags().StringSliceVar(&allowDestructive, "allow-destructive", nil,
		"capability IDs allowed despite being destructive (e.g. pg.table.drop)")
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
