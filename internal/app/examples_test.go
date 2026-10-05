package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

// commandWords splits one example line the way a shell would for the cases an
// example uses — quotes, and a trailing comment — and nothing else.
func commandWords(line string) []string {
	var words []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == '#' && !inWord:
			return words
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// An example is only worth printing if it works. Every command line in the map
// is parsed against the real tree: the command is found, each flag is one it
// has, the arguments are as many as it takes, and what it requires is there. So
// a flag renamed or removed under an example fails here, and not for somebody
// who copied the line off the help.
func TestEveryExampleParsesAgainstTheRealTree(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	for path, lines := range examples {
		for _, line := range lines {
			words := commandWords(line)
			if len(words) == 0 {
				continue
			}
			if words[0] != "rta" {
				t.Errorf("%s: %q does not start with rta", path, line)
				continue
			}
			// A tree of its own for each line: a flag set keeps what the
			// last parse set, and the next line would inherit it.
			cmd, rest, err := NewRoot(reg, "test").Find(words[1:])
			if err != nil {
				t.Errorf("%s: %q: %v", path, line, err)
				continue
			}
			if cmd.CommandPath() != path {
				t.Errorf("%s lists %q, which is the example of %s", path, line, cmd.CommandPath())
				continue
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Errorf("%s: %q: %v", path, line, err)
				continue
			}
			if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
				t.Errorf("%s: %q: %v", path, line, err)
			}
			if err := cmd.ValidateRequiredFlags(); err != nil {
				t.Errorf("%s: %q: %v", path, line, err)
			}
		}
	}
}

// A key that names no command is an example nobody will see, and the likeliest
// way to get one is to rename a command.
func TestEveryExampleKeyIsACommandThatHasNoExampleOfItsOwn(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	found := map[string]bool{}
	walkCommands(root, func(c *cobra.Command) {
		found[c.CommandPath()] = true
		if _, listed := examples[c.CommandPath()]; listed && !strings.HasPrefix(c.Example, strings.Join(examples[c.CommandPath()], "\n")) {
			t.Errorf("%s declares an Example of its own and is in the examples map too", c.CommandPath())
		}
	})
	for path := range examples {
		if !found[path] {
			t.Errorf("examples has %q, which is not a command", path)
		}
	}
}

// The commands a person types every day each have one, and the list is what
// keeps a new daily command from landing without — and a removal from being
// quiet.
func TestTheDailyCommandsHaveExamples(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	for _, path := range []string{
		"grant allow", "grant revoke", "grant issue", "grant list", "grant renew",
		"lock add", "lock rm", "agent log", "agent allow", "agent show", "use",
		"kv set", "kv init", "plugin install", "plugin trust", "mcp install", "mcp serve",
		"policy init", "doctor", "init", "profile set",
	} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := plainHelp(cmd, 80); !strings.Contains(got, "EXAMPLES") {
			t.Errorf("`rta %s --help` has no examples:\n%s", path, got)
		}
	}
}

// An example line is never cut short — a command truncated is one that cannot
// be copied — so it is written to fit the screen it is read on: the indent help
// gives it and an 80-column terminal's width.
func TestExamplesFitAnEightyColumnTerminal(t *testing.T) {
	for path, lines := range examples {
		for _, line := range lines {
			if w := ansi.StringWidth(line) + helpIndent; w > 80 {
				t.Errorf("%s: the example %q is %d columns wide once indented", path, line, w)
			}
		}
	}
}
