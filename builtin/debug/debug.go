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
	"io"

	"golang.org/x/term"

	"github.com/this-is-tobi/rule-them-all/internal/stdio"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Plugin returns the debug plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "debug",
		Summary: "Explain raw ANSI/terminal escape sequences",
		Capabilities: []plugin.Capability{
			{
				ID:      "debug.ansi",
				Summary: "Break down the ANSI escape sequences in a string, byte by byte",
				Description: "Walks the input the way a terminal would, one printable run or " +
					"control/escape sequence at a time, and names what each one does — SGR " +
					"colors and attributes, cursor movement, screen/line erase, and the OSC " +
					"sequences most worth knowing about on sight: window title, hyperlink " +
					"target, and the system clipboard write (decoded, not left as base64). " +
					"Never prints a raw control byte back at the terminal it is running in — " +
					"the whole point is seeing what a sequence does without it happening.",
				Safety:     plugin.Read,
				Idempotent: true,
				// input is Positional but not Required — stdin can supply
				// it instead — so the dashboard's auto-tile heuristic
				// (every Read capability that needs no input, §4.3) would
				// otherwise pick this up and call it, unasked, every five
				// seconds with nothing to explain: not a cost the way
				// pg's or eol's off-box calls are, but not a tile either,
				// the exact gen.password precedent §4.3 already names.
				NoPreview: true,
				Inputs: []plugin.Field{
					{Name: "input", Type: plugin.Text, Positional: true,
						Help: "text containing escape sequences; reads piped stdin when omitted"},
				},
				Run: runAnsi,
			},
		},
	}
}

func runAnsi(_ context.Context, req plugin.Request) (view.View, error) {
	input := req.String("input")
	if input == "" {
		piped, verr := readPipedStdin(req)
		if verr != nil {
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

// readPipedStdin returns "" — not an error — for every case where there is
// nothing to read rather than something merely absent: a non-CLI surface,
// where stdin is not this call's business (TUI owns the screen, MCP has no
// terminal at the other end), and a CLI call with no pipe behind it, where
// reading would block on a person who is never going to send EOF. Mirrors
// builtin/kv's canPrompt, which draws the same "is there really something
// there" line for the opposite direction (asking a question instead of
// reading an answer).
func readPipedStdin(req plugin.Request) (string, *view.Error) {
	if req.Surface() != plugin.SurfaceCLI {
		return "", nil
	}
	f := stdio.Real()
	if term.IsTerminal(int(f.Fd())) {
		return "", nil
	}
	return slurpStdin(f)
}

func slurpStdin(r io.Reader) (string, *view.Error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", view.Errorf("debug.ansi.stdin", "reading stdin: %v", err)
	}
	return string(data), nil
}
