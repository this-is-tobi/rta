package debug

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"

	"github.com/this-is-tobi/rta/pkg/view"
)

// explainAnsi walks input one grapheme-or-sequence at a time — the exact
// loop ansi.DecodeSequence's own doc comment shows — and turns it into one
// row per thing found. Every control and escape sequence gets its own row;
// everything else is walked rune by rune, so printable runes accumulate into
// one text row and each character that hides itself (hidden.go) breaks the run
// and gets a row of its own.
func explainAnsi(input string) view.Table {
	w := walker{t: view.Table{Columns: []view.Column{
		{Name: "Sequence"}, {Name: "Kind"}, {Name: "Meaning"},
	}}}

	pooled := ansi.GetParser()
	defer ansi.PutParser(pooled)
	var params paramRoom

	var state byte
	data := input
	for len(data) > 0 {
		p := params.parserFor(data, state, pooled)
		seq, _, n, newState := ansi.DecodeSequence(data, state, p)
		state = newState
		data = data[n:]
		if isSequence(seq) {
			w.flush()
			kind, meaning := explainSeq(seq, p)
			if state != ansi.NormalState {
				meaning = unfinished(state, kind, meaning)
			}
			w.row(visualize(seq), kind, meaning)
			continue
		}
		// Printable, or a grapheme with nothing visible in it — walked rune
		// by rune either way, because a tag character or a bidi control can
		// ride inside a cluster that renders as one ordinary letter, and a
		// text row would then carry it unseen.
		w.printable(seq)
	}
	w.flush()
	w.t.Total = len(w.t.Rows)
	return w.t
}

// paramRoom hands DecodeSequence a parser with a slot for every parameter of
// the sequence at hand.
//
// The pooled parser has parser.MaxParamsSize slots, and DecodeSequence
// indexes past them rather than stop: a digit or a colon after the 32nd
// separator panicked, in the CLI, over MCP and in the TUI on every launch
// once a tile held one. Its last slot is also never counted, so a sequence
// with exactly 32 parameters lost the 32nd without a sound. Terminal output
// is attacker-chosen here, and nothing stops a CSI or a DCS from carrying a
// thousand parameters, so a token that could hold more than the pool has room
// for is decoded with a parser sized to it — counted from the token itself,
// measured first without a parser, which reads the same bytes and fills no
// slots. Counting every ';' and ':' in the token overcounts a string whose
// data carries some — a DCS's, and an OSC's or an APC's, which ESC introduces
// too and which hold no parameters at all — and that costs a larger buffer,
// one int a separator, and nothing else; the count can never come out short.
type paramRoom struct {
	wide *ansi.Parser
	size int
}

func (r *paramRoom) parserFor(data string, state byte, pooled *ansi.Parser) *ansi.Parser {
	// Only an ESC or an 8-bit CSI or DCS introducer starts a sequence with
	// parameters; ESC can reach one through intermediates first.
	if c := data[0]; c != ansi.ESC && c != ansi.CSI && c != ansi.DCS {
		return pooled
	}
	_, _, n, _ := ansi.DecodeSequence(data, state, nil)
	token := data[:n]
	// A sequence with k separators has k+1 parameters, and the parser counts
	// the last only while it sits below its final slot: k+2 slots.
	need := strings.Count(token, ";") + strings.Count(token, ":") + 2
	if need <= parser.MaxParamsSize {
		return pooled
	}
	if need > r.size {
		r.wide = new(ansi.Parser)
		r.wide.SetParamsSize(need)
		r.wide.SetDataSize(pooledDataSize)
		r.size = need
	}
	return r.wide
}

// pooledDataSize is the data buffer ansi.GetParser's parsers carry, so that a
// DCS or an OSC collects the same bytes whichever parser reads it.
const pooledDataSize = 4 << 10

// unfinished explains a sequence the input ends inside: the truncated
// capture, or the write caught halfway, that somebody reaches for this
// capability to understand. DecodeSequence hands such a token back whole, and
// its prefix alone sent it to the explanation of a finished one — an OSC 52
// with no terminator read as a clipboard write, although a terminal writes
// nothing until a terminator arrives and meanwhile swallows everything it is
// sent. The decoder's state says where the input stopped: inside a string, a
// terminal takes whatever comes next as more of it; anywhere before a final
// byte, as the rest of the sequence.
func unfinished(state byte, kind, meaning string) string {
	if state == ansi.StringState {
		return "incomplete — the input ends before its terminator, so a terminal swallows whatever it is " +
			"sent next until one arrives. Once terminated: " + meaning
	}
	return "incomplete " + sequenceName(kind) + " — the input ends before its final byte, so a terminal " +
		"reads whatever it is sent next as the rest of it"
}

// sequenceName names the sequences that end in a final byte, by the kind
// explainSeq gave them: a lone ESC is a control of its own until something
// follows it.
func sequenceName(kind string) string {
	switch kind {
	case "CSI":
		return "CSI sequence"
	case "DCS":
		return "device control string"
	default:
		return "escape sequence"
	}
}

// cutShort explains a CSI, DCS or escape sequence that a byte unable to
// continue it ended before its final byte — DecodeSequence's zero command.
// Naming the final byte then printed a NUL the input never held.
func cutShort(kind string) string {
	return "incomplete " + sequenceName(kind) + " — cut short before its final byte"
}

// walker accumulates rows: printable runs into one text row, and tag
// characters and variation selectors — which DecodeSequence may hand over
// across several tokens — into one row per run that decodes them together.
type walker struct {
	t    view.Table
	text strings.Builder
	tags []rune
	// selectors is held until the run ends, because only its length says
	// what it is: one selector after a character is part of that character,
	// the way an emoji gets its colour form, and stays in the text.
	selectors []rune
}

func (w *walker) row(cells ...string) { w.t.Rows = append(w.t.Rows, cells) }

func (w *walker) flushText() {
	if w.text.Len() > 0 {
		w.row(w.text.String(), "text", "-")
		w.text.Reset()
	}
}

func (w *walker) flush() {
	w.endSelectors()
	w.flushText()
	if len(w.tags) > 0 {
		w.row(tagRow(w.tags)...)
		w.tags = nil
	}
}

// endSelectors closes a run of variation selectors: a single one after text
// joins it, and anything else gets its own row after the text it followed.
func (w *walker) endSelectors() {
	if len(w.selectors) == 1 && w.text.Len() > 0 {
		w.text.WriteRune(w.selectors[0])
	} else if len(w.selectors) > 0 {
		w.flushText()
		w.row(selectorRow(w.selectors)...)
	}
	w.selectors = nil
}

func (w *walker) printable(seq string) {
	for i := 0; i < len(seq); {
		r, size := utf8.DecodeRuneInString(seq[i:])
		raw := seq[i : i+size]
		i += size
		if isSelector(r) {
			if len(w.tags) > 0 {
				w.flush()
			}
			w.selectors = append(w.selectors, r)
			continue
		}
		w.endSelectors()
		if isTag(r) {
			w.flushText()
			w.tags = append(w.tags, r)
			continue
		}
		if kind, meaning, ok := hidden(r, raw); ok {
			w.flush()
			w.row(visualize(raw), kind, meaning)
			continue
		}
		if len(w.tags) > 0 {
			w.flush()
		}
		w.text.WriteString(raw)
	}
}

// isSequence reports whether a token is a control or escape sequence for
// explainSeq, as opposed to text for printable. Every sequence begins with
// ESC or an 8-bit introducer, or is a lone C0 control or DEL.
//
// An 8-bit introducer begins a sequence however long it is. DecodeSequence
// hands back a raw 0x9D and everything up to its BEL as one token, the way it
// does for ESC ], and a check for 0x80-0x9F on single bytes alone sent the
// 8-bit clipboard write to printable: a byte row, its payload as text, and
// the BEL as an invisible character — the one sequence this capability most
// exists to name, not named. No token of valid UTF-8 starts with those
// bytes, so nothing that is text is taken for one.
func isSequence(seq string) bool {
	if seq == "" {
		return false
	}
	c := seq[0]
	return c == 0x1b || (c >= 0x80 && c <= 0x9f) || (len(seq) == 1 && (c < 0x20 || c == 0x7f))
}

// explainSeq classifies one non-printable token DecodeSequence returned.
// Order matters: CSI, OSC, DCS, APC, PM and SOS all begin with ESC, so the
// specific introducers have to be checked before the bare-ESC fallback catches
// them instead.
func explainSeq(seq string, p *ansi.Parser) (kind, meaning string) {
	switch {
	case ansi.HasCsiPrefix(seq):
		return "CSI", explainCSI(p)
	case ansi.HasOscPrefix(seq):
		return "OSC", explainOSC(p)
	case ansi.HasDcsPrefix(seq):
		return "DCS", explainDCS(ansi.Cmd(p.Command()), string(p.Data()))
	case ansi.HasApcPrefix(seq):
		return "APC", explainAPC(string(p.Data()))
	case ansi.HasPmPrefix(seq):
		return "PM", "privacy message — a string terminals ignore, and a place to hide one"
	case ansi.HasSosPrefix(seq):
		return "SOS", "start of string — a string terminals ignore, and a place to hide one"
	case len(seq) == 1 && seq[0] >= 0x80:
		return "C1 control", rawC1(seq[0])
	case len(seq) == 1:
		return "control", explainControl(seq[0])
	case ansi.HasEscPrefix(seq):
		return "ESC", explainEsc(p)
	default:
		// Every token isSequence passes is one byte or begins with an
		// introducer above, and each of those names its own incomplete form:
		// a zero command reaching explainCSI, explainDCS or explainEsc, and a
		// token the input ends inside reaching unfinished. The prefix alone
		// used to send both to an explanation of a finished sequence, while
		// this row, which says incomplete, was never reached. It stays for an
		// introducer a newer decoder learns: still one row, not a crash.
		return "?", "unrecognized or incomplete sequence"
	}
}

// --- CSI ---

// explainCSI names the parameter count past what the pooled parser holds,
// because what this explains is then not what every terminal does: one keeps
// the parameters up to its limit and drops the rest, another ignores the
// whole sequence.
func explainCSI(p *ansi.Parser) string {
	params := collectParams(p.Params())
	meaning := csiCommand(ansi.Cmd(p.Command()), params)
	if len(params) > parser.MaxParamsSize {
		meaning += fmt.Sprintf(" — %d parameters, more than many terminals keep: "+
			"one drops those past its limit, another ignores the whole sequence", len(params))
	}
	return meaning
}

func csiCommand(cmd ansi.Cmd, params []int) string {
	switch cmd.Final() {
	case 0:
		return cutShort("CSI")
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

// explainOSC shows a title and a link target through visualize, like the
// sequence carrying them: the payload is whatever the decoder collected up to
// the terminator — backspaces, carriage returns, an 8-bit CSI, an override —
// and quoting it as it came handed the terminal, one cell to the right, what
// the Sequence cell had just escaped.
func explainOSC(p *ansi.Parser) string {
	digits, payload := splitOSC(string(p.Data()))
	if digits == "" {
		return "OSC with no command number — a terminal ignores it"
	}
	cmd, err := strconv.Atoi(digits)
	if err != nil {
		return "OSC " + digits
	}
	switch cmd {
	case 0, 1, 2:
		return "set window/icon title: " + visualize(payload)
	case 8:
		return "hyperlink: " + visualize(hyperlinkURI(payload))
	case 52:
		return explainClipboard(payload)
	default:
		return fmt.Sprintf("OSC %d", cmd)
	}
}

// splitOSC splits an OSC's data into its command number and the payload
// after the ';' that follows it.
//
// The number is read here rather than from Command(). The decoder's
// parseOscCmd (parser_decode.go) reads the leading ASCII digits of the data
// only once it meets a ';' or a terminator, so an OSC the input ends inside
// before either had no number yet; and one with no digits keeps
// MissingCommand, which formatted as "OSC 2147483647" — a number the input
// never held. It also never removes the digits from Data(): Data() for
// "OSC 52 ; c ; aGVsbG8=" is the literal bytes "52;c;aGVsbG8=". Undocumented
// behavior, not a stable contract to lean on silently — hence the pointer to
// the exact function, so the next reader can re-check it against whatever
// version of the dependency is in go.mod by then.
func splitOSC(data string) (digits, payload string) {
	i := 0
	for i < len(data) && data[i] >= '0' && data[i] <= '9' {
		i++
	}
	digits = data[:i]
	if i < len(data) && data[i] == ';' {
		i++
	}
	return digits, data[i:]
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

// --- DCS and APC: strings the terminal is handed whole ---

// explainDCS names the device control strings met in practice, from the
// parser's own reading of them: the introducer's intermediate and final bytes
// are the command, and what follows is data.
//
// tmux's passthrough matters most. tmux forwards the sequence it wraps to the
// terminal outside it, unfiltered — which is how a clipboard write tmux itself
// would refuse reaches the real clipboard anyway. The parser ends the DCS at
// the wrapped sequence's first ESC, so that sequence arrives as the next rows
// and is explained there as itself.
func explainDCS(cmd ansi.Cmd, data string) string {
	switch {
	case cmd.Final() == 0:
		return cutShort("DCS")
	case cmd.Final() == 't' && strings.HasPrefix(data, "mux;"):
		return "tmux passthrough — tmux hands the sequence that follows to the terminal outside it, unfiltered, " +
			"which is how a clipboard write tmux would refuse reaches the real clipboard"
	case cmd.Intermediate() == '$' && cmd.Final() == 'q':
		return "request status string (DECRQSS) — asks the terminal to report a setting back, as if typed"
	case cmd.Intermediate() == '+' && cmd.Final() == 'q':
		return "request terminfo capabilities (XTGETTCAP" + capNames(data) + ") — asks the terminal to report back, as if typed"
	}
	return "device control string"
}

// capNames decodes XTGETTCAP's hex-encoded capability names, "544e" being TN.
func capNames(data string) string {
	var names []string
	for _, h := range strings.Split(data, ";") {
		raw, err := hex.DecodeString(h)
		if err != nil || len(raw) == 0 {
			return ""
		}
		names = append(names, visualize(string(raw)))
	}
	return ": " + strings.Join(names, ", ")
}

// explainAPC names the application program command worth knowing on sight:
// kitty's graphics protocol, which draws an image — and can read a file the
// terminal has access to when told to.
func explainAPC(data string) string {
	if strings.HasPrefix(data, "G") {
		return "kitty graphics protocol — draws an image, or reads one from a file the terminal can open"
	}
	return "application program command — a string for the terminal program itself"
}

// --- bare ESC (no CSI/OSC/DCS introducer) ---

func explainEsc(p *ansi.Parser) string {
	cmd := ansi.Cmd(p.Command())
	switch cmd.Final() {
	case 0:
		return cutShort("ESC")
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
