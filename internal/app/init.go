package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// newInitCommand implements `rta init`: an interactive wizard that writes
// the config file. Config stays optional — the wizard exists so nobody ever
// has to hand-write YAML to change a default.
func newInitCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:               "init",
		Annotations:       outputExempt(),
		Short:             "Create or update the rta config file interactively",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, out, release, err := initTerminal()
			if err != nil {
				return view.Errorf("core.init.terminal", "rta init is interactive and needs a terminal").
					WithHint("run it at one, or write the file by hand — `rta config schema` describes " +
						"every key, and `rta doctor` says where the file goes")
			}
			defer release()
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

			answers, err := askInit(cmd.Context(), reg, current, opts.dryRun, in, out)
			if err != nil {
				return initFormError(err)
			}
			// --dry-run still opens the form, since the answers are what a
			// preview is of, and then writes nothing. The flag never reached
			// this command, which wrote the file as though it were not there.
			if answers.confirmed && !opts.dryRun {
				// Folded into the file as it is *now*, not into the copy read
				// before the form opened. The wizard is interactive, so that
				// gap is measured in minutes rather than microseconds — the
				// longest read-to-write window of any writer here — and
				// anything else that touched the config meanwhile should
				// survive answers that say nothing about it.
				if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
					return initConfig(cfg, answers.output, answers.tiles), true
				}); err != nil {
					return err
				}
			}

			// init stays exempt from the check of a default output format,
			// because it is how that default gets rewritten: refusing it
			// over a broken output: key would leave the key nothing to fix
			// it with. So a default nothing renders draws this answer in
			// pretty, the one format left, as doctor draws its report.
			format, ferr := opts.format()
			if ferr != nil {
				format = cli.Pretty
			}
			return cli.Render(cmd.OutOrStdout(), initAnswer(config.Path(), answers, opts.dryRun),
				renderOptions(cmd, format, opts.noColor))
		},
	}
}

// initAnswers is what the wizard's form was answered with.
type initAnswers struct {
	output    string
	tiles     []string
	confirmed bool
}

// initTerminal is the terminal the wizard's form is drawn on and read from,
// and what to do once it is closed.
//
// Never stdout. The answer goes there, in the format -o asks for, and the
// form was gated on stdout being a terminal: `rta init -o json >
// answer.json` is a person at a terminal who wants the answer in a file, and
// was refused for it, so the one command whose questions only a person can
// answer could not hand its answer on. The form is drawn where huh draws
// it, on stderr, and reads stdin, when both are that terminal; when either
// is not — stdin a pipe, stderr a log file — it opens the controlling
// terminal itself, which is where the person is whatever the streams point
// at. Only with no terminal at all is there nobody to ask.
//
// A variable, as isTTY is, so a test can say whether there is one.
var initTerminal = func() (io.Reader, io.Writer, func(), error) {
	return formTerminal(stdio.Real(), os.Stderr, func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }, tea.OpenTTY)
}

// formTerminal is initTerminal's choice, with what it asks of the machine
// passed in: the streams, whether a file is a terminal, and how to open the
// controlling one.
func formTerminal(in, out *os.File, isTerminal func(*os.File) bool,
	openTTY func() (*os.File, *os.File, error),
) (io.Reader, io.Writer, func(), error) {
	if isTerminal(in) && isTerminal(out) {
		return in, out, func() {}, nil
	}
	ttyIn, ttyOut, err := openTTY()
	if err != nil {
		return nil, nil, nil, err
	}
	return ttyIn, ttyOut, func() {
		_ = ttyIn.Close()
		if ttyOut != ttyIn {
			_ = ttyOut.Close()
		}
	}, nil
}

// askInit runs the wizard's form on the terminal initTerminal found, seeded
// from the file as it stands.
//
// A variable, as isTTY is, so a test can stand in for the person: the form
// needs one at a terminal, and what init answers once it has been filled in
// can only be tested if a test can say how it was filled in.
var askInit = func(ctx context.Context, reg *registry.Registry, current config.Config, dryRun bool,
	in io.Reader, out io.Writer,
) (initAnswers, error) {
	a := initAnswers{output: current.Output, confirmed: true,
		// An empty selection means "leave the dashboard automatic": one
		// tile per plugin, including plugins installed later. Only someone
		// who actively wants a fixed set should get one.
		tiles: tileIDs(current.Dashboard.Tiles)}
	if a.output == "" {
		a.output = "pretty"
	}
	confirm := huh.NewConfirm().
		Title(fmt.Sprintf("Write %s?", config.Path())).
		Affirmative("write").Negative("cancel").
		Value(&a.confirmed)
	if dryRun {
		// The last question says what pressing it does. A button reading
		// "write" under --dry-run is the flag being ignored again, on the
		// screen this time, however faithfully the file is left alone.
		confirm = confirm.Title(fmt.Sprintf("Preview writing %s?", config.Path())).
			Description("--dry-run: nothing is written; the answer says what would be").
			Affirmative("preview")
	}
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
				Value(&a.output),
			huh.NewMultiSelect[string]().
				Title("Dashboard tiles").
				Description("Leave empty for the automatic dashboard: one tile per plugin.\n"+
					"Choosing here fixes the set instead — new plugins will not appear.").
				Options(tileOptions(reg, a.tiles)...).
				Value(&a.tiles),
			confirm,
		),
	).WithInput(in).WithOutput(out)
	err := form.RunWithContext(ctx)
	return a, err
}

// initAnswer is what init answers once the form is closed: the file, the two
// things the wizard decides in it, and what to do next — or, cancelled, that
// the file is as it was.
//
// Pairs rather than the one line of prose it was, which went to stdout
// whatever -o said: the form is interactive and stays so, but its outcome is
// a result like any other command's, in the format asked for. Cancelling is
// "unchanged", in the words profile set and dashboard hide answer a write that
// changed nothing with, and exits 0 as it always has: nothing failed.
func initAnswer(path string, a initAnswers, dryRun bool) view.KeyValue {
	if !a.confirmed {
		return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
			Value: "the wizard was cancelled — nothing written to " + path}}}
	}
	dashboard := "automatic — one tile per plugin, plugins installed later included"
	if len(a.tiles) > 0 {
		dashboard = format.Count(len(a.tiles), "tile", "tiles") + ", a fixed set: " +
			strings.Join(a.tiles, ", ")
	}
	label, next := "wrote", "run `rta` to see your dashboard"
	if dryRun {
		label, next = "would write", "run without --dry-run to write it"
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: label, Value: path},
		{Key: "output", Value: a.output},
		{Key: "dashboard", Value: dashboard},
		{Key: "next", Value: next},
	}}
}

// initFormError codes what ended the wizard before it wrote anything. huh's
// own error for a person pressing ctrl-c is the bare "user aborted", which
// reached the terminal as a box nothing coded; leaving is a thing a person
// did, and what they need back is that the file is as it was.
func initFormError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return view.Errorf("core.init.aborted", "the wizard was closed before it wrote anything").
			WithHint("the config file is as it was")
	}
	return view.AsError(err, "core.init.form").
		WithHint("the wizard could not run on this terminal; nothing was written")
}

// initConfig folds the wizard's answers into the config as it stands on disk.
//
// Into it, not over it. This built a fresh config.Config and wrote that, so
// `rta init` — "Create or update the rta config file interactively" — deleted
// the entire `plugins:` block every time it ran. That block was added
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

// hasRequiredInputs counts a Piped input as required, as the dashboard does
// (tui.MissingInputs): the CLI reads it from a pipe when it is left out, and
// a tile runs where there is none. Offered anyway, codec.jwt and debug.ansi
// became tiles answering "nothing to read" on every refresh.
//
// A Required input's Default is no exception to look for: Validate refuses
// every one beside Required, an empty one included, so a required input is
// one a tile would run without.
func hasRequiredInputs(c plugin.Capability) bool {
	for _, f := range c.Inputs {
		if f.Piped || f.Required {
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
