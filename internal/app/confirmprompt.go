package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The question a destructive command asks a person at a terminal.
//
// On this surface it used to be a refusal: `rta note rm 6` exited 3 with
// "re-run with --yes", and did not say what it would have removed, so the
// person typed the command again with a flag to confirm something they had
// not been shown. Worse, the refusal came before anything was looked up, so
// `rta note rm 99` asked for confirmation of a note that was not there and
// said so only on the second try.
//
// What the old rule protected was the script: a command that waits on a
// question nobody is there to answer hangs a pipeline, and one that answers
// itself is no confirmation. That holds, and is kept — the question is asked
// only when standard input and standard error are both a terminal, which is
// when a person is typing; anywhere else, in a script, a pipe, cron, an
// agent's shell, nothing is asked and the command exits 3 without --yes as it
// always did, which is a question and not a failure. Nothing here makes a
// confirmation easier for anything that is not at a keyboard, and nothing
// gives an agent a way it lacked: with a shell it can pass --yes already.
//
// The order is the point. The target is found first — a capability that cannot
// find what it was asked to act on says so and asks nothing — then shown as the
// capability's own dry run states it ("would remove note 6: past"), the same
// line --dry-run prints and the TUI shows before enter, and only then asked.
// A confirmation of an intention is a formality; of an outcome it is a decision.
//
// A plugin from outside this binary is shown what it will be run with and not
// what it says it would do: its dry run is its own claim, and a claim that
// lies is the one thing that must not run before anyone has said yes. The TUI
// holds the same line (internal/render/tui/confirm.go).

// confirmTerminal says whether a person is there to ask. A variable, as isTTY
// is, so a test can say whether there is one.
var confirmTerminal = func() bool {
	return term.IsTerminal(int(stdio.Real().Fd())) && stderrIsTerminal()
}

// askLine writes prompt where a person will see it and reads the line they
// answer with, from the real standard input — never os.Stdin, which is
// /dev/null once main has claimed it (internal/stdio). The prompt goes to
// standard error, so a result piped onward is not preceded by a question. An
// end of input is an error, which the caller reads as no.
//
// A variable, so a test can answer without a terminal.
var askLine = func(prompt string) (string, error) {
	defer shutdown.Prompting()()
	fmt.Fprint(os.Stderr, prompt)
	return bufio.NewReader(stdio.Real()).ReadString('\n')
}

// declinedConfirmation is what a person who answered no, or nothing, gets: the
// same exit as a script that did not say --yes, since nothing ran either way,
// and a line saying so — an empty answer is easily taken for a command that
// hung.
func declinedConfirmation(w io.Writer) error {
	fmt.Fprintln(w, "Not confirmed — nothing was changed.")
	return Rendered(&view.Error{Code: CodeConfirmRequired, Message: "not confirmed"})
}

// askToProceed asks the question and reports whether the answer was yes. Only
// "y" and "yes" are: anything else, an empty line and ^D included, is no.
func askToProceed() bool {
	answer, err := askLine("Go ahead? [y/N] ")
	if err != nil && answer == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// confirmCapability is runCapability's destructive gate for a person at a
// terminal: it shows what the call would do and asks. It returns nil when the
// answer was yes, and otherwise the error to end the command with, already
// printed — the capability's own, for a target that was not found, or the
// declined confirmation.
func confirmCapability(ctx context.Context, cmd *cobra.Command, c plugin.Capability, inputs plugin.Inputs,
	opts *globalOpts, renderOpts cli.Options,
) error {
	w := cmd.ErrOrStderr()
	preview := renderOptions(cmd, cli.Pretty, opts.noColor)
	if opts.external != nil && opts.external(c) {
		fmt.Fprintf(w, "%s comes from a plugin outside this binary, so rta does not run its preview before "+
			"you have said yes. It will run with:\n", c.ID)
		_ = cli.Render(w, inputsOnly(c, inputs.Caller), preview)
	} else {
		v, err := c.Run(ctx, plugin.ResolveRequest(c, inputs, true, false).WithSurface(plugin.SurfaceCLI))
		if err != nil {
			ve := view.AsError(err, c.ID+".failed")
			_ = cli.RenderError(w, ve, renderOpts)
			return Rendered(ve)
		}
		if v != nil {
			_ = cli.Render(w, v, preview)
		}
	}
	if !askToProceed() {
		return declinedConfirmation(w)
	}
	return nil
}

// inputsOnly is what a call will run with, for a confirmation that has no
// preview of its own to show: the inputs that have a value, in the order the
// capability declares them, a credential masked.
func inputsOnly(c plugin.Capability, values map[string]any) view.View {
	var kv view.KeyValue
	for _, f := range c.Inputs {
		v, ok := values[f.Name]
		if !ok || v == nil || v == "" {
			continue
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: f.Name, Value: fmt.Sprint(v)})
		if f.Type.Sensitive() {
			kv.Redacted = append(kv.Redacted, f.Name)
		}
	}
	if len(kv.Pairs) == 0 {
		return view.Text{Body: "(no inputs)"}
	}
	return kv
}
