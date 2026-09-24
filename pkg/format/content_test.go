package format

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Text is told from binary, not from text a renderer would show differently:
// a directional mark, a byte order mark, a form feed or an escape sequence is
// text, and the renderer that prints it deals with it. NUL, the C0 controls
// text is never written with, and invalid UTF-8 are binary.
func TestPlainTextTellsTextFromBinary(t *testing.T) {
	for in, want := range map[string]bool{
		"hello":                                 true,
		"two\nlines\tand a tab":                 true,
		"windows\r\nline ending":                true,
		"a lone\rcarriage return":               true,
		"page one\fpage two\vtab":               true,
		"café":                                  true,
		"\x1b[31mred\x1b[0m":                    true,
		"zero" + string(rune(0x200b)) + "width": true,
		"hebrew" + string(rune(0x200f)) + ".":   true,
		string(rune(0xfeff)) + `{"ok":true}`:    true,
		"in" + string(rune(0x202e)) + "fdp.exe": true,
		"del\x7f and C1 " + string(rune(0x9b)):  true,
		"nul\x00":                               false,
		"\x01\x02\x03":                          false,
		"unit\x1fseparator":                     false,
		"\x0e shift out":                        false,
		"\xff\xfe":                              false, // not UTF-8
	} {
		if got := PlainText([]byte(in)); got != want {
			t.Errorf("PlainText(%q) = %v, want %v", in, got, want)
		}
	}
}

// The layout people read hexdump -C in: a short last line keeps the ASCII
// column where the full lines put it.
func TestDumpLaysOutEveryByte(t *testing.T) {
	raw := []byte("0123456789abcdef\x00")
	want := "17 bytes, not plain text:\n" +
		"00000000  30 31 32 33 34 35 36 37  38 39 61 62 63 64 65 66  |0123456789abcdef|\n" +
		"00000010  00                                                |.|"
	if got := Dump(raw, 64); got != want {
		t.Errorf("dump =\n%s\nwant\n%s", got, want)
	}
	if got := Dump(raw, 16); !strings.HasPrefix(got, "17 bytes, not plain text — the first 16 bytes shown:") ||
		strings.Contains(got, "00000010") {
		t.Errorf("bounded dump = %q", got)
	}
}

func TestTruncateNeverSplitsACharacter(t *testing.T) {
	text := strings.Repeat("é", 10) // twenty bytes
	got := Truncate(text, 5)
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "éé\n") || !strings.HasSuffix(got, "(16 more bytes)") {
		t.Errorf("truncated = %q", got)
	}
	if got := Truncate("short", 10); got != "short" {
		t.Errorf("a short text was changed: %q", got)
	}
	if got := Truncate("abcdef", 5); !strings.HasSuffix(got, "(1 more byte)") {
		t.Errorf("one byte over = %q", got)
	}
}

// A limit is whatever arithmetic the caller did, a negative result included,
// and a formatting helper answers rather than panicking inside a handler.
func TestDumpAndTruncateTakeANegativeLimit(t *testing.T) {
	if got := Dump([]byte{0}, -1); !strings.HasPrefix(got, "1 byte, not plain text — the first 0 bytes shown:") {
		t.Errorf("Dump with a negative limit = %q", got)
	}
	if got := Truncate("abc", -1); got != "\n… (3 more bytes)" {
		t.Errorf("Truncate with a negative limit = %q", got)
	}
}
