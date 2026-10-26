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

// A value at the bound is a value.
func TestAValueAtTheEntryBoundIsStored(t *testing.T) {
	setup(t)
	if _, err := runSet(context.Background(), req(map[string]any{"key": "k", "value": strings.Repeat("x", maxValueBytes)}, false)); err != nil {
		t.Fatalf("a value of exactly the bound was refused: %v", err)
	}
}
