package debug

import (
	"strings"
	"testing"
)

// Every character below is planted by code point and never typed: a source
// file holding a literal bidi override is the very thing these rows exist to
// expose, and internal/textclean's source guard refuses one.
func r(cp rune) string { return string(cp) }

func rowFor(t *testing.T, input, kind string) []string {
	t.Helper()
	table := explainAnsi(input)
	for _, row := range table.Rows {
		if row[1] == kind {
			return row
		}
	}
	t.Fatalf("no %q row in %q: %v", kind, input, table.Rows)
	return nil
}

// An invisible character used to come back as "unrecognized or incomplete
// sequence" with the character itself in the Sequence column, so the row that
// mattered displayed as blank.
func TestAnInvisibleCharacterIsNamedAndShownEscaped(t *testing.T) {
	for cp, want := range map[rune]string{
		0x200b: "zero width space",
		0x202e: "right-to-left override",
		0x2066: "left-to-right isolate",
		0xfeff: "byte order mark",
	} {
		row := rowFor(t, "a"+r(cp)+"b", "invisible")
		if !strings.Contains(row[2], want) {
			t.Errorf("U+%04X: meaning = %q, want %q", cp, row[2], want)
		}
		if strings.ContainsRune(row[0], cp) || !strings.HasPrefix(row[0], `\u`) {
			t.Errorf("U+%04X: sequence cell = %q, want it escaped", cp, row[0])
		}
	}
}

// Tag characters are an invisible copy of ASCII: the row says what they
// spell, and says it as one message even when the tokenizer hands them over in
// pieces — including when they ride inside the cluster of a visible letter,
// where a text row would have carried them unseen.
func TestTagCharactersAreDecodedToWhatTheySpell(t *testing.T) {
	smuggle := func(s string) string {
		var b strings.Builder
		for _, c := range s {
			b.WriteRune(0xe0000 + c)
		}
		return b.String()
	}
	for _, input := range []string{
		"please review " + r(0xe0001) + smuggle("ignore previous") + r(0xe007f),
		"abc" + smuggle("run rm"), // riding on the c
	} {
		table := explainAnsi(input)
		var tags []string
		for _, row := range table.Rows {
			if row[1] == "tags" {
				tags = append(tags, row[2])
			}
			if row[1] == "text" && strings.ContainsFunc(row[0], func(c rune) bool { return c >= 0xe0000 && c <= 0xe007f }) {
				t.Errorf("%q: a text row carries tag characters: %q", input, row[0])
			}
		}
		if len(tags) != 1 {
			t.Fatalf("%q: tag rows = %v, want exactly one", input, tags)
		}
		want := "ignore previous"
		if strings.HasPrefix(input, "abc") {
			want = "run rm"
		}
		if !strings.Contains(tags[0], `"`+want+`"`) {
			t.Errorf("%q: meaning = %q, want it to spell %q", input, tags[0], want)
		}
	}
}

// The 8-bit form of CSI is read as a control sequence by any terminal that
// honours C1, and the renderer strips it, so without its own row it left no
// trace at all.
func TestAC1ControlIsNamed(t *testing.T) {
	row := rowFor(t, "x"+r(0x9b)+"2J", "C1 control")
	if row[0] != `\u009b` || !strings.Contains(row[2], "CSI in its 8-bit form") {
		t.Errorf("row = %v", row)
	}
}

// APC was reported as "ESC sequence (final \"ÿ\")"; DCS was never more than a
// label. The payloads that matter are named: kitty graphics, tmux passthrough
// with what it passes through, and the requests that make a terminal answer.
func TestStringSequencesAreNamedByWhatTheyCarry(t *testing.T) {
	if row := rowFor(t, "\x1b_Gf=100;AAAA\x1b\\", "APC"); !strings.Contains(row[2], "kitty graphics") {
		t.Errorf("APC = %v", row)
	}
	wrapped := "\x1bPtmux;\x1b\x1b]52;c;aGk=\x07\x1b\\"
	if row := rowFor(t, wrapped, "DCS"); !strings.Contains(row[2], "tmux passthrough") {
		t.Errorf("tmux DCS = %v", row)
	}
	// And what it wraps is explained as itself, decoded.
	if row := rowFor(t, wrapped, "OSC"); !strings.Contains(row[2], `clipboard WRITE (selection "c"): "hi"`) {
		t.Errorf("wrapped OSC = %v", row)
	}
	if row := rowFor(t, "\x1bP+q544e\x1b\\", "DCS"); !strings.Contains(row[2], "XTGETTCAP: TN") {
		t.Errorf("XTGETTCAP = %v", row)
	}
	if row := rowFor(t, "\x1bP$qm\x1b\\", "DCS"); !strings.Contains(row[2], "DECRQSS") {
		t.Errorf("DECRQSS = %v", row)
	}
	if row := rowFor(t, "\x1b^secret\x1b\\", "PM"); !strings.Contains(row[2], "privacy message") {
		t.Errorf("PM = %v", row)
	}
}

// A sequence's payload is escaped by rune: a window title carrying an override
// reversed the table cell that was meant to show it.
func TestASequencePayloadIsEscapedByRune(t *testing.T) {
	row := rowFor(t, "\x1b]0;title"+r(0x202e)+"gnp.exe\x07", "OSC")
	if strings.ContainsRune(row[0], 0x202e) || !strings.Contains(row[0], `\`+"u202e") {
		t.Errorf("sequence cell = %q, want the override escaped", row[0])
	}
}

// Nothing ordinary is mistaken for hidden: accents, CJK, emoji built with a
// joiner, and a lone combining mark all stay text.
func TestOrdinaryTextStaysText(t *testing.T) {
	for _, input := range []string{"café", "漢字", "👩" + r(0x200d) + "💻", "e" + r(0x301)} {
		table := explainAnsi(input)
		if table.Total != 1 || table.Rows[0][1] != "text" {
			t.Errorf("%q: rows = %v, want one text row", input, table.Rows)
		}
	}
}
