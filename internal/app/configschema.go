package app

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
)

// newConfigCommand groups the commands about the config file itself, as
// opposed to what the file configures.
func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "The config file itself",
		RunE:  groupRunE,
	}
	cmd.AddCommand(configSchemaCommand())
	return cmd
}

// configSchemaCommand implements `rta config schema`.
func configSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the config file's JSON Schema, for editor completion",
		Long: "Prints a JSON Schema describing every key the config file may carry, with the\n" +
			"explanation an editor shows on hover. Redirect it into a schema.json next to\n" +
			"the config file — `rta doctor` prints where that is — \n\n" +
			"  rta config schema > schema.json\n\n" +
			"then put this modeline at the top of the config file:\n\n" +
			"  # yaml-language-server: $schema=schema.json\n\n" +
			"VS Code's YAML extension (redhat.vscode-yaml) and every other editor speaking\n" +
			"yaml-language-server read that line and complete, validate and explain each\n" +
			"field in place. The schema states the envelope: what a plugins: section or a\n" +
			"set: overlay may hold inside is each plugin's own declaration, so `rta explain`\n" +
			"and `rta doctor` remain the deep validators.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Raw JSON straight to stdout, deliberately outside the render
			// pipeline: the output already is a machine format, and wrapping
			// it in an --output envelope would break the one thing the
			// command exists for — redirecting into a file an editor reads.
			out, err := json.MarshalIndent(config.Schema(), "", "  ")
			if err != nil {
				return err
			}
			out = append(out, '\n')
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
}
