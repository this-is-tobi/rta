package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/view"
)

// newInitCommand implements `rta init`: the first-run assistant.
//
// It writes no configuration, and that is the design. rta works with no file
// at all, and the two questions this command used to ask — a default output
// format, and which of sixty-odd capabilities to pin to the dashboard — had a
// default answer for nearly everybody, wrote an empty file when it was given,
// and froze the dashboard against plugins installed later when it was not. What
// a person has to do once, on a new machine, is connect the agent clients that
// are on it, and turn on completion: so that is what it looks for, and offers,
// each as the command it runs. Anything else about the file is a key to set
// (`rta config schema` lists them) or, for the dashboard, `+` in the TUI and
// `rta dashboard add`, which add a tile without turning the rest into a list.
func newInitCommand(opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "First-run setup: connect your agent clients, and shell completion",
		Long: "rta works with no configuration, so this writes none. It looks at what is on this machine " +
			"and offers the few things worth doing once, each shown as the command it runs and each " +
			"skipped by pressing Enter: registering rta with each AI client it finds, for every project " +
			"where the client has a flag for that and read-only until you grant more (`rta mcp install " +
			"<client>` does the same by hand, with --consent, --root and --max-result); the line that " +
			"turns on tab completion for your shell; and the first-party plugin index, which at a " +
			"terminal it asks to attach (and --yes answers) and otherwise names the command for.\n\n" +
			"rta never writes a client's own file: the client's own command does, as for `rta mcp " +
			"install`. A client that already has a registration is left as it is. --yes registers every " +
			"client it lists without asking, which is what a dotfiles script or a devcontainer wants, and " +
			"--dry-run shows what that would run. Run again it offers only what is not done yet, and " +
			"says so when there is nothing.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			plan := planInit(cmd.Context())
			ask := !opts.yes && !opts.dryRun && len(plan.offers) > 0
			accepted := make([]bool, len(plan.offers))
			for i := range accepted {
				accepted[i] = !ask
			}
			if ask {
				in, out, release, err := initTerminal()
				if err != nil {
					return view.Errorf(CodeConfirmRequired,
						"rta init asks before it registers rta with a client, and there is no terminal to ask at").
						WithHint("`rta init --yes` registers every client it lists without asking, and " +
							"`rta init --dry-run` lists what that would run")
				}
				defer release()
				if accepted, err = askInit(cmd.Context(), plan.offers, in, out); err != nil {
					return initFormError(err)
				}
			}

			indexErr := plan.offerIndex(cmd.Context(), cmd.ErrOrStderr(), opts.yes, opts.dryRun)
			result := plan.run(cmd.Context(), cmd.ErrOrStderr(), accepted, opts.dryRun)
			if err := renderView(cmd, opts, result.answer()); err != nil {
				return err
			}
			if err := result.failure(); err != nil {
				return err
			}
			if indexErr != nil {
				return indexErr
			}
			return nil
		},
	}
}

// initTerminal is the terminal the assistant's questions are drawn on and read
// from, and what to do once it is closed.
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

// askInit asks, for each registration init would make, whether to make it, on
// the terminal initTerminal found: a page to a question, each showing where it
// registers and the command it would run, and every default is no — pressing
// Enter all the way through changes nothing.
//
// A variable, as isTTY is, so a test can stand in for the person: the form
// needs one at a terminal, and what init does once it has been filled in can
// only be tested if a test can say how it was filled in.
var askInit = func(ctx context.Context, offers []initOffer, in io.Reader, out io.Writer) ([]bool, error) {
	accepted := make([]bool, len(offers))
	if os.Getenv("TERM") == "dumb" {
		return accepted, askInitPlain(ctx, offers, accepted, in, out)
	}
	groups := make([]*huh.Group, len(offers))
	for i, o := range offers {
		groups[i] = huh.NewGroup(huh.NewConfirm().
			Title(o.question()).
			Description(o.explain()).
			Affirmative("register").Negative("skip").
			Value(&accepted[i]))
	}
	err := huh.NewForm(groups...).WithInput(in).WithOutput(out).RunWithContext(ctx)
	return accepted, err
}

// askInitPlain is askInit for a terminal that cannot redraw, which huh asks in
// a line prompt. That prompt prints a question's title and nothing of its
// description, and what yes does is the description: an answer given without it
// is not a consent. So the question and what yes does are printed here, as the
// page shows them, and the prompt under them is only the [y/N]. Not in the
// title instead: a title is drawn as one block padded to its widest line, which
// is the command, and every shorter line wraps into blank ones.
//
// The rule is huh's own, which would have chosen the line prompt for this form
// anyway; stating it here is what keeps the description from being dropped.
func askInitPlain(ctx context.Context, offers []initOffer, accepted []bool, in io.Reader, out io.Writer) error {
	for i, o := range offers {
		if _, err := fmt.Fprintf(out, "%s\n%s\n", o.question(), o.explain()); err != nil {
			return err
		}
		prompt := huh.NewConfirm().Title("Register it?").Value(&accepted[i])
		if err := huh.NewForm(huh.NewGroup(prompt)).WithAccessible(true).WithInput(in).WithOutput(out).
			RunWithContext(ctx); err != nil {
			return err
		}
	}
	return nil
}

// initFormError codes what ended the assistant before it changed anything. huh's
// own error for a person pressing ctrl-c is the bare "user aborted", which
// reached the terminal as a box nothing coded; leaving is a thing a person
// did, and what they need back is that nothing happened.
//
// Nothing is registered until every question is answered, so closing the form
// halfway is always "nothing changed" and never a half-finished setup.
func initFormError(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return view.Errorf("core.init.aborted", "rta init was closed before it registered anything").
			WithHint("nothing was changed")
	}
	return view.AsError(err, "core.init.form").
		WithHint("the assistant could not run on this terminal; nothing was changed")
}
