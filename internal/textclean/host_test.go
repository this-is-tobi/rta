package textclean

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/textclean/glyph"
)

// github.com with the Cyrillic letter U+0456 for the i, which draws as the Latin
// one in most fonts, and the name it resolves as. Every character that is not
// ASCII is planted by code point: a source file may not hold a lookalike.
const (
	cyrillicHost  = "g\u0456thub.com"
	lookalikeName = "xn--gthub-n2e.com"
)

// A record that names a host in anything but ASCII is shown with the name the
// host resolves as beside it, whatever shape the record has: a URL, a bare
// host, a host and a port. The gate compares the record as written, so the
// person answering reads a host that looks like one they know while the name
// resolves to another, and `grant allow http.get` on such a host said only
// "any record under" the host as written. Everything else is shown as it was.
func TestARecordNamingANonASCIIHostSaysWhatItResolvesAs(t *testing.T) {
	for _, tc := range []struct {
		name, record, note string
	}{
		{"a URL", "https://" + cyrillicHost + "/", lookalikeName},
		{"a URL with no path", "https://" + cyrillicHost, lookalikeName},
		{"a URL with a port", "https://" + cyrillicHost + ":8443/v1/", lookalikeName},
		{"a URL with credentials before the host", "https://github.com@" + cyrillicHost + "/", lookalikeName},
		{"a URL of another scheme", "postgres://app@" + cyrillicHost + "/db", lookalikeName},
		{"a bare host", cyrillicHost, lookalikeName},
		{"a host and a port", cyrillicHost + ":8443", lookalikeName},
		{"a subdomain, whose other labels stay as they are", "api." + cyrillicHost, "api." + lookalikeName},
		{"capitals", "https://G\u0406THUB.COM/", lookalikeName},
		{"percent-encoded bytes, which a URL decodes in its host", "https://g%D1%96thub.com/", lookalikeName},
		// Not a Cyrillic letter at all: a fullwidth one, which the name maps to
		// the ASCII letter, so the note says it resolves as the host it imitates.
		{"a fullwidth letter", "https://\uff47ithub.com/", "github.com"},
		// An ideographic full stop is a dot to the resolver.
		{"an ideographic full stop", "https://github\u3002com/", "github.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.record + " (" + tc.note + ")"
			if got := Record(tc.record); got != want {
				t.Errorf("Record(%q) = %q, want %q", tc.record, got, want)
			}
		})
	}
}

// What is ASCII is shown byte for byte, and so is what is not a host: a note
// naming a name that nothing resolves would be noise on every kv key.
func TestARecordThatNamesNoNonASCIIHostIsShownAsItWas(t *testing.T) {
	for _, record := range []string{
		"https://github.com/", "github.com", "github.com:443", "GitHub.com", lookalikeName,
		"https://" + lookalikeName + "/", "https://github.com/caf\u00e9", "https://github.com/%D1%96",
		"https://github.com@evil.example/", "[::1]:8443", "https://[::1]/",
		// Not hosts: a key, a path, a name without a dot, a number.
		"db-password", "prod/caf\u00e9.db", "caf\u00e9", "/etc/h\u00f4te.conf", "4", "caf\u00e9:x", "a:1b",
	} {
		if got := Record(record); got != record {
			t.Errorf("Record(%q) = %q, want it as it is", record, got)
		}
	}
}

// A host holding what no name holds gets no note: a space, a colon that opens
// no port, a quote, a bad escape. No request is made to it, and the note would
// name something that is not a name.
func TestAHostHoldingWhatNoNameHoldsIsNotGivenOne(t *testing.T) {
	for _, record := range []string{
		"https://" + cyrillicHost + " /", "https://" + cyrillicHost + ":80x/", "https://" + cyrillicHost + "'x/",
		"https://g" + cp(0x456) + "t{hub}.com/", "https://g%D1%thub.com/", "https://g" + cp(0x456) + "t@hub.com:/x y",
		// What a request refuses rather than drops: a no-break space, an em
		// space, a control, a character that reorders what is drawn.
		"https://" + cyrillicHost + cp(0xa0) + "/", "https://g" + cp(0x2003, 0x456) + "thub.com/",
		"https://g" + cp(0x456) + "thub.com" + cp(0x202e) + "/", "https://g" + cp(0x456) + "thub.com" + cp(0x85) + "/",
	} {
		if got := Record(record); strings.Contains(got, "xn--") {
			t.Errorf("Record(%q) = %q, want no name made up for it", record, got)
		}
	}
}

// The note is beside the record's own spelling, which is quoted as it always
// was when the record holds what a reader would not see: the Cyrillic letter
// is drawn, the zero-width space named, and the note says what resolves. The
// note is ASCII whatever the record holds, so it can hide nothing itself.
func TestTheNoteBesideAQuotedRecordIsBesideTheQuotedRecord(t *testing.T) {
	record := "https://" + cyrillicHost + "\u200b/" + "\u202e"
	got := Record(record)
	if want := glyph.Quote(record) + " (" + lookalikeName + ")"; got != want {
		t.Fatalf("Record(%q) = %q, want %q", record, got, want)
	}
	for _, r := range got[strings.LastIndex(got, " ("):] {
		if r >= 0x80 {
			t.Errorf("Record(%q) = %q: the note holds %U", record, got, r)
		}
	}
}

// Two records are never shown alike, as before: the record whose text is the
// way another is shown has a space in it, so it is quoted; and a list is read
// the one way, the note following the record it belongs to.
func TestANoteCannotMakeTwoRecordsLookAlike(t *testing.T) {
	annotated := Record(cyrillicHost)
	spelledOut := cyrillicHost + " (" + lookalikeName + ")"
	if got := Record(spelledOut); got == annotated || !strings.HasPrefix(got, `"`) {
		t.Errorf("Record(%q) = %q, want it quoted and not the note's showing of %q", spelledOut, got, cyrillicHost)
	}
	shown := map[string][]string{}
	for _, list := range [][]string{
		{cyrillicHost}, {cyrillicHost, "(" + lookalikeName + ")"}, {cyrillicHost, cyrillicHost}, {spelledOut},
		{cyrillicHost, "x"}, {"x", cyrillicHost}, {"(" + lookalikeName + ")", cyrillicHost},
	} {
		line := Records(list)
		if other, seen := shown[line]; seen {
			t.Errorf("%q and %q are both shown as %q", other, list, line)
		}
		shown[line] = list
	}
}

// WithoutNote gives back the record's own spelling, for what draws a record
// and reads the drawing back: a record with no note is returned as it is, and
// nothing else is ever cut.
func TestWithoutNoteGivesBackTheRecordsOwnSpelling(t *testing.T) {
	for _, record := range []string{
		cyrillicHost, "https://" + cyrillicHost + "/", cyrillicHost + ":8443", "https://" + cyrillicHost + "\u200b/",
		"db", "prod/db ", `"quoted"`, "a (b)", "x)", "(x)", "a b (c.d)", "https://github.com/", "",
	} {
		shown := Record(record)
		want := glyph.Quote(record)
		if readsAsItself(record) {
			want = record
		}
		if got := WithoutNote(shown); got != want {
			t.Errorf("WithoutNote(%q) = %q, want %q", shown, got, want)
		}
		if got := WithoutNote(want); got != want {
			t.Errorf("WithoutNote(%q) = %q, want it as it is: it carries no note", want, got)
		}
	}
}
