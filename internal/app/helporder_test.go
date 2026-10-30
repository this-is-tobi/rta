package app

import (
	"strings"
	"testing"
)

// A command's help says what it is in a line, then how to type it, what its
// arguments are, examples and flags, and only then the rest of its long text:
// the order clig.dev gives and gh and kubectl use. grant allow's long text ran
// to seventeen lines above its usage line, and its arguments sat under a
// heading of another style ("Arguments:") above the upper-case ones.
func TestAHelpScreenLeadsWithHowToTypeTheCommand(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cmd, _, err := NewRoot(reg, "test").Find([]string{"grant", "allow"})
	if err != nil {
		t.Fatal(err)
	}
	help := plainHelp(cmd, 80)
	last := -1
	for _, heading := range []string{"USAGE", "ARGUMENTS", "EXAMPLES", "FLAGS", "DETAILS"} {
		at := strings.Index(help, "\n  "+heading+"\n")
		if at < 0 {
			t.Fatalf("no %s section:\n%s", heading, help)
		}
		if at < last {
			t.Errorf("%s comes before the section above it in the order:\n%s", heading, help)
		}
		last = at
	}
	if strings.Contains(help, "Arguments:") {
		t.Errorf("the arguments kept their heading of another style:\n%s", help)
	}
	if n := strings.Count(help, cmd.Short); n != 1 {
		t.Errorf("the summary is said %d times, want once:\n%s", n, help)
	}
}
