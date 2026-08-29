package debug

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// explainAnsi walks input one grapheme-or-sequence at a time — the exact
// loop ansi.DecodeSequence's own doc comment shows — and turns it into one
// row per thing found. Consecutive printable output (width > 0) is
// accumulated into a single text row rather than one row per character;
// everything else (width == 0: every control and escape sequence) flushes
// that run and gets its own row.
func explainAnsi(input string) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Sequence"}, {Name: "Kind"}, {Name: "Meaning"},
	}}

	p := ansi.GetParser()
	defer ansi.PutParser(p)

	var state byte
	var text strings.Builder
	flush := func() {
		if text.Len() == 0 {
			return
		}
		t.Rows = append(t.Rows, []string{text.String(), "text", "-"})
		text.Reset()
	}

	data := input
	for len(data) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(data, state, p)
		state = newState
		data = data[n:]
		if width > 0 {
			text.WriteString(seq)
			continue
		}
		flush()
		kind, meaning := explainSeq(seq, p)
		t.Rows = append(t.Rows, []string{visualize(seq), kind, meaning})
	}
	flush()
	t.Total = len(t.Rows)
	return t
}

// explainSeq classifies one non-printable token DecodeSequence returned.
// Order matters: CSI, OSC and DCS all begin with ESC, so the specific
// introducers have to be checked before the bare-ESC fallback catches them
// instead.
func explainSeq(seq string, p *ansi.Parser) (kind, meaning string) {
	switch {
	case ansi.HasCsiPrefix(seq):
		return "CSI", explainCSI(p)
	case ansi.HasOscPrefix(seq):
		return "OSC", explainOSC(p)
	case ansi.HasDcsPrefix(seq):
		return "DCS", "device control string"
	case len(seq) == 1:
		return "control", explainControl(seq[0])
	case ansi.HasEscPrefix(seq):
		return "ESC", explainEsc(p)
	default:
		// DecodeSequence's own doc: a zero Cmd means the sequence was
		// invalid — truncated input, mid-write capture, a fuzzer. Still one
		// row, not a crash: an incomplete sequence is exactly the kind of
		// thing somebody reaches for this capability to understand.
		return "?", "unrecognized or incomplete sequence"
	}
}

// --- CSI ---

func explainCSI(p *ansi.Parser) string {
	cmd := ansi.Cmd(p.Command())
	params := collectParams(p.Params())
	switch cmd.Final() {
	case 'm':
		return explainSGR(params)
	case 'A':
		return "cursor up " + countOrOne(params)
	case 'B':
		return "cursor down " + countOrOne(params)
	case 'C':
		return "cursor forward " + countOrOne(params)
	case 'D':
		return "cursor back " + countOrOne(params)
	case 'H', 'f':
		return "cursor position: " + cursorPos(params)
	case 'J':
		return eraseDisplay(params)
	case 'K':
		return eraseLine(params)
	case 's':
		return "save cursor position"
	case 'u':
		return "restore cursor position"
	default:
		return fmt.Sprintf("CSI sequence (final %q)", string(cmd.Final()))
	}
}

func collectParams(pp ansi.Params) []int {
	var out []int
	pp.ForEach(0, func(_, param int, _ bool) {
		out = append(out, param)
	})
	return out
}

func countOrOne(params []int) string {
	n := 1
	if len(params) > 0 && params[0] > 0 {
		n = params[0]
	}
	return fmt.Sprintf("%d", n)
}

func cursorPos(params []int) string {
	row, col := 1, 1
	if len(params) > 0 && params[0] > 0 {
		row = params[0]
	}
	if len(params) > 1 && params[1] > 0 {
		col = params[1]
	}
	return fmt.Sprintf("row %d, column %d", row, col)
}

func eraseDisplay(params []int) string {
	switch firstOrZero(params) {
	case 0:
		return "erase from cursor to end of screen"
	case 1:
		return "erase from start of screen to cursor"
	case 2:
		return "erase entire screen"
	case 3:
		return "erase entire screen and scrollback"
	default:
		return fmt.Sprintf("erase display (mode %d)", firstOrZero(params))
	}
}

func eraseLine(params []int) string {
	switch firstOrZero(params) {
	case 0:
		return "erase from cursor to end of line"
	case 1:
		return "erase from start of line to cursor"
	case 2:
		return "erase entire line"
	default:
		return fmt.Sprintf("erase line (mode %d)", firstOrZero(params))
	}
}

func firstOrZero(params []int) int {
	if len(params) > 0 {
		return params[0]
	}
	return 0
}

// --- SGR (CSI ... m): the common case, so it earns the real table ---

// sgrNames is keyed by ansi's own Attr* constants rather than bare numbers,
// so a wrong transcription is a compile error against the upstream source of
// truth instead of a silent mismatch nobody would notice until asked about
// exactly that code.
var sgrNames = map[int]string{
	ansi.AttrReset:                  "reset",
	ansi.AttrBold:                   "bold",
	ansi.AttrFaint:                  "faint",
	ansi.AttrItalic:                 "italic",
	ansi.AttrUnderline:              "underline",
	ansi.AttrBlink:                  "blink",
	ansi.AttrRapidBlink:             "rapid blink",
	ansi.AttrReverse:                "reverse video",
	ansi.AttrConceal:                "conceal",
	ansi.AttrStrikethrough:          "strikethrough",
	ansi.AttrNormalIntensity:        "normal intensity (not bold or faint)",
	ansi.AttrNoItalic:               "not italic",
	ansi.AttrNoUnderline:            "not underlined",
	ansi.AttrNoBlink:                "not blinking",
	ansi.AttrNoReverse:              "not reversed",
	ansi.AttrNoConceal:              "not concealed",
	ansi.AttrNoStrikethrough:        "not strikethrough",
	ansi.AttrDefaultForegroundColor: "default foreground",
	ansi.AttrDefaultBackgroundColor: "default background",
	ansi.AttrDefaultUnderlineColor:  "default underline color",
}

func init() {
	names := []struct {
		attr  int
		color string
	}{
		{ansi.AttrBlackForegroundColor, "black"}, {ansi.AttrRedForegroundColor, "red"},
		{ansi.AttrGreenForegroundColor, "green"}, {ansi.AttrYellowForegroundColor, "yellow"},
		{ansi.AttrBlueForegroundColor, "blue"}, {ansi.AttrMagentaForegroundColor, "magenta"},
		{ansi.AttrCyanForegroundColor, "cyan"}, {ansi.AttrWhiteForegroundColor, "white"},
		{ansi.AttrBlackBackgroundColor, "black"}, {ansi.AttrRedBackgroundColor, "red"},
		{ansi.AttrGreenBackgroundColor, "green"}, {ansi.AttrYellowBackgroundColor, "yellow"},
		{ansi.AttrBlueBackgroundColor, "blue"}, {ansi.AttrMagentaBackgroundColor, "magenta"},
		{ansi.AttrCyanBackgroundColor, "cyan"}, {ansi.AttrWhiteBackgroundColor, "white"},
		{ansi.AttrBrightBlackForegroundColor, "bright black"}, {ansi.AttrBrightRedForegroundColor, "bright red"},
		{ansi.AttrBrightGreenForegroundColor, "bright green"}, {ansi.AttrBrightYellowForegroundColor, "bright yellow"},
		{ansi.AttrBrightBlueForegroundColor, "bright blue"}, {ansi.AttrBrightMagentaForegroundColor, "bright magenta"},
		{ansi.AttrBrightCyanForegroundColor, "bright cyan"}, {ansi.AttrBrightWhiteForegroundColor, "bright white"},
		{ansi.AttrBrightBlackBackgroundColor, "bright black"}, {ansi.AttrBrightRedBackgroundColor, "bright red"},
		{ansi.AttrBrightGreenBackgroundColor, "bright green"}, {ansi.AttrBrightYellowBackgroundColor, "bright yellow"},
		{ansi.AttrBrightBlueBackgroundColor, "bright blue"}, {ansi.AttrBrightMagentaBackgroundColor, "bright magenta"},
		{ansi.AttrBrightCyanBackgroundColor, "bright cyan"}, {ansi.AttrBrightWhiteBackgroundColor, "bright white"},
	}
	fg := map[int]bool{
		ansi.AttrBlackForegroundColor: true, ansi.AttrRedForegroundColor: true, ansi.AttrGreenForegroundColor: true,
		ansi.AttrYellowForegroundColor: true, ansi.AttrBlueForegroundColor: true, ansi.AttrMagentaForegroundColor: true,
		ansi.AttrCyanForegroundColor: true, ansi.AttrWhiteForegroundColor: true,
		ansi.AttrBrightBlackForegroundColor: true, ansi.AttrBrightRedForegroundColor: true, ansi.AttrBrightGreenForegroundColor: true,
		ansi.AttrBrightYellowForegroundColor: true, ansi.AttrBrightBlueForegroundColor: true, ansi.AttrBrightMagentaForegroundColor: true,
		ansi.AttrBrightCyanForegroundColor: true, ansi.AttrBrightWhiteForegroundColor: true,
	}
	for _, n := range names {
		if fg[n.attr] {
			sgrNames[n.attr] = n.color + " foreground"
		} else {
			sgrNames[n.attr] = n.color + " background"
		}
	}
}

// explainSGR walks every parameter in order. 38/48/58 (extended
// foreground/background/underline color) are not one code, they are a
// prefix followed by either "5;N" (256-color) or "2;R;G;B" (truecolor) —
// scanned by value, the way real terminals resolve it, rather than by the
// parser's sub-parameter bit: a colon-joined "38:2:R:G:B" would set that
// bit, but a semicolon-joined one (what almost everything in the wild
// actually emits) does not, and the value-based scan reads both the same
// way.
func explainSGR(params []int) string {
	if len(params) == 0 {
		params = []int{ansi.AttrReset}
	}
	var parts []string
	for i := 0; i < len(params); i++ {
		n := params[i]
		if consumed, desc, ok := extendedColor(params, i, n); ok {
			parts = append(parts, desc)
			i += consumed
			continue
		}
		if name, ok := sgrNames[n]; ok {
			parts = append(parts, name)
		} else {
			parts = append(parts, fmt.Sprintf("SGR %d", n))
		}
	}
	return strings.Join(parts, ", ")
}

// extendedColor recognizes 38/48/58 followed by a 256-color or truecolor
// selector starting at params[i]. Returns how many extra entries (beyond
// params[i] itself) it consumed, so the caller's loop can skip over them.
func extendedColor(params []int, i, n int) (consumed int, desc string, ok bool) {
	if n != 38 && n != 48 && n != 58 {
		return 0, "", false
	}
	which := map[int]string{38: "foreground", 48: "background", 58: "underline color"}[n]
	if i+1 >= len(params) {
		return 0, "", false
	}
	switch params[i+1] {
	case 5:
		if i+2 >= len(params) {
			return 0, "", false
		}
		return 2, fmt.Sprintf("%s (256-color %d)", which, params[i+2]), true
	case 2:
		if i+4 >= len(params) {
			return 0, "", false
		}
		return 4, fmt.Sprintf("%s (rgb %d,%d,%d)", which, params[i+2], params[i+3], params[i+4]), true
	default:
		return 0, "", false
	}
}

// --- OSC: the three that matter for debugging what a program sent you ---

func explainOSC(p *ansi.Parser) string {
	switch p.Command() {
	case 0, 1, 2:
		return "set window/icon title: " + oscPayload(p)
	case 8:
		return "hyperlink: " + hyperlinkURI(oscPayload(p))
	case 52:
		return explainClipboard(oscPayload(p))
	default:
		return fmt.Sprintf("OSC %d", p.Command())
	}
}

// oscPayload is Data() with the OSC command number stripped back off.
//
// Confirmed by reading the vendored source (parser.go's parseStringCmd):
// the parser builds Command() by reading the leading ASCII digits out of
// the collected data, but never removes them from it — Data() for "OSC 52 ;
// c ; aGVsbG8=" is the literal bytes "52;c;aGVsbG8=", not "c;aGVsbG8=" the
// way Command() already having parsed the "52" would suggest. Undocumented
// behavior, not a stable contract to lean on silently — hence the pointer
// to the exact function, so the next reader can re-check it against
// whatever version of the dependency is in go.mod by then.
func oscPayload(p *ansi.Parser) string {
	data := string(p.Data())
	i := 0
	for i < len(data) && data[i] >= '0' && data[i] <= '9' {
		i++
	}
	if i < len(data) && data[i] == ';' {
		i++
	}
	return data[i:]
}

// hyperlinkURI splits OSC 8's "params;URI" data. The params half (an
// optional id=... for grouping related runs) is not shown: it groups
// hyperlink spans on screen and says nothing about where the link goes,
// which is the one fact this exists to surface.
func hyperlinkURI(data string) string {
	if _, uri, found := strings.Cut(data, ";"); found {
		return uri
	}
	return data
}

// explainClipboard decodes OSC 52's payload rather than showing the base64,
// because "what text is this about to put on my clipboard" is exactly the
// question this sequence answers
// wrongly for a bare terminal — this capability exists partly to make that
// answer visible before it happens somewhere else.
func explainClipboard(data string) string {
	sel, payload, found := strings.Cut(data, ";")
	if !found {
		return "clipboard write (malformed: no selection/payload separator)"
	}
	if payload == "?" {
		return fmt.Sprintf("clipboard READ request (selection %q)", sel)
	}
	if payload == "" {
		return fmt.Sprintf("clipboard CLEAR (selection %q)", sel)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Sprintf("clipboard WRITE (selection %q, %d bytes, not valid base64)", sel, len(payload))
	}
	return fmt.Sprintf("clipboard WRITE (selection %q): %q", sel, string(decoded))
}

// --- bare ESC (no CSI/OSC/DCS introducer) ---

func explainEsc(p *ansi.Parser) string {
	cmd := ansi.Cmd(p.Command())
	switch cmd.Final() {
	case '7':
		return "save cursor position"
	case '8':
		return "restore cursor position"
	case 'c':
		return "full reset (RIS)"
	case 'M':
		return "reverse index (scroll back one line)"
	case '=':
		return "enable keypad application mode"
	case '>':
		return "enable keypad numeric mode"
	default:
		return fmt.Sprintf("ESC sequence (final %q)", string(cmd.Final()))
	}
}

// --- control characters and safe visualization ---

// ctrlInfo names one C0 control byte twice: short is what visualize prints
// inline (has to stay short — it sits next to ordinary text), meaning is
// the Meaning column's full sentence.
type ctrlInfo struct{ short, meaning string }

var controlChars = map[byte]ctrlInfo{
	0x07: {"BEL", "bell"},
	0x08: {"BS", "backspace"},
	0x09: {"TAB", "horizontal tab"},
	0x0a: {"LF", "line feed"},
	0x0b: {"VT", "vertical tab"},
	0x0c: {"FF", "form feed"},
	0x0d: {"CR", "carriage return — overwrites the current line unless followed by LF"},
	0x1b: {"ESC", "escape"},
}

func explainControl(b byte) string {
	if info, ok := controlChars[b]; ok {
		return info.meaning
	}
	return fmt.Sprintf("control character 0x%02x", b)
}

// visualize renders a raw escape/control token as safe, literal text for
// display — never the bytes themselves. This is not a formatting choice:
// It is on record what happens when a
// control sequence reaches a terminal as itself rather than as a
// description of itself (OSC 52 into the system clipboard, OSC 0 rewriting
// the window title, a bare CR overwriting the line already drawn). A tool
// whose entire purpose is showing somebody what a sequence does must not
// also be a second way to have it happen — cli.Render's own cleaning is a
// backstop for content that arrives from elsewhere, not a reason for this
// package to hand it raw bytes on purpose.
func visualize(seq string) string {
	var b strings.Builder
	for i := 0; i < len(seq); i++ {
		c := seq[i]
		if info, ok := controlChars[c]; ok {
			b.WriteString(info.short)
			continue
		}
		if c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "\\x%02x", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
