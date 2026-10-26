package mcp

import (
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The cut an answer gets is a matter of what fits, and how it is found is not.
// Measuring a value alone rather than the page around it, and each part of a
// page once rather than at every step, has to land on the very cut that
// encoding the whole page for every question found, byte for byte: that one is
// kept below as the oracle, and every fixture is cut both ways.
//
// Under budgets smaller than a result's, so the oracle, which is what was too
// slow, stays a fraction of a second; the code is the same at any budget.
func TestACutIsTheSameCutHoweverCheaplyItIsFound(t *testing.T) {
	narrow := narrowingFor(plugin.Capability{})
	for _, limit := range []int{4 << 10, 9 << 10} {
		for _, tc := range costFixtures(limit) {
			t.Run(strconv.Itoa(limit)+"/"+tc.name, func(t *testing.T) {
				v := cleanedView(tc.v)
				if size(v) <= limit {
					if tc.name != fitsFixture {
						t.Fatalf("the fixture fits in %d bytes, so it tests no cut", limit)
					}
					return
				}
				got, ok := shrink(v, size(v), limit, narrow)
				want, wantOK := probedShrink(v, limit, narrow)
				if ok != wantOK {
					t.Fatalf("cut %t, the whole-page probing cut %t", ok, wantOK)
				}
				if !ok {
					return
				}
				gotText, wantText := encodedText(t, got), encodedText(t, want)
				if gotText != wantText {
					t.Errorf("sent %d bytes, the whole-page probing sent %d, and they part at byte %d",
						len(gotText), len(wantText), firstDifference(gotText, wantText))
				}
			})
		}
	}
}

func encodedText(t *testing.T, v view.View) string {
	t.Helper()
	raw, err := encoded(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func firstDifference(a, b string) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// probedShrink is the cut as it was found before it was made cheap: every
// question about a candidate answered by encoding the whole page.
func probedShrink(v view.View, limit int, narrow narrowing) (view.View, bool) {
	switch t := v.(type) {
	case view.Table:
		return shrinkTable(t, limit, narrow)
	case view.Text:
		return shrinkText(t, limit)
	case view.KeyValue:
		pairs := append([]view.Pair(nil), t.Pairs...)
		out := func() view.KeyValue { o := t; o.Pairs = pairs; return o }
		for size(out()) > limit {
			longest := 0
			for i, p := range pairs {
				if len(p.Value) > len(pairs[longest].Value) {
					longest = i
				}
			}
			whole := pairs[longest].Value
			if len(whole) == 0 {
				return nil, false
			}
			n := largest(min(len(whole), limit), func(n int) bool {
				pairs[longest].Value = cutText(whole, n)
				return size(out()) <= limit
			})
			pairs[longest].Value = cutText(whole, n)
			if n == 0 {
				pairs[longest].Value = ""
			}
		}
		return out(), true
	case view.Sections:
		items := append([]view.Section(nil), t.Items...)
		out := func() view.Sections { o := t; o.Items = items; return o }
		stuck := make([]bool, len(items))
		for size(out()) > limit {
			pick := -1
			for i, it := range items {
				if !stuck[i] && (pick < 0 || size(it.View) > size(items[pick].View)) {
					pick = i
				}
			}
			if pick < 0 {
				return nil, false
			}
			excess := size(out()) - limit
			cut, ok := probedShrink(items[pick].View, max(size(items[pick].View)-excess, 0), narrow)
			if !ok {
				stuck[pick] = true
				continue
			}
			items[pick].View = cut
		}
		return out(), true
	}
	return nil, false
}

const fitsFixture = "a page that fits"

type costFixture struct {
	name string
	v    view.View
}

// costFixtures are answers over a budget of limit bytes in every shape that is
// cut, with what makes measuring one part apart from the rest hard: values
// that need escaping, characters of several bytes, lines to cut at, a number
// beside the pairs, keys that alone are over the budget, and a section nothing
// can cut.
func costFixtures(limit int) []costFixture {
	// A generator of its own rather than math/rand, so the fixtures are the
	// same text on every Go release.
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	alphabet := []string{"a", "b", "z", " ", "\n", "é", "日本", "😀", `"`, `\`, "<", "&", "\t", "x", "\x01", "\u2028", "\xff"}
	text := func(n int) string {
		var b strings.Builder
		for b.Len() < n {
			b.WriteString(alphabet[next(len(alphabet))])
		}
		return b.String()
	}
	pairs := func(n, longest int) view.KeyValue {
		kv := view.KeyValue{Pairs: []view.Pair{{Key: "status", Value: "200 OK"}}, Numbers: map[string]float64{"pairs": float64(n)}}
		for i := range n {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: "field_" + strconv.Itoa(i), Value: text(next(longest))})
		}
		return kv
	}
	return []costFixture{
		{name: fitsFixture, v: pairs(8, limit/20)},
		{name: "one large value", v: view.KeyValue{Pairs: []view.Pair{{Key: "status", Value: "200 OK"}, {Key: "body", Value: text(limit * 4)}}}},
		{name: "many large values", v: pairs(24, limit*3/20)},
		{name: "many values of every size", v: pairs(60, limit/13)},
		{name: "keys over the budget", v: view.KeyValue{Pairs: []view.Pair{
			{Key: strings.Repeat("k", limit*5/8), Value: text(limit / 90)},
			{Key: strings.Repeat("q", limit*5/8), Value: text(limit / 115)},
		}}},
		{name: "a sections page", v: view.Sections{Items: []view.Section{
			{ID: "summary", Title: "summary", View: pairs(4, limit/400+1)},
			{ID: "rows", Title: "rows", View: bigTable(limit / 55)},
			{ID: "values", Title: "values", View: pairs(12, limit/9)},
			{ID: "body", Title: "body", View: view.Text{Body: text(limit / 2)}},
		}}},
		{name: "a sections page with a part that cannot be cut", v: view.Sections{Items: []view.Section{
			{ID: "tree", Title: "tree", View: view.Tree{Roots: []view.Node{{Label: text(limit * 3 / 8)}}}},
			{ID: "values", Title: "values", View: pairs(10, limit/4)},
		}}},
	}
}

// What sending an answer costs, at the budget a result is held to: one that
// fits, which is most of them, and the shapes whose cut is searched for.
func BenchmarkFittingAnAnswer(b *testing.B) {
	for _, tc := range costFixtures(maxResultBytes) {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := viewResult(tc.v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
