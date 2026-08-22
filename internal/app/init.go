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
			// LoadFile, not Load. config.LoadFile says why in as many
			// words — "anything that reads the config in order to write it
			// back must start here: Load would fold this session's RTA_* into
			// the value, and saving that would bake one shell's environment
			// into the file for every future run" — and this was the one
			// writer that did not. `RTA_OUTPUT=json rta init` offered json as
			// the current setting and wrote it, permanently, from a variable
			// the operator had exported for one command.
			current, err := config.LoadFile()
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

			if err := config.Write(initConfig(current, output, selectedTiles)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ wrote %s — run `rta` to see your dashboard\n", config.Path())
			return nil
		},
	}
}

// initConfig folds the wizard's answers into the config as it stands on disk.
//
// Into it, not over it. This built a fresh config.Config and wrote that, so
// `rta init` — "Create or update the rta config file interactively" — deleted
// the entire `plugins:` block every time it ran. ADR 0016 added that block
// and nothing brought this along, which is the failure mode of writing back a
// value assembled from scratch: it is not that somebody made a mistake, it is
// that the next field added to config.Config is dropped too, silently, by a
// command whose own summary promises the opposite.
//
// internal/render/tui/arrange.go had it right from the start and says why:
// "the dashboard is one part of the file, and moving a tile must not rewrite
// anything else". Two writers, one discipline, held in one of them.
//
// What this owns is Output and Dashboard. Everything else is carried, and
// TestInitKeepsEveryPartOfTheFileItDoesNotOwn refuses to compile past a field
// nobody has classified.
func initConfig(current config.Config, output string, tiles []string) config.Config {
	next := current
	// "pretty" is the default, so writing it would pin a value that is
	// already what happens when the key is absent.
	next.Output = ""
	if output != "pretty" {
		next.Output = output
	}
	if len(tiles) > 0 {
		next.Dashboard.Tiles = tilesFor(tiles)
	} else {
		// Automatic dashboard: drop any fixed set, and keep the arrangement
		// made from inside the app, which is where hiding and reordering live.
		next.Dashboard.Tiles = nil
	}
	return next
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
