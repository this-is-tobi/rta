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

// A capability the help names is spelled as the command a person types there:
// `rta grant list`, not grant.list, which no terminal runs. Only a reference,
// in backticks and naming a command: the target typed in "e.g. kv.get" is a
// value, and a dotted config key is no command.
func TestHelpNamesACapabilityAsTheCommandThatRunsIt(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	cmd, _, err := root.Find([]string{"grant", "allow"})
	if err != nil {
		t.Fatal(err)
	}
	help := plainHelp(cmd, 200)
	for _, want := range []string{"shown by `rta grant list`", "e.g. kv.get"} {
		if !strings.Contains(help, want) {
			t.Errorf("the help does not say %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "`grant.list`") {
		t.Errorf("the help still names grant.list by its ID:\n%s", help)
	}
	for text, want := range map[string]string{
		"see `kv.get` first":                      "see `rta kv get` first",
		"set `plugins.http.timeout` instead":      "set `plugins.http.timeout` instead",
		"refused as `git.status.timeout` past 2s": "refused as `git.status.timeout` past 2s",
		"`kv.get.nothing` is no command":          "`kv.get.nothing` is no command",
	} {
		if got := asCommands(root, text); got != want {
			t.Errorf("asCommands(%q) = %q, want %q", text, got, want)
		}
	}
}
