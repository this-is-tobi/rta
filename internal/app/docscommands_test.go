package app

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The quickstart told a new reader to run `rta cert check example.com`, and
// no such verb exists — cert has chain, expiry, inspect, pem and tls. It sat
// on the second page anyone reads, in a bash block, for long enough to ship,
// because a command spelled in prose is checked by nobody and looks right at
// any value. The count test beside this one exists for the same reason; this
// one covers the spelling of every command the docs ask a reader to type.
//
// Checked against the real command tree rather than a list kept here, so a
// renamed verb fails the page that still uses the old name, and a management
// command (`rta plugin index add`) is covered exactly as a capability is.
// Only what reads as a command is checked: fenced shell blocks and inline
// code spans. Output blocks — the ones with no language — quote rta's own
// sentences ("rta mcp server listening on stdio"), which are not commands
// and must not be read as one.
func TestEveryCommandTheDocsSpellExists(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	// NewRoot attaches cobra's own completion and help, so `rta completion
	// zsh` and `rta help` are in the tree like every other command.
	root := NewRoot(reg, "test")
	tree := map[string]*cobra.Command{}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			p := prefix + " " + sub.Name()
			tree[p] = sub
			walk(sub, p)
		}
	}
	walk(root, "rta")

	repo := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(repo, "docs", "*", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	top, _ := filepath.Glob(filepath.Join(repo, "docs", "*.md"))
	pages = append(append(pages, top...), filepath.Join(repo, "README.md"))
	if len(pages) < 10 {
		t.Fatalf("found only %d doc pages under %s", len(pages), repo)
	}
	for _, page := range pages {
		rel, _ := filepath.Rel(repo, page)
		for _, snippet := range commandSnippets(readDoc(t, repo, rel)) {
			for _, m := range spelledCommand.FindAllStringSubmatch(snippet.text, -1) {
				words := strings.Fields(m[1])
				if _, known := tree["rta "+words[0]]; !known {
					// Not rta's own noun: a plugin from another repository, or
					// prose that happened to start with the word.
					continue
				}
				longest := 0
				for k := len(words); k >= 1; k-- {
					if _, ok := tree["rta "+strings.Join(words[:k], " ")]; ok {
						longest = k
						break
					}
				}
				cmd := tree["rta "+strings.Join(words[:longest], " ")]
				// A leaf takes arguments, so whatever follows it is fine. A
				// group takes verbs, so a word after it that is not one is a
				// command nobody can run.
				if longest == len(words) || !cmd.HasSubCommands() {
					continue
				}
				if cmd.SuggestionsMinimumDistance <= 0 {
					cmd.SuggestionsMinimumDistance = 2
				}
				hint := ""
				if s := cmd.SuggestionsFor(words[longest]); len(s) > 0 {
					hint = " — did you mean " + strings.Join(s, " or ") + "?"
				}
				t.Errorf("%s:%d: `rta %s` names no command: %q is not a verb of `%s`%s",
					rel, snippet.line, strings.Join(words, " "), words[longest], cmd.CommandPath(), hint)
			}
		}
	}
}

// spelledCommand is `rta` followed by one to three words, bounded on the
// left so that rta-plugin-pg, ghcr.io/…/rta:latest and rta_0.1.0_linux are
// not read as commands. Three words is the deepest the tree goes.
var spelledCommand = regexp.MustCompile("(?:^|[\\s$(|;&`'\"])rta((?: [a-z][a-z0-9-]*){1,3})")

type snippet struct {
	line int
	text string
}

// commandFences are the fence languages whose contents are commands a reader
// types. A fence with no language is output rta printed, and yaml, json and
// go carry rta only as a string.
var commandFences = map[string]bool{
	"bash": true, "sh": true, "shell": true, "zsh": true, "console": true, "dockerfile": true,
}

// commandSnippets yields every line of a shell fence and every inline code
// span outside one, with the line it came from.
func commandSnippets(body string) []snippet {
	var out []snippet
	inFence, fenceIsCommand := false, false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				inFence, fenceIsCommand = false, false
			} else {
				inFence = true
				fenceIsCommand = commandFences[strings.TrimPrefix(trimmed, "```")]
			}
			continue
		}
		if inFence {
			if fenceIsCommand {
				out = append(out, snippet{line: i + 1, text: line})
			}
			continue
		}
		parts := strings.Split(line, "`")
		// Odd indices are inside backticks.
		for j := 1; j < len(parts); j += 2 {
			out = append(out, snippet{line: i + 1, text: parts[j]})
		}
	}
	return out
}

// The spelling test above checks that a command the docs ask for exists. That
// is not that it can be typed: the installation page ended on `rta net probe
// db.internal:5432`, a verb that exists, taking a host and a port as two
// arguments, given one. A reader copying it got "missing <port>" from the one
// page that sends them into a cluster to debug a database.
//
// So every command line in a shell fence is parsed as the command it names —
// its flags, then its arguments — by the real tree, and refused where the tree
// would refuse it. Only what can be judged from the text alone: a line with a
// placeholder, an ellipsis, a substitution or a plugin the default build does
// not carry is skipped, and so is anything after a pipe or a redirect.
func TestEveryCommandTheDocsSpellParsesAsWritten(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	repo := repoRoot(t)
	pages, err := filepath.Glob(filepath.Join(repo, "docs", "*", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	top, _ := filepath.Glob(filepath.Join(repo, "docs", "*.md"))
	pages = append(append(pages, top...), filepath.Join(repo, "README.md"))
	for _, page := range pages {
		rel, _ := filepath.Rel(repo, page)
		root := NewRoot(reg, "test")
		for _, line := range shellLines(readDoc(t, repo, rel)) {
			words, ok := plainCommand(line.text)
			if !ok {
				continue
			}
			cmd, args, err := root.Find(words)
			if err != nil || cmd == root || cmd.HasSubCommands() {
				continue
			}
			if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
				continue
			}
			if err := cmd.ParseFlags(args); err != nil {
				t.Errorf("%s:%d: `%s`: %v", rel, line.line, line.text, err)
				continue
			}
			if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
				t.Errorf("%s:%d: `%s`: %v", rel, line.line, line.text, err)
			}
		}
	}
}

// shellLines is every logical line of every shell fence, a backslash
// continuation joined onto the line it continues, and the inline code spans
// outside a fence that run rta in its image, with the line it began on. Not
// the other spans: a span names a command in a sentence — `rta grant allow` —
// and is no more complete than the sentence around it.
func shellLines(body string) []snippet {
	var out []snippet
	inFence, fenceIsCommand := false, false
	var pending *snippet
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			fenceIsCommand = inFence && commandFences[strings.TrimPrefix(trimmed, "```")]
			pending = nil
			continue
		}
		if !inFence {
			for j, span := range strings.Split(line, "`") {
				if j%2 == 1 && ranInImage(span) != span {
					out = append(out, snippet{line: i + 1, text: span})
				}
			}
			continue
		}
		if !fenceIsCommand {
			continue
		}
		if pending != nil {
			pending.text += " " + trimmed
		} else {
			out = append(out, snippet{line: i + 1, text: trimmed})
			pending = &out[len(out)-1]
		}
		if strings.HasSuffix(pending.text, "\\") {
			pending.text = strings.TrimSpace(strings.TrimSuffix(pending.text, "\\"))
		} else {
			pending = nil
		}
	}
	return out
}

// ranInImage turns `docker run … ghcr.io/this-is-tobi/rta:latest <args>` and
// `kubectl run … --image=ghcr.io/this-is-tobi/rta:latest -- <args>` into the
// `rta <args>` they run, and leaves any other line as it was.
func ranInImage(line string) string {
	if !strings.Contains(line, "this-is-tobi/rta") {
		return line
	}
	words := strings.Fields(line)
	if len(words) < 2 || (words[0] != "docker" && words[0] != "kubectl") || words[1] != "run" {
		return line
	}
	for i, w := range words {
		switch {
		case words[0] == "kubectl" && w == "--":
			return "rta " + strings.Join(words[i+1:], " ")
		case words[0] == "docker" && strings.Contains(w, "this-is-tobi/rta"):
			return "rta " + strings.Join(words[i+1:], " ")
		}
	}
	return line
}

// plainCommand splits a line that is one rta command and nothing the text
// cannot settle: no placeholder, ellipsis, substitution or variable, nothing
// after a pipe, a redirect, a list or a comment, and an ordinary word-by-word
// quoting. The words are rta's arguments, `rta` itself dropped.
func plainCommand(line string) ([]string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
	// The image's entrypoint is rta, so what follows the image — or the `--`
	// of a kubectl run — is rta's own arguments.
	line = ranInImage(line)
	if !strings.HasPrefix(line, "rta ") || strings.ContainsAny(line, "<$`…") || strings.Contains(line, "...") {
		return nil, false
	}
	var words []string
	var cur strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, cur.String())
		}
		cur.Reset()
		started = false
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == ' ' || r == '\t':
			flush()
		case r == '|' || r == ';' || r == '&' || r == '#' || r == '(' || r == ')' || r == '>':
			flush()
			return words[1:], len(words) > 1
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return words[1:], len(words) > 1
}
