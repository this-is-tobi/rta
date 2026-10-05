package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// withFirstPartyOffer gives `plugin install` the one step it used to refuse
// with a pointer at the next.
//
// `rta plugin install pg` on a machine with no index attached said so and named
// the command that attaches one, and the person copied it, ran it, and typed
// the install again: two actions, the first of which the install already knew
// it needed, for the plugins the product leads with. Now, at a terminal, the
// install asks — "Attach the first-party index <url>? [y/N]", with the URL in
// front of the person — and carries on.
//
// **What did not change is who decides.** Nothing is attached until somebody
// says so, and a "yes" typed at that question is saying so: it names the
// destination, which is the thing the docs promise rta will never choose for
// you. Only the first-party index is ever offered, only for a first-party
// plugin's name, and trust is untouched — an install still verifies the bytes
// and binds trust to their digest, whichever index claimed them.
//
// **Where there is nobody to ask, it is the error it always was.** A run
// without a terminal on both the input the answer comes from and the stream the
// question goes to, a --dry-run (which changes nothing, an attach included),
// and a script all get plugin.index.none and its hint, so a pipeline never
// reaches a network destination on a prompt nobody could have seen. --yes
// answers the question at a terminal and does not conjure a terminal.
//
// The install runs first and the offer follows its refusal, rather than the
// offer guessing at what the install will find: the refusal comes before
// anything is fetched or written, and it is the one place that knows which
// attached indexes carry the name.
func withFirstPartyOffer(install *cobra.Command, opts *globalOpts) {
	run := install.RunE
	install.RunE = func(cmd *cobra.Command, args []string) error {
		err := run(cmd, args)
		var verr *view.Error
		if err == nil || !errors.As(err, &verr) || !slices.Contains(notFoundCodes, verr.Code) {
			return err
		}
		if aliasedFirstParty(args[0]) {
			return verr.WithHint(plugindist.FirstPartyHint(args[0], nil))
		}
		name, wants := wantsFirstPartyIndex(args[0])
		if !wants {
			return err
		}
		refusal := err
		if verr.Code == "plugin.install.unknown" {
			refusal = verr.WithHint(plugindist.NoIndexAttached().Hint)
		}
		if opts.dryRun || !indexOfferTerminal() {
			return refusal
		}
		ask := askToAttach
		if opts.yes {
			ask = func(string) (bool, error) { return true, nil }
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%s is a first-party plugin, and no attached index carries it.\n", name)
		attached, aerr := offerFirstPartyIndex(cmd.Context(), ask, cmd.ErrOrStderr())
		if aerr != nil {
			return aerr
		}
		if !attached {
			return refusal
		}
		return run(cmd, args)
	}
	install.Long += "\n\nA first-party plugin's name (`rta plugin install pg`) with no index that carries it " +
		"asks, at a terminal, whether to attach the first-party index first; --yes answers it. " +
		"Without a terminal the refusal names `rta plugin index add official`."
}

// notFoundCodes are the refusals of an install that say no attached index
// carries what was asked for: none attached, none carries it, or the index it
// names is not attached. Only these are about the name, so only these are
// answered about the name — an install that found the plugin and failed for
// another reason keeps the words and the hint of that failure.
var notFoundCodes = []string{"plugin.index.none", "plugin.install.unknown", "plugin.index.unknown"}

// aliasedFirstParty reports whether an install was asked for a first-party
// plugin under another name — `postgres` for pg. No index carries "postgres",
// so the install's refusal would say none does; the useful answer is the name
// it goes by.
func aliasedFirstParty(spec string) bool {
	name, ok := plugindist.FirstParty(spec)
	return ok && name != spec
}

// wantsFirstPartyIndex is the first-party plugin a not-found install asked
// for, and whether attaching the first-party index could change that: the spec
// names one, bare or under `official/`, and no index called official is
// attached.
func wantsFirstPartyIndex(spec string) (name string, wants bool) {
	index, bare, qualified := strings.Cut(spec, "/")
	if !qualified {
		bare, index = spec, ""
	}
	if index != "" && index != plugindist.FirstPartyIndex {
		return "", false
	}
	if first, ok := plugindist.FirstParty(bare); !ok || first != bare {
		return "", false
	}
	if _, attached := plugindist.IndexByName(plugindist.FirstPartyIndex); attached {
		return "", false
	}
	return bare, true
}

// offerFirstPartyIndex asks whether to attach the first-party index and, on a
// yes, attaches it.
//
// Shared rather than written into the install, because the install is not the
// only place a person first needs the index: `rta init` can offer it as a step
// of its own, with its own way of asking, and gets the same attach, the same
// preflight and the same receipt. The question is the caller's — ask is given
// the URL the answer is about, and the line prompt at a terminal and a form
// field are both an ask — and everything after the yes is here.
//
// attached is false, with no error, when nothing was done: the first-party
// index is already attached, or the answer was no. The preflight is a dry run
// of the attach, made before the question, so a machine without git is told so
// rather than asked whether to do something that cannot work.
func offerFirstPartyIndex(ctx context.Context, ask func(url string) (bool, error), said io.Writer) (attached bool, verr *view.Error) {
	if _, already := plugindist.IndexByName(plugindist.FirstPartyIndex); already {
		return false, nil
	}
	url, _ := plugindist.KnownIndexURL(plugindist.FirstPartyIndex)
	if verr := plugindist.PreviewAddIndex(ctx, plugindist.FirstPartyIndex, ""); verr != nil {
		return false, verr
	}
	yes, err := ask(url)
	if err != nil {
		return false, view.Errorf("plugin.index.ask", "reading the answer: %v", err)
	}
	if !yes {
		return false, nil
	}
	fmt.Fprintf(said, "attaching %s …\n", plugindist.OriginForDisplay(url))
	if verr := attachFirstPartyIndex(ctx); verr != nil {
		return false, verr
	}
	claims := ""
	if ix, ok := plugindist.IndexByName(plugindist.FirstPartyIndex); ok {
		listed, _ := plugindist.Manifests(ix)
		claims = ", which claims " + format.CountOf(len(listed), "plugin")
	}
	fmt.Fprintf(said, "attached %s%s\n", plugindist.FirstPartyIndex, claims)
	return true, nil
}

// attachFirstPartyIndex is the attach itself, a var so a test can stand in for
// the clone.
var attachFirstPartyIndex = func(ctx context.Context) *view.Error {
	return plugindist.AddIndexAt(ctx, plugindist.FirstPartyIndex, "", "")
}

// indexOfferTerminal reports whether a person can be asked and can see it: a
// terminal on the real standard input, which the answer is read from, and on
// standard error, which the question is written to. Standard error and not
// standard output, so `rta plugin install pg -o json | jq` still has a clean
// stream and a person at its terminal is still asked.
//
// A var for the reason stderrIsTerminal is one: the answer is a property of
// the process, and what is built on it is untestable unless a test can say
// what it is.
var indexOfferTerminal = func() bool {
	return term.IsTerminal(int(stdio.Real().Fd())) && stderrIsTerminal()
}

// askToAttach is the question at a terminal, in the owner's words: what is
// being attached and from where, and a default of no.
var askToAttach = func(url string) (bool, error) {
	return askYesNo(stdio.Real(), os.Stderr,
		"Attach the first-party index "+plugindist.OriginForDisplay(url)+"? [y/N] ")
}

// askYesNo writes question and reads one line of answer: true for "y" or
// "yes" in either case, false for anything else, an empty line and the end of
// the input included. No is the default because the question is whether to
// reach a network destination, and the quiet answer to that is not to.
//
// Marked as a prompt while it waits (shutdown.Prompting), as every other read
// of a person is: an exit taken inside it reports on a line of its own and not
// after the question's words.
func askYesNo(in io.Reader, out io.Writer, question string) (bool, error) {
	defer shutdown.Prompting()()
	if _, err := fmt.Fprint(out, question); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
