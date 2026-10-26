package mcp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/pkg/plugin"
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
// n is what it weighs encoded, which the caller has measured already and which
// is not measured again. narrow words the way to a smaller answer for the tool
// that was called.
func fitResult(v view.View, n int, narrow narrowing) (view.View, *view.Error) {
	if n <= maxResultBytes {
		return v, nil
	}
	if cut, ok := shrink(v, n, maxResultBytes, narrow); ok {
		return cut, nil
	}
	return nil, view.Errorf("core.mcp.result.toolarge",
		"the answer is %d KiB, over the %d KiB a tool result may hold, and this kind of answer cannot be cut",
		n>>10, maxResultBytes>>10).
		WithHint(narrow(0))
}

// narrowing is the hint that says how to ask for less, given how many rows of
// a table were kept (0 when the answer is not one that was cut to rows).
type narrowing func(rowsKept int) string

// genericNarrowing is what is true of any tool, for one that has no input
// whose job is to make an answer smaller.
const genericNarrowing = "narrow the call: a smaller path, a tighter filter or range, or a lower limit where the tool has one"

// narrowingFor words the hint from the inputs the called tool offers: the ones
// that bound how much comes back (limit, count, depth) and the one that aims
// it (path), and for a table that was cut the number of rows that fit. A
// model told to "lower a limit where the tool has one" has to go and find out
// whether it does, and gen_password has count and no limit.
//
// From the inputs an agent is offered and nothing else: a Local one is not in
// the schema, so naming it would send a model after an argument that is
// refused. The names are the catalogue's own (sdktest holds a plugin to
// `limit`), so a tool that spells its bound another way keeps the generic
// sentence rather than a guess.
func narrowingFor(c plugin.Capability) narrowing {
	offered := map[string]bool{}
	for _, f := range c.Inputs {
		if !f.Local {
			offered[f.Name] = true
		}
	}
	return func(rowsKept int) string {
		var clauses, lower []string
		for _, name := range []string{"limit", "count"} {
			switch {
			case !offered[name]:
			case rowsKept > 0:
				clauses = append(clauses, "set `"+name+"` to "+strconv.Itoa(rowsKept)+" or less")
			default:
				lower = append(lower, "`"+name+"`")
			}
		}
		if offered["depth"] {
			lower = append(lower, "`depth`")
		}
		if len(lower) > 0 {
			clauses = append(clauses, "lower "+joinWith(lower, ", ", " or "))
		}
		if offered["path"] {
			clauses = append(clauses, "give a narrower `path`")
		}
		if len(clauses) == 0 {
			return genericNarrowing
		}
		return "narrow the call: " + joinWith(clauses, ", ", ", or ")
	}
}

// joinWith joins items with sep, and the last with last.
func joinWith(items []string, sep, last string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], sep) + last + items[len(items)-1]
}

// encoded is v as the one JSON text a result is sent as.
func encoded(v view.View) ([]byte, error) {
	m, err := view.ToMap(v)
	if err != nil {
		return nil, err
	}
	return view.Marshal(m)
}

// size is what v weighs once encoded the way viewResult sends it.
func size(v view.View) int {
	raw, err := encoded(v)
	if err != nil {
		return 0
	}
	return len(raw)
}

// valueSize is what s weighs as a pair's value, plus what a page holding only
// that pair weighs besides, which cancels out wherever two are compared. The
// encoding of a string is a function of the string alone, so the weight of a
// page with one value changed is the weight of the page with the value's
// weight swapped.
//
// Measured through the same encoding as the page rather than worked out from
// the string, since the encoding is several passes and each could write the
// same string at a different length (an invalid byte is six bytes the first
// time and three the next).
func valueSize(s string) int {
	return size(view.KeyValue{Pairs: []view.Pair{{Value: s}}})
}

// shrink returns v cut to encode within limit bytes, and false when v is of a
// shape that cannot be, or when nothing of it is left to show. n is what v
// weighs, which every caller has measured already.
func shrink(v view.View, n, limit int, narrow narrowing) (view.View, bool) {
	switch t := v.(type) {
	case view.Table:
		return shrinkTable(t, limit, narrow)
	case view.Text:
		return shrinkText(t, limit)
	case view.KeyValue:
		return shrinkPairs(t, n, limit)
	case view.Sections:
		return shrinkSections(t, n, limit, narrow)
	}
	return nil, false
}

// largest is the greatest k in [0, n] for which fits(k) holds, given that
// fits only goes from true to false as k grows.
func largest(n int, fits func(k int) bool) int {
	return sort.Search(n, func(i int) bool { return !fits(i + 1) })
}

func shrinkTable(t view.Table, limit int, narrow narrowing) (view.View, bool) {
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
			Hint: narrow(k),
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
//
// total is what the page weighs, and each cut of a value it tries is measured
// as that value alone against what the rest of the page weighs (valueSize). It
// used to encode the whole page for every cut it tried, which is once per step
// of the search for each value it cut: a redis hash of a hundred large fields
// was minutes of CPU on the call, and a page whose keys alone were over the
// budget blanked its values one whole-page encoding after another, to be
// refused at the end. The cut it lands on is the same one, byte for byte, and
// TestACutIsTheSameCutHoweverCheaplyItIsFound holds it to that.
func shrinkPairs(kv view.KeyValue, total, limit int) (view.View, bool) {
	pairs := append([]view.Pair(nil), kv.Pairs...)
	// Every value blank is the least the page can weigh, so a page over the
	// budget even then is refused without blanking them one at a time first.
	blank := kv
	blank.Pairs = make([]view.Pair, len(pairs))
	for i, p := range pairs {
		blank.Pairs[i] = view.Pair{Key: p.Key}
	}
	if size(blank) > limit {
		return nil, false
	}
	for total > limit {
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
		rest := total - valueSize(whole)
		fits := func(n int) bool { return rest+valueSize(cutText(whole, n)) <= limit }
		// A page whose other values still put it over the budget however short
		// this one is cut has it blanked without a search for a cut, since a
		// longer cut weighs no less and none fits.
		n := 0
		if fits(1) {
			n = largest(min(len(whole), limit), fits)
		}
		pairs[longest].Value = cutText(whole, n)
		if n == 0 {
			pairs[longest].Value = ""
		}
		total = rest + valueSize(pairs[longest].Value)
	}
	kv.Pairs = pairs
	return kv, true
}

// shrinkSections cuts the largest section, and then the next, until the page
// fits; a section that cannot be cut is left whole, and a page whose parts
// all are too big is refused.
//
// What each section weighs is measured once and again only when it is cut, and
// the page once a step, where every section used to be encoded again on every
// step to find the largest, and the page twice. total is what the page weighs.
func shrinkSections(s view.Sections, total, limit int, narrow narrowing) (view.View, bool) {
	items := append([]view.Section(nil), s.Items...)
	out := func() view.Sections { o := s; o.Items = items; return o }
	stuck := make([]bool, len(items))
	weights := make([]int, len(items))
	for i, it := range items {
		weights[i] = size(it.View)
	}
	for total > limit {
		pick := -1
		for i := range items {
			if !stuck[i] && (pick < 0 || weights[i] > weights[pick]) {
				pick = i
			}
		}
		if pick < 0 {
			return nil, false
		}
		cut, ok := shrink(items[pick].View, weights[pick], max(weights[pick]-(total-limit), 0), narrow)
		if !ok {
			stuck[pick] = true
			continue
		}
		items[pick].View = cut
		weights[pick] = size(cut)
		total = size(out())
	}
	return out(), true
}
