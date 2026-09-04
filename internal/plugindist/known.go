package plugindist

import "github.com/this-is-tobi/rule-them-all/pkg/view"

// The first-party index, known by name.
//
// For as long as rta shipped, there was no default index and the reason was
// concrete: the official repository did not exist, and hardcoding its future
// URL would have made rta reach for a name nobody controlled on the day
// somebody registered it. That reason ended when the repository was pushed.
//
// What survives is the shape of the decision. rta still attaches nothing on
// its own — `rta plugin index add official` is typed, once, and reaches one
// URL rta ships — and the name is *reserved*: `index add official <elsewhere>`
// is refused, so `official` in `index list`, in a lock entry or in a search
// result means the repository rta names here and cannot mean anything else.
// Without that, the word would be an assertion of provenance anybody could
// attach to any URL, which is the reason nothing in rta drew a rule from it
// before.
//
// Not auto-attached on first use, deliberately: that would have rta reach a
// network destination the operator never named, and the docs promise it does
// not.
//
// A var rather than a const so a test can point the name at a repository on
// this machine; nothing outside this package writes it.
var knownIndexes = map[string]string{
	"official": "https://github.com/this-is-tobi/rta-plugins",
}

// KnownIndexURL is the repository rta ships for name, if it ships one.
func KnownIndexURL(name string) (string, bool) {
	url, ok := knownIndexes[name]
	return url, ok
}

// knownIndexHint is the one line every "no index is attached" refusal
// carries: the command that attaches the first-party index, and the general
// form for any other.
func knownIndexHint() string {
	return "`rta plugin index add official` attaches the first-party index (" +
		knownIndexes["official"] + "); `rta plugin index add <name> <repository>` any other"
}

// NoIndexAttached is the refusal every command that needs an index returns
// when none is attached, so the places that used to spell it out cannot
// disagree about which command fixes it.
func NoIndexAttached() *view.Error {
	return view.Errorf("plugin.index.none", "no index is attached").WithHint(knownIndexHint())
}
