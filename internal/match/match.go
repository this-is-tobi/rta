// Package match is the one rule for "which of these did the person mean by
// those words": the TUI's search box, `rta explain`'s did-you-mean and the
// hint an unknown command gets all ask it, so a word finds the same thing
// wherever it is typed.
//
// It exists because each of them had its own, and they disagreed. The TUI
// matched a query as a substring of the ID or the summary, so `ports` found
// kv.env first (its summary says "exports") and the capability that scans
// ports below it, while `todo`, `ssl`, `secret` and `disk space` found nothing
// at all; `rta explain` scored shared dotted segments and happened to suggest
// sys.disk for `disk space`; and the unknown-command error did not look at the
// catalogue.
//
// The rule is words, not letters. A query is split into words on anything that
// is not a letter or a digit, so `net.hosts`, `net-hosts` and `net hosts` are
// the same query, and a word has to start a word of the ID, a keyword or the
// summary to count in full. Every word of a query has to find something (Find),
// and a query that finds nothing as a whole can still be answered with the
// nearest things (Nearest). An ID's own segments outrank prose: `ps` is the
// leaf of sys.ps and a word in no summary at all, and it should win either way.
// A word found only in the middle of another (`ports` in "exports") still
// counts, last, because somebody typing a fragment of a name means it.
package match

import (
	"sort"
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/internal/near"
)

// Item is one thing a query can find.
type Item struct {
	// ID is a dotted name — sys.ps, or grant.revoke for a command. Its
	// segments weigh the most, the last one a little more than the rest, since
	// a leaf is what a person means to type.
	ID string
	// Summary is the sentence that says what it is for.
	Summary string
	// Keywords are words that name it without being in either: `todo` for a
	// note, `ssl` for a certificate, `freeze` for a lock.
	Keywords []string
}

// Result is one item a query found: where it is in the slice that was asked
// about, and how well it was found. Higher is better.
type Result struct {
	Index int
	Score int
}

// Where a word was found, best first. The gaps are wide enough that a word at
// a better place always beats any number of weaker ones: a query is a few words
// and these are summed.
const (
	leafExact     = 100 // the whole last segment of the ID
	segmentExact  = 90  // the whole of an earlier segment
	partExact     = 80  // the whole of a hyphen-separated part of a segment
	keywordExact  = 85  // a keyword
	segmentPrefix = 70  // the start of a segment
	partPrefix    = 60  // the start of a hyphen-separated part
	keywordPrefix = 55  // the start of a keyword
	summaryExact  = 50  // a word of the summary
	summaryPrefix = 40  // the start of a word of the summary
	withinID      = 20  // inside a segment, after its first letter
	withinText    = 10  // inside a keyword or a word of the summary
	typedMore     = 45  // an item's word that the query only extends: `cpuu`, `ports`
	typo          = 45  // an item's word one or two letters from the query's
)

// Whole is the least that Find scores a single word found as a whole: the last
// segment of an ID, an earlier one, or a keyword. A caller that has to say "the
// word is this thing's name" rather than "this thing mentions the word" — the
// unknown-command hint, which names a command, never one whose summary happens
// to contain what was typed — asks for at least this.
const Whole = keywordExact

// Likely is the least that Nearest scores a single word for an item worth
// offering a person who typed it: a start of a segment, a whole word of the
// summary, a word one typo away. A word found only inside another, or only as
// the start of a summary word, scores below it.
const Likely = typo + 30

// idPrefixBonus is what a query that is literally the start of the ID earns on
// top: `net.hosts.l` is not a bag of words, it is a place in the tree.
const idPrefixBonus = 50

// penalty is what a word found only after its trailing s was dropped loses, so
// `notes` finds note and `ports` finds port, a little below finding what was
// typed.
const penalty = 10

// Words splits s into the lower-cased words a query or an ID is made of.
func Words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Find is the items that every word of query finds, best first. Equal scores
// keep the order they were given in, so a caller's own ordering (the registry's)
// is the tie-break. An empty query finds nothing.
func Find(query string, items []Item) []Result {
	words := Words(query)
	if len(words) == 0 {
		return nil
	}
	prefix := normalised(query)
	var out []Result
	for i, it := range items {
		if score, ok := scoreAll(words, it); ok {
			out = append(out, Result{Index: i, Score: score + bonus(prefix, it)})
		}
	}
	return ranked(out)
}

// Nearest is for a query that Find answered with nothing: the items that at
// least one word of it finds, or comes within a typo of, with the ones that
// find more of the words first. It is what turns `rta explain sys.cpuu` into
// sys.cpu and `disk space` into sys.disk.
func Nearest(query string, items []Item) []Result {
	words := Words(query)
	if len(words) == 0 {
		return nil
	}
	prefix := normalised(query)
	var out []Result
	for i, it := range items {
		parts, tokens := it.tokens()
		total, found := 0, 0
		for _, w := range words {
			s := wordScore(w, parts, tokens, true)
			if s == 0 {
				continue
			}
			total += s
			found++
		}
		if found == 0 {
			continue
		}
		// Words found count for more than their scores say: an item that two of
		// three words find is a better answer than one that one of them finds well.
		out = append(out, Result{Index: i, Score: total + found*30 + bonus(prefix, it)})
	}
	return ranked(out)
}

func ranked(out []Result) []Result {
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func normalised(query string) string { return strings.Join(Words(query), ".") }

func bonus(prefix string, it Item) int {
	if strings.Contains(prefix, ".") && strings.HasPrefix(strings.Join(Words(it.ID), "."), prefix) {
		return idPrefixBonus
	}
	return 0
}

func scoreAll(words []string, it Item) (int, bool) {
	parts, tokens := it.tokens()
	total := 0
	for _, w := range words {
		s := wordScore(w, parts, tokens, false)
		if s == 0 {
			return 0, false
		}
		total += s
	}
	return total, true
}

// idPart is one segment of an ID and the hyphen-separated parts inside it.
type idPart struct {
	whole string
	words []string
	leaf  bool
}

// text is a word of prose, or a keyword.
type text struct {
	word    string
	keyword bool
}

func (it Item) tokens() ([]idPart, []text) {
	segments := strings.FieldsFunc(strings.ToLower(it.ID), func(r rune) bool { return r == '.' || r == ' ' || r == '/' })
	parts := make([]idPart, len(segments))
	for i, s := range segments {
		parts[i] = idPart{whole: s, words: Words(s), leaf: i == len(segments)-1}
	}
	var tokens []text
	for _, k := range it.Keywords {
		for _, w := range Words(k) {
			tokens = append(tokens, text{w, true})
		}
	}
	for _, w := range Words(it.Summary) {
		tokens = append(tokens, text{w, false})
	}
	return parts, tokens
}

// wordScore is how well one query word is found in an item: the best place it
// is found, or 0. With near set, a word may also be found by an item's word it
// extends or sits a typo away from; Find leaves that off, because a search that
// shows `cpu` for `cpuu` is guessing, and a hint that does is helping.
func wordScore(w string, parts []idPart, tokens []text, near bool) int {
	best := directScore(w, parts, tokens)
	// "ports" is "port" with an s, and "notes" is "note": the plural is not
	// another word. Not for a word short enough that dropping a letter makes it
	// something else.
	if best == 0 && len(w) > 3 && strings.HasSuffix(w, "s") {
		if s := directScore(w[:len(w)-1], parts, tokens); s > penalty {
			best = s - penalty
		}
	}
	if best == 0 && near {
		best = nearScore(w, parts, tokens)
	}
	return best
}

func directScore(w string, parts []idPart, tokens []text) int {
	best := 0
	raise := func(s int) {
		if s > best {
			best = s
		}
	}
	for _, p := range parts {
		switch {
		case p.whole == w && p.leaf:
			raise(leafExact)
		case p.whole == w:
			raise(segmentExact)
		case strings.HasPrefix(p.whole, w):
			raise(segmentPrefix)
		}
		for _, part := range p.words {
			switch {
			case part == w:
				raise(partExact)
			case strings.HasPrefix(part, w):
				raise(partPrefix)
			}
		}
		if strings.Contains(p.whole, w) {
			raise(withinID)
		}
	}
	for _, t := range tokens {
		switch {
		case t.word == w && t.keyword:
			raise(keywordExact)
		case t.word == w:
			raise(summaryExact)
		case strings.HasPrefix(t.word, w) && t.keyword:
			raise(keywordPrefix)
		case strings.HasPrefix(t.word, w):
			raise(summaryPrefix)
		case strings.Contains(t.word, w):
			raise(withinText)
		}
	}
	return best
}

// nearScore is for a word that is not in the item as typed: one the item's
// own word is the start of (`cpuu` for cpu, `ports` for port) or is a letter or
// two away from (`reovke`).
func nearScore(w string, parts []idPart, tokens []text) int {
	words := make([]string, 0, len(parts)*2+len(tokens))
	for _, p := range parts {
		words = append(words, p.whole)
		words = append(words, p.words...)
	}
	for _, t := range tokens {
		words = append(words, t.word)
	}
	for _, have := range words {
		if len(have) >= 3 && strings.HasPrefix(w, have) && extendsByALetterOrTwo(w, have) {
			return typedMore
		}
		if len(w) >= 3 && len(have) >= 2 && Distance(w, have) <= max(1, len(w)/4) {
			return typo
		}
	}
	return 0
}

// extendsByALetterOrTwo says whether w is have with a slip at its end: one more
// letter, or the plural -es on a word long enough to take it. Any other tail
// is another word that happens to begin the same way — `postgres` begins with
// `post`, `login` with `log`, `backup` with `back` — and offering the shorter
// one for it is a wrong answer given with confidence.
func extendsByALetterOrTwo(w, have string) bool {
	extra := len(w) - len(have)
	return extra <= 1 || (extra == 2 && len(have) >= 4 && strings.HasSuffix(w, "es"))
}

// Distance is the optimal-string-alignment distance between two words, one
// function with the one internal/near answers "did you mean" by, so a typo is
// the same distance away wherever it is asked.
func Distance(a, b string) int { return near.Distance(a, b) }
