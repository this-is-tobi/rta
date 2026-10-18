package textclean

import (
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/this-is-tobi/rta/internal/textclean/glyph"
)

// noteOpen opens the note Record puts after a record whose host is not
// written in ASCII: the xn-- form of that host, in parentheses.
const noteOpen = " ("

// hostNote is the ASCII form of the host a record names when the record
// writes that host in anything but ASCII, and empty for every other record.
//
// **A lookalike host is a different host that reads as the one a person
// knows.** github.com written with the Cyrillic letter U+0456 for its i draws
// as github.com in nearly every font, and a grant, a parked call or a ledger
// row on it said so and nothing more, although the name is published as
// xn--gthub-n2e.com. The gate compares the record as written, which is right,
// and Record shows the record as the gate compares it, which is why the note
// is Record's to add: every surface that shows a record draws it through
// Record, so a surface cannot show the record without it, which a note each
// of them added would have let one forget.
//
// The shapes are the three a host is named in: scheme://[user@]host[:port]
// followed by anything, a bare host, and host:port. A bare word with neither a
// dot nor a port is a key or a file name, not a host, and is shown as it is
// however it is spelled. A host holding anything a name does not hold — a
// space, a colon that opens no port, a quote — gets no note: no request is
// made to it, and the note would be a name for something that is not one.
//
// Only the host is repeated, never the rest of the record, so the note is
// ASCII whatever the record holds and cannot carry what Record quotes it for.
func hostNote(record string) string {
	// Cheap first: almost every record is ASCII with no escape in it, and the
	// TUI draws all of them on every keystroke.
	if !strings.ContainsFunc(record, func(r rune) bool { return r > unicode.MaxASCII }) &&
		!strings.Contains(record, "%") {
		return ""
	}
	host, ok := hostIn(record)
	if !ok {
		return ""
	}
	// A URL's host is percent-decoded before the name is made, which is how
	// https://g%D1%96thub.com/ reaches the Cyrillic one with nothing but ASCII
	// in the record.
	if strings.Contains(host, "%") {
		decoded, err := url.PathUnescape(host)
		if err != nil {
			return ""
		}
		host = decoded
	}
	if !strings.ContainsFunc(host, func(r rune) bool { return r > unicode.MaxASCII }) {
		return ""
	}
	name := asciiHost(host)
	if !isName(name) {
		return ""
	}
	return name
}

// asciiHost is host as DNS publishes it: mapped the way a request maps a name
// before it resolves it, and then each label that is not ASCII in its xn--
// form (RFC 3492).
//
// The mapping is the part of IDNA that decides whether two spellings are one
// name, and it is done with what the binary carries: what the request drops
// from a name is dropped, compatibility forms are folded (NFKC: a fullwidth
// letter is the ASCII letter, and the note then says the host it imitates
// rather than a name that is nobody's), capitals are lowered, and an
// ideographic full stop is a dot. golang.org/x/net/idna does all of that and
// more, and linking it costs some 115 KiB of tables and code that nothing
// else in the binary uses; the fold costs some 65 KiB, the code of the
// normaliser whose tables are in the binary already, and that is the whole
// price of the note. IDNA's validation is not here: it decides whether a
// request is made, and the note only has to say what the name would be.
func asciiHost(host string) string {
	var kept strings.Builder
	for _, r := range host {
		switch {
		case ignored(r):
		case !glyph.Seen(r):
			// A space, a control, a character that reorders what is drawn: the
			// request refuses the name rather than drop them, so there is no
			// name to give.
			return ""
		default:
			kept.WriteRune(r)
		}
	}
	mapped := strings.ReplaceAll(strings.ToLower(norm.NFKC.String(kept.String())), string(rune(0x3002)), ".")
	labels := strings.Split(mapped, ".")
	for i, label := range labels {
		if strings.ContainsFunc(label, func(r rune) bool { return r > unicode.MaxASCII }) {
			labels[i] = "xn--" + punycode(label)
		}
	}
	return strings.Join(labels, ".")
}

// ignored reports whether the request drops r from a name rather than refuse
// it: the format characters, the soft hyphen and the zero-width space among
// them, and the variation selectors. Not the ones that reorder what is drawn,
// which no name holds.
func ignored(r rune) bool {
	return !reorders(r) && (unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r))
}

// Bootstring parameters for Punycode (RFC 3492, section 5).
const (
	pcBase        = 36
	pcTMin        = 1
	pcTMax        = 26
	pcSkew        = 38
	pcDamp        = 700
	pcInitialBias = 72
	pcInitialN    = 128
)

// punycode is label encoded as RFC 3492 section 6.3 does, without the xn--
// prefix: its ASCII characters, a hyphen when there are any, and the others as
// a run of base-36 numbers that says where each goes.
func punycode(label string) string {
	runes := []rune(label)
	var out []byte
	for _, r := range runes {
		if r <= unicode.MaxASCII {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	n, delta, bias := pcInitialN, 0, pcInitialBias
	for handled < len(runes) {
		next := int(^uint32(0) >> 1)
		for _, r := range runes {
			if int(r) >= n && int(r) < next {
				next = int(r)
			}
		}
		delta += (next - n) * (handled + 1)
		n = next
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := pcBase; ; k += pcBase {
				t := min(max(k-bias, pcTMin), pcTMax)
				if q < t {
					break
				}
				out = append(out, pcDigit(t+(q-t)%(pcBase-t)))
				q = (q - t) / (pcBase - t)
			}
			out = append(out, pcDigit(q))
			bias = pcAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return string(out)
}

// pcDigits are the digits of a base-36 number, a to z for 0 to 25 and 0 to 9
// for 26 to 35.
const pcDigits = "abcdefghijklmnopqrstuvwxyz0123456789"

func pcDigit(d int) byte { return pcDigits[d] }

func pcAdapt(delta, points int, first bool) int {
	if first {
		delta /= pcDamp
	} else {
		delta /= 2
	}
	delta += delta / points
	k := 0
	for delta > ((pcBase-pcTMin)*pcTMax)/2 {
		delta /= pcBase - pcTMin
		k += pcBase
	}
	return k + (pcBase-pcTMin+1)*delta/(delta+pcSkew)
}

// hostIn is the host a record names, as written, and false for a record that
// names none in a shape a host is named in.
func hostIn(record string) (string, bool) {
	authority := record
	named := false
	if i := strings.Index(record, "://"); i >= 0 {
		if !isScheme(record[:i]) {
			return "", false
		}
		authority = record[i+len("://"):]
		if end := strings.IndexAny(authority, "/?#"); end >= 0 {
			authority = authority[:end]
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		named = true
	} else if strings.ContainsAny(record, "/?#@\\ \t\n") {
		return "", false
	}
	host, port := splitPort(authority)
	switch {
	case host == "" || strings.HasPrefix(host, "["):
		return "", false
	case named || port != "" || strings.Contains(host, "."):
		return host, true
	}
	return "", false
}

// splitPort cuts a port off an authority: a colon and digits, last in it. The
// port may be empty, as a URL's may, and the host is then still the host.
func splitPort(authority string) (host, port string) {
	i := strings.LastIndex(authority, ":")
	if i < 0 {
		return authority, ""
	}
	for _, c := range authority[i+1:] {
		if c < '0' || c > '9' {
			return authority, ""
		}
	}
	return authority[:i], authority[i+1:]
}

// isScheme reports whether s is a URL scheme (RFC 3986): a letter, then
// letters, digits, plus, hyphen or dot.
func isScheme(s string) bool {
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// WithoutNote is shown, a record as Record drew it, without the note Record
// adds beside a host that is not written in ASCII; and shown itself when it
// carries none.
//
// For what draws a record and reads the drawing back, as the roster's Record
// cell is read to seed a revoke (grant.RecordOfRoster). The cut is never
// ambiguous: a record shown as itself holds no space, so " (" opens nothing
// in one, and a quoted record ends in its closing quotation mark, so a
// drawing that ends in a parenthesis after a space can only end in a note.
func WithoutNote(shown string) string {
	if !strings.HasSuffix(shown, ")") {
		return shown
	}
	i := strings.LastIndex(shown, noteOpen)
	if i < 0 || !isName(shown[i+len(noteOpen):len(shown)-1]) {
		return shown
	}
	return shown[:i]
}

// isName reports whether s is made of what an ASCII host name is made of:
// letters, digits, hyphens, underscores and the dots between labels.
func isName(s string) bool {
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return s != ""
}
