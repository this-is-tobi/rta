package tui

import (
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The one question the dashboard's search bar and the catalogue's filter box
// both ask: which of these did the person mean by those words.
//
// They used to carry a copy of the answer each and are held to one by asking
// rankItems, so that the rule is a function with a single body. That body is
// the only part of the TUI that knows how a word finds a capability; the
// callers hand it items and take indexes back, which is the shape of the
// shared matcher `rta explain` and the unknown-command hint ask as well. When
// the TUI uses that one, rankItems is what changes and nothing around it does.

// searchItem is what a query can find a capability by: its ID, the sentence
// that says what it is for, and the words that name it without being in
// either. Field for field the matcher's item, so the one is a conversion of
// the other.
type searchItem struct {
	ID       string
	Summary  string
	Keywords []string
}

func itemOf(c plugin.Capability) searchItem {
	return searchItem{ID: c.ID, Summary: c.Summary}
}

// rankItems is the indexes of the items query finds, best first; equal ones
// keep the order they were given in, which is the registry's. A blank query
// finds nothing.
func rankItems(query string, items []searchItem) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var lead, rest []int
	for i, it := range items {
		switch tier, ok := matchCapability(it.ID, it.Summary, q); {
		case !ok:
		case tier == matchPrefix:
			lead = append(lead, i)
		default:
			rest = append(rest, i)
		}
	}
	return append(lead, rest...)
}

// How a query found a capability. The dashboard's search and the catalogue's
// filter answer one question, so they share the rule: they ranked differently
// when the catalogue used the list's fuzzy matcher, which found "gen" in
// "agent.deny" and in "git.log" (g, then an e and an n from the summary) and
// put both above gen.password, with sixty-two of a hundred and twenty-eight
// rows matching a three-letter query.
const (
	matchPrefix = iota // the ID starts with the query
	matchWithin        // the query is somewhere in the ID or the summary
)

// matchCapability says whether q, already lower-cased and trimmed, finds a
// capability, and how well. Every word of q has to be somewhere in the ID or
// the summary, so "hosts list" finds net.hosts.list as the fuzzy matcher did
// without finding what only a scattering of its letters spelled.
func matchCapability(id, summary, q string) (tier int, ok bool) {
	words := strings.Fields(q)
	if len(words) == 0 {
		return 0, false
	}
	id = strings.ToLower(id)
	haystack := id + " " + strings.ToLower(summary)
	for _, w := range words {
		if !strings.Contains(haystack, w) {
			return 0, false
		}
	}
	if strings.HasPrefix(id, words[0]) {
		return matchPrefix, true
	}
	return matchWithin, true
}
