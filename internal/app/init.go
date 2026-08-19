package app

import (
	"fmt"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// newInitCommand implements `rta init`: an interactive wizard that writes
// the config file. Config stays optional — the wizard exists so nobody ever
// has to hand-write YAML to change a default (PROJECT.md §5.1).
func newInitCommand(reg *registry.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create or update the rta config file interactively",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY() {
				return fmt.Errorf("rta init is interactive and needs a terminal")
			}
			current, err := config.Load()
			if err != nil {
				// A broken file should not block re-initializing it.
				fmt.Fprintln(cmd.ErrOrStderr(), "warning:", err)
				current = config.Config{}
			}

			output := current.Output
			if output == "" {
				output = "pretty"
			}
			// An empty selection means "leave the dashboard automatic": one
			// tile per plugin, including plugins installed later. Only
			// someone who actively wants a fixed set should get one.
			selectedTiles := tileIDs(current.Dashboard.Tiles)
			confirmed := true

			form := huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Default output format").
						Description("Used when --output is not given").
						Options(
							huh.NewOption("pretty (human)", "pretty"),
							huh.NewOption("json", "json"),
							huh.NewOption("yaml", "yaml"),
						).
						Value(&output),
					huh.NewMultiSelect[string]().
						Title("Dashboard tiles").
						Description("Leave empty for the automatic dashboard: one tile per plugin.\n"+
							"Choosing here fixes the set instead — new plugins will not appear.").
						Options(tileOptions(reg, selectedTiles)...).
						Value(&selectedTiles),
					huh.NewConfirm().
						Title(fmt.Sprintf("Write %s?", config.Path())).
						Affirmative("write").Negative("cancel").
						Value(&confirmed),
				),
			)
			if err := form.RunWithContext(cmd.Context()); err != nil {
				return err
			}
			if !confirmed {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing written")
				return nil
			}

			var cfg config.Config
			if output != "pretty" {
				cfg.Output = output
			}
			if len(selectedTiles) > 0 {
				cfg.Dashboard.Tiles = tilesFor(selectedTiles)
			} else {
				// Automatic dashboard: carry over any arrangement made from
				// inside the app, which is where hiding and reordering live.
				cfg.Dashboard.Hidden = current.Dashboard.Hidden
				cfg.Dashboard.Order = current.Dashboard.Order
			}
			if err := config.Write(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ wrote %s — run `rta` to see your dashboard\n", config.Path())
			return nil
		},
	}
}

// tileOptions lists dashboard-eligible capabilities: read-only, no required
// inputs — the same rule the dashboard itself enforces.
func tileOptions(reg *registry.Registry, selected []string) []huh.Option[string] {
	chosen := map[string]bool{}
	for _, id := range selected {
		chosen[id] = true
	}
	var opts []huh.Option[string]
	for _, c := range reg.Capabilities() {
		if c.Safety != plugin.Read || hasRequiredInputs(c) {
			continue
		}
		opts = append(opts, huh.NewOption(c.ID+" — "+c.Summary, c.ID).Selected(chosen[c.ID]))
	}
	return opts
}

func hasRequiredInputs(c plugin.Capability) bool {
	for _, f := range c.Inputs {
		if f.Required && f.Default == nil {
			return true
		}
	}
	return false
}

func tileIDs(tiles []config.Tile) []string {
	ids := make([]string, 0, len(tiles))
	for _, t := range tiles {
		ids = append(ids, t.ID)
	}
	return ids
}

// tilesFor rebuilds tile configs, keeping the useful default inputs.
func tilesFor(ids []string) []config.Tile {
	tiles := make([]config.Tile, 0, len(ids))
	for _, id := range ids {
		t := config.Tile{ID: id}
		if id == "sys.cpu" {
			t.With = map[string]any{"cores": true}
		}
		tiles = append(tiles, t)
	}
	return tiles
}
