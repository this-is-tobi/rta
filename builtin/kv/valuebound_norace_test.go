//go:build !race

package kv

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The bound is held to the edited value, not to the editor's file: a value of
// exactly the bound comes back from vim with the newline it adds to every
// file, or with "\r\n" from an editor writing Windows line ends, and is within
// the bound once that is taken off. Held to the file, the value kv set had
// stored was refused when it was opened and saved unchanged.
//
// The race build leaves this test out, which is what this file is for.
// Each case stores, edits and reads back a value of 16 MiB, and every open
// of that store parses 22 MB of JSON and base64, each byte of it a memory
// access the race detector instruments: the test takes under a second
// without the detector and twenty with it, on a laptop. In CI it was the
// slowest test of the package `make hard-rest` spends longest on, and the
// one a slow runner's ten minutes ran out in. The detector has nothing to
// find in one goroutine, and the edit path is raced by the tests in
// edit_test.go, whose values are short. The passes without the detector
// still run this on every change: CI's coverage pass and its plain passes
// on arm64 Linux and macOS, and `make test-plain` on a laptop.
func TestAnEditedValueIsHeldToTheBoundWithoutTheNewlineTheEditorAdded(t *testing.T) {
	at := strings.Repeat("x", maxValueBytes)
	for name, tc := range map[string]struct {
		edit func([]byte) []byte
		want string // what the edit answers with
	}{
		"saved unchanged with a newline": {
			func(seen []byte) []byte { return append(seen, '\n') }, "unchanged"},
		"saved unchanged with a CRLF": {
			func(seen []byte) []byte { return append(seen, "\r\n"...) }, "unchanged"},
		"changed, still at the bound": {
			func(seen []byte) []byte { return append(append(seen[:len(seen)-1:len(seen)-1], 'y'), '\n') }, "updated"},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			text(t, runSet, map[string]any{"key": "k", "value": at}, false)
			stubEditor(t, func(_ string, seen []byte) []byte { return tc.edit(seen) })

			v, err := cliEdit(t, "k")
			if err != nil {
				t.Fatalf("an edit of a value at the bound = %v, want it %s", err, tc.want)
			}
			if body := v.(view.Text).Body; !strings.Contains(body, tc.want) {
				t.Errorf("the edit said %q, want it %s", body, tc.want)
			}
			if got := text(t, runGet, map[string]any{"key": "k"}, false); len(got) != maxValueBytes {
				t.Errorf("stored %d bytes, want the %d of the bound", len(got), maxValueBytes)
			}
		})
	}
}
