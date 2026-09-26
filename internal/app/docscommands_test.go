package app

import (
	"path/filepath"
	"regexp"
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
