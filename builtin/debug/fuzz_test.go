package debug

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean"
)

// explainAnsi reads captured terminal output, attacker-chosen byte for byte,
// through a third-party decoder that has already panicked on a sequence with
// too many parameters. What it owes any input: rows of three cells, each
// valid UTF-8 and holding nothing a terminal acts on or a reader cannot see,
// since a row shows a sequence rather than sending it. A text row is held to
// that too: every character that hides itself leaves the run for a row that
// names it.
func FuzzExplainAnsi(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "\x1b[31mred\x1b[0m", "\x1b]52;c;Y3VybA==\x07", "\x1b]0;title\x1b\\",
		"\x9b2J", "\x9d52;c;aGk=\x07", "\x1bP$qm\x1b\\", "\x1bPtmux;\x1b\x1b]52;c;aGk=\x07\x1b\\",
		"\x1b_Gf=100;AAAA\x1b\\", "\x1b[" + strings.Repeat("9;", 40) + "9m",
		"\x1b[" + strings.Repeat("9:", 40) + "9m", "\x1b [" + strings.Repeat("1;", 40) + "m",
		"\x1b[2", "\x1b]52;c;Y3VybA==", "\x1b(", "\x1b", "\x85\x8d\x9c", "\xff\xfe",
		"\x1b]0;a\bb\x9b2J\x07", "\x1b]8;;https://x.test/\r\x07",
		"a" + string(rune(0x202e)) + "b", string(rune(0x1f600)) + string(rune(0xfe0f)),
		string(rune(0xe0001)) + string(rune(0xe0041)),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		table := explainAnsi(input)
		if table.Total != len(table.Rows) {
			t.Fatalf("%q: Total %d for %d rows", input, table.Total, len(table.Rows))
		}
		for _, row := range table.Rows {
			if len(row) != 3 {
				t.Fatalf("%q: row %q has %d cells", input, row, len(row))
			}
			for _, cell := range row {
				if !utf8.ValidString(cell) || textclean.Deceives(cell) {
					t.Fatalf("%q: row %q carries a cell a terminal acts on or a reader cannot see: %q", input, row, cell)
				}
			}
		}
	})
}
