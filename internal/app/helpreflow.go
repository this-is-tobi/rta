package app

import (
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

// reflowHelp joins the hand-wrapped lines of every command's long text into
// paragraphs, so that the renderer wraps them to the terminal and not to the
// column somebody wrapped them at when they wrote the text.
//
// **A line break inside a paragraph is the author's editor, not the author's
// meaning.** Help wraps long text to the terminal's width and leaves a line
// break where it finds one: text broken at seventy
// characters came out ragged on a wide terminal, each line stopping short of
// the edge, and as a long line and a short stub on a narrow one, where the
// terminal wrapped a line the author had already wrapped. About a hundred and
// fifty commands carry their text that way. Reflowing here, once, over the
// finished tree, keeps the source as readable as it was and fixes every one,
// where editing each text would leave the next one written to break again.
//
// What is not prose is left as it is: a paragraph with an indented line (the
// arguments block, an example, a table), one that opens with a list marker or
// a prompt, and a fenced block, which are laid out by their line breaks.
func reflowHelp(cmd *cobra.Command) {
	cmd.Long = reflowLong(cmd.Long)
	for _, sub := range cmd.Commands() {
		reflowHelp(sub)
	}
}

func reflowLong(long string) string {
	if !strings.Contains(long, "\n") {
		return long
	}
	paragraphs := strings.Split(long, "\n\n")
	fenced := false
	for i, p := range paragraphs {
		if strings.Count(p, "```")%2 == 1 {
			fenced = !fenced
			continue
		}
		if !fenced && isProse(p) {
			paragraphs[i] = joinLines(p)
		}
	}
	return strings.Join(paragraphs, "\n\n")
}

// isProse is whether p is lines of a sentence and nothing laid out by its
// line breaks.
func isProse(p string) bool {
	lines := strings.Split(p, "\n")
	if len(lines) < 2 || strings.Contains(p, "```") {
		return false
	}
	for _, line := range lines {
		if line == "" || unicode.IsSpace(rune(line[0])) || isListItem(line) {
			return false
		}
	}
	return true
}

// isListItem is whether line opens a list entry or a shell prompt, either of
// which is a line of its own.
func isListItem(line string) bool {
	for _, marker := range []string{"- ", "* ", "• ", "$ ", "> ", "| "} {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	digits := strings.TrimLeft(line, "0123456789")
	return len(digits) < len(line) && (strings.HasPrefix(digits, ". ") || strings.HasPrefix(digits, ") "))
}

func joinLines(p string) string {
	lines := strings.Split(p, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " ")
}
