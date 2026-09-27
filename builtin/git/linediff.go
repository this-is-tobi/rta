package git

import (
	"context"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/diff"
)

// matchTime is how long one call spends matching lines, across every file it
// compares.
//
// **Bytes were bounded; the work done on them was not.** go-git's line diff
// (utils/diff.Do) runs with a one-hour timeout, and matching lines costs the
// product of the lines and the lines that changed: well inside the 16 MiB a
// diff reads of one file, a rewritten 2 MiB file cost one git.diff 7 s of
// CPU, and a megabyte of lines in a new order more than two minutes, from a
// file a caller need only have written. go-diff's own deadline does not close
// it: its containment check and its half-match run before the deadline is
// ever looked at, and a megabyte of repeated lines held one diff a minute
// under a two-second deadline. So the matching here is rta's own
// (diffLines), which looks at the deadline as it works; past it, what is
// left is shown whole, removed and added, which is a correct diff and a
// coarser one, and the caller is told which files were compared that way. A
// variable so a test can lower it.
var matchTime = 2 * time.Second

// matchDeadline is when a call stops matching lines: matchTime from now, or
// the caller's own deadline when that comes first.
func matchDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(matchTime)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

// lineRun is one run of a line diff: n lines of the old text kept (Equal)
// or removed (Delete), or n lines of the new one added (Add), starting at
// line a of the old text and line b of the new.
type lineRun struct {
	op   diff.Operation
	a, b int
	n    int
}

// lineChanges is how the new text differs from the old, line by line, and
// whether the matching was cut short: when it was, some lines both texts
// share may be shown removed and added rather than kept.
type lineChanges struct {
	old, new     string
	oldAt, newAt []int
	runs         []lineRun
	cut          bool
}

// text is what a run covers, as a patch shows it.
func (c *lineChanges) text(r lineRun) string {
	if r.op == diff.Add {
		return c.new[c.newAt[r.b]:c.newAt[r.b+r.n]]
	}
	return c.old[c.oldAt[r.a]:c.oldAt[r.a+r.n]]
}

// chunks are the runs in the shape go-git's unified encoder takes.
func (c *lineChanges) chunks() []diff.Chunk {
	out := make([]diff.Chunk, 0, len(c.runs))
	for _, r := range c.runs {
		out = append(out, &textChunk{content: c.text(r), op: r.op})
	}
	return out
}

// splitLines is where each line of text starts, each with its newline, and
// then where the last one ends: line i is text[at[i]:at[i+1]]. Offsets
// rather than copies, since a text here can be sixteen megabytes.
func splitLines(text string) []int {
	at := make([]int, 0, strings.Count(text, "\n")+2)
	for i := 0; i < len(text); {
		at = append(at, i)
		next := strings.IndexByte(text[i:], '\n')
		if next < 0 {
			break
		}
		i += next + 1
	}
	return append(at, len(text))
}

// diffLines is how new differs from old, line by line, matched until the
// deadline: a shortest edit script where the matching finished, and the lines
// it had not reached shown removed and added where it did not.
//
// Myers' algorithm, in the linear-space form that splits at the middle of an
// optimal path, over a number per distinct line. Two steps ahead of it bring
// the work down without changing the answer: the lines both texts begin and
// end with are matched first, and a line that appears in only one of the two
// is left out of the matching altogether, since it can only be removed or
// added. A full rewrite is then no matching at all. What neither step
// removes costs the product of the lines and the lines that differ, and that
// is what the deadline holds, checked as the work is done rather than
// between steps, so that no one step can run past it for long.
func diffLines(old, new string, deadline time.Time) lineChanges {
	c := lineChanges{old: old, new: new, oldAt: splitLines(old), newAt: splitLines(new)}
	ids := map[string]int32{}
	number := func(text string, at []int) []int32 {
		out := make([]int32, len(at)-1)
		for i := range out {
			line := text[at[i]:at[i+1]]
			id, ok := ids[line]
			if !ok {
				id = int32(len(ids)) //nolint:gosec // one per distinct line of two texts held to maxDiffBytes each
				ids[line] = id
			}
			out[i] = id
		}
		return out
	}
	oldIDs, newIDs := number(old, c.oldAt), number(new, c.newAt)

	inOld, inNew := make([]bool, len(ids)), make([]bool, len(ids))
	for _, id := range oldIDs {
		inOld[id] = true
	}
	for _, id := range newIDs {
		inNew[id] = true
	}
	var oldKept, newKept []int
	m := matcher{deadline: deadline}
	for i, id := range oldIDs {
		if inNew[id] {
			oldKept = append(oldKept, i)
			m.a = append(m.a, id)
		}
	}
	for i, id := range newIDs {
		if inOld[id] {
			newKept = append(newKept, i)
			m.b = append(m.b, id)
		}
	}
	m.compare(0, len(m.a), 0, len(m.b))
	c.cut = m.cut

	// The matched lines, back in the texts' own numbering; everything
	// between two of them is removed from the old text and added from the
	// new, in that order, as git and go-git both show a change.
	na, nb := 0, 0
	gap := func(a, b int) {
		if a > na {
			c.runs = append(c.runs, lineRun{op: diff.Delete, a: na, b: nb, n: a - na})
		}
		if b > nb {
			c.runs = append(c.runs, lineRun{op: diff.Add, a: a, b: nb, n: b - nb})
		}
	}
	for _, r := range m.matches {
		for k := range r.n {
			a, b := oldKept[r.a+k], newKept[r.b+k]
			gap(a, b)
			if last := len(c.runs) - 1; last >= 0 && c.runs[last].op == diff.Equal &&
				c.runs[last].a+c.runs[last].n == a && c.runs[last].b+c.runs[last].n == b {
				c.runs[last].n++
			} else {
				c.runs = append(c.runs, lineRun{op: diff.Equal, a: a, b: b, n: 1})
			}
			na, nb = a+1, b+1
		}
	}
	gap(len(oldIDs), len(newIDs))
	return c
}

// matcher finds the longest common subsequence of a and b, as runs of
// matched positions in order, until its deadline.
type matcher struct {
	a, b     []int32
	deadline time.Time
	matches  []lineRun
	// work counts the steps since the clock was last read, so that it is
	// read every few thousand steps rather than on every one, and expired
	// says the deadline has passed and nothing more is matched.
	work    int
	expired bool
	cut     bool
	fwd     []int
	rev     []int
}

// checkEvery is how many steps of matching go by between two readings of the
// clock: a few tens of microseconds of work, against a reading that costs as
// much as a few dozen steps.
const checkEvery = 1 << 14

// spend accounts for n steps of matching and reports whether there is time
// left for more.
func (m *matcher) spend(n int) bool {
	if m.work += n + 1; m.work >= checkEvery {
		m.work = 0
		if time.Now().After(m.deadline) {
			m.expired = true
		}
	}
	return !m.expired
}

func (m *matcher) match(a, b, n int) {
	if last := len(m.matches) - 1; last >= 0 &&
		m.matches[last].a+m.matches[last].n == a && m.matches[last].b+m.matches[last].n == b {
		m.matches[last].n += n
		return
	}
	m.matches = append(m.matches, lineRun{op: diff.Equal, a: a, b: b, n: n})
}

// compare matches a[a0:a1] against b[b0:b1].
func (m *matcher) compare(a0, a1, b0, b1 int) {
	prefix := 0
	for a0+prefix < a1 && b0+prefix < b1 && m.a[a0+prefix] == m.b[b0+prefix] {
		prefix++
	}
	if prefix > 0 {
		m.match(a0, b0, prefix)
		a0, b0 = a0+prefix, b0+prefix
	}
	suffix := 0
	for a1-suffix > a0 && b1-suffix > b0 && m.a[a1-suffix-1] == m.b[b1-suffix-1] {
		suffix++
	}
	a1, b1 = a1-suffix, b1-suffix
	if a0 < a1 && b0 < b1 {
		if m.expired {
			m.cut = true
		} else if x, y, ok := m.middle(a0, a1, b0, b1); ok {
			m.compare(a0, x, b0, y)
			m.compare(x, a1, y, b1)
		} else {
			m.cut = true
		}
	}
	if suffix > 0 {
		m.match(a1, b1, suffix)
	}
}

// middle is a point on a shortest edit path through a[a0:a1] and
// b[b0:b1], found where a path from the start and a path from the end first
// meet, strictly inside the box so that both halves are smaller than it; ok
// is false when the deadline passed first. The box begins and ends with a
// difference, which compare has made sure of: it is what keeps the point
// off both corners.
//
// fwd[k] is how far along a the furthest path from the start on diagonal k
// (x - y = k) has reached, and rev[k] the same for the path from the end,
// measured from the end on the mirrored diagonal; -1 is a diagonal no path
// of that length reaches. Diagonals are kept within the box, and the entry
// either side of the ones an iteration wrote is set to -1, so that the next
// iteration never reads a value an earlier one left behind.
func (m *matcher) middle(a0, a1, b0, b1 int) (int, int, bool) {
	n, mm := a1-a0, b1-b0
	delta := n - mm
	odd := delta&1 != 0
	off := mm + 2
	size := n + mm + 5
	if cap(m.fwd) < size {
		m.fwd, m.rev = make([]int, size), make([]int, size)
	}
	fwd, rev := m.fwd[:size], m.rev[:size]
	// A path "one before" the start on diagonal 1, so that the first step
	// down from it lands on (0, 0).
	fwd[off-1], fwd[off+1] = -1, 0
	rev[off-1], rev[off+1] = -1, 0
	a, b := m.a[a0:a1], m.b[b0:b1]
	var revLo, revHi int
	for d := 0; d <= (n+mm+1)/2; d++ {
		lo, hi := bounds(d, n, mm)
		for k := hi; k >= lo; k -= 2 {
			x := step(fwd, off, k, n, mm)
			if x < 0 {
				fwd[off+k] = -1
				continue
			}
			y, start := x-k, x
			for x < n && y < mm && a[x] == b[y] {
				x, y = x+1, y+1
			}
			fwd[off+k] = x
			if !m.spend(x - start) {
				return 0, 0, false
			}
			if r := delta - k; odd && d > 0 && r >= revLo && r <= revHi && (r-revLo)&1 == 0 &&
				rev[off+r] >= 0 && x+rev[off+r] >= n {
				return a0 + x, b0 + y, true
			}
		}
		fwd[off+lo-2], fwd[off+hi+2] = -1, -1
		fwdLo, fwdHi := lo, hi

		revLo, revHi = lo, hi
		for r := hi; r >= lo; r -= 2 {
			x := step(rev, off, r, n, mm)
			if x < 0 {
				rev[off+r] = -1
				continue
			}
			y, start := x-r, x
			for x < n && y < mm && a[n-1-x] == b[mm-1-y] {
				x, y = x+1, y+1
			}
			rev[off+r] = x
			if !m.spend(x - start) {
				return 0, 0, false
			}
			if k := delta - r; !odd && k >= fwdLo && k <= fwdHi && (k-fwdLo)&1 == 0 &&
				fwd[off+k] >= 0 && x+fwd[off+k] >= n {
				return a0 + n - x, b0 + mm - y, true
			}
		}
		rev[off+lo-2], rev[off+hi+2] = -1, -1
	}
	return 0, 0, false
}

// bounds is the diagonals a path of d edits can end on inside an n by m box:
// -d to d in steps of two, as far as the box reaches.
func bounds(d, n, m int) (lo, hi int) {
	lo, hi = -d, d
	if lo < -m {
		lo = -m + (d-m)&1
	}
	if hi > n {
		hi = n - (d-n)&1
	}
	return lo, hi
}

// step is where a path of one more edit starts on diagonal k: one down from
// diagonal k+1, or one along from diagonal k-1, whichever reaches further
// and stays inside the box; -1 when neither does.
func step(v []int, off, k, n, m int) int {
	x := -1
	if down := v[off+k+1]; down >= 0 && down-k <= m {
		x = down
	}
	if along := v[off+k-1]; along >= 0 && along+1 <= n && along+1 > x {
		x = along + 1
	}
	return x
}
