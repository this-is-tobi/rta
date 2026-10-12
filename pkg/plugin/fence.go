package plugin

import (
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
