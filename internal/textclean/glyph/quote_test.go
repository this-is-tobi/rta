package glyph_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/internal/textclean/glyph"
)

// Quote names each character a reader does not see by its code point and
// keeps the rest as they are, and what it spells reads back as the value,
// byte for byte: it is Go's quoting, with Seen deciding what is escaped.
func TestQuoteNamesWhatAReaderDoesNotSeeAndReadsBack(t *testing.T) {
	for _, s := range []string{
		"", "plain", "a b", `say "hi"`, `back\slash`, "tab\there", "line\nbreak", "bad\xff",
		"café", "東京", string(rune(0x1f600)),
		"prod/db" + string(rune(0x3164)), "prod/db" + string(rune(0x2800)), "prod/db" + string(rune(0x16fe4)),
		"vs" + string(rune(0xfe0f)), "nbsp" + string(rune(0xa0)) + "x", "a" + string(rune(0x202e)) + "b",
	} {
		q := glyph.Quote(s)
		back, err := strconv.Unquote(q)
		if err != nil || back != s {
			t.Errorf("Quote(%q) = %s, which reads back as %q (%v)", s, q, back, err)
		}
		for _, r := range q {
			if !glyph.Seen(r) {
				t.Errorf("Quote(%q) = %s keeps U+%04X as it is", s, q, r)
			}
		}
	}
	for _, s := range []string{"café", "東京", string(rune(0x1f600)), "it's"} {
		if got, want := glyph.Quote(s), `"`+s+`"`; got != want {
			t.Errorf("Quote(%q) = %s, want %s", s, got, want)
		}
	}
	if got, want := glyph.Quote("prod/db"+string(rune(0x3164))),
		`"prod/db`+strings.Trim(strconv.QuoteRuneToASCII(0x3164), "'")+`"`; got != want {
		t.Errorf("a Hangul filler = %s, want %s", got, want)
	}
}

// A value the TUI's call spelling quotes reads the way textclean.Record
// shows the same record wherever it does not read as itself, so a person
// comparing the call with the record the gate names sees one spelling.
func TestQuoteSpellsAValueAsRecordShowsIt(t *testing.T) {
	for _, s := range []string{
		"a b", " lead", "trail ", `"quoted`, "tab\there", "bad\xff", `back\slash with space`,
		"prod/db" + string(rune(0x3164)), "prod/db" + string(rune(0x16fe4)), "vs" + string(rune(0xfe0f)),
		"nbsp" + string(rune(0xa0)) + "x", "zw" + string(rune(0x200b)), "a" + string(rune(0x202e)) + "b",
	} {
		record := textclean.Record(s)
		if record == s {
			t.Fatalf("%q reads as itself to Record, so it tests nothing", s)
		}
		if got := glyph.Quote(s); got != record {
			t.Errorf("Quote(%q) = %s, and Record shows it as %s", s, got, record)
		}
	}
}
