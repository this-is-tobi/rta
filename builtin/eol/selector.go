package eol

import (
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A cycle selector is what eol.check's cycle argument and the part after
// `@` in a watch entry hold: one cycle by name, or a numeric range of them.
//
// `13..16` is every cycle from 13 to 16, `15..` is 15 and newer, `..16` is
// 16 and older, every bound inclusive — which is what "13 to 16" means when
// somebody says it. Two dots rather than `>=` and `<`, because a selector
// is typed in two places where those characters cost something: a shell,
// where `<16` is a redirection until quoted, and a YAML flow list, where
// `[postgresql@>=13,<17]` splits on its own comma before rta ever reads it.
//
// Only a cycle whose name is a dotted number can fall inside a range. A
// codename (bookworm, Tahoe) has no order for a range to use, so it never
// matches one — and can still be named exactly, the way it always could.
type cycleSelector struct {
	exact string
	// lo and hi are the parsed bounds, nil for an open end. ranged tells a
	// range apart from an exact name, since both ends may be open only in
	// the form parseSelector refuses.
	lo, hi []int
	ranged bool
}

func parseSelector(s string) (cycleSelector, *view.Error) {
	s = strings.TrimSpace(s)
	before, after, found := strings.Cut(s, "..")
	if !found {
		return cycleSelector{exact: s}, nil
	}
	if before == "" && after == "" {
		return cycleSelector{}, view.Errorf("eol.cycle.selector", "%q selects nothing: a range needs at least one bound", s).
			WithHint("13..16, 15.. or ..16")
	}
	sel := cycleSelector{ranged: true}
	var ok bool
	if before != "" {
		if sel.lo, ok = numericCycle(before); !ok {
			return cycleSelector{}, view.Errorf("eol.cycle.selector", "%q is not a number a range can start from", before).
				WithHint("a range runs between numbered cycles — 13..16, 15.., ..16; a codename like bookworm is named on its own")
		}
	}
	if after != "" {
		if sel.hi, ok = numericCycle(after); !ok {
			return cycleSelector{}, view.Errorf("eol.cycle.selector", "%q is not a number a range can end at", after).
				WithHint("a range runs between numbered cycles — 13..16, 15.., ..16; a codename like bookworm is named on its own")
		}
	}
	if sel.lo != nil && sel.hi != nil && compareNumeric(sel.lo, sel.hi) > 0 {
		return cycleSelector{}, view.Errorf("eol.cycle.selector", "%q runs backwards: the lower bound comes first", s).
			WithHint(after + ".." + before)
	}
	return sel, nil
}

// pick returns the releases the selector names, in the order the API listed
// them (newest first), and nothing when none match — the caller decides
// whether that is an error or a row.
func (sel cycleSelector) pick(releases []release) []release {
	if !sel.ranged {
		if r, found := findRelease(releases, sel.exact); found {
			return []release{r}
		}
		return nil
	}
	var out []release
	for _, r := range releases {
		n, ok := numericCycle(r.Name)
		if !ok {
			continue
		}
		if sel.lo != nil && compareNumeric(n, sel.lo) < 0 {
			continue
		}
		if sel.hi != nil && compareNumeric(n, sel.hi) > 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// numericCycle reads "15", "9.6" or "24.04" as components; anything else is
// not a number and reports so rather than guessing.
func numericCycle(name string) ([]int, bool) {
	parts := strings.Split(name, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		if p == "" {
			return nil, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// compareNumeric orders dotted numbers component by component, a missing
// component counting as zero: 15 == 15.0 < 15.4 < 16, and 9.6 < 10.
func compareNumeric(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}
