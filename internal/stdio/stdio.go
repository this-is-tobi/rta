// Package stdio takes standard input away from anything rta launches.
//
// go-plugin's client.go does `cmd.Stdin = os.Stdin` unconditionally, with no
// ClientConfig field to suppress it, and it does so *after* the block that
// applies the host's own command settings — so a host that carefully sets
// cmd.Stdin to /dev/null has it overwritten one line later. There is no way
// to opt out at the call site.
//
// That matters most under `rta mcp serve`, which speaks its protocol on
// stdio: fd 0 in the host is the agent's JSON-RPC request stream, and
// sandbox-exec passes an already-open descriptor straight through to the
// child. Reproduced before this existed — a confined plugin took all eight
// tools/call lines whole and the parent read zero. It is the same defect on
// the human surfaces with a different symptom: fd 0 there is the user's
// keystrokes, and a plugin holding it eats them from the TUI.
//
// The contract specifies dup(2) on fd 0 plus a reopen. This does the simpler
// thing that covers the actual gap: go-plugin reads the `os.Stdin` *variable*
// at spawn time, so repointing that variable is sufficient. What the dup
// would additionally cover is a child inheriting raw fd 0 without going
// through exec.Cmd's descriptor plumbing, which is not reachable here:
// exec.Cmd sets a child's fds 0/1/2 from Stdin/Stdout/Stderr, and exec.Cmd is
// the only spawn path rta has. If a second one ever appears, this is the
// function that has to grow the dup.
//
// # Ownership, not courtesy
//
// This package was first written as a courtesy each surface performed for
// itself: `mcp serve` called Claim, and the human surfaces were meant to call
// Silence. Silence was never called by anything, and `mcp serve` called Claim
// from inside its own RunE — which cobra runs long after main() has already
// loaded plugins. Plugins are discovered eagerly at startup, because the
// command tree is built from their declarations, so *every* rta invocation
// spawned every installed plugin with the caller's fd 0. Reproduced with a
// binary that does not even complete the handshake: `printf 'secret' | rta
// explain sys.status` handed it 36 bytes.
//
// So fd 0 is owned here and taken once, by main, before anything is launched.
// Surfaces that need the real stream ask for it with Real. Nothing else in
// rta may name os.Stdin, and TestOnlyStdioNamesOsStdin enforces that by
// walking the tree — a rule that four surfaces have to remember is a rule
// that the fifth breaks, which is the history above.
package stdio

import (
	"fmt"
	"io"
	"os"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// claimed is the process's real standard input, held here from the moment
// Claim runs. A package variable because fd 0 is a process-wide resource with
// exactly one owner — os.Stdin is the same thing wearing a different name,
// and threading it through every constructor would only mean a surface could
// be built without it and silently read nothing.
var claimed *os.File

// Claim points os.Stdin at /dev/null and keeps the real stream for Real.
//
// It must be the first thing main does. Not "before the first plugin call" —
// before the first plugin *spawn*, which is what copies the descriptor, and
// rta spawns every installed plugin during startup to build its command tree
// from their declarations. Any later is too late, and "later" includes every
// cobra RunE.
//
// Idempotent: a second call is a no-op rather than a way to lose the real
// stream behind a second /dev/null.
func Claim() error {
	if claimed != nil {
		return nil
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	claimed, os.Stdin = os.Stdin, devnull
	return nil
}

// Real is the process's actual standard input: the agent's JSON-RPC stream
// under `mcp serve`, the user's keyboard everywhere else.
//
// Before Claim runs — in tests, and in anything embedding rta as a library —
// os.Stdin is still the real stream, so this returns it and the caller cannot
// tell the difference. That is the point: a surface asks for the real input
// and gets it, whether or not it has been taken away from the children yet.
//
// The result is NOT closed by anything here and callers must not close it:
// closing fd 0 in a live process makes every later read fail confusingly
// rather than releasing anything.
func Real() *os.File {
	if claimed == nil {
		return os.Stdin
	}
	return claimed
}

// readPassword is the reader for a secret's line (readTerminalSecret), a var
// so a test can answer a prompt without a terminal.
var readPassword = readTerminalSecret

// ReadSecret asks the person at the terminal for a secret: prompt on standard
// error, one line read from the real standard input with echo off, and the
// line the answer was not echoed on ended. Standard error, never standard
// output: `eval "$(rta kv env x)"` must not eval a prompt, and a redirected
// `kv get > file` must not hold one. The caller has already decided a person
// is there to ask — a CLI request with a terminal on standard input.
//
// The prompt is marked open while it waits (shutdown.Prompting): an exit taken
// inside it reports on a line of its own rather than after the prompt's words.
// Every prompt for a secret comes through here, or through ReadSecretLine for
// a value that may arrive pasted, so that none of them can be the one that
// forgot to say so.
//
// The line is read as ReadSecretLine reads it, by rta rather than by the
// terminal's line discipline, and for the same two reasons: term.ReadPassword,
// which read it before, waited on after ^D on an empty line, so a passphrase
// prompt could only be left with ^C and the wait for the forced exit after it,
// and a line past the discipline's 1024 bytes on macOS never ended. ^D on an
// empty line is nothing typed (no answer), which every caller refuses in its
// own words.
func ReadSecret(prompt string) ([]byte, error) {
	defer shutdown.Prompting()()
	fmt.Fprint(os.Stderr, prompt)
	secret, err := readPassword(int(Real().Fd()))
	fmt.Fprintln(os.Stderr)
	return noAnswer(secret, err)
}

// nopCloser wraps a writer whose Close must not reach the underlying file.
//
// The MCP transport takes an io.WriteCloser and closes it at session end; the
// underlying writer here is os.Stdout, and closing the process's own stdout
// at the end of a session turns every later write — including the error that
// explains the shutdown — into a silent failure. The SDK's own
// StdioTransport wraps os.Stdout the same way, which is the evidence that
// this is the intended shape rather than a workaround.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// Writer wraps w so that closing it is a no-op.
func Writer(w io.Writer) io.WriteCloser { return nopCloser{w} }
