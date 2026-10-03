// Package headerlist puts back together the header lines a comma-separated list
// cut apart.
//
// It is shared by the http capabilities, which send the headers, and by the
// completion history, which remembers them: both read the same list of pieces,
// and a piece that is half of a header is wrong in either place.
package headerlist

import (
	"net/netip"
	"regexp"
	"strings"
)

// headerStart is the beginning of a header line: a field name, which is a run of
// token characters, and its colon. A piece of a list that does not begin this
// way is not a header of its own.
var headerStart = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+\\s*:")

// Join gives back the headers a person meant from the list a surface that
// splits on commas made of them.
//
// A header's value is allowed commas, and the ordinary ones have them: `Accept:
// text/html, application/json`, `Cache-Control: no-cache, no-store`, `Via: 1.1
// a, 1.1 b`. The command line and the form both take a list as comma-separated
// text, so each of those arrived as two entries, the second of which — `
// application/json` — is no header, and the request was refused for "invalid
// header". A piece that does not begin like a header is the rest of the one
// before it, and is put back with the comma it was cut at.
func Join(pieces []string) []string {
	var out []string
	for _, p := range pieces {
		if len(out) > 0 && !beginsHeader(p) {
			out[len(out)-1] += "," + p
			continue
		}
		out = append(out, p)
	}
	return out
}

// beginsHeader says whether a piece of the list is a header of its own.
//
// An IPv6 address passes headerStart: `2001` is a run of token characters and
// the colon after it ends the "name". `X-Forwarded-For: 203.0.113.1,
// 2001:db8::1` therefore sent a header called 2001 and dropped the address from
// the one it belonged to, without a word, where the refusal it replaced at
// least said something was wrong. A header named by nothing but hex digits and
// whose whole text is an address is the address.
func beginsHeader(piece string) bool {
	piece = strings.TrimSpace(piece)
	if !headerStart.MatchString(piece) {
		return false
	}
	if _, err := netip.ParseAddr(piece); err == nil {
		return false
	}
	_, err := netip.ParsePrefix(piece)
	return err != nil
}
