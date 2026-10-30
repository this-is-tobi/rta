package cli

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/pkg/view"
)

func drawn(t *testing.T, body string, noColor bool, width int) string {
	t.Helper()
	var out bytes.Buffer
	if err := Render(&out, view.Text{Body: body, Markdown: true}, Options{Format: Pretty, NoColor: noColor, Width: width}); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

const noteBody = "# Deploy plan\n\nRoll out **staging** first, then *prod*.\n\n" +
	"- check `rta kv get db/password`\n- read [the runbook](https://example.org/run)\n\n" +
	"1. drain\n2. upgrade\n\n> careful with the replica\n\n```\nkubectl rollout restart deploy/api\n```\n\n---\n"

// Without colour the markers are what says what the text is, so they stay, and
// nothing a terminal would draw is added: the text reads as it was written,
// lists and quotes drawn with the glyphs every other screen uses.
func TestMarkdownWithoutColourKeepsItsMarkersAndReadsAsWritten(t *testing.T) {
	got := drawn(t, noteBody, true, 80)
	for _, want := range []string{
		"# Deploy plan",
		"Roll out staging first, then prod.",
		"• check `rta kv get db/password`",
		"• read the runbook (https://example.org/run)",
		"1. drain",
		"│ careful with the replica",
		"  kubectl rollout restart deploy/api",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the note lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("a note drawn without colour carries an escape:\n%q", got)
	}
	if strings.Contains(got, "```") || strings.Contains(got, "\n\n\n") {
		t.Errorf("the fence or a run of blank lines was drawn:\n%s", got)
	}
}

// With colour the markers give way to the styles they stand for.
func TestMarkdownWithColourStylesWhatTheMarkersSaid(t *testing.T) {
	got := drawn(t, noteBody, false, 80)
	plain := ansi.Strip(got)
	for _, want := range []string{"Deploy plan", "Roll out staging first, then prod.", "check rta kv get db/password"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the note lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "# Deploy") || strings.Contains(plain, "**") || strings.Contains(plain, "`rta") {
		t.Errorf("a marker was drawn as text where a style stands for it:\n%s", plain)
	}
}

// A code span is left as it is inside, since a ** in a command is not emphasis,
// and emphasis needs text on both sides of its markers.
func TestCodeAndArithmeticAreNotEmphasis(t *testing.T) {
	got := drawn(t, "run `ls **/*.go` and 5 * 3 * 2", true, 80)
	if !strings.Contains(got, "run `ls **/*.go` and 5 * 3 * 2") {
		t.Errorf("code or arithmetic was read as emphasis: %q", got)
	}
}

// A list item, a quote and a paragraph wrap inside the width, each continuing
// under itself.
func TestMarkdownWrapsInsideTheWidthAndHangsUnderItself(t *testing.T) {
	long := strings.Repeat("word ", 30)
	got := drawn(t, "- "+long+"\n\n> "+long+"\n\n"+long, true, 40)
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if lipgloss.Width(line) > 40 {
			t.Errorf("a line is %d wide, past 40: %q", lipgloss.Width(line), line)
		}
	}
	if !strings.Contains(got, "\n  word") || !strings.Contains(got, "\n│ word") {
		t.Errorf("the item or the quote does not continue under itself:\n%s", got)
	}
}

// What the body carries cannot pass as a style or a command to the terminal:
// it is cleaned after the styling, as glamour's output was.
func TestAHostileNoteBodyIsCleanedAfterItIsStyled(t *testing.T) {
	got := drawn(t, "# hi\x1b]52;c;aGk=\x07 there\n\n**\x1b[2Jbold**", false, 80)
	if strings.Contains(got, "\x1b]52") || strings.Contains(got, "\x1b[2J") {
		t.Errorf("a control sequence from the body reached the terminal: %q", got)
	}
}
