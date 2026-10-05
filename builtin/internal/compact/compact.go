// Package compact decides whether a listing is drawn as the few columns a
// person reads across a screen or as every field it has.
//
// The agent record and the grant roster each carry more than a dozen fields,
// and at an ordinary terminal width a table that wide is not drawn as a table
// at all: the renderer falls back to a card for each row, nine lines a call,
// and thirty calls are most of a screen of scrolling. What a person scans for
// is the time, the capability, the record it named and what became of it, so
// that is what a terminal gets by default — and every field is still one
// `--detail` away, and is what every reader that is not a person receives.
//
// **Which reader is which is a fact about stdout, and nothing else is
// consulted.** A capability is not told which format the result is written
// in, and the contract is that json, yaml and csv are exact: a log shipper
// reading `agent log --after N -o json` is promised every field of every row
// whatever else changes. So the narrow listing is for the two readers that are
// certainly looking at it — the TUI, and the command line writing to a
// terminal — and everything piped, redirected or run from a script keeps the
// full table, as the renderer already keeps its natural width for them. An
// explicit `-o json` typed at a terminal is still a machine format and is
// detected from the command line, because a promise of exactness that holds
// until somebody forgets to pipe it is not one.
package compact

import (
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// For reports whether req is answered with the compact listing.
//
// The TUI always is: its whole-screen result carries `detail` by default, and
// that means "the page", not "every column". On the command line the listing
// is compact when a person is reading it, which is a terminal on stdout, no
// machine format asked for, and no `--detail`.
func For(req plugin.Request) bool {
	switch req.Surface() {
	case plugin.SurfaceTUI:
		return true
	case plugin.SurfaceCLI:
		return !req.Bool("detail") && stdoutIsTerminal() && !machineFormatAsked(commandLine())
	}
	return false
}

var (
	stdoutIsTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
	commandLine      = func() []string { return os.Args[1:] }
	envOutput        = func() string { return os.Getenv("RTA_OUTPUT") }
)

// Pretend makes For see the command line as written to a terminal, or to a
// pipe, with no output format asked for. For tests, which run with neither.
func Pretend(terminal bool) (restore func()) {
	was, wasArgs, wasEnv := stdoutIsTerminal, commandLine, envOutput
	stdoutIsTerminal = func() bool { return terminal }
	commandLine = func() []string { return nil }
	envOutput = func() string { return "" }
	return func() { stdoutIsTerminal, commandLine, envOutput = was, wasArgs, wasEnv }
}

// machineFormatAsked reports whether the command line, or RTA_OUTPUT under it,
// names an output format other than pretty. A format the config file's
// `output:` key names is not seen from here; that file sets a default the
// command line then overrides, and a person who made json their default is
// reading json on a terminal on purpose.
func machineFormatAsked(args []string) bool {
	asked := envOutput()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			i = len(args)
		case arg == "-o" || arg == "--output":
			if i+1 < len(args) {
				asked = args[i+1]
			}
		case strings.HasPrefix(arg, "--output="):
			asked = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o=") && len(arg) > len("-o="):
			asked = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "-o") && len(arg) > 2 && !strings.HasPrefix(arg, "--"):
			asked = arg[2:]
		}
	}
	asked = strings.ToLower(strings.TrimSpace(asked))
	return asked != "" && asked != "pretty"
}
