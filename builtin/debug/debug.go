// Package debug absorbs sequin: explain
// the raw ANSI/terminal escape sequences in a string, byte by byte, rather
// than letting a terminal silently act on them.
//
// Built-in rather than an external plugin, unlike pg and eol — reclassified
// forward, alongside `keys`, once building `eol` raised the
// question of what else on that list was filed as "external" out of habit
// rather than necessity. This one has no such necessity: it needs no
// credential, no live service, and no filesystem access a confined plugin
// would be denied, so the only real question was cost, and the answer is
// close to free — charmbracelet/x/ansi is already a direct dependency
// (internal/render/tui uses it today), so this adds no new one.
//
// Useful to rta's own development, not just to whoever installs it: the
// same OSC 52/OSC 0/CSI J hazards found reaching a
// real terminal from this project's own renderer are exactly what this
// capability exists to make visible before they happen somewhere else.
package debug

import (
	"context"
	"errors"

	"github.com/this-is-tobi/rta/builtin/internal/pipein"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Plugin returns the debug plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "debug",
		Summary: "Explain terminal escape sequences, and the characters that hide themselves",
		Capabilities: []plugin.Capability{
			{
				ID:      "debug.ansi",
				Summary: "Break down the escape sequences and hidden characters in a string",
				Description: "Walks the input the way a terminal would, one printable run or " +
					"control/escape sequence at a time, and names what each one does — SGR " +
					"colors and attributes, cursor movement, screen/line erase, and the OSC " +
					"sequences most worth knowing about on sight: window title, hyperlink " +
					"target, and the system clipboard write (decoded, not left as base64); " +
					"tmux passthrough and kitty graphics strings. Also names every character " +
					"that displays as something other than itself: bidi overrides (the Trojan " +
					"Source trick), zero-width characters, 8-bit C1 controls, and tag characters, " +
					"decoded to the text they invisibly spell — the way a prompt injection hides " +
					"in an innocent sentence. Never prints a raw control byte back at the " +
					"terminal it is running in — the whole point is seeing what a sequence does " +
					"without it happening. Given no input, reads the text from standard input.",
				Safety:     plugin.Read,
				Idempotent: true,
				// input is Positional but not Required — stdin can supply
				// it instead — so the dashboard's auto-tile heuristic
				// (every Read capability that needs no input) would
				// otherwise pick this up and call it, unasked, every five
				// seconds with nothing to explain: not a cost the way
				// pg's or eol's off-box calls are, but not a tile either,
				// the exact precedent gen.password already sets.
				NoPreview: true,
				Inputs: []plugin.Field{
					{Name: "input", Type: plugin.Text, Positional: true,
						Help: "text containing escape sequences"},
				},
				Run: runAnsi,
			},
		},
	}
}

// maxPipedInput bounds what debug.ansi reads from a pipe. The explanation is a
// row per sequence and per printable run, so an input past this size is a
// capture to be cut down rather than one to be read whole — and an unbounded
// read was one `cat /dev/urandom | rta debug ansi` away from holding the
// machine's memory.
const maxPipedInput = 1 << 20

func runAnsi(_ context.Context, req plugin.Request) (view.View, error) {
	input := req.String("input")
	if input == "" {
		piped, err := pipein.Read(req, maxPipedInput)
		if verr := stdinError(err); verr != nil {
			return nil, verr
		}
		input = piped
	}
	if input == "" {
		return nil, view.Errorf("debug.ansi.noinput", "no text to explain").
			WithHint("pass it as an argument, or pipe it: my-app | rta debug ansi")
	}
	return explainAnsi(input), nil
}

func stdinError(err error) *view.Error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pipein.ErrTooLarge):
		return view.Errorf("debug.ansi.stdin", "stdin holds more than the %s debug ansi explains",
			format.Bytes(maxPipedInput)).
			WithHint("pipe the part that misbehaves: head -c 65536 capture.log | rta debug ansi")
	default:
		return view.Errorf("debug.ansi.stdin", "reading stdin: %v", err)
	}
}
