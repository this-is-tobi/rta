package app

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func plainHelp(cmd *cobra.Command, width int) string {
	var buf bytes.Buffer
	writeHelp(&buf, cmd, width, colorprofile.NoTTY)
	return buf.String()
}

func walkCommands(c *cobra.Command, visit func(*cobra.Command)) {
	visit(c)
	for _, sub := range c.Commands() {
		walkCommands(sub, visit)
	}
}

// The command list and the flag table did not wrap at the terminal: a summary
// longer than the room left ran off the edge and the terminal broke it
// mid-word, with no indent, on the first screen anybody sees. Every screen of
// the real tree is drawn at 80 columns and at 60, and no line is wider than
// that — except an example or a code block of the long text, which are never
// cut short because a command truncated is one that cannot be copied, and a
// single word with no place to break.
func TestEveryHelpScreenFitsTheTerminal(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for _, width := range []int{80, 60} {
		walkCommands(root, func(c *cobra.Command) {
			verbatim := map[string]bool{}
			for _, l := range exampleLines(c) {
				verbatim["    "+l] = true
			}
			for _, l := range strings.Split(c.Long, "\n") {
				if strings.HasPrefix(l, " ") {
					verbatim["  "+l] = true
				}
			}
			for _, line := range strings.Split(plainHelp(c, width), "\n") {
				if ansi.StringWidth(line) <= width || verbatim[line] {
					continue
				}
				indent := len(line) - len(strings.TrimLeft(line, " "))
				longest := 0
				for _, word := range strings.Fields(line) {
					longest = max(longest, ansi.StringWidth(word))
				}
				if indent+longest > width {
					continue
				}
				t.Errorf("`%s --help` at %d columns has a line %d wide: %q", c.CommandPath(), width, ansi.StringWidth(line), line)
			}
		})
	}
}

// Help sent to a pipe is laid out for the terminal a pipe most often ends in, a
// pager, which is eighty columns whatever the one that ran the command was: a
// wider layout is broken again by the pager at the edge, mid-word, with no
// hanging indent. COLUMNS, which says a width was asked for, still wins.
func TestHelpToAPipeIsEightyColumnsWide(t *testing.T) {
	savedTTY := isTTY
	t.Cleanup(func() { isTTY = savedTTY })
	isTTY = func() bool { return false }
	t.Setenv("COLUMNS", "")
	if got := helpWidth(); got != 80 {
		t.Errorf("help to a pipe is %d columns wide, want 80", got)
	}
	t.Setenv("COLUMNS", "100")
	if got := helpWidth(); got != 100 {
		t.Errorf("COLUMNS=100 gave %d columns, want 100", got)
	}
}

// A description that wraps hangs under its own first line, so the column of
// descriptions reads as a column.
func TestAWrappedDescriptionHangsUnderItsFirstLine(t *testing.T) {
	reg := testRegistry(t)
	root := NewRoot(reg, "test")
	root.Commands()[0].Short = "a deliberately long summary that cannot possibly fit on the one line it was given at this width"
	got := plainHelp(root, 50)
	var first, second string
	lines := strings.Split(got, "\n")
	for i, l := range lines {
		if strings.Contains(l, "a deliberately long") {
			first, second = l, lines[i+1]
		}
	}
	if first == "" {
		t.Fatalf("the summary is not in the help:\n%s", got)
	}
	column := strings.Index(first, "a deliberately")
	if column <= 4 || !strings.HasPrefix(second, strings.Repeat(" ", column)) || strings.TrimSpace(second) == "" {
		t.Errorf("the second line does not hang at column %d:\n%s\n%s", column, first, second)
	}
}

// The renderer used to put a title-casing transform over the first word of a
// description, so "One-screen host health" read "One-Screen" and "TCP
// connect-scan" read "Tcp". A summary is drawn the way it was written.
func TestASummaryIsDrawnTheWayItWasWritten(t *testing.T) {
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	for _, p := range []plugin.Plugin{
		{Name: "alpha", Summary: "One-screen host health", Capabilities: []plugin.Capability{
			{ID: "alpha.one", Summary: "TCP connect-scan ports on a host", Safety: plugin.Read, Run: run}}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	root := NewRoot(reg, "test")
	for _, c := range []struct {
		cmd  *cobra.Command
		want string
	}{{root, "One-screen host health"}} {
		if got := plainHelp(c.cmd, 80); !strings.Contains(got, c.want) {
			t.Errorf("`%s --help` does not say %q:\n%s", c.cmd.CommandPath(), c.want, got)
		}
	}
	sub, _, _ := root.Find([]string{"alpha"})
	if got := plainHelp(sub, 80); !strings.Contains(got, "TCP connect-scan ports on a host") ||
		strings.Contains(got, "Tcp") {
		t.Errorf("a summary starting with an initialism was re-cased:\n%s", got)
	}
}

// A flag is listed where it means something. --yes and --dry-run are for a
// command that writes, and the --profile every capability gets is for one with a
// connection to point it at; listing them on `rta sys cpu` taught people that
// the flags were decoration. A command's own --profile (`grant allow` narrows a
// grant by it) is listed whatever it connects to. They are accepted everywhere
// all the same — a script that passes --yes to every command keeps working.
func TestAFlagIsListedOnlyWhereItMeansSomething(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{
		Name: "conn", Summary: "has a connection",
		Capabilities: []plugin.Capability{{
			ID: "conn.ping", Summary: "ping the service", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host", Local: true, Endpoint: plugin.EndpointAddress}},
			Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for _, c := range []struct {
		path   []string
		listed []string
		absent []string
	}{
		{[]string{"sys", "cpu"}, []string{"--cores", "--output"}, []string{"--dry-run", "--yes", "--profile"}},
		{[]string{"note", "add"}, []string{"--dry-run", "--yes"}, []string{"--profile"}},
		{[]string{"note", "rm"}, []string{"--dry-run", "--yes"}, []string{"--profile"}},
		{[]string{"http", "get"}, []string{"--output"}, []string{"--yes", "--dry-run", "--profile"}},
		{[]string{"conn", "ping"}, []string{"--profile", "--host"}, []string{"--yes", "--dry-run"}},
		{[]string{"kv"}, []string{"--output"}, []string{"--yes", "--dry-run"}},
		{[]string{"doctor"}, []string{"--output"}, []string{"--yes", "--dry-run"}},
		{[]string{"plugin", "index", "list"}, []string{"--output"}, []string{"--yes", "--dry-run"}},
		{[]string{"profile", "set"}, []string{"--dry-run", "--yes"}, nil},
		{[]string{"grant", "allow"}, []string{"--profile", "--yes"}, nil},
		{[]string{"dashboard", "add"}, []string{"--profile"}, nil},
	} {
		cmd, _, err := root.Find(c.path)
		if err != nil {
			t.Fatal(err)
		}
		got := plainHelp(cmd, 100)
		for _, flag := range c.listed {
			if !strings.Contains(got, flag) {
				t.Errorf("`rta %s --help` does not list %s:\n%s", strings.Join(c.path, " "), flag, got)
			}
		}
		for _, flag := range c.absent {
			if strings.Contains(got, flag) {
				t.Errorf("`rta %s --help` lists %s, which means nothing there:\n%s", strings.Join(c.path, " "), flag, got)
			}
		}
	}
	// Accepted, though not listed.
	if _, _, err := run(t, testRegistry(t), "demo", "item", "list", "--yes", "--dry-run"); err != nil {
		t.Errorf("a read capability refused --yes --dry-run: %v", err)
	}
}

// `rta --help` was a flat list with no next step. It opens with the four
// things to type first, ends on where the documentation is, says bare `rta`
// opens the dashboard, and files the incident and team controls under their own
// heading after setup.
func TestTheRootHelpSaysWhereToStart(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	got := plainHelp(NewRoot(reg, "test"), 80)
	for _, want := range []string{
		"rta sys overview", "rta mcp install claude", "rta doctor", "no arguments opens it", docsURL,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("`rta --help` does not say %q:\n%s", want, got)
		}
	}
	order := []string{"START HERE", "USAGE", "CAPABILITIES", "AGENTS AND CONSENT", "SETUP", "ADVANCED", "FLAGS"}
	last := -1
	for _, heading := range order {
		at := strings.Index(got, heading)
		if at < 0 || at < last {
			t.Errorf("heading %q is missing or out of order in:\n%s", heading, got)
		}
		last = max(last, at)
	}
	if strings.Contains(got, "START HERE") != true || strings.Index(got, "START HERE") > strings.Index(got, "CAPABILITIES") {
		t.Error("the start block is not the first thing after the description")
	}
}

// The start block is the top of eighty lines of help, and a terminal that holds
// twenty-four of them shows the bottom: what a newcomer sees last has to say
// what to type first.
func TestTheLastScreenOfTheRootHelpSaysWhatToTypeFirst(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(plainHelp(NewRoot(reg, "test"), 80), "\n"), "\n")
	last := strings.Join(lines[len(lines)-24:], "\n")
	for _, want := range []string{"rta sys overview", docsURL} {
		if !strings.Contains(last, want) {
			t.Errorf("the last 24 lines of `rta --help` do not say %q:\n%s", want, last)
		}
	}
}

// A subcommand's help does not repeat the root's start block or its footer.
func TestOnlyTheRootHelpCarriesTheStartBlock(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cmd, _, _ := NewRoot(reg, "test").Find([]string{"sys"})
	got := plainHelp(cmd, 80)
	if strings.Contains(got, "START HERE") || strings.Contains(got, docsURL) {
		t.Errorf("`rta sys --help` carries the root's start block:\n%s", got)
	}
}

// --no-color is a flag of the help being asked for, and a pipe is never styled.
func TestHelpIsStyledOnlyWhereItCanBe(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	var styled, plain bytes.Buffer
	writeHelp(&styled, root, 80, colorprofile.TrueColor)
	writeHelp(&plain, root, 80, colorprofile.NoTTY)
	escape := regexp.MustCompile("\x1b\\[")
	if !escape.Match(styled.Bytes()) {
		t.Error("a truecolor terminal got help with no styling at all")
	}
	if escape.Match(plain.Bytes()) {
		t.Error("help to something that is not a terminal carries escape sequences")
	}
	out, _, err := run(t, testRegistry(t), "--no-color", "--help")
	if err != nil || escape.MatchString(out) {
		t.Errorf("--no-color help: err = %v, styled = %v", err, escape.MatchString(out))
	}
}
