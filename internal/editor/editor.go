// Package editor opens a file in the editor a person has chosen, and waits.
//
// A package of its own because two commands open one — a stored value and the
// config file — and what they have to get right is the same and easy to get
// subtly wrong: which variable names the editor, that it is split and never
// handed to a shell, and that the terminal is the editor's while it runs.
package editor

import (
	"os"
	"os/exec"
	"strings"

	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/internal/stdio"
)

// Command is the editor to run, and with what: $VISUAL, then $EDITOR, then vi.
//
// $EDITOR routinely carries flags — "code --wait", "emacsclient -nw",
// "subl -w" — so the value is split rather than treated as a program name.
// Split rather than handed to a shell, deliberately: somebody's $EDITOR is not
// a script to expand $, ` and ; out of on their behalf.
//
// vi, not nano: POSIX requires it, so it is the one editor that is certainly
// installed, and refusing to run until somebody exports a variable is a worse
// answer than an unfamiliar editor.
func Command() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if fields := strings.Fields(os.Getenv(env)); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

// Run starts argv on path with the terminal and waits for it to end. A var so
// a test, which has neither a terminal to hand over nor an editor to hand it
// to, can stand in for a person.
var Run = func(argv []string, path string) error {
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	// The editor gets the terminal, whole. Anything less and a full-screen
	// editor draws into a pipe.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio.Real(), os.Stdout, os.Stderr
	// The key an editor binds to interrupt — emacs's C-g — sends SIGINT to the
	// whole foreground group, rta included, and the editor is being used, not
	// asking rta to stop.
	defer shutdown.LendTerminal()()
	return cmd.Run()
}
