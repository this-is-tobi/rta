// Package pipein reads what was piped to a built-in's standard input, for the
// capabilities that take their value from a pipe when no argument names it.
//
// Three of them do — debug.ansi reads the text it explains, keys.restore the
// seed words, and codec's JOSE decoders the token or key — and the first two
// each carried their own copy of the same eight lines, one bounded and one
// not. A pipe is also the one channel that keeps a live bearer token out of
// the shell's history and out of the process table, where an argument is
// readable by every user on the machine for as long as the call runs, which
// is why a third copy was the moment to stop copying.
package pipein

import (
	"errors"
	"io"

	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// ErrTooLarge is returned when the pipe holds more than the caller's limit.
// The caller words the refusal, because only it knows what the pipe was
// supposed to hold.
var ErrTooLarge = errors.New("stdin holds more than this input takes")

// Read returns what was piped to a CLI call's standard input, bounded to
// limit bytes, or "" — not an error — whenever there is nothing to read
// rather than something merely absent: a surface other than the CLI, where
// stdin is not this call's business (the TUI owns the screen, and MCP's stdin
// is the agent's request stream), and a CLI call with a terminal behind it,
// where reading would block on a person who is never going to send EOF.
func Read(req plugin.Request, limit int) (string, error) {
	if req.Surface() != plugin.SurfaceCLI {
		return "", nil
	}
	f := stdio.Real()
	if term.IsTerminal(int(f.Fd())) {
		return "", nil
	}
	return ReadFrom(f, limit)
}

// ReadFrom is Read's bounded read without its surface and terminal checks,
// separate so a test can hand it a reader.
//
// It reads one byte past the limit rather than stopping at it, because a pipe
// that holds exactly the limit and one that holds more must not look alike: a
// truncated seed phrase or token handed on as if it were whole is a wrong
// answer, where a refusal is only an inconvenience.
func ReadFrom(r io.Reader, limit int) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", ErrTooLarge
	}
	return string(data), nil
}
