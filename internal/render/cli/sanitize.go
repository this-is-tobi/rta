package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

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
// `-o json` is the one byte-exact format. encoding/json escapes the C0
// controls on the way out, and view.Marshal what it leaves raw that a terminal
// still acts on — DEL, the C1 controls, the characters that reorder text — so
// nothing reaches a terminal raw from the encoder, and a parser still reads
// back every string exactly. It is what the contract promises works in a
// pipe. Every other format is cleaned, which was measured rather than
// assumed: goccy/go-yaml writes a control character straight into a plain
// scalar (which is also YAML that is not legal), and encoding/csv quotes a
// field for comma, quote and newline, none of which ESC is.
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

// cleanRendered is sanitize for what a renderer wrote rather than for what it
// was handed: glamour's output, which is the one place this package prints a
// string it did not build itself.
//
// Cleaning the body first is not enough, and cannot be made enough. goldmark
// decodes character references, so &#x1b;]52;…&#x07; is ASCII in a note —
// nothing Terminal has a reason to touch — and a real OSC 52 in what glamour
// writes. A note body is what an agent with a note grant writes over MCP, so
// that was an agent putting text on the operator's clipboard, and rewriting
// the window title, the moment they read the note. &#x9d; and &#x8d; come out
// as the C1 controls themselves (holes in the cp1252 table HTML maps
// &#128;-&#159; through), and &#8238; as the right-to-left override. Escaping
// every "&" before glamour runs would stop it too, and would also stop
// &amp; and &mdash; meaning what the author wrote them to mean.
//
// So the output is walked sequence by sequence and allowed to keep only what a
// markdown renderer needs: SGR, which is colour and nothing else, and an OSC 8
// link whose target reads as what it is — glamour prints that target beside
// the label too, so the link hides nothing the screen does not show. Text
// between them goes through Terminal, which drops the C0 and C1 controls and
// spells out the reorder characters; every other sequence and control is
// dropped whole. Unstyled output keeps no sequence at all: it is a pipe or
// --no-color, where an escape is bytes in a file, and glamour writes its
// links even there.
func cleanRendered(s string, styled bool) string {
	var out, run strings.Builder
	flush := func() {
		out.WriteString(textclean.Terminal(run.String()))
		run.Reset()
	}
	for s != "" {
		// No parser: a sequence is judged by its bytes here, so nothing
		// collects its parameters into a fixed buffer a long one overruns.
		seq, _, n, state := ansi.DecodeSequence(s, ansi.NormalState, nil)
		n = max(n, 1)
		if seq == "" {
			seq = s[:n]
		}
		s = s[n:]
		switch c := seq[0]; {
		case state != ansi.NormalState:
			// Unterminated: the rest of the output was swallowed into it, and
			// goes with it rather than being guessed back into text.
		case c == '\n' || c == '\t':
			run.WriteByte(c)
		case c == ansi.ESC || (c >= 0x80 && c <= 0x9f):
			if styled && (isSGR(seq) || isHonestLink(seq)) {
				flush()
				out.WriteString(seq)
			}
		case c < 0x20 || c == 0x7f:
		default:
			run.WriteString(seq)
		}
	}
	flush()
	return out.String()
}

// isSGR reports whether seq is Select Graphic Rendition in its 7-bit form and
// nothing else: no prefix or intermediate byte, which is what makes
// CSI > 4 ; 2 m a keyboard mode rather than a colour.
func isSGR(seq string) bool {
	if len(seq) < 3 || seq[0] != ansi.ESC || seq[1] != '[' || seq[len(seq)-1] != 'm' {
		return false
	}
	return strings.Trim(seq[2:len(seq)-1], "0123456789;:") == ""
}

// isHonestLink reports whether seq is an OSC 8 hyperlink, in its 7-bit form
// with a 7-bit terminator, whose parameters and target hold nothing that
// would display as something other than what it is.
func isHonestLink(seq string) bool {
	const open = string(rune(ansi.ESC)) + "]8;"
	data, ok := strings.CutPrefix(seq, open)
	if !ok {
		return false
	}
	for _, term := range []string{string(rune(ansi.BEL)), string(rune(ansi.ESC)) + `\`} {
		if link, ok := strings.CutSuffix(data, term); ok {
			return !textclean.Deceives(link)
		}
	}
	return false
}
