package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/view"
)

// renderFn is the closure each command group builds over its global options:
// render this view, or render this refusal to stderr and fail.
//
// Named because the group is no longer one function — `set` and `rm` live in
// profileset.go — and passing it is what keeps `--output` meaning the same
// thing on every subcommand.
type renderFn func(*cobra.Command, view.View, *view.Error) error

// viewRender is the renderFn a command group renders with. The profile,
// dashboard, policy and config groups each wrote it out, and the config one
// had gained a rule the others had not; a rule added to how a group renders
// now reaches every group, and a test keeps a fifth copy from being written
// (TestTheRenderOrRefuseClosureIsWrittenOnce).
//
// prettyOnBadFormat draws in pretty when the default output format names
// nothing, rather than refuse: the config commands', which are exempt from a
// broken default since fixing one is what they are for, as doctor does.
func viewRender(opts *globalOpts, prettyOnBadFormat bool) renderFn {
	return func(cmd *cobra.Command, v view.View, verr *view.Error) error {
		format, err := opts.format()
		if err != nil {
			if !prettyOnBadFormat {
				return err
			}
			format = cli.Pretty
		}
		renderOpts := renderOptions(cmd, format, opts.noColor)
		if verr != nil {
			_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
			return Rendered(verr)
		}
		return cli.Render(cmd.OutOrStdout(), v, renderOpts)
	}
}

// renderOptions is what a command draws its result with. Every render path
// in this package builds its options here, and a test holds it to that
// (TestRenderOptionsAreBuiltInOnePlace).
//
// They were spelled out at each path, and the copies drifted. Screen, what an
// empty result's sentence waits for (cli.Options.Screen), reached five paths
// and not the other nine, so any of those nine whose view came to carry one
// would have drawn headings over nothing on a terminal. Notes, where csv
// says a table is one page of more or could not read part of what it lists,
// reached only the capability runner, so every other command's csv kept
// both to itself.
//
// Colour, width and Screen follow stdout, the stream the result is written
// to: a terminal gets output shaped for a person, a pipe or a file bytes that
// do not depend on who ran the command. A nil cmd — a failure reported before
// a command was found — has no stderr of its own to put a note on.
//
// The characters follow the locale instead (cli.ASCIIOnly), whichever stream
// it is: LC_ALL=C says the session cannot show a box, and a CI log captured
// under it cannot either.
func renderOptions(cmd *cobra.Command, format cli.Format, noColor bool) cli.Options {
	screen := isTTY()
	o := cli.Options{Format: format, NoColor: noColor || !screen, Width: termWidth(), Screen: screen,
		ASCII: cli.ASCIIOnly(os.Getenv)}
	if cmd != nil {
		o.Notes = cmd.ErrOrStderr()
	}
	return o
}

// invalidOutputDefault names where the default came from, in the words
// somebody would search for it by: the variable, or the key and the file it is
// in — a drop-in may state it, and naming config.yaml then sent the reader to
// a file without the key. The precedence is config.Load's — the environment
// over the file.
func invalidOutputDefault(value string) *view.Error {
	if os.Getenv("RTA_OUTPUT") != "" {
		return &view.Error{Code: CodeOutputInvalid,
			Message: fmt.Sprintf("RTA_OUTPUT is %q, which is not an output format", value),
			Hint: "RTA_OUTPUT takes " + formatNames() +
				" — unset it for pretty, or pass -o for one command"}
	}
	return &view.Error{Code: CodeOutputInvalid,
		Message: fmt.Sprintf("output: %q in %s is not an output format", value, config.Where("output", "")),
		Hint: "output: takes " + formatNames() +
			" — remove the key for pretty, or pass -o for one command"}
}

// formatNames is cli.Formats without the descriptions, as a sentence, so a
// refusal lists the formats that are accepted rather than a copy of them.
func formatNames() string {
	all := cli.Formats()
	names := make([]string, len(all))
	for i, f := range all {
		names[i], _, _ = strings.Cut(f, "\t")
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// renderView is viewRender's success half, for a command with no refusal of
// its own to draw.
func renderView(cmd *cobra.Command, opts *globalOpts, v view.View) error {
	format, err := opts.format()
	if err != nil {
		return err
	}
	return cli.Render(cmd.OutOrStdout(), v,
		renderOptions(cmd, format, opts.noColor))
}
