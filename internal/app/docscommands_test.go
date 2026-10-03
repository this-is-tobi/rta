package app

import (
	"fmt"
	"os"
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
	tree := rtaCommandTree(t)

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
				if problem := unknownVerb(tree, strings.Fields(m[1])); problem != "" {
					t.Errorf("%s:%d: %s", rel, snippet.line, problem)
				}
			}
		}
	}
}

// rtaCommandTree is every command of the real tree by its full path, `rta
// kv get` and the like. NewRoot attaches cobra's own completion and help, so
// `rta completion zsh` and `rta help` are in it like every other command.
func rtaCommandTree(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	tree := map[string]*cobra.Command{}
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			p := prefix + " " + sub.Name()
			tree[p] = sub
			walk(sub, p)
		}
	}
	walk(NewRoot(reg, "test"), "rta")
	return tree
}

// unknownVerb is what is wrong with `rta` followed by words, or "" when
// nothing is: the first word is not one of rta's own nouns (a plugin from
// another repository, or prose that happened to start with it), or the words
// reach a command, or a leaf that takes whatever follows as arguments. What it
// refuses is a word after a group that is not one of its verbs.
func unknownVerb(tree map[string]*cobra.Command, words []string) string {
	if _, known := tree["rta "+words[0]]; !known {
		return ""
	}
	longest := 0
	for k := len(words); k >= 1; k-- {
		if _, ok := tree["rta "+strings.Join(words[:k], " ")]; ok {
			longest = k
			break
		}
	}
	cmd := tree["rta "+strings.Join(words[:longest], " ")]
	// A leaf takes arguments, so whatever follows it is fine. A group takes
	// verbs, so a word after it that is not one is a command nobody can run.
	if longest == len(words) || !cmd.HasSubCommands() {
		return ""
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	hint := ""
	if s := cmd.SuggestionsFor(words[longest]); len(s) > 0 {
		hint = " — did you mean " + strings.Join(s, " or ") + "?"
	}
	return fmt.Sprintf("`rta %s` names no command: %q is not a verb of `%s`%s",
		strings.Join(words, " "), words[longest], cmd.CommandPath(), hint)
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

// pluginNouns are the commands a plugin from another repository brings, which
// the default build does not have: the first-party twelve, and the example
// plugin's.
var pluginNouns = map[string]bool{
	"pg": true, "mysql": true, "mariadb": true, "etcd": true, "qdrant": true, "redis": true,
	"s3": true, "vault": true, "kube": true, "cnpg": true, "docker": true, "keycloak": true,
	"hello": true,
}

// The commands rta tells a person to run are written in Go strings as well as
// in the docs — an error's hint, a card's description, the schema an editor
// shows on hover — and nothing checked those: the config schema described the
// theme block as "the names `rta theme` lists", and there is no such command,
// so the one place an editor's reader looked for the answer named a command
// that answers "unknown". The docs have had this test for a long time; the
// strings the binary prints are the same promise.
//
// A code span that begins with `rta` in a string literal (not a comment, which
// nobody reads at a terminal) is held to the same rule as a docs page: where
// it names a command rta or a plugin has, and the words after it are verbs that exist.
func TestEveryCommandAGoStringTellsAPersonToRunExists(t *testing.T) {
	tree := rtaCommandTree(t)
	root := repoRoot(t)
	span := regexp.MustCompile("`rta((?: [a-z][a-z0-9-]*){1,3})")

	checked := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".local", "docs", "proto":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, m := range span.FindAllStringSubmatch(line, -1) {
				checked++
				words := strings.Fields(m[1])
				if _, builtIn := tree["rta "+words[0]]; !builtIn && !pluginNouns[words[0]] {
					t.Errorf("%s:%d: `rta %s` names no command: %q is neither one of rta's own nor a plugin's", rel, i+1, m[1][1:], words[0])
					continue
				}
				if problem := unknownVerb(tree, words); problem != "" {
					t.Errorf("%s:%d: %s", rel, i+1, problem)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the checkout: %v", err)
	}
	if checked < 30 {
		t.Fatalf("checked %d commands in Go strings, want the seventy or so there are; has the quoting moved?", checked)
	}
}
