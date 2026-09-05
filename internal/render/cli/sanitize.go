package cli

import (
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/view"
)

// sanitize removes terminal control sequences from every string a
// presentation renderer is about to print.
//
// A view is meant to carry "data and semantic hints only — never ANSI codes".
// That held for the code we write and not at all for the data we display,
// which is the half that matters: an HTTP response body, a DNS TXT record, a
// certificate subject, a filename, a container name, a database row.
//
// What it cost, measured end to end against the shipped binary: a server
// returning ESC ] 52 ; c ; <base64> BEL in its body had `rta http get` write
// that base64 into the reader's system clipboard. OSC 52 is honoured by
// default in iTerm2, kitty, foot, WezTerm and Windows Terminal, and by tmux
// with set-clipboard on, so the next paste into a shell was whatever the
// server chose — one URL, no plugin, no prompt, no grant. Alongside it: OSC 0
// rewrites the window title, a bare CR overwrites the line already drawn so
// the text on screen is not the text in the data, and CSI 2 J erases what was
// above.
//
// `-o json` is the one byte-exact format. It escapes control characters on the
// way out, so nothing raw reaches a terminal from the encoder, and it is what
// the contract promises works in a pipe. Every other format is cleaned, which was
// measured rather than assumed: goccy/go-yaml writes a control character
// straight into a plain scalar (which is also YAML that is not legal), and
// encoding/csv quotes a field for comma, quote and newline, none of which ESC
// is.
//
// The escaping that makes json safe here is safe *against a terminal*. It buys
// nothing against a model, which reads the decoded string — see
// internal/mcp, which cleans the same views with textclean.Model.
func sanitize(v view.View) view.View {
	return view.MapStrings(v, textclean.Terminal)
}

// cleanPtr is sanitize for the pointer form RenderError is handed.
func cleanPtr(e *view.Error) *view.Error {
	return view.MapErrorStrings(e, textclean.Terminal)
}
