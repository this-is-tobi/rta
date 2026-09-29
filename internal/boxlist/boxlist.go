// Package boxlist is the text a TUI form's box holds a list in, read and
// written by one grammar: the form reads what is typed with it, seeds a
// box's default with it, and pkg/plugin spells a list for the TUI with it
// (Surface.Call, InputTo), so what a reader is told to type is what the box
// reads back.
//
// **The CLI's list flag's grammar, CSV.** pflag's StringSlice reads each
// value it is given as a line of CSV, where an element in double quotes keeps
// the commas and the spaces inside them and a quote inside is written twice.
// The box split at every comma and trimmed each piece, so no text typed into
// it gave an element holding a comma or space at an end — a tag "a,b", a
// recipient " x" — and a default holding one was written back as the
// elements it was not. An element outside quotes is trimmed as it was, since
// "recipe, italian" is how a list is typed into a box; one inside them is
// kept exactly, which is how any element at all is typed, and how one is
// written wherever it would otherwise read back as another.
//
// A quote inside an element that no quote opened is read as itself, where
// CSV refuses it: the box is where a password goes into a secret list, and a
// quote in one is not a mistake to refuse. A quote that opens an element and
// is never closed, or one followed by anything but a comma, is refused
// (Split's error), since the element it meant cannot be told from the text.
package boxlist

import (
	"errors"
	"strings"
	"unicode"
)

// The refusals Split makes, worded for the person typing into the box.
var (
	errUnclosed = errors.New("an element opened with a double quote needs one to close it")
	errTrailing = errors.New("only a comma may follow an element's closing quote; " +
		"a quote inside one is written twice")
)

// Split reads text as a box's list: nil for a box holding nothing but space,
// which answers nothing. An error says why text is no list, beside the
// reading of it a form's suggestions can still use while it is being typed:
// an unclosed element runs to the end, and what follows a closing quote is
// kept in its element.
func Split(text string) ([]string, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var err error
	elements := scan(text)
	out := make([]string, len(elements))
	for i, e := range elements {
		out[i] = e.value
		// The first refusal only: a form's footer has one line for it, and
		// two elements wrong the same way are one thing to fix.
		if err == nil {
			err = e.err
		}
	}
	return out, err
}

// Join is list as a box's text, its elements separated by sep, each bare
// where the box reads it back as itself and quoted where it would not
// (Element). held is false when no box text gives list: an empty list, which
// an empty box does not give, since an empty box answers nothing, and an
// element holding a line break, which a box of one line does not keep.
func Join(list []string, sep string) (text string, held bool) {
	parts := make([]string, len(list))
	held = len(list) > 0
	for i, e := range list {
		parts[i] = Element(e)
		if strings.ContainsAny(e, "\r\n") {
			held = false
		}
	}
	return strings.Join(parts, sep), held
}

// Element is one element as a box holds it: bare when Split reads it back
// as itself, quoted (Quote) when it is empty, holds a comma or a quote, or
// has space at an end.
func Element(e string) string {
	if e == "" || strings.ContainsAny(e, `,"`) || e != strings.TrimSpace(e) {
		return Quote(e)
	}
	return e
}

// Quote is e in the box's double quotes, a quote inside it written twice.
func Quote(e string) string {
	return `"` + strings.ReplaceAll(e, `"`, `""`) + `"`
}

// Last splits text before the element being typed, for a completion that
// extends it: head is everything up to the comma before it and the space
// typed after that comma, fragment the element so far as Split reads it, and
// quoted whether a quote opened it — a suggestion then goes in quotes,
// since the box already holds the opening one.
func Last(text string) (head, fragment string, quoted bool) {
	elements := scan(text)
	last := elements[len(elements)-1]
	raw := text[last.start:]
	gap := len(raw) - len(strings.TrimLeftFunc(raw, unicode.IsSpace))
	return text[:last.start+gap], last.value, last.quoted
}

// element is one element of a box's text: where its text starts, just after
// the comma before it, what it reads as, whether a quote opened it, and why
// it is no element when it is none.
type element struct {
	start  int
	value  string
	quoted bool
	err    error
}

// scan reads text an element at a time, at least one: the text before the
// first comma is an element even when it is empty, as the text after the
// last one is — "a," is the elements a and "", as CSV reads it.
func scan(text string) []element {
	var out []element
	for at := 0; ; {
		e, comma := read(text, at)
		out = append(out, e)
		if comma == len(text) {
			return out
		}
		at = comma + 1
	}
}

// read is the element whose text starts at text[at:], and where the comma
// after it is, or len(text) when none is.
func read(text string, at int) (element, int) {
	e := element{start: at}
	i := len(text) - len(strings.TrimLeftFunc(text[at:], unicode.IsSpace))
	if !strings.HasPrefix(text[i:], `"`) {
		comma := commaFrom(text, i)
		e.value = strings.TrimSpace(text[i:comma])
		return e, comma
	}
	e.quoted = true
	var b strings.Builder
	for i++; ; {
		q := strings.IndexByte(text[i:], '"')
		if q < 0 {
			b.WriteString(text[i:])
			e.value, e.err = b.String(), errUnclosed
			return e, len(text)
		}
		b.WriteString(text[i : i+q])
		i += q + 1
		if !strings.HasPrefix(text[i:], `"`) {
			break
		}
		b.WriteByte('"')
		i++
	}
	comma := commaFrom(text, i)
	if stray := strings.TrimSpace(text[i:comma]); stray != "" {
		b.WriteString(stray)
		e.err = errTrailing
	}
	e.value = b.String()
	return e, comma
}

// commaFrom is where the first comma at or after i is in text, or len(text).
func commaFrom(text string, i int) int {
	if c := strings.IndexByte(text[i:], ','); c >= 0 {
		return i + c
	}
	return len(text)
}
