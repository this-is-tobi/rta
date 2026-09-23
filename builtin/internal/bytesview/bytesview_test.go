package bytesview

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPlainTextIsTextATerminalShowsAsItIs(t *testing.T) {
	for in, want := range map[string]bool{
		"hello":                                 true,
		"two\nlines\tand a tab":                 true,
		"windows\r\nline ending":                true,
		"café":                                  true,
		"\x1b[2J":                               false, // a terminal would act on it
		"nul\x00":                               false,
		"\xff\xfe":                              false, // not UTF-8
		"zero" + string(rune(0x200b)) + "width": false,
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
