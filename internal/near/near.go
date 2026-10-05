// Package near answers "did you mean" for a word typed against a closed set:
// the config key somebody misspelt, the setting they guessed.
//
// A package of its own because the answer is wanted by a leaf that must not
// import the command tree (internal/config, which names the misspelt keys of a
// file) and by the commands that write those keys (internal/app), and the same
// rule has to hold in both or one of them suggests what the other would not.
package near

import "strings"

// Distance is the optimal-string-alignment distance between two words:
// Levenshtein's insertions, deletions and substitutions, and a swap of two
// neighbouring letters as one edit, since that is how most typos are made.
func Distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// Word is the candidate word was most likely meant to be, or "" when none is
// close enough to be worth saying.
//
// Close is a small fraction of the word, not a fixed number of edits: two
// edits are the whole of a two-letter name, so a suggestion is kept when it is
// one edit away, or a third of the word's length for a longer one. A prefix of
// three letters or more is kept whatever its length, which is how `col` finds
// `columns`. The nearest wins, and the first of equals in the order given, so
// a caller that lists its candidates in a meaningful order gets a stable
// answer.
func Word(word string, candidates []string) string {
	word = strings.ToLower(word)
	allowed := max(1, len([]rune(word))/3)
	best, bestDistance := "", allowed+1
	for _, c := range candidates {
		lc := strings.ToLower(c)
		d := Distance(word, lc)
		if len(word) >= 3 && strings.HasPrefix(lc, word) {
			d = min(d, 1)
		}
		if d < bestDistance {
			best, bestDistance = c, d
		}
	}
	return best
}
