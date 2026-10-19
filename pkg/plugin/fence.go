package plugin

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file is the allowlist half of a profile's fence: what a list of allowed
// values may hold for an input, and whether a value is in one.
//
// A profile's `set:` supplies a default and the caller always wins, so a
// profile alone cannot say "this connection reaches that namespace and no
// other". `only:` can. The operator writes, per config key, the values the
// connection reaches, and the host holds the value a call *resolves to* — the
// layering done, the empty string a caller can send included — to the list
// before anything is dialled. What the list is and how a value is compared
// with it live here, beside Resolve's other readings of a value, so that the
// loader, `rta profile set`, doctor and the run all answer the same question
// with the same code.

// FenceList reads the value written under an `only:` key as the list it has to
// be, in both shapes a decoded document and a flag hand over.
func FenceList(list any) ([]any, bool) {
	switch l := list.(type) {
	case []any:
		return l, true
	case []string:
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// FenceElement is the declaration one entry of f's allowlist is read against:
// f itself, except that a list-shaped input is fenced one element at a time —
// `only: {namespaces: [a, b]}` says which names a call's list may be made of,
// not what the list as a whole is.
func FenceElement(f Field) Field {
	if f.Type == StringSlice {
		f.Type = String
	}
	return f
}

// FenceProblem reports why list cannot be the allowlist of f, and how to write
// it. Both are empty when it can.
//
// Never the entries themselves, for the reason StatedTypeProblem gives: this
// runs over configuration the operator wrote, and a sentence quoting it is one
// mistyped block from printing a credential.
//
// Every entry is held to what a run would hold it to — its type, the options
// the field offers, the range it takes — because an entry no call could
// resolve to is a value the operator believes the connection reaches and no
// call ever does, which is the failure `set:` is already checked for.
func FenceProblem(f Field, list any) (problem, hint string) {
	entries, isList := FenceList(list)
	if !isList {
		return "is written as " + statedShape(list) + " where a list of allowed values is declared",
			"write it as `[a, b]`, or as a `- ` list — even for one value"
	}
	if len(entries) == 0 {
		return "lists no value, so nothing would pass it",
			"list at least one allowed value, or remove the key"
	}
	ef := FenceElement(f)
	for _, e := range entries {
		// A key written with nothing after it is the one entry that reads as the
		// zero of the type, which is exactly the value a fence is there to keep
		// out — so it is refused rather than compared.
		if _, ok := fenceKey(ef, e); !ok || e == nil {
			return "lists an entry written as " + statedShape(e) + " where " + fenceTypeWord(ef.Type) + " is declared",
				fenceTypeHint(ef.Type)
		}
		if s, isText := e.(string); isText && len(ef.Options) > 0 {
			if _, named := ef.CanonicalOption(s); !named {
				return "lists a value the input does not accept",
					"one of: " + strings.Join(ef.Options, "|")
			}
		}
		if bounds := ef.Bounds(); bounds != "" {
			if _, inRange := ef.Range(e); !inRange {
				return "lists a value outside what the input takes", "write a value " + bounds
			}
		}
	}
	return "", ""
}

func fenceTypeWord(t FieldType) string {
	switch t {
	case Int:
		return "an integer"
	case Float:
		return "a number"
	case Bool:
		return "a boolean"
	case Duration:
		return "a duration"
	}
	return "text"
}

func fenceTypeHint(t FieldType) string {
	switch t {
	case Int, Float:
		return "write each entry as a bare number"
	case Bool:
		return "write each entry unquoted as `true` or `false`"
	case Duration:
		return durationHint
	}
	return "quote each entry, so it is read as text rather than as a number, a boolean or a date"
}

// FenceAllows reports whether v, the value an input of type f resolves to, is
// one of the allowed entries. Compared by the input's declared type — `5432`
// and `"5432"` are different entries of an Int's list, `30s` and `30000ms`
// the same one of a Duration's, `Alpha` and `alpha` the same option — never by
// how a value happens to print.
//
// **A value that is not there is the zero of its type and is compared as
// that**, so an input nobody supplied is in the list only if the list holds
// the empty string, zero or false. That is what stops a blank lifting the
// fence: `--namespace ""` resolves to the empty string, which is not in a list
// that does not contain it, and neither is the input left out when nothing
// else fills it. A list-shaped input is held element by element and must have
// at least one — an empty list reads as "no filter" to most handlers, which is
// the widest reach there is.
func FenceAllows(f Field, allowed []any, v any) bool {
	ef := FenceElement(f)
	in := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		if k, ok := fenceKey(ef, a); ok {
			in[k] = true
		}
	}
	if f.Type == StringSlice {
		elements := stringSlice(v)
		if len(elements) == 0 {
			return false
		}
		for _, e := range elements {
			if k, ok := fenceKey(ef, e); !ok || !in[k] {
				return false
			}
		}
		return true
	}
	k, ok := fenceKey(ef, v)
	return ok && in[k]
}

// fenceKey is the form of one scalar under which two values of f's type are the
// same value, and whether v is a value of that type at all. nil is the zero of
// the type.
//
// Strict where Resolve's readers are: a boolean is a boolean and text is text,
// never coerced, because the next coercion is `yes`, and a fence that treated
// "true" as true would be one a spelling gets through.
func fenceKey(f Field, v any) (string, bool) {
	if v == nil {
		v = fenceZero(f.Type)
	}
	switch f.Type {
	case Int:
		n, ok := toInt(v)
		return "i" + strconv.Itoa(n), ok
	case Float:
		x, ok := toFloat(v)
		return "f" + strconv.FormatFloat(x, 'g', -1, 64), ok
	case Bool:
		b, ok := v.(bool)
		return "b" + strconv.FormatBool(b), ok
	case Duration:
		d, ok := toDuration(v)
		return "d" + strconv.FormatInt(int64(d), 10), ok
	case String, Text:
		s, ok := v.(string)
		return "s" + canonicalOption(f, s), ok
	}
	return "", false
}

// fenceZero is what an input of type t reads as when nobody supplied it: the
// zero every accessor on Request returns.
func fenceZero(t FieldType) any {
	switch t {
	case Int:
		return 0
	case Float:
		return 0.0
	case Bool:
		return false
	case Duration:
		return time.Duration(0)
	}
	return ""
}

// Fence is the allowlist a connection states, by Field.Config key: the values
// each fenced input may resolve to. It rides on Inputs, beside the layers, and
// is not one of them — it supplies a value only where nothing else does and a
// list holds exactly one (see Resolve), and otherwise it is what CheckFence
// holds the layers' answer to.
//
// A key present with no entries is a fence that allows nothing, which refuses
// every call, and that is the point of keeping the two apart: an allowlist that
// cannot be read must not become "no allowlist".
type Fence map[string][]any

// of is the list that fences f, and whether one does. By Config key, which is
// the spelling an operator writes and the only one an input can be fenced by.
func (fn Fence) of(f Field) ([]any, bool) {
	if f.Config == "" {
		return nil, false
	}
	list, fenced := fn[f.Config]
	return list, fenced
}

// fenceDefault is what an input the fence allows values of resolves to when
// nobody supplied one, and whether it resolves to anything.
//
// An input that holds one value is that value when the fence allows exactly one:
// the operator who wrote a one-entry list has said where the connection goes. An
// input that holds a list is every value the fence allows, however many: the
// list is what the connection reaches, a handler given a list reads each of its
// entries, and a call that names none has asked for what it may have. That is
// the same reach a call naming every entry would have, so it widens nothing, and
// it is the one way for a call to say "all of mine" to a connection that fences
// a dozen namespaces without a call per namespace. An input that holds one value
// and has several to choose from gets none: somebody has to say which, and a
// blank is not in a list that does not hold it.
func fenceDefault(f Field, allowed []any) (value any, filled bool) {
	switch {
	case f.Type == StringSlice && len(allowed) > 0:
		return stringSlice(allowed), true
	case len(allowed) == 1:
		return allowed[0], true
	}
	return nil, false
}

// Fenced reports whether the fence has an allowlist for f.
func (fn Fence) Fenced(f Field) bool {
	_, fenced := fn.of(f)
	return fenced
}

// Filter narrows a completion list for f to what the fence allows, each entry
// kept as it came — a description after a tab included. An input the fence
// does not name keeps its list whole.
//
// Completion that offered what the fence will refuse is a screen that lists
// everything the connection can see — every namespace of a cluster, the
// databases of a server — beside a profile that says it reaches one: the
// operator's own inventory on offer to whoever is typing, and a suggestion
// accepted with one keystroke into a call that is then refused.
func (fn Fence) Filter(f Field, entries []string) []string {
	allowed, fenced := fn.of(f)
	if !fenced {
		return entries
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if fenceAllowsText(f, allowed, CandidateValue(entry)) {
			out = append(out, entry)
		}
	}
	return out
}

// Offer is the allowlist as a list of completions: what an input offers when
// it has nothing to complete from of its own, since the operator already wrote
// down what the connection reaches. nil for an input the fence does not name.
func (fn Fence) Offer(f Field) []string {
	allowed, fenced := fn.of(f)
	if !fenced {
		return nil
	}
	out := make([]string, 0, len(allowed))
	for _, entry := range allowed {
		out = append(out, fmt.Sprint(entry))
	}
	return out
}

// Narrow is what a completion list for f comes to under the fence, given what
// the field offers of its own: the closed set or the suggestions, narrowed to
// the allowlist — and the allowlist itself when the field has nothing to offer
// on this channel, which is a field with no Suggest and no Options, and a Live
// one, whose Suggest never answers per keystroke or in a shell.
func (fn Fence) Narrow(f Field, declared []string) []string {
	if !fn.Fenced(f) {
		return declared
	}
	if len(f.Options) == 0 && (f.Suggest == nil || f.Live) {
		return fn.Offer(f)
	}
	return fn.Filter(f, declared)
}

// fenceAllowsText is FenceAllows for a completion entry, which is text whatever
// the input's type: an integer's entry is read as the number it spells, and one
// that spells none is not in the list.
func fenceAllowsText(f Field, allowed []any, text string) bool {
	var v any = text
	switch FenceElement(f).Type {
	case Int:
		n, err := strconv.Atoi(text)
		if err != nil {
			return false
		}
		v = n
	case Float:
		x, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return false
		}
		v = x
	case Bool:
		b, err := strconv.ParseBool(text)
		if err != nil || (text != "true" && text != "false") {
			return false
		}
		v = b
	}
	ef := FenceElement(f)
	if f.Type == StringSlice {
		return FenceAllows(ef, allowed, v)
	}
	return FenceAllows(f, allowed, v)
}

// FenceBreach is how a call went outside a Fence: the input it went outside
// of, what the fence allows there, and — when the way was a switch the plugin
// declares widens that input (Field.Widens) — the switch.
//
// A description and not an error, because the three surfaces that hold a call
// to a fence word the refusal for three different readers, and one of them
// must not be told what the fence holds.
type FenceBreach struct {
	// Input is the fenced input the call is outside, whether by its own value
	// or because a switch reads past it.
	Input Field
	// Switch is the widening switch the call set, the zero Field when the
	// breach is the input's own value (Widened says which).
	Switch Field
	// Allowed is what the fence allows for Input.
	Allowed []any
	// Blank is true when the input resolved to nothing — left out, or sent as
	// the empty string or an empty list — and nothing the fence holds is that.
	Blank bool
}

// CheckFence holds the values a call resolves to against the connection's
// fence, and reports the first way it is outside — nil when it is not.
//
// **Asked of the resolved value, after every layer.** The caller wins every
// layer, an empty string included, so a check of what a profile's `set:`
// states, or of what the caller sent, is a check of the wrong thing: `--namespace
// ""` resolves to the empty string, which a fence that does not list it
// refuses, and a call that sends nothing resolves to whatever the layers
// below give it, which the fence holds just the same. That is also why this
// takes the whole Inputs rather than a value: the single value of a one-entry
// list is a layer of Resolve's, and the answer for an input nobody filled is
// whatever it fills.
//
// And the switches. A Bool the plugin declares widens a fenced input is a
// second way to reach what the list leaves out that never passes through the
// input's own value — `all-namespaces` beside a pinned `namespace` — so a call
// that resolves one to true while the input it widens is fenced is outside the
// fence, whatever the input says. The host cannot read a handler to find that
// out; the declaration is how it knows.
//
// Pure, and made before anything is fetched or opened: a credential is not
// read and a forward is not raised for a call that is about to be refused.
func CheckFence(c Capability, in Inputs) *FenceBreach {
	if len(in.Fence) == 0 {
		return nil
	}
	resolved := Resolve(c, in)
	for _, f := range c.Inputs {
		allowed, fenced := in.Fence.of(f)
		if !fenced {
			continue
		}
		if !FenceAllows(f, allowed, resolved[f.Name]) {
			return &FenceBreach{Input: f, Allowed: allowed, Blank: fenceBlank(f, resolved[f.Name])}
		}
	}
	for _, w := range c.Inputs {
		if w.Widens == "" {
			continue
		}
		if on, _ := resolved[w.Name].(bool); !on {
			continue
		}
		for _, target := range c.Inputs {
			if target.Name != w.Widens {
				continue
			}
			if allowed, fenced := in.Fence.of(target); fenced {
				return &FenceBreach{Input: target, Switch: w, Allowed: allowed}
			}
		}
	}
	return nil
}

// Widened reports whether the call went outside the fence by a switch rather
// than by the input's own value.
func (b *FenceBreach) Widened() bool { return b.Switch.Name != "" }

// fenceBlank reports whether v is nothing at all as f reads it: not supplied,
// the empty string, or an empty list.
func fenceBlank(f Field, v any) bool {
	if v == nil {
		return true
	}
	if f.Type == StringSlice {
		return len(stringSlice(v)) == 0
	}
	s, isText := v.(string)
	return isText && s == ""
}
