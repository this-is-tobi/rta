package boxlist

import (
	"slices"
	"testing"

	"github.com/spf13/pflag"
)

// Every list a box can hold is written as text Split reads back as it — an
// element holding a comma, a double quote, space at an end, or nothing at
// all among them — with either separator a caller joins by.
func TestAListWrittenForABoxReadsBackAsItself(t *testing.T) {
	for _, list := range [][]string{
		{"ops"}, {"ops", "db"}, {"a,b", "c"}, {`say "hi"`, `"`, `""`}, {" lead", "trail ", " both "},
		{""}, {"", "x", ""}, {"night shift", "café", "東京"}, {`"a,b" , c`}, {"tab\tinside", "\tedge"},
	} {
		for _, sep := range []string{",", ", "} {
			text, held := Join(list, sep)
			if !held {
				t.Errorf("%q was said not to be held", list)
				continue
			}
			got, err := Split(text)
			if err != nil || !slices.Equal(got, list) {
				t.Errorf("%q written as %q reads back as %q (%v)", list, text, got, err)
			}
		}
	}
	for _, list := range [][]string{{}, {"line\nbreak"}, {"ok", "cr\r"}} {
		if text, held := Join(list, ","); held {
			t.Errorf("%q was said to be held, as %q", list, text)
		}
	}
}

// A list that needs no quoting is written as it always was, and an element
// is quoted only where the box would read it as something else.
func TestAPlainListIsWrittenBare(t *testing.T) {
	for _, c := range []struct {
		list []string
		want string
	}{
		{[]string{"recipe", "italian"}, "recipe, italian"},
		{[]string{"a,b", "c"}, `"a,b", c`},
		{[]string{`say "hi"`}, `"say ""hi"""`},
		{[]string{" x", "y "}, `" x", "y "`},
		{[]string{"", "z"}, `"", z`},
		{[]string{`a"b`}, `"a""b"`},
	} {
		if got, _ := Join(c.list, ", "); got != c.want {
			t.Errorf("Join(%q) = %q, want %q", c.list, got, c.want)
		}
	}
}

// What a person types reads as CSV reads it, with the space around an
// element outside quotes taken off and a quote inside one no quote opened
// kept as itself; the text CSV refuses and the box cannot read as a list is
// refused with why.
func TestWhatIsTypedIntoABoxReadsAsItsList(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a", []string{"a"}},
		{"recipe, italian", []string{"recipe", "italian"}},
		{"a,,b", []string{"a", "", "b"}},
		{"a,", []string{"a", ""}},
		{` "a, b" , c `, []string{"a, b", "c"}},
		{`"  padded  "`, []string{"  padded  "}},
		{`"say ""hi"""`, []string{`say "hi"`}},
		{`""`, []string{""}},
		{`a"b, 5" screen`, []string{`a"b`, `5" screen`}},
	} {
		got, err := Split(c.text)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("Split(%q) = %q, %v; want %q", c.text, got, err, c.want)
		}
	}
	for _, c := range []struct {
		text string
		want []string
		err  error
	}{
		{`a, "open`, []string{"a", "open"}, errUnclosed},
		{`"a, b`, []string{"a, b"}, errUnclosed},
		{`"a"b, c`, []string{"ab", "c"}, errTrailing},
		{`x, "a" "b"`, []string{"x", `a"b"`}, errTrailing},
	} {
		got, err := Split(c.text)
		if err != c.err || !slices.Equal(got, c.want) {
			t.Errorf("Split(%q) = %q, %v; want %q, %v", c.text, got, err, c.want, c.err)
		}
	}
}

// The box reads a quoted element as the CLI's list flag reads it: what
// pflag's StringSlice gives for the same text, wherever that has no space
// outside the quotes for the box to trim.
func TestABoxReadsQuotesAsTheListFlagDoes(t *testing.T) {
	for _, text := range []string{`a,b`, `"a,b",c`, `"say ""hi""",x`, `"",y`, `" lead","trail "`, `a,`} {
		flags := pflag.NewFlagSet("t", pflag.ContinueOnError)
		tags := flags.StringSlice("tag", nil, "")
		if err := flags.Parse([]string{"--tag", text}); err != nil {
			t.Fatal(err)
		}
		if got, err := Split(text); err != nil || !slices.Equal(got, *tags) {
			t.Errorf("the box reads %q as %q (%v), the flag as %q", text, got, err, *tags)
		}
	}
}

// Last is the element being typed, for a completion that extends it: the
// head keeps the spacing typed, and a comma inside quotes separates nothing.
func TestLastIsTheElementBeingTyped(t *testing.T) {
	for _, c := range []struct{ text, head, fragment string }{
		{"", "", ""},
		{"ita", "", "ita"},
		{"recipe,ita", "recipe,", "ita"},
		{"recipe,  ita", "recipe,  ", "ita"},
		{`"a,b", "c,d`, `"a,b", `, "c,d"},
		{`x, "say ""h`, `x, `, `say "h`},
		{"a, ", "a, ", ""},
	} {
		head, fragment, _ := Last(c.text)
		if head != c.head || fragment != c.fragment {
			t.Errorf("Last(%q) = %q, %q; want %q, %q", c.text, head, fragment, c.head, c.fragment)
		}
	}
	if _, _, quoted := Last(`a, "b`); !quoted {
		t.Error("an element opened with a quote was not said to be")
	}
	if _, _, quoted := Last(`"a", b`); quoted {
		t.Error("an element no quote opened was said to be")
	}
}
