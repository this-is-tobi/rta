package plugindist

import (
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

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
// not. What rta may do is ask: `rta plugin install pg` with no index attached
// offers to attach this one, at a terminal, naming the URL, and a "yes" typed
// there is the operator naming the destination.
//
// A var rather than a const so a test can point the name at a repository on
// this machine; nothing outside this package writes it.
var knownIndexes = map[string]string{
	FirstPartyIndex: "https://github.com/this-is-tobi/rta-plugins",
}

// FirstPartyIndex is the name the first-party index is attached under.
const FirstPartyIndex = "official"

// firstParty is every plugin that repository publishes, by the name `rta
// plugin install` takes.
//
// Static, and in the binary, because the moment it is needed is the moment
// nothing else knows it: on a machine with no index attached, `rta pg` has no
// manifest to read, and fetching one to answer a word that may be a typo would
// reach the network for a guess. Before this the plugin hint was given or
// withheld by how close the word lay to a command: `rta pg` was told it meant
// "pkg" and `rta docker` that it meant "doctor", while `rta redis`, a word no
// command resembles, was told it was a plugin.
//
// A claim about names and nothing else: it grants nothing, installs nothing
// and is not consulted by anything that decides what may run. Whether a name
// is installable is still the attached index's statement, and what an install
// trusts is still the digest of the bytes it verified.
var firstParty = []string{
	"cnpg", "docker", "etcd", "keycloak", "kube", "mariadb",
	"mysql", "pg", "qdrant", "redis", "s3", "vault",
}

// firstPartyAliases are other names a first-party plugin is called by, and
// only the ones nobody would dispute: the long spelling of the service, which
// is what a person types before they have learned rta's short one.
var firstPartyAliases = map[string]string{
	"postgres":   "pg",
	"postgresql": "pg",
	"k8s":        "kube",
	"kubernetes": "kube",
}

// FirstPartyNames lists the first-party plugins, sorted.
func FirstPartyNames() []string { return slices.Clone(firstParty) }

// FirstParty is the first-party plugin a word names, spelled as `rta plugin
// install` takes it: the plugin's own name, or one it is known by.
func FirstParty(word string) (name string, ok bool) {
	word = strings.ToLower(word)
	if slices.Contains(firstParty, word) {
		return word, true
	}
	name, ok = firstPartyAliases[word]
	return name, ok
}

// FirstPartyHint answers a word that names a first-party plugin which is not
// installed: that it is one, and the command that gets it. "" for any other
// word.
//
// The command is the install and not the index attach, because the install is
// the whole of what the person wants and attaches the index itself when it has
// to (a terminal is asked first; anything else is told how).
func FirstPartyHint(word string) string {
	name, ok := FirstParty(word)
	if !ok {
		return ""
	}
	install := "`rta plugin install " + name + "` installs it"
	if strings.ToLower(word) != name {
		return word + " is the first-party plugin " + name + " — " + install
	}
	return name + " is a first-party plugin — " + install
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
