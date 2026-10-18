package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// drawn is a command's help as the test reads it, without the styling a
// terminal would add.
func drawn(cmd *cobra.Command) string { return ansi.Strip(newHelper(80).render(cmd)) }

func helpTree(t *testing.T) *cobra.Command {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return NewRoot(reg, "test")
}

func everyCommand(root *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{root}
	for _, sub := range root.Commands() {
		out = append(out, everyCommand(sub)...)
	}
	return out
}

// **A switch is on by being named.** The cli row of `rta explain sys.cpu` said
// `[--cores <bool>]`, and `rta sys cpu --cores true` is refused as an unexpected
// argument; the same row drew `--detail` bare. A flag that is given once more
// for each value, or takes a list, is a value and three dots.
func TestASwitchIsShownBareAndAListAsRepeatedValues(t *testing.T) {
	c := plugin.Capability{ID: "knob.turn", Summary: "turn it", Safety: plugin.Read, Detailed: true,
		Inputs: []plugin.Field{
			{Name: "cores", Type: plugin.Bool},
			{Name: "tool", Type: plugin.StringSlice},
			{Name: "data", Type: plugin.SecretSlice, Local: true},
			{Name: "count", Type: plugin.Int},
			{Name: "force", Type: plugin.Bool, Required: true},
			{Name: "name", Type: plugin.String, Required: true},
		}}
	got := cliForm(c)
	for _, want := range []string{
		"[--cores]", "[--tool <value>...]", "[--data <secret>...]", "[--count <int>]", " --force ", "--name <string>", "[--detail]",
	} {
		if !strings.Contains(got+" ", want) {
			t.Errorf("the cli form %q does not show %q", got, want)
		}
	}
	if strings.Contains(got, "<bool>") || strings.Contains(got, "stringSlice") {
		t.Errorf("the cli form %q still draws a Go type for a switch or a list", got)
	}
}

var usageSection = regexp.MustCompile(`(?s)USAGE\n(.*?)\n\n`)

// **`rta help X` and `rta X --help` are one page.** The second runs the command's
// flag parser, which merges the root's flags into its own, and the first never
// does: 85 of the 214 pages ended their usage line without the [--flags] the
// other drew.
func TestBothDoorsToAPageOfHelpDrawTheSameUsage(t *testing.T) {
	pristine, parsed := helpTree(t), helpTree(t)
	unparsed := everyCommand(pristine)
	for i, cmd := range everyCommand(parsed) {
		if err := cmd.ParseFlags(nil); err != nil {
			t.Fatal(err)
		}
		before, after := drawn(unparsed[i]), drawn(cmd)
		if usage := usageSection.FindString(after); !strings.Contains(usage, "[--flags]") {
			t.Errorf("%s: the usage line %q does not show that it takes flags, which every page lists", cmd.CommandPath(), usage)
		}
		if before != after {
			t.Errorf("%s: `help` draws\n%s\nand `--help` draws\n%s", cmd.CommandPath(),
				usageSection.FindString(before), usageSection.FindString(after))
		}
	}
	// And the page that had none says so: a group with flags to take.
	if usage := usageSection.FindString(drawn(unparsedByPath(t, unparsed, "rta sys"))); !strings.Contains(usage, "[--flags]") {
		t.Errorf("`rta help sys` shows %q, without [--flags]", usage)
	}
}

func unparsedByPath(t *testing.T, cmds []*cobra.Command, path string) *cobra.Command {
	t.Helper()
	for _, c := range cmds {
		if c.CommandPath() == path {
			return c
		}
	}
	t.Fatalf("no command %q", path)
	return nil
}

// **A group is described, not listed by its verbs.** `rta grant --help` drew
// `guard: off, on, remote, status` beside real sentences, for the switch that
// puts a passphrase in front of issuing grants.
func TestEveryBuiltInGroupHasASentenceOfItsOwn(t *testing.T) {
	root := helpTree(t)
	for _, cmd := range everyCommand(root) {
		if !cmd.HasSubCommands() || !cmd.HasParent() {
			continue
		}
		if strings.HasPrefix(cmd.Short, cmd.Name()+": ") || cmd.Short == cmd.Name()+" operations" {
			t.Errorf("%s is described as %q, which is its name and its verbs", cmd.CommandPath(), cmd.Short)
		}
	}
	for path := range groupSummaries {
		if cmd := commandAt(root, path); cmd == nil || !cmd.HasSubCommands() {
			t.Errorf("groupSummaries names %q, which is not a group", path)
		}
	}
}

// An input is a word in backticks in the text every surface shares, and on the
// command line it is a flag.
func TestAnInputInBackticksIsSpelledAsItsFlagOnTheCommandLine(t *testing.T) {
	desc := "Lists the rows. With `detail` it also shows the `ttl` of each, and `key` names one; `json` is a format."
	c := plugin.Capability{
		ID: "knob.turn", Summary: "turn it", Description: desc, Safety: plugin.Read, Detailed: true,
		Inputs: []plugin.Field{
			{Name: "key", Type: plugin.String, Positional: true, Help: "the key; `ttl` is not part of it"},
			{Name: "ttl", Type: plugin.String, Help: "how long, as `detail` shows it"},
		},
		Run: nil,
	}
	cmd := &cobra.Command{Use: "rta"}
	attach(cmd, c, &globalOpts{})
	turn := commandAt(cmd, "rta turn")
	if turn == nil {
		t.Fatal("the capability was not attached")
	}
	for _, want := range []string{"With `--detail` it also shows the `--ttl` of each", "`key` names one", "`json` is a format"} {
		if !strings.Contains(turn.Long, want) {
			t.Errorf("the help reads %q, want it to contain %q", turn.Long, want)
		}
	}
	if usage := turn.Flags().Lookup("ttl").Usage; usage != "how long, as `--detail` shows it" {
		t.Errorf("the flag's own description reads %q", usage)
	}
	if c.Description != desc {
		t.Error("the capability's own description, which an agent reads, was rewritten")
	}
}

// A command that only reads has nothing to confirm or to preview, and lists
// neither flag; it still accepts them, as every command does.
func TestACommandThatOnlyReadsListsNeitherDryRunNorYes(t *testing.T) {
	root := helpTree(t)
	for _, path := range []string{"rta config get", "rta config path", "rta config check", "rta config show"} {
		cmd := commandAt(root, path)
		if cmd == nil {
			t.Fatalf("no command %q", path)
		}
		help := ansi.Strip(newHelper(100).render(cmd))
		for _, flag := range []string{"--dry-run", "--yes"} {
			if strings.Contains(help, flag) {
				t.Errorf("`%s --help` lists %s", path, flag)
			}
		}
	}
	if help := drawn(commandAt(root, "rta config set")); !strings.Contains(help, "--dry-run") {
		t.Error("`rta config set`, which writes, no longer lists --dry-run")
	}
}
