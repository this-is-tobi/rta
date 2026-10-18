package glyph_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/internal/textclean/glyph"
	"github.com/this-is-tobi/rta/pkg/view"
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

// A tree draws the entries it does not list as a leaf labelled with one
// ellipsis (view.CutLabel), and the machine encodings drop it by that label. A
// file or an object really named "…" shown as it is would be the same label,
// and vanish from `fs tree -o json` — a name an attacker picks to hide an
// entry — so a name that is only that character is shown quoted, as one that
// opens with a quotation mark is, and is never mistaken for the marker.
func TestANameThatIsTheCutMarkerIsQuoted(t *testing.T) {
	if got, want := glyph.Name(view.CutLabel), `"`+view.CutLabel+`"`; got != want {
		t.Errorf("Name(%q) = %s, want %s", view.CutLabel, got, want)
	}
	if got := textclean.Name(view.CutLabel); got == view.CutLabel {
		t.Errorf("textclean.Name(%q) = %s, the label of a cut marker", view.CutLabel, got)
	}
	for _, s := range []string{"…a", "a…", "...", "…/"} {
		if got := glyph.Name(s); got != s {
			t.Errorf("Name(%q) = %s, want it as it is: only the lone marker is ambiguous", s, got)
		}
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
