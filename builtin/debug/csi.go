package debug

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// A CSI sequence is named by its prefix, its intermediate and its final byte
// together, never by the final alone.
//
// **The final byte is shared by sequences that have nothing to do with each
// other.** ESC[m is SGR, and ESC[>4;1m is xterm's modifyOtherKeys, which sets
// how a terminal reports keys and was explained as "underline, bold"; ESC[u
// restores the cursor, and ESC[=1u sets the kitty keyboard protocol's flags,
// also explained as a restored cursor; ESC[J erases the display and ESC[?2J
// erases it selectively. The prefix (one of < = > ?) and the intermediate (a
// byte from space to /) are part of the command, and the parser reports them
// beside the final one for that reason. The sequence a program leaves behind
// to garble a terminal is as often one of these as a colour, so a tool that
// reads them as their plain neighbours explains the wrong thing exactly where
// it is looked at.

// A mode is a name, and what turning it on and off does where that is not just
// "enable" and "disable" the name.
type mode struct{ name, on, off string }

// privateModes are the DEC private modes (CSI ? Pm h and l) a program turns on
// and forgets to turn off. A terminal left with one of them set is what a
// reset is for: the mouse reporting that prints escape codes as the pointer
// moves, the alternate screen that never returns, a cursor that stays hidden.
var privateModes = map[int]mode{
	1:    {name: "application cursor keys"},
	3:    {name: "132-column mode"},
	5:    {name: "reverse-video screen"},
	6:    {name: "origin mode"},
	7:    {name: "auto-wrap"},
	9:    {name: "X10 mouse reporting"},
	12:   {name: "cursor blinking"},
	25:   {name: "cursor visibility", on: "show the cursor", off: "hide the cursor"},
	47:   {name: "alternate screen", on: "switch to the alternate screen", off: "leave the alternate screen"},
	66:   {name: "application keypad"},
	69:   {name: "left and right margins"},
	1000: {name: "mouse button reporting"},
	1001: {name: "mouse highlight tracking"},
	1002: {name: "mouse drag reporting"},
	1003: {name: "mouse motion reporting"},
	1004: {name: "focus in and out reporting"},
	1005: {name: "UTF-8 mouse encoding"},
	1006: {name: "SGR mouse encoding"},
	1015: {name: "urxvt mouse encoding"},
	1047: {name: "alternate screen", on: "switch to the alternate screen", off: "leave the alternate screen"},
	1048: {name: "saved cursor", on: "save the cursor", off: "restore the cursor"},
	1049: {
		name: "alternate screen with the cursor saved",
		on:   "switch to the alternate screen, saving the cursor",
		off:  "leave the alternate screen, restoring the cursor",
	},
	2004: {name: "bracketed paste"},
	2026: {name: "synchronized output"},
	2027: {name: "grapheme clustering"},
	2031: {name: "colour-scheme change reports"},
}

// ansiModes are the ANSI modes (CSI Pm h and l) in use.
var ansiModes = map[int]mode{
	2:  {name: "keyboard lock"},
	4:  {name: "insert mode"},
	12: {name: "local echo", on: "turn local echo off", off: "turn local echo on"},
	20: {name: "newline mode"},
}

// cursorStyles are DECSCUSR's, CSI Ps SP q.
var cursorStyles = map[int]string{
	0: "blinking block", 1: "blinking block", 2: "steady block",
	3: "blinking underline", 4: "steady underline", 5: "blinking bar", 6: "steady bar",
}

// windowOps are xterm's window manipulations (CSI Ps t) and reports. The
// reports are the ones that matter: the terminal answers into the program's
// input, as if typed, and a title report put the title, which any program
// that ever printed to the terminal chose, on the command line.
var windowOps = map[int]string{
	1:  "de-iconify the window",
	2:  "iconify the window",
	3:  "move the window",
	4:  "resize the window in pixels",
	8:  "resize the window in characters",
	9:  "maximize or restore the window",
	10: "toggle full screen",
	11: "report whether the window is iconified, as typed input",
	13: "report the window position, as typed input",
	14: "report the window size in pixels, as typed input",
	15: "report the screen size in pixels, as typed input",
	16: "report the size of a character cell, as typed input",
	18: "report the window size in characters, as typed input",
	19: "report the screen size in characters, as typed input",
	20: "report the icon label, as typed input",
	21: "report the window title, as typed input",
	22: "save the title on the stack",
	23: "restore the title from the stack",
}

// modeChange words h (on) and l (off) for the modes in params, by the table's
// names: "enable bracketed paste", "hide the cursor".
func modeChange(table map[int]mode, kind string, params []int, on bool) string {
	if len(params) == 0 {
		return "set no mode"
	}
	out := make([]string, len(params))
	for i, p := range params {
		m, ok := table[p]
		switch {
		case !ok && on:
			out[i] = fmt.Sprintf("enable %smode %d", kind, p)
		case !ok:
			out[i] = fmt.Sprintf("disable %smode %d", kind, p)
		case on && m.on != "":
			out[i] = m.on
		case !on && m.off != "":
			out[i] = m.off
		case on:
			out[i] = "enable " + m.name
		default:
			out[i] = "disable " + m.name
		}
	}
	return strings.Join(out, ", ")
}

// markedCSI explains a CSI sequence that carries a prefix or an intermediate:
// the named ones, and the bare statement of what it carries for the rest.
func markedCSI(cmd ansi.Cmd, groups [][]int) string {
	prefix, inter, final := cmd.Prefix(), cmd.Intermediate(), cmd.Final()
	params := firstOfEach(groups)
	switch {
	case prefix == '?' && inter == 0:
		switch final {
		case 'h', 'l':
			return modeChange(privateModes, "private ", params, final == 'h')
		case 'J':
			return "selectively " + eraseDisplay(params) + " (protected characters stay)"
		case 'K':
			return "selectively " + eraseLine(params) + " (protected characters stay)"
		case 'n':
			return "device status request (DEC) — the terminal answers into the input, as if typed"
		case 'u':
			return "kitty keyboard protocol: query the enhancement flags — the terminal answers into the input"
		}
	case prefix == '>' && inter == 0:
		switch final {
		case 'm':
			return xtermKeyModifiers(params)
		case 'c':
			return "secondary device attributes request — the terminal answers with its version, as if typed"
		case 'q':
			return "terminal version request (XTVERSION) — the terminal answers with its name, as if typed"
		case 'u':
			return fmt.Sprintf("kitty keyboard protocol: push enhancement flags %d", firstOrZero(params))
		}
	case prefix == '<' && inter == 0 && final == 'u':
		return fmt.Sprintf("kitty keyboard protocol: pop %s of enhancement flags", countOrOne(params))
	case prefix == '=' && inter == 0:
		switch final {
		case 'u':
			return "kitty keyboard protocol: set the enhancement flags"
		case 'c':
			return "tertiary device attributes request — the terminal answers into the input, as if typed"
		}
	case prefix == 0 && inter == ' ' && final == 'q':
		if name, ok := cursorStyles[firstOrZero(params)]; ok {
			return "cursor style: " + name
		}
		return fmt.Sprintf("cursor style %d", firstOrZero(params))
	case prefix == 0 && inter == '!' && final == 'p':
		return "soft terminal reset (DECSTR)"
	case inter == '$' && final == 'p':
		kind := "ANSI"
		if prefix == '?' {
			kind = "private"
		}
		return fmt.Sprintf("request the state of %s mode %d (DECRQM) — the terminal answers into the input", kind, firstOrZero(params))
	}
	return fmt.Sprintf("CSI sequence (%s)", sequenceMarks(prefix, inter, final))
}

// sequenceMarks spells what a CSI carries, for the sequence nothing here names.
func sequenceMarks(prefix, inter, final byte) string {
	var parts []string
	if prefix != 0 {
		parts = append(parts, fmt.Sprintf("prefix %q", string(prefix)))
	}
	if inter != 0 {
		parts = append(parts, fmt.Sprintf("intermediate %q", string(inter)))
	}
	return strings.Join(append(parts, fmt.Sprintf("final %q", string(final))), ", ")
}

// xtermKeyModifiers is CSI > Pp ; Pv m: the xterm key-modifier options, not an
// SGR for all it ends in m. Pp 4 is modifyOtherKeys, which asks the terminal to
// report keys the way a program that has set it expects, and which a program
// that exits without resetting leaves the shell receiving escape codes for
// keys it used to get as letters.
func xtermKeyModifiers(params []int) string {
	if len(params) == 0 {
		return "xterm key modifier options: reset all"
	}
	names := map[int]string{0: "modifyKeyboard", 1: "modifyCursorKeys", 2: "modifyFunctionKeys", 4: "modifyOtherKeys"}
	name, ok := names[params[0]]
	if !ok {
		name = fmt.Sprintf("option %d", params[0])
	}
	if len(params) > 1 {
		return fmt.Sprintf("xterm key modifier option %s = %d", name, params[1])
	}
	return "xterm key modifier option " + name + ": reset"
}

// firstOfEach is the leading value of every parameter, which is the parameter
// itself for every command whose parameters have no sub-parameters.
func firstOfEach(groups [][]int) []int {
	out := make([]int, len(groups))
	for i, g := range groups {
		out[i] = g[0]
	}
	return out
}

// collectGroups reads a sequence's parameters as the parser packed them: one
// group per parameter, a colon-separated run of sub-parameters in a group of
// more than one. Missing values are zero.
func collectGroups(pp ansi.Params) [][]int {
	var groups [][]int
	var open []int
	pp.ForEach(0, func(_, param int, hasMore bool) {
		open = append(open, param)
		if !hasMore {
			groups = append(groups, open)
			open = nil
		}
	})
	if open != nil {
		groups = append(groups, open)
	}
	return groups
}

// countParams is how many values a sequence carries, sub-parameters included:
// the number a terminal's parameter limit is measured in.
func countParams(groups [][]int) int {
	n := 0
	for _, g := range groups {
		n += len(g)
	}
	return n
}

// underlineStyles are SGR 4's sub-parameters, 4:n.
var underlineStyles = map[int]string{
	0: "no underline", 1: "single underline", 2: "double underline",
	3: "curly underline", 4: "dotted underline", 5: "dashed underline",
}

// colonSGR explains one SGR parameter that carries colon-separated
// sub-parameters: 4:3 is a curly underline, and 38:2::R:G:B a colour whose
// colour-space field is empty. Read as separate parameters they were
// "underline, italic" and a colour of the wrong channels followed by a black
// foreground.
func colonSGR(g []int) string {
	switch g[0] {
	case 4:
		if name, ok := underlineStyles[g[1]]; ok {
			return name
		}
	case 38, 48, 58:
		which := map[int]string{38: "foreground", 48: "background", 58: "underline color"}[g[0]]
		switch {
		case g[1] == 5 && len(g) == 3:
			return fmt.Sprintf("%s (256-color %d)", which, g[2])
		case g[1] == 2 && len(g) == 5:
			return fmt.Sprintf("%s (rgb %d,%d,%d)", which, g[2], g[3], g[4])
		case g[1] == 2 && len(g) >= 6:
			return fmt.Sprintf("%s (rgb %d,%d,%d)", which, g[3], g[4], g[5])
		}
	}
	texts := make([]string, len(g))
	for i, v := range g {
		texts[i] = strconv.Itoa(v)
	}
	return "SGR " + strings.Join(texts, ":")
}
