// Package credname tells a name a credential is kept under from one it is
// not, by the name alone: a field's, a header's, a URL query parameter's.
//
// It is shared by what masks a credential and what forgets one: the http dry
// run, which shows a request with its credential headers masked, and the
// completion history, which never writes one down. They kept a list each.
// The history's began as Authorization alone and grew to every header an API
// takes its key in; the dry run kept Authorization, Cookie and
// Proxy-Authorization, so `rta http post … --header 'PRIVATE-TOKEN: glpat-…'
// --dry-run` printed GitLab's token in clear, on the terminal and in -o json,
// while the history knew it for a credential. One list, so that a word added
// for one surface covers the other.
//
// A name heuristic and never a gate: the value is never read, and nothing is
// allowed or refused by it. Wrong in the direction it leans, it masks a header
// that held nothing secret, or leaves out a suggestion nobody sees.
package credname

import "strings"

// fieldWords are the names a field carries when it holds a credential,
// whatever type its author declared it as.
//
// A backstop, not the rule. `Type: Secret` is the rule, and it is what keeps a
// value out of a form's plain box as well as out of the completion history —
// but a declaration can be wrong, and one was: builtin/http declared `bearer`
// and `basic` as plain Strings, so a token went to disk and came back on a
// completion list rendered in the clear. That is fixed at the declaration;
// this is here because the next plugin to get it wrong will not be one this
// repo can edit.
var fieldWords = []string{
	"secret", "token", "password", "passwd", "credential", "auth", "bearer", "apikey",
}

// headerWords are what a header that carries a credential has in its name,
// separators removed: the words a field's has, and the ones particular to
// headers — the key, the cookie, the session, the signature.
var headerWords = append([]string{"cookie", "session", "signature", "key"}, fieldWords...)

// Field reports whether a field, an input or a setting, named name holds a
// credential.
func Field(name string) bool {
	return containsAny(strings.ToLower(name), fieldWords)
}

// Header reports whether a header named name carries a credential. The
// separators go first, so that `X-Api-Key`, `x_api_key` and `XApiKey` are one
// word.
func Header(name string) bool {
	return containsAny(fold(name), headerWords)
}

// Param reports whether a URL query parameter named name carries a
// credential: one named as such a header is (`?api_key=…`,
// `?access_token=…`), or `sig`, the signature of a shared-access URL, a word
// too short to look for inside other names.
func Param(name string) bool {
	folded := fold(name)
	return folded == "sig" || containsAny(folded, headerWords)
}

func fold(name string) string {
	return strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(name))
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
