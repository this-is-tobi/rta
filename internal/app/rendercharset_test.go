package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/cli"
)

// Every result is drawn with the characters the session can show, decided where
// the options are built and not by each command.
func TestRenderOptionsDrawInASCIIWhenTheLocaleCannotShowABox(t *testing.T) {
	for _, c := range []struct {
		lcAll, lang, term string
		want              bool
	}{
		{"", "en_US.UTF-8", "xterm-256color", false},
		{"C", "en_US.UTF-8", "xterm-256color", true},
		{"", "C", "xterm-256color", true},
		{"", "en_US.UTF-8", "linux", true},
		{"", "", "xterm-256color", false},
	} {
		t.Setenv("LC_ALL", c.lcAll)
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", c.lang)
		t.Setenv("TERM", c.term)
		if got := renderOptions(nil, cli.Pretty, false).ASCII; got != c.want {
			t.Errorf("LC_ALL=%q LANG=%q TERM=%q: ASCII = %v, want %v", c.lcAll, c.lang, c.term, got, c.want)
		}
	}
}

// And through a whole command, which is where the report came from: the grid of
// `rta profile list` under LC_ALL=C is plus, dash and pipe.
func TestACommandUnderTheCLocaleDrawsItsTableInASCII(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "C")
	out, errOut, err := runWith(t, setRegistry(t), "profiles:\n  staging:\n    plugins:\n      db: {}\n", "profile", "list")
	if err != nil {
		t.Fatalf("%v %s", err, errOut)
	}
	if !strings.Contains(out, "+--") || strings.ContainsAny(out, "╭─│") {
		t.Errorf("not an ASCII grid:\n%s", out)
	}

	t.Setenv("LC_ALL", "C.UTF-8")
	out, _, _ = runWith(t, setRegistry(t), "profiles:\n  staging:\n    plugins:\n      db: {}\n", "profile", "list")
	if !strings.Contains(out, "╭") {
		t.Errorf("a UTF-8 locale lost the box:\n%s", out)
	}
}
