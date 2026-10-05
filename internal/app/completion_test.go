package app

import (
	"strings"
	"testing"
)

// The binary's own help for completion printed the recipe the installation
// page says to avoid: `> "${fpath[1]}/_rta"`, a directory that is often not
// the person's to write. The three shells with a conventional directory name
// one the person owns.
func TestTheCompletionHelpIsTheRecipeThatWorks(t *testing.T) {
	for shell, want := range map[string]string{
		"zsh":  "~/.zsh/completions",
		"bash": "~/.local/share/bash-completion/completions",
		"fish": "~/.config/fish/completions",
	} {
		out, _, err := run(t, testRegistry(t), "completion", shell, "--help")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("`rta completion %s --help` does not name %s:\n%s", shell, want, out)
		}
		if strings.Contains(out, "fpath[1]") {
			t.Errorf("`rta completion %s --help` teaches the recipe that fails with permission denied:\n%s", shell, out)
		}
	}
}

// Two hundred lines of shell on a screen scroll the instruction off it and
// nobody reads them there. A terminal gets the steps; a pipe or a file, which is
// how the script is used, gets the script.
func TestCompletionOnATerminalShowsTheStepsAndInAPipeTheScript(t *testing.T) {
	reg := testRegistry(t)
	piped, _, err := run(t, reg, "completion", "zsh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(piped, "#compdef rta") || strings.Contains(piped, "mkdir -p") {
		t.Errorf("a pipe did not get the script:\n%.200s", piped)
	}

	onATerminal(t)
	for _, shell := range []string{"zsh", "bash", "fish"} {
		shown, _, err := run(t, reg, "completion", shell)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(shown, "compdef") || strings.Contains(shown, "complete -") || strings.Count(shown, "\n") > 20 {
			t.Errorf("a terminal was sent the %s script:\n%.300s", shell, shown)
		}
		if !strings.Contains(shown, "rta completion "+shell+" >") {
			t.Errorf("a terminal was not shown the command to run for %s:\n%s", shell, shown)
		}
	}
}
