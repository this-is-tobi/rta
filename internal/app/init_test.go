package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/pkg/view"
)

// noTerminal says, for the rest of the test, that there is no terminal to ask
// the assistant's questions on.
func noTerminal(t *testing.T) {
	t.Helper()
	saved := initTerminal
	t.Cleanup(func() { initTerminal = saved })
	initTerminal = func() (io.Reader, io.Writer, func(), error) {
		return nil, nil, nil, errors.New("open /dev/tty: device not configured")
	}
}

// answerPairsList is answerPairs with the repeats kept: a client each, under one key.
func answerPairsList(t *testing.T, out string) []view.Pair {
	t.Helper()
	var env struct {
		Type  string      `json:"type"`
		Pairs []view.Pair `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Type != "keyvalue" {
		t.Fatalf("-o json answered %q (%v), want a keyvalue view", out, err)
	}
	return env.Pairs
}

// Stdout carries the answer, and only the answer: `rta init -o json >
// answer.json` was refused because stdout was not a terminal, although the
// person answering was at one. The questions are drawn on the terminal
// initTerminal finds, and stdout holds the json alone.
func TestInitAnswersIntoAFileWhileItAsksAtTheTerminal(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, map[string]string{"claude": "exit 0"})
	attachedIndex(t)
	saved := isTTY
	t.Cleanup(func() { isTTY = saved })
	isTTY = func() bool { return false }
	answers(t, true)
	inner := askInit
	var drawnOn io.Writer
	askInit = func(ctx context.Context, offers []initOffer, in io.Reader, out io.Writer) ([]bool, error) {
		drawnOn = out
		return inner(ctx, offers, in, out)
	}

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("init with stdout redirected: %v %q", err, errOut)
	}
	if pairs := answerPairsList(t, out); len(pairs) == 0 || pairs[0].Key != "registered" {
		t.Errorf("stdout held %q, want the answer alone", out)
	}
	if drawnOn == nil || drawnOn == io.Writer(os.Stdout) {
		t.Errorf("the questions were drawn on %v, want the terminal initTerminal found", drawnOn)
	}
}

// Where the questions go: the standard streams when both are the terminal, and
// the controlling terminal when either is pointed elsewhere — stdin a pipe,
// stderr a log — since the person is there whatever the streams say. No
// terminal at all is the one case with nobody to ask.
func TestTheFormIsDrawnOnTheTerminalWhereverTheStreamsPoint(t *testing.T) {
	dir := t.TempDir()
	file := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	stdin, stderr, tty := file("stdin"), file("stderr"), file("tty")
	opened := 0
	open := func() (*os.File, *os.File, error) { opened++; return tty, tty, nil }
	for _, tc := range []struct {
		name      string
		terminals []*os.File
		in, out   *os.File
	}{
		{"both streams at the terminal", []*os.File{stdin, stderr}, stdin, stderr},
		{"stdin a pipe", []*os.File{stderr}, tty, tty},
		{"stderr a log", []*os.File{stdin}, tty, tty},
	} {
		opened = 0
		isTerminal := func(f *os.File) bool { return slices.Contains(tc.terminals, f) }
		in, out, release, err := formTerminal(stdin, stderr, isTerminal, open)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		release()
		if in != io.Reader(tc.in) || out != io.Writer(tc.out) {
			t.Errorf("%s: drawn on %v reading %v, want %v reading %v", tc.name, out, in, tc.out, tc.in)
		}
		if want := tc.in == tty; (opened == 1) != want {
			t.Errorf("%s: the controlling terminal was opened %d times", tc.name, opened)
		}
	}
	none := func() (*os.File, *os.File, error) { return nil, nil, errors.New("no controlling terminal") }
	if _, _, _, err := formTerminal(stdin, stderr, func(*os.File) bool { return false }, none); err == nil {
		t.Error("with no terminal anywhere the questions were drawn anyway")
	}
}

// Leaving the assistant is coded as that, and anything else that stops the form
// under its own code; neither is huh's bare sentence.
func TestInitFormErrorsAreCoded(t *testing.T) {
	for err, code := range map[error]string{
		huh.ErrUserAborted:                  "core.init.aborted",
		fmt.Errorf("open /dev/tty: denied"): "core.init.form",
	} {
		ve := view.AsError(initFormError(err), "")
		if ve.Code != code || ve.Hint == "" {
			t.Errorf("%v: %#v, want %s with a hint", err, ve, code)
		}
	}
}

// init is a command like the rest on a default output nothing renders: the
// reason it ran under one was that it rewrote the key, which it no longer does.
func TestInitRefusesABrokenOutputDefaultLikeEveryOtherCommand(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, nil)
	t.Setenv("RTA_OUTPUT", "bogus")
	_, _, err := run("init")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeOutputInvalid {
		t.Errorf("err = %#v, want %s", err, CodeOutputInvalid)
	}
}

// The line that turns completion on is the installation page's recipe for the
// shell in $SHELL, printed and never run, and is not said again once the script
// is where the shell reads it from.
func TestInitSaysHowToTurnOnCompletionForTheShellInUse(t *testing.T) {
	home := t.TempDir()
	for shell, want := range map[string][]string{
		"zsh":  {"zsh is not set up", "rta completion zsh > ~/.zsh/completions/_rta", "fpath=(~/.zsh/completions $fpath)"},
		"bash": {"bash is not set up", "rta completion bash > ~/.local/share/bash-completion/completions/rta"},
		"fish": {"fish is not set up", "rta completion fish > ~/.config/fish/completions/rta.fish"},
	} {
		note, ok := completionNote(shell, home)
		if !ok || note.Key != "completion" {
			t.Fatalf("%s: no note (%v)", shell, ok)
		}
		for _, w := range want {
			if !strings.Contains(note.Value, w) {
				t.Errorf("%s: %q does not say %q", shell, note.Value, w)
			}
		}
	}
	if _, ok := completionNote("nu", home); ok {
		t.Error("a shell rta has no recipe for was given one")
	}
	if _, ok := completionNote("", home); ok {
		t.Error("no $SHELL was given a recipe")
	}
	done := filepath.Join(home, ".config", "fish", "completions", "rta.fish")
	if err := os.MkdirAll(filepath.Dir(done), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(done, []byte("#"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := completionNote("fish", home); ok {
		t.Error("completion was offered again once the script was in place")
	}
}

// Until an index is attached the databases, storage and secrets are invisible,
// and the one command that fixes that is the line; attached, there is nothing
// to say.
func TestInitNamesTheCommandThatAttachesTheFirstPartyIndex(t *testing.T) {
	session(t, testRegistry(t))
	note, ok := pluginsNote()
	if !ok || note.Key != "plugins" || !strings.Contains(note.Value, "`rta plugin index add official`") ||
		!strings.Contains(note.Value, "https://") {
		t.Fatalf("note = %+v (%v), want the command and the address it attaches", note, ok)
	}
	attachedIndex(t)
	if _, ok := pluginsNote(); ok {
		t.Error("the index is attached and init still says to attach it")
	}
}
