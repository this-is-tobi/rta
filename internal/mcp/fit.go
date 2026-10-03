package mcp

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/pkg/view"
)

// maxResultBytes is the most one tool result may weigh, as the JSON a model is
// handed. A result is read in full, in the model's context, whether or not it
// can use it: a git_blame of a 1900-line file was 208 KB, about 50,000
// tokens, with nothing in the call to ask for less, and clients that cap a
// tool result (25,000 tokens is a common ceiling) refuse it outright, which
// tells the model nothing about what to change. At about 3.5 bytes a token for
// this JSON, 80 KiB is a result a model can still hold beside the task, and
// the 500 rows git_log may be asked for fit in it.
const maxResultBytes = 80 << 10

// fitResult cuts a view that encodes to more than maxResultBytes down to what
// fits, and says it did. The reader is told what it has and what it does not:
// a table keeps its true Total and warns it is partial, which is how every
// surface already says a list is, and a text or a value ends on a line
// naming what was left out. A shape that cannot be cut without changing what
// it means, a tree or a chart, is refused instead, since half a tree reads as
// a whole one.
//
// The view is already masked and cleaned (viewResult), so this only removes.
func fitResult(v view.View) (view.View, *view.Error) {
	if size(v) <= maxResultBytes {
		return v, nil
	}
	if cut, ok := shrink(v, maxResultBytes); ok {
		return cut, nil
	}
	return nil, view.Errorf("core.mcp.result.toolarge",
		"the answer is %d KiB, over the %d KiB a tool result may hold, and this kind of answer cannot be cut",
		size(v)>>10, maxResultBytes>>10).
		WithHint("narrow the call: a smaller path, a tighter filter or range, or a lower limit where the tool has one")
}

// size is what v weighs once encoded the way viewResult sends it.
func size(v view.View) int {
	m, err := view.ToMap(v)
	if err != nil {
		return 0
	}
	raw, err := view.Marshal(m)
	if err != nil {
		return 0
	}
	return len(raw)
}

// shrink returns v cut to encode within limit bytes, and false when v is of a
// shape that cannot be, or when nothing of it is left to show.
func shrink(v view.View, limit int) (view.View, bool) {
	switch t := v.(type) {
	case view.Table:
		return shrinkTable(t, limit)
	case view.Text:
		return shrinkText(t, limit)
	case view.KeyValue:
		return shrinkPairs(t, limit)
	case view.Sections:
		return shrinkSections(t, limit)
	}
	return nil, false
}

// largest is the greatest k in [0, n] for which fits(k) holds, given that
// fits only goes from true to false as k grows.
func largest(n int, fits func(k int) bool) int {
	return sort.Search(n, func(i int) bool { return !fits(i + 1) })
}

func shrinkTable(t view.Table, limit int) (view.View, bool) {
	rows, total := t.Rows, max(t.Total, len(t.Rows))
	with := func(k int) view.Table {
		out := t
		out.Rows = rows[:k]
		if t.Tail {
			out.Rows = rows[len(rows)-k:]
		}
		out.Total = total
		// A cursor names where the call the rows came from left off, which is
		// past the rows this cut dropped: handed on, it would send the next
		// call over them and nothing would say they were skipped. Without it
		// the way on is the lower limit the warning names, which brings the
		// whole page and its cursor within the budget.
		out.Page = nil
		out.Warnings = append(append([]view.Error(nil), t.Warnings...), view.Error{
			Code: "core.mcp.result.cut",
			Message: fmt.Sprintf("showing %d of %d rows: the answer is too large for one tool result",
				k, total),
			Hint: "narrow the call: a smaller path, a tighter filter or range, or a lower limit where the tool has one",
		})
		return out
	}
	k := largest(len(rows), func(k int) bool { return size(with(k)) <= limit })
	if k == 0 {
		return nil, false
	}
	return with(k), true
}

func shrinkText(t view.Text, limit int) (view.View, bool) {
	body := t.Body
	with := func(n int) view.Text {
		out := t
		out.Body = cutText(body, n)
		return out
	}
	// The escaped form is never shorter than the text, so nothing past limit
	// bytes can fit, and searching only that much keeps a megabyte of body from
	// being encoded again for each probe.
	n := largest(min(len(body), limit), func(n int) bool { return size(with(n)) <= limit })
	if n == 0 {
		return nil, false
	}
	return with(n), true
}

// cutText keeps the first n bytes of s up to a whole line where there is one
// in them, and ends on the line that says how much was left out.
func cutText(s string, n int) string {
	kept := s[:n]
	if i := strings.LastIndexByte(kept, '\n'); i > 0 {
		kept = kept[:i]
	}
	for kept != "" && !utf8.ValidString(kept) {
		kept = kept[:len(kept)-1]
	}
	return fmt.Sprintf("%s\n[cut: the first %d of %d bytes; narrow the call to see the rest]", kept, len(kept), len(s))
}

// shrinkPairs cuts the longest values, one at a time, until the rest fits: a
// response body is a pair beside a status and some headers, and the status is
// the part that must survive.
func shrinkPairs(kv view.KeyValue, limit int) (view.View, bool) {
	pairs := append([]view.Pair(nil), kv.Pairs...)
	out := func() view.KeyValue { o := kv; o.Pairs = pairs; return o }
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
}

// shrinkSections cuts the largest section, and then the next, until the page
// fits; a section that cannot be cut is left whole, and a page whose parts
// all are too big is refused.
func shrinkSections(s view.Sections, limit int) (view.View, bool) {
	items := append([]view.Section(nil), s.Items...)
	out := func() view.Sections { o := s; o.Items = items; return o }
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
		cut, ok := shrink(items[pick].View, max(size(items[pick].View)-excess, 0))
		if !ok {
			stuck[pick] = true
			continue
		}
		items[pick].View = cut
	}
	return out(), true
}
