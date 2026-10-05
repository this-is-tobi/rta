package tui

import (
	"strings"

	"github.com/this-is-tobi/rta/internal/match"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The one question the dashboard's search bar and the catalogue's filter box
// both ask: which of these did the person mean by those words.
//
// They used to carry a copy of the answer each, and now ask rankItems, which
// hands the items to the shared matcher (internal/match) that `rta explain`
// and the unknown-command hint ask as well, so a word finds the same thing
// wherever it is typed and the rule is a function with a single body.

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
	return searchItem{ID: c.ID, Summary: c.Summary, Keywords: c.Keywords}
}

// rankItems is the indexes of the items query finds, best first; equal ones
// keep the order they were given in, which is the registry's. A blank query
// finds nothing.
//
// What finds an item is internal/match's rule, the one `rta explain` and an
// unknown command's hint ask: words, not letters, with an ID's own segments
// above prose and a keyword above a word of the summary. A query that finds
// nothing as a whole is answered with what it most likely misspells, once it
// is long enough for that not to be chance (`cpuu` is sys.cpu, `pg` is no typo
// of anything), as the hint does.
func rankItems(query string, items []searchItem) []int {
	found := make([]match.Item, len(items))
	for i, it := range items {
		found[i] = match.Item(it)
	}
	results := match.Find(query, found)
	if len(results) == 0 && len([]rune(strings.TrimSpace(query))) >= minTypoQuery {
		for _, r := range match.Nearest(query, found) {
			if r.Score >= match.Likely {
				results = append(results, r)
			}
		}
	}
	out := make([]int, len(results))
	for n, r := range results {
		out[n] = r.Index
	}
	return out
}

// minTypoQuery is the shortest query that is offered what it might misspell.
const minTypoQuery = 4

// filterTarget is what the catalogue's list filters a capability by: the ID, a
// space and the summary, and after a unit separator the keywords that name it
// without being in either, which the list hands back to catalogueFilter whole.
func filterTarget(c plugin.Capability) string {
	target := c.ID + " " + c.Summary
	if len(c.Keywords) > 0 {
		target += keywordSeparator + strings.Join(c.Keywords, " ")
	}
	return target
}

// keywordSeparator cannot be in a summary or a keyword, which are one line of
// words.
const keywordSeparator = "\x1f"

// itemOfTarget is the search item filterTarget wrote.
func itemOfTarget(target string) searchItem {
	var it searchItem
	text, keywords, _ := strings.Cut(target, keywordSeparator)
	it.ID, it.Summary, _ = strings.Cut(text, " ")
	it.Keywords = strings.Fields(keywords)
	return it
}
