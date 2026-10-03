package app

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Prose is joined into one line a paragraph, and what is laid out by its line
// breaks is not: the arguments block with its hang indent, a list, an
// indented example and a fenced block.
func TestReflowJoinsProseAndLeavesLaidOutTextAlone(t *testing.T) {
	long := "Writes a plugin that builds and runs as it stands,\n" +
		"rather than a skeleton with TODOs in it.\n\n" +
		"Two things it does:\n" +
		"- the first, which is\n" +
		"  wrapped by its author\n" +
		"- the second\n\n" +
		"    rta plugin new foo\n" +
		"    rta plugin new bar\n\n" +
		"```\nfenced line one\n\nfenced line two\n```\n\n" +
		"Arguments:\n  name  namespace for the new plugin: it becomes\n        `rta <name> ...`"
	want := "Writes a plugin that builds and runs as it stands, rather than a skeleton with TODOs in it.\n\n" +
		"Two things it does:\n" +
		"- the first, which is\n" +
		"  wrapped by its author\n" +
		"- the second\n\n" +
		"    rta plugin new foo\n" +
		"    rta plugin new bar\n\n" +
		"```\nfenced line one\n\nfenced line two\n```\n\n" +
		"Arguments:\n  name  namespace for the new plugin: it becomes\n        `rta <name> ...`"
	if got := reflowLong(long); got != want {
		t.Errorf("reflowLong:\n%s\nwant:\n%s", got, want)
	}
	if got := reflowLong("one line"); got != "one line" {
		t.Errorf("reflowLong of one line = %q", got)
	}
}

// Help is wrapped by the renderer to the terminal, which leaves a line break
// where it finds one: text an author broke at seventy characters came out
// ragged on a wide terminal and as a line and a stub on a narrow one. After
// the tree is built no paragraph of prose in any command's long text is
// spread over more than one line, so a new command written the same way is
// reflowed too and this fails only if the pass stops running.
func TestNoLongTextIsLeftHandWrapped(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, p := range strings.Split(c.Long, "\n\n") {
			if isProse(p) {
				t.Errorf("%s has a hand-wrapped paragraph: %q", c.CommandPath(), p)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRoot(reg, "test"))
}
