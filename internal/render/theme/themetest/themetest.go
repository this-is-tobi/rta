// Package themetest is what a test of a renderer needs to show what it draws
// under each way of drawing status: the setting put in force and put back, and
// the colour a rendered word was painted in, read from the escape sequence a
// terminal would receive.
//
// Three renderers decide a colour from a grade (the command line's tables, key/value
// pages and records; the dashboard's tiles, borders and headers; the profile
// panes), and each is tested through its own real render function with colour on.
// What those tests share is only this: the table of modes, and the question "what
// colour is this word in".
package themetest

import (
	"regexp"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/render/theme"
)

// Each runs f once for every way of drawing status, with it in force, as a
// subtest named for it, and leaves the built-in palette behind afterwards. The
// palette is package state, so the subtests do not run in parallel.
func Each(t *testing.T, f func(t *testing.T, mode theme.Grades)) {
	t.Helper()
	t.Cleanup(func() { theme.Apply(nil) })
	for _, name := range theme.GradeModes() {
		mode, _ := theme.ParseGrades(name)
		t.Run("status="+name, func(t *testing.T) {
			if problems := theme.Apply(map[string]string{theme.GradesKey: name}); len(problems) != 0 {
				t.Fatalf("status: %s was not taken: %v", name, problems)
			}
			f(t, mode)
		})
	}
}

// Drawn is the colour a grade of kind k must be drawn in under mode, as the
// "38;2;r;g;b" a terminal receives, or "" for none: the setting's definition
// written out once for every renderer's test to hold its output to, and written
// as a table and not derived from theme.Coloured, which is what is under test.
//
//	            all     alerts  off
//	ok          green   plain   plain
//	warn        amber   amber   plain
//	bad         red     red     plain
//	muted       grey    grey    grey      (it recedes; see theme.Coloured)
//	neutral     plain   plain   plain
func Drawn(mode theme.Grades, kind theme.StatusKind) string {
	switch {
	case kind == theme.StatusMuted:
		return ColourOf(theme.Subtle)
	case kind == theme.StatusGood && mode == theme.GradesAll:
		return ColourOf(theme.GoodText)
	case kind == theme.StatusWarn && mode != theme.GradesOff:
		return ColourOf(theme.WarnText)
	case kind == theme.StatusBad && mode != theme.GradesOff:
		return ColourOf(theme.BadText)
	}
	return ""
}

var (
	sgr       = `\x1b\[([0-9;]*)m`
	truecolor = regexp.MustCompile(`38;2;\d+;\d+;\d+`)
)

// ColourBefore is the foreground colour text was drawn in, as the "38;2;r;g;b" a
// terminal receives, or "" when it was drawn in none. The sequence is the one
// directly before the text, past any spaces: a table puts its padding inside the
// style, so a cell reads as ` 96%` under one colour.
//
// The first occurrence of text, which a test makes unambiguous by choosing words
// that appear once. Fails the test when text is not in rendered at all, so that a
// word that went missing is never read as a word drawn plain.
func ColourBefore(t testing.TB, rendered, text string) string {
	t.Helper()
	if !regexp.MustCompile(regexp.QuoteMeta(text)).MatchString(rendered) {
		t.Fatalf("%q is not drawn at all in %q", text, rendered)
	}
	m := regexp.MustCompile(sgr + ` *` + regexp.QuoteMeta(text)).FindStringSubmatch(rendered)
	if m == nil {
		return ""
	}
	return truecolor.FindString(m[1])
}

// ColourOf is the foreground colour a style paints with, in the same form.
func ColourOf(style lipgloss.Style) string {
	return truecolor.FindString(sgrOf(style.Render("x")))
}

func sgrOf(rendered string) string {
	m := regexp.MustCompile(sgr).FindStringSubmatch(rendered)
	if m == nil {
		return ""
	}
	return m[1]
}
