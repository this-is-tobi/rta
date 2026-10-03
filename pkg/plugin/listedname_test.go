package plugin

import "testing"

// A name a stranger chooses is shown, not cleaned: the renderer strips an
// escape sequence from a cell, which turned `esc` ESC `[31mred` into `escred`,
// another and ordinary name. ListedName is what a plugin that lists keys or
// files hands its rows, and it agrees with the built-in listings by being the
// same rule.
func TestListedNameShowsAnOddNameAndLeavesAnOrdinaryOne(t *testing.T) {
	for name, want := range map[string]string{
		"reports/Annual Report.pdf": "reports/Annual Report.pdf",
		"esc\x1b[31mred":            `"esc\x1b[31mred"`,
		"line\nbreak":               `"line\nbreak"`,
		"bad\xffbyte":               `"bad\xffbyte"`,
		" leading":                  `" leading"`,
		"":                          `""`,
	} {
		if got := ListedName(name); got != want {
			t.Errorf("ListedName(%q) = %s, want %s", name, got, want)
		}
	}
}
