package debug

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// An escape character written out is read as the character. What a captured
// log, cat -v and a JSON line show of a colour was one row of printable text
// and a dash from a command that exists to say what a sequence does.
func TestAnEscapeWrittenOutIsExplainedAsTheSequenceItSpells(t *testing.T) {
	for _, c := range []struct {
		in       string
		wantRow  string
		spelling string
	}{
		{`\033[1mhi\033[0m`, "ESC[1m", `\033`},
		{`\x1b[31mred`, "ESC[31m", `\x1b`},
		{`\u001b[4mu`, "ESC[4m", `\u001b`},
		{`\e[7mreverse`, "ESC[7m", `\e`},
		{"^[[0m", "ESC[0m", "^["},
		{`\033]0;title\007`, "ESC]0;titleBEL", `\007`},
		{"^[]0;user@host: ~^G", "ESC]0;user@host: ~BEL", "^G"},
	} {
		v, err := runAnsi(context.Background(), req(map[string]any{"input": c.in}))
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		table := v.(view.Table)
		if len(table.Rows) == 0 || table.Rows[0][0] != c.wantRow || table.Rows[0][1] == "text" {
			t.Errorf("%s: rows %v, want the first to be the sequence %s", c.in, table.Rows, c.wantRow)
		}
		if len(table.Warnings) != 1 || table.Warnings[0].Code != "debug.ansi.spelled" ||
			!strings.Contains(table.Warnings[0].Message, c.spelling) {
			t.Errorf("%s: warnings %+v, want debug.ansi.spelled naming %s", c.in, table.Warnings, c.spelling)
		}
	}
}

// What is not an escape character spelled out stays text, and says nothing: a
// path, a regular expression, a spelling ahead of something that starts no
// sequence, and a text that holds the character itself, which means what it
// says.
func TestTextThatSpellsNoEscapeIsLeftAlone(t *testing.T) {
	for _, in := range []string{
		`C:\Users\e\notes`,
		`^[a-z]+$`,
		`\033 alone`,
		`\x1b? no introducer`,
		"real \x1b[1m and spelled \\033[0m",
		"bell ^G outside a title and \\007 too",
	} {
		decoded, found := unspell(in)
		if decoded != in || len(found) != 0 {
			t.Errorf("unspell(%q) = %q, %v, want it unchanged", in, decoded, found)
		}
	}
	v, err := runAnsi(context.Background(), req(map[string]any{"input": `\033 alone`}))
	if err != nil {
		t.Fatal(err)
	}
	if table := v.(view.Table); len(table.Warnings) != 0 {
		t.Errorf("warned about text that spells no escape: %+v", table.Warnings)
	}
}
