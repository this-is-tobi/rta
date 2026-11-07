package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
)

// LoadNamed reads the plugins installed under one name and no other file on
// $PATH, and holds them to trust as the full sweep does: an untrusted one is
// hashed, recorded and never run, whichever load found it.
func TestLoadNamedReadsOnlyThePluginsOfThatName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"aaa", "bbb"} {
		if err := os.WriteFile(filepath.Join(dir, BinaryName(name)), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	untrusted := func(load func(*Host) []error) []string {
		h := New()
		t.Cleanup(h.CloseAll)
		if problems := load(h); len(problems) != 0 {
			t.Fatalf("problems = %v", problems)
		}
		var names []string
		for _, u := range h.Untrusted() {
			if u.Digest == "" {
				t.Errorf("%s was recorded with no digest", u.Name)
			}
			names = append(names, u.Name)
		}
		return names
	}
	if got := untrusted(func(h *Host) []error { return h.LoadNamed(context.Background(), registry.New(), "aaa") }); len(got) != 1 || got[0] != "aaa" {
		t.Errorf("LoadNamed(aaa) read %v, want aaa alone", got)
	}
	if got := untrusted(func(h *Host) []error { return h.LoadInto(context.Background(), registry.New()) }); len(got) != 2 {
		t.Errorf("LoadInto read %v, want both", got)
	}
	if got := untrusted(func(h *Host) []error { return h.LoadNamed(context.Background(), registry.New(), "ccc") }); len(got) != 0 {
		t.Errorf("LoadNamed(ccc) read %v, want nothing", got)
	}
}
