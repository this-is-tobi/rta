package debug

import (
	"strings"
	"testing"
)

// explainAnsi reads captured terminal output, attacker-chosen byte for byte,
// through a third-party decoder that has already panicked on a sequence with
// too many parameters. What it owes any input: rows of three cells, and a
// Sequence column holding no byte a terminal acts on, since that column shows
// the sequence rather than sending it.
func FuzzExplainAnsi(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "\x1b[31mred\x1b[0m", "\x1b]52;c;Y3VybA==\x07", "\x1b]0;title\x1b\\",
		"\x9b2J", "\x9d52;c;aGk=\x07", "\x1bP$qm\x1b\\", "\x1bPtmux;\x1b\x1b]52;c;aGk=\x07\x1b\\",
		"\x1b_Gf=100;AAAA\x1b\\", "\x1b[" + strings.Repeat("9;", 40) + "9m",
		"\x1b[" + strings.Repeat("9:", 40) + "9m", "\x1b [" + strings.Repeat("1;", 40) + "m",
		"\x1b[2", "\x1b]52;c;Y3VybA==", "\x1b(", "\x1b", "\x85\x8d\x9c", "\xff\xfe",
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
			if row[1] == "text" {
				continue
			}
			for i := 0; i < len(row[0]); i++ {
				if c := row[0][i]; c < 0x20 || c == 0x7f {
					t.Fatalf("%q: row %q carries the raw byte 0x%02x in its Sequence column", input, row, c)
				}
			}
		}
	})
}
