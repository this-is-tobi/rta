package textclean

import "testing"

// A name in a listing is shown as it is when it reads as itself, with the
// spaces a file name has, and quoted with every character a reader would not
// see written out when it does not: never cleaned, which is what Terminal
// does to a cell.
func TestNameIsQuotedOnlyWhereItDoesNotReadAsItself(t *testing.T) {
	nbsp, zwsp := string(rune(0xa0)), string(rune(0x200b))
	// The escape Go writes for a code point, built here from its parts so
	// the file holds no character that draws as nothing.
	escaped := func(code string) string { return `"a` + string(rune(92)) + `u` + code + `b"` }
	for name, want := range map[string]string{
		"main.go":                "main.go",
		"Annual Report (v2).pdf": "Annual Report (v2).pdf",
		"naïve café.txt":         "naïve café.txt",
		"esc\x1b[31mred":         `"esc\x1b[31mred"`,
		"line\nbreak":            `"line\nbreak"`,
		"tab\there":              `"tab\there"`,
		"bad\xffbyte":            `"bad\xffbyte"`,
		" leading":               `" leading"`,
		"trailing ":              `"trailing "`,
		`"quoted"`:               `"\"quoted\""`,
		"a" + nbsp + "b":         escaped("00a0"),
		"a" + zwsp + "b":         escaped("200b"),
	} {
		if got := Name(name); got != want {
			t.Errorf("Name(%q) = %s, want %s", name, got, want)
		}
	}
}
