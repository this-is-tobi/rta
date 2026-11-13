package kv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// What --file names is read only as far as one entry holds, so a stream with
// no end, /dev/zero or a pipe nobody closes, or a log the size of the disk is
// refused instead of being read until memory runs out; and a value given
// outright is held to the same bound. Nothing is stored.
func TestAValueOverTheEntryBoundIsRefusedWithoutBeingReadWhole(t *testing.T) {
	sparse := filepath.Join(t.TempDir(), "sparse")
	f, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	for name, tc := range map[string]struct {
		values map[string]any
		input  string
	}{
		"a sparse file of a gigabyte": {map[string]any{"key": "k", "file": sparse}, "file"},
		"/dev/zero":                   {map[string]any{"key": "k", "file": "/dev/zero"}, "file"},
		"a value given outright":      {map[string]any{"key": "k", "value": strings.Repeat("x", maxValueBytes+1)}, "value"},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			done := make(chan error, 1)
			go func() {
				_, err := runSet(context.Background(), req(tc.values, false))
				done <- err
			}()
			select {
			case err = <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("still reading after 30 s: the read has no end")
			}
			runtime.ReadMemStats(&after)
			var ve *view.Error
			if !errors.As(err, &ve) || ve.Code != "kv.value.toolarge" || ve.Input != tc.input {
				t.Fatalf("err = %#v, want kv.value.toolarge naming %s", err, tc.input)
			}
			if grown := after.TotalAlloc - before.TotalAlloc; grown > 256<<20 {
				t.Errorf("refusing it allocated %d MiB", grown>>20)
			}
			if fileExists(storePath()) {
				t.Error("a store was written for a value that was refused")
			}
		})
	}
}

// An entry is held to the bound however it arrives, the editor's buffer and
// the TUI's credential action included. kv edit read whatever the editor left
// and stored it: `:r ~/logs/big.log` in vim, saved, and every later call
// decrypted and encrypted that log, with up to five copies of it in history,
// where kv set refused the same value.
func TestAnEditedOrStoredValueOverTheEntryBoundIsRefused(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "kubeconfig", "value": "apiVersion: v1"}, false)
	// A byte over, and past the two bytes more the file is read to for the
	// newline an editor adds.
	for _, size := range []int{maxValueBytes + 1, maxValueBytes + len("\r\n") + 1} {
		stubEditor(t, replaceWith(strings.Repeat("x", size)))
		_, err := cliEdit(t, "kubeconfig")
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != "kv.value.toolarge" || !strings.Contains(ve.Hint, "nothing was changed") {
			t.Fatalf("an edit to %d bytes = %#v, want kv.value.toolarge saying nothing was changed", size, err)
		}
		if got := text(t, runGet, map[string]any{"key": "kubeconfig"}, false); got != "apiVersion: v1" {
			t.Errorf("the value past the bound was stored: %d bytes", len(got))
		}
	}

	t.Setenv(passphraseEnv, "correct horse battery staple")
	if verr := Store("pasted", strings.Repeat("x", maxValueBytes+1), "", ""); verr == nil || verr.Code != "kv.value.toolarge" {
		t.Fatalf("Store past the bound = %v, want kv.value.toolarge", verr)
	}
	if _, verr := Reveal("pasted"); verr == nil {
		t.Error("Store kept a value past the bound")
	}
}

// A value at the bound is a value.
func TestAValueAtTheEntryBoundIsStored(t *testing.T) {
	setup(t)
	if _, err := runSet(context.Background(), req(map[string]any{"key": "k", "value": strings.Repeat("x", maxValueBytes)}, false)); err != nil {
		t.Fatalf("a value of exactly the bound was refused: %v", err)
	}
}
