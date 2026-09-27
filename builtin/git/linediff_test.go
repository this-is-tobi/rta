package git

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/diff"
)

// applied is the two texts a line diff describes, rebuilt from its runs
// alone, and how many lines it removed or added.
func applied(t *testing.T, c lineChanges) (old, new string, edits int) {
	t.Helper()
	var o, n strings.Builder
	na, nb := 0, 0
	for _, r := range c.runs {
		if r.n <= 0 {
			t.Fatalf("an empty run: %+v", r)
		}
		if r.a != na || r.b != nb {
			t.Fatalf("run %+v does not start where the last ended (%d, %d)", r, na, nb)
		}
		text := c.text(r)
		switch r.op {
		case diff.Equal:
			o.WriteString(text)
			n.WriteString(text)
			na, nb = na+r.n, nb+r.n
		case diff.Delete:
			o.WriteString(text)
			na += r.n
			edits += r.n
		case diff.Add:
			n.WriteString(text)
			nb += r.n
			edits += r.n
		}
	}
	return o.String(), n.String(), edits
}

// shortestEdits is the fewest lines removed and added that turn a into b,
// by the textbook table: the check the matching is held to.
func shortestEdits(a, b []string) int {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return len(a) + len(b) - 2*lcs[0][0]
}

func randomText(r *rand.Rand, lines, alphabet int) (string, []string) {
	var out []string
	for range lines {
		out = append(out, string(rune('a'+r.Intn(alphabet)))+"\n")
	}
	return strings.Join(out, ""), out
}

// Every diff rebuilds both texts, keeps its runs in order, and, given the
// time, removes and adds no more lines than the fewest that do it — over
// thousands of random pairs of every shape the matching has a branch for:
// empty sides, one side far longer, few distinct lines and many.
func TestLineDiffIsAShortestEditScript(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	far := time.Now().Add(time.Hour)
	for i := range 5000 {
		a, al := randomText(r, r.Intn(30), 1+r.Intn(6))
		b, bl := randomText(r, r.Intn(30), 1+r.Intn(6))
		if i%7 == 0 {
			b, bl = a, al
		}
		c := diffLines(a, b, far)
		gotOld, gotNew, edits := applied(t, c)
		if gotOld != a || gotNew != b {
			t.Fatalf("the diff of %q and %q rebuilds %q and %q", a, b, gotOld, gotNew)
		}
		if want := shortestEdits(al, bl); edits != want || c.cut {
			t.Fatalf("%q -> %q: %d lines removed and added, cut %v; the fewest is %d", a, b, edits, c.cut, want)
		}
	}
}

// A last line with no newline is its own line, different from the same text
// with one, as git and go-git both diff it.
func TestLineDiffKeepsALastLineWithoutANewline(t *testing.T) {
	c := diffLines("one\ntwo", "one\ntwo\n", time.Now().Add(time.Hour))
	old, new, edits := applied(t, c)
	if old != "one\ntwo" || new != "one\ntwo\n" || edits != 2 {
		t.Fatalf("rebuilt %q and %q with %d edits, want the last line removed and added", old, new, edits)
	}
}

// Past the deadline the diff is still a diff: what the matching had not
// reached is shown removed and added, both texts are rebuilt, and it says it
// was cut short.
func TestLineDiffPastItsDeadlineIsCoarseButCorrect(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	a, _ := randomText(r, 400, 3)
	b, _ := randomText(r, 400, 3)
	c := diffLines(a, b, time.Now().Add(-time.Second))
	old, new, _ := applied(t, c)
	if old != a || new != b {
		t.Fatal("a diff cut short does not rebuild its texts")
	}
	if !c.cut {
		t.Error("a diff that never got to match says it finished")
	}
	// The lines both begin and end with are matched whatever the deadline.
	c = diffLines("same\n"+a+"end\n", "same\n"+b+"end\n", time.Now().Add(-time.Second))
	if first, last := c.runs[0], c.runs[len(c.runs)-1]; first.op != diff.Equal || last.op != diff.Equal {
		t.Errorf("a diff cut short dropped the lines both texts share at either end: %+v ... %+v", first, last)
	}
}

// The matching stops at its deadline whatever the texts hold. go-diff's
// deadline did not hold on repeated lines: its containment check and its
// half-match run before the deadline is looked at, and a megabyte of them
// held one diff a minute under a two-second deadline.
func TestLineDiffHoldsItsDeadlineOnAnyText(t *testing.T) {
	if testing.Short() {
		t.Skip("measures time")
	}
	r := rand.New(rand.NewSource(3))
	shapes := map[string][2]string{
		"repeated lines": {"b\n" + strings.Repeat("a\n", 500000), strings.Repeat("a\n", 250000) + "c\n"},
	}
	x, _ := randomText(r, 200000, 2)
	y, _ := randomText(r, 200000, 2)
	shapes["two lines in a random order"] = [2]string{x, y}
	for name, texts := range shapes {
		start := time.Now()
		c := diffLines(texts[0], texts[1], start.Add(200*time.Millisecond))
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("%s: %v under a 200ms deadline", name, took)
		}
		if old, new, _ := applied(t, c); old != texts[0] || new != texts[1] {
			t.Errorf("%s: the diff does not rebuild its texts", name)
		}
	}
}
