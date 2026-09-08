package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/builtin/all"
)

// A positional argument's description had nowhere to go. `rta explain` printed
// it, `--help` did not, and the two disagreed on every capability that takes an
// argument — `rta cert expiry <targets>` never said "host[:port]" to the one
// audience reading a terminal. These tests cover the rendering, not the
// metadata: every description asserted here was already declared, and already
// reachable the long way round.

func TestArgumentsBlockNamesEachPositionalAndItsDescription(t *testing.T) {
	got := argumentsBlock([]argDoc{
		{Name: "target", Help: "capability to allow, e.g. kv.get"},
		{Name: "scope", Help: "narrow it to one record"},
	})
	for _, want := range []string{
		"Arguments:",
		"target  capability to allow, e.g. kv.get",
		"scope   narrow it to one record",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing %q:\n%s", want, got)
		}
	}
}

// A handful of declared positional descriptions run well past 90 characters —
// `audit deps <path>` is 161. fang renders Long through lipgloss at terminal
// width, so an over-long row is re-wrapped there with no idea a column exists
// and the description restarts under the argument name. Wrapping here, to a
// width narrower than any terminal lipgloss will be asked about, keeps the
// column: a hang indent that lipgloss then leaves alone.
func TestALongDescriptionWrapsUnderItsOwnColumn(t *testing.T) {
	long := "directory holding the lockfile or SBOM, or the file itself — or, from a " +
		"terminal, a repository URL (https://, ssh://, git@host:path) cloned to a " +
		"temporary directory and read there"
	got := argumentsBlock([]argDoc{{Name: "path", Help: long}})
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("a 180-character description did not wrap:\n%s", got)
	}
	// Display columns, not bytes: these descriptions are full of em dashes,
	// and a byte count would call a line over-long that a terminal renders
	// well inside the wrap.
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > argWrapWidth {
			t.Errorf("line runs to %d columns, past the %d wrap:\n%s", w, argWrapWidth, l)
		}
	}
	// "  path  " is 8 columns, so every continuation hangs under the "d" of
	// "directory" rather than under "path".
	for _, l := range lines[2:] {
		if !strings.HasPrefix(l, strings.Repeat(" ", 8)) || strings.HasPrefix(l, strings.Repeat(" ", 9)) {
			t.Errorf("continuation lost the description column: %q", l)
		}
	}
	var rejoined []string
	for _, l := range lines[1:] {
		rejoined = append(rejoined, strings.Fields(l)...)
	}
	if got := strings.Join(rejoined, " "); got != "path "+long {
		t.Errorf("wrapping lost or broke a word:\n got %q\nwant %q", got, "path "+long)
	}
}

// The block has to reach the command, not just exist: attach already collects
// a capability's positionals to build the usage line, so the descriptions ride
// along from the same slice and no capability has to opt in.
func TestACapabilityCommandDocumentsItsArguments(t *testing.T) {
	root := NewRoot(testRegistry(t), "test")
	cmd, _, err := root.Find([]string{"demo", "item", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Long, "Arguments:") || !strings.Contains(cmd.Long, "name  filter") {
		t.Errorf("demo item list did not document <name>:\n%s", cmd.Long)
	}
	// And the summary and description it already carried are still there,
	// above it.
	if !strings.HasPrefix(cmd.Long, "list items") {
		t.Errorf("the block displaced the description:\n%s", cmd.Long)
	}
}

// demo.item.pick declares a required positional with no Help. A heading over a
// bare name is worse than the usage line alone, so it earns no section.
func TestACapabilityWithNoDescribedArgumentGetsNoSection(t *testing.T) {
	root := NewRoot(testRegistry(t), "test")
	cmd, _, err := root.Find([]string{"demo", "item", "pick"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cmd.Long, "Arguments:") {
		t.Errorf("an undescribed positional earned a section:\n%s", cmd.Long)
	}
}

// Several hand-written commands carry a Short and no Long — `rta profile show`
// is one. fang renders cmp.Or(Long, Short), so giving such a command a Long
// consisting only of an arguments block would document the argument by
// deleting the sentence saying what the command does. documentArgs seeds from
// Short for exactly that case.
func TestDocumentingAnArgumentNeverDropsASummaryOnlyCommand(t *testing.T) {
	cmd := &cobra.Command{Use: "show <profile>", Short: "What one environment sets"}
	documentArgs(cmd, argDoc{Name: "profile", Help: "environment to describe"})
	if !strings.HasPrefix(cmd.Long, "What one environment sets") {
		t.Errorf("the summary was lost:\n%s", cmd.Long)
	}
	if !strings.Contains(cmd.Long, "profile  environment to describe") {
		t.Errorf("the argument was not documented:\n%s", cmd.Long)
	}
}

// And a command that already has a Long keeps it: Short is the fallback, not
// an addition, or every such command would open by saying the same thing
// twice.
func TestDocumentingAnArgumentDoesNotRepeatAnExistingLong(t *testing.T) {
	cmd := &cobra.Command{Use: "rm <profile>", Short: "Remove an environment",
		Long: "Without --plugin, removes the environment."}
	documentArgs(cmd, argDoc{Name: "profile", Help: "environment to remove"})
	if strings.Contains(cmd.Long, "Remove an environment") {
		t.Errorf("Short was appended to an existing Long:\n%s", cmd.Long)
	}
}

// The guard that keeps this true. Rendering the descriptions fixed every
// capability command at once and 22 hand-written ones by hand; nothing but a
// test stops the next one from shipping undocumented, and the hand-written
// trees have no Inputs to fall back on, so theirs would simply be absent with
// no signal at all.
//
// Over the real registry rather than the demo one: the point is the shipped
// surface, and a capability that forgets to describe a positional is exactly
// what this should refuse.
func TestEveryCommandWithAnArgumentDocumentsIt(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	var undocumented []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if args := usePositionals(c.Use); len(args) > 0 && !strings.Contains(c.Long, "Arguments:") {
			undocumented = append(undocumented,
				c.CommandPath()+" — "+strings.Join(args, ", "))
		}
	}
	walk(NewRoot(reg, "test"))
	if len(undocumented) > 0 {
		t.Errorf("%d commands take an argument that --help never describes:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}
}

// usePositionals pulls the argument tokens out of a cobra Use line, ignoring
// the two cobra and fang put there themselves and the `--` passthrough that is
// a command's own argv rather than an argument it names.
func usePositionals(use string) []string {
	var out []string
	for _, tok := range regexp.MustCompile(`<[^>]+>|\[[^\]]+\]`).FindAllString(use, -1) {
		inner := strings.Trim(tok, "<>[]")
		if inner == "--flags" || inner == "command" || strings.HasPrefix(inner, "--") {
			continue
		}
		out = append(out, inner)
	}
	return out
}

// An argument nobody described earns no row, and a command whose arguments are
// all undescribed earns no section: an "Arguments:" heading over a list of bare
// names tells a reader less than the usage line above it already did.
func TestArgumentsBlockSkipsWhatNobodyDescribed(t *testing.T) {
	if got := argumentsBlock([]argDoc{{Name: "name"}}); got != "" {
		t.Errorf("undescribed argument rendered a section:\n%s", got)
	}
	got := argumentsBlock([]argDoc{{Name: "name"}, {Name: "mode", Help: "how"}})
	if strings.Contains(got, "name") {
		t.Errorf("undescribed argument earned a row:\n%s", got)
	}
	if !strings.Contains(got, "mode  how") {
		t.Errorf("described argument lost its row:\n%s", got)
	}
}
