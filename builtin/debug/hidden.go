package debug

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean"
)

// What displays as something other than what it is, without being an escape
// sequence.
//
// debug.ansi walked the input the way a terminal parses it, which sees ESC and
// the C0 controls and nothing else. Every other character that hides itself
// came back as "unrecognized or incomplete sequence" with the character itself
// in the Sequence column — so the one row that mattered displayed as blank:
//
//   - A bidi override reorders how a line displays without changing what a
//     program reads: the Trojan Source trick, and how a program named evil,
//     then an override, then txt.exe displays as evilexe.txt, a text file.
//   - A zero-width character splits a word, or two identical-looking names,
//     without showing.
//   - Tag characters (U+E0000 block) are an invisible copy of printable ASCII.
//     A person sees nothing; a model reads the text they spell — the "ASCII
//     smuggling" that hides a prompt injection inside an innocent sentence.
//   - C1 controls are the 8-bit forms of CSI, OSC and friends, which some
//     terminals still honour.
//   - A run of variation selectors after one character is bytes carried where
//     nobody sees them, the same smuggling as the tag block with another
//     alphabet: one selector per byte.
//   - Fillers and other default-ignorable characters draw as nothing or as a
//     blank, which is how a name that looks empty, or two names that look
//     alike, are made.
//
// The set starts from textclean's: the characters rta strips from what it
// hands a model and a terminal, named here instead of stripped, because this
// capability exists to show them. The last two kinds are not in it — each is
// ordinary somewhere, a selector picking an emoji's or an ideograph's form, a
// filler inside a Hangul syllable — so they are named here and left alone
// there. It is a list of the known kinds, not a guarantee that nothing else
// can hide; the Description says so.

// invisibleNames names the invisible characters textclean removes. The two
// joiners it deliberately keeps, U+200C and U+200D, are left out here for the
// same reason: they build emoji and letter forms in several scripts, and a
// row for each one inside a family emoji would bury the characters that do
// hide something.
var invisibleNames = map[rune]string{
	0x200b: "zero width space — invisible; splits a word, or makes two identical-looking names differ",
	0x200e: "left-to-right mark — invisible; changes how the text around it is ordered",
	0x200f: "right-to-left mark — invisible; changes how the text around it is ordered",
	0x202a: "left-to-right embedding — reorders what follows until a pop",
	0x202b: "right-to-left embedding — reorders what follows until a pop",
	0x202c: "pop directional formatting — ends the last embedding or override",
	0x202d: "left-to-right override — what follows displays in forced order until a pop",
	0x202e: "right-to-left override — what follows displays reversed until a pop: the Trojan Source trick, and how a file name ending exe.txt is really txt.exe",
	0x2060: "word joiner — invisible",
	0x2061: "function application — invisible",
	0x2062: "invisible times",
	0x2063: "invisible separator",
	0x2064: "invisible plus",
	0x2066: "left-to-right isolate — reorders what follows until a pop",
	0x2067: "right-to-left isolate — reorders what follows until a pop",
	0x2068: "first strong isolate — reorders what follows until a pop",
	0x2069: "pop directional isolate — ends the last isolate",
	0xfeff: "zero width no-break space, or a byte order mark — invisible",
}

// c1Names are the C1 controls worth naming (ECMA-48 §5.3). The ones with an
// ESC equivalent are what makes C1 dangerous: a terminal that honours the
// 8-bit form reads 0x9B exactly as it reads ESC [.
var c1Names = map[rune]string{
	0x84: "IND — index",
	0x85: "NEL — next line",
	0x88: "HTS — set a tab stop",
	0x8d: "RI — reverse index",
	0x90: "DCS in its 8-bit form — starts a device control string",
	0x98: "SOS in its 8-bit form — starts a string",
	0x9b: "CSI in its 8-bit form — a terminal that honours C1 reads what follows as a control sequence",
	0x9c: "ST — string terminator",
	0x9d: "OSC in its 8-bit form — a terminal that honours C1 reads what follows as a title, link or clipboard write",
	0x9e: "PM in its 8-bit form — starts a privacy message",
	0x9f: "APC in its 8-bit form — starts an application program command",
}

// blankNames names characters textclean leaves in place, because a script
// uses them, that on their own draw as nothing or as blank space.
var blankNames = map[rune]string{
	0x00ad: "soft hyphen — invisible unless a line breaks there",
	0x034f: "combining grapheme joiner — invisible",
	0x061c: "Arabic letter mark — invisible; changes how the text around it is ordered",
	0x115f: "Hangul choseong filler — draws as blank space",
	0x1160: "Hangul jungseong filler — draws as blank space",
	0x17b4: "Khmer inherent vowel AQ — invisible",
	0x17b5: "Khmer inherent vowel AA — invisible",
	0x180e: "Mongolian vowel separator — invisible",
	0x2028: "line separator — most terminals draw nothing, and JavaScript reads a line break",
	0x2029: "paragraph separator — most terminals draw nothing, and JavaScript reads a line break",
	0x3164: "Hangul filler — draws as blank space",
	0xffa0: "halfwidth Hangul filler — draws as blank space",
}

func isTag(r rune) bool { return r >= 0xe0000 && r <= 0xe007f }

// isSelector reports whether r is a variation selector: VS1 to VS16 in the
// BMP, VS17 to VS256 in the supplement.
func isSelector(r rune) bool {
	return (r >= 0xfe00 && r <= 0xfe0f) || (r >= 0xe0100 && r <= 0xe01ef)
}

// hidden classifies one rune that is not an escape sequence, reporting false
// for an ordinary one.
func hidden(r rune, raw string) (kind, meaning string, ok bool) {
	switch {
	case r == utf8.RuneError && len(raw) == 1:
		return "byte", fmt.Sprintf("byte 0x%02x, not valid UTF-8 — a terminal shows whatever its own encoding makes of it", raw[0]), true
	case r >= 0x80 && r <= 0x9f:
		if name, named := c1Names[r]; named {
			return "C1 control", name, true
		}
		return "C1 control", fmt.Sprintf("C1 control U+%04X", r), true
	}
	if name, named := invisibleNames[r]; named {
		return "invisible", name, true
	}
	if name, named := blankNames[r]; named {
		return "invisible", name, true
	}
	if textclean.Deceives(string(r)) {
		return "invisible", fmt.Sprintf("U+%04X, which displays as nothing", r), true
	}
	return "", "", false
}

// tagRow explains a run of tag characters by decoding it: each one from
// U+E0020 to U+E007E is an invisible copy of the ASCII character 0xE0000 below
// it. U+E0001 and U+E007F — a language tag and the cancel tag — carry no text.
func tagRow(tags []rune) []string {
	var text strings.Builder
	for _, r := range tags {
		if r >= 0xe0020 && r <= 0xe007e {
			text.WriteByte(byte(r - 0xe0000))
		}
	}
	cell := fmt.Sprintf("<%d tag characters>", len(tags))
	if len(tags) == 1 {
		cell = fmt.Sprintf("<tag character U+%X>", tags[0])
	}
	if text.Len() == 0 {
		return []string{cell, "tags", "a language or cancel tag — invisible, and spelling nothing"}
	}
	return []string{cell, "tags", fmt.Sprintf("invisible, spelling %q — a person sees nothing and a model reads "+
		"the text: the way a prompt injection is hidden in an innocent sentence", text.String())}
}

// selectorRow explains variation selectors that are not one selector picking
// a form for the character before it — a run, or one with no character to
// modify — by decoding them the way the smuggling encodes a byte: VS1 to VS16
// are 0 to 15, and VS17 to VS256 are 16 to 255.
func selectorRow(vs []rune) []string {
	data := make([]byte, len(vs))
	for i, r := range vs {
		switch {
		case r >= 0xfe00 && r <= 0xfe0f:
			data[i] = byte(r - 0xfe00)
		case r >= 0xe0100 && r <= 0xe01ef:
			data[i] = byte(r - 0xe0100 + 16)
		}
	}
	cell := fmt.Sprintf("<%d variation selectors>", len(vs))
	if len(vs) == 1 {
		cell = fmt.Sprintf("<variation selector U+%X>", vs[0])
	}
	return []string{cell, "selectors", fmt.Sprintf("invisible, carrying the bytes %q — one selector after a "+
		"character picks how it is drawn, and more are data a person cannot see", data)}
}

// visualize renders a token as safe, literal text for display — never the
// bytes themselves. It is not a formatting choice: it is on record what
// happens when a control sequence reaches a terminal as itself rather than as
// a description of itself (OSC 52 into the system clipboard, OSC 0 rewriting
// the window title, a bare CR overwriting the line already drawn). A tool
// whose entire purpose is showing somebody what a sequence does must not also
// be a second way to have it happen — cli.Render's own cleaning is a backstop
// for content that arrives from elsewhere, not a reason for this package to
// hand it raw bytes on purpose.
//
// By rune rather than by byte, so a C1 control, an invisible character or a
// bidi override inside a sequence's payload — a window title that reverses the
// text after it — is escaped too, where the byte loop wrote it through.
func visualize(seq string) string {
	var b strings.Builder
	for i := 0; i < len(seq); {
		r, size := utf8.DecodeRuneInString(seq[i:])
		switch {
		case size == 1 && r < utf8.RuneSelf:
			c := seq[i]
			if info, ok := controlChars[c]; ok {
				b.WriteString(info.short)
			} else if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, "\\x%02x", c)
			} else {
				b.WriteByte(c)
			}
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "\\x%02x", seq[i])
		case textclean.Deceives(string(r)) || isTag(r) || isSelector(r) || blankNames[r] != "":
			if r > 0xffff {
				fmt.Fprintf(&b, "\\U%08x", r)
			} else {
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		default:
			b.WriteString(seq[i : i+size])
		}
		i += size
	}
	return b.String()
}
