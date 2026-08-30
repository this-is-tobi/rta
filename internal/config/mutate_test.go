package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// **The lost update this closes, measured.**
//
// Every writer of this file did LoadFile, mutate, Write, with nothing between
// the load and the write stopping a second writer from doing the same and one
// of them silently losing. That was survivable while every writer was a
// keystroke in a form — a person cannot press two keys in two processes at
// once. `rta profile set` ends it: the command exists to be scripted, and a
// script that states four environments states them in parallel as readily as
// in sequence.
//
// Eight concurrent writers against the built binary lost between one and
// three profiles on three runs out of five, with all eight reporting success.
// For this file that means a profile an operator believes exists, and a
// `--profile staging` that quietly reaches the base configuration instead.

func configAt(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	return path
}

func TestConcurrentMutationsDoNotLoseEachOther(t *testing.T) {
	configAt(t)
	const writers = 12

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Mutate(func(cfg Config) (Config, bool) {
				if cfg.Profiles == nil {
					cfg.Profiles = map[string]Profile{}
				}
				cfg.Profiles["p"+strconv.Itoa(i)] = Profile{
					Plugins: map[string]Connection{"db": {Set: map[string]any{"host": "h"}}},
				}
				return cfg, true
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a writer failed: %v", err)
		}
	}

	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	// Every writer reported success, so every profile has to be there. This
	// is the assertion: a lost write is indistinguishable from one that never
	// happened, except that somebody was told it had.
	if len(cfg.Profiles) != writers {
		t.Fatalf("%d of %d profiles survived — writes were lost while reporting success: %v",
			len(cfg.Profiles), writers, cfg.ProfileNames())
	}
}

// The read has to be inside the lock, not just the write. A version that
// locked only the write would still hand every caller the same starting
// value, and the last one to finish would win.
func TestMutateReadsTheFileItIsAboutToWrite(t *testing.T) {
	configAt(t)
	if err := Mutate(func(cfg Config) (Config, bool) {
		cfg.Output = "json"
		return cfg, true
	}); err != nil {
		t.Fatal(err)
	}
	var saw string
	if err := Mutate(func(cfg Config) (Config, bool) {
		saw = cfg.Output
		return cfg, false
	}); err != nil {
		t.Fatal(err)
	}
	if saw != "json" {
		t.Errorf("the second call read %q, not what the first wrote", saw)
	}
}

// Returning false writes nothing. This is how a caller that decides mid-edit
// to refuse gets out without leaving a half-applied file behind — and without
// having to return early out of the closure, which would skip the release.
func TestMutateWritesNothingWhenItSaysSo(t *testing.T) {
	path := configAt(t)
	if err := Mutate(func(cfg Config) (Config, bool) {
		cfg.Output = "json"
		return cfg, false
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		body, _ := os.ReadFile(path)
		t.Errorf("a refused mutation wrote the file:\n%s", body)
	}
}

// Mutate must not fold this shell's RTA_* into the file. LoadFile's own doc
// comment carries the argument; this is the test that keeps the newest writer
// honest about it, since `rta profile set` is the one most likely to be run
// from a shell with RTA_OUTPUT exported for something else.
func TestMutateDoesNotBakeTheEnvironmentIntoTheFile(t *testing.T) {
	path := configAt(t)
	t.Setenv("RTA_OUTPUT", "json")
	if err := Mutate(func(cfg Config) (Config, bool) {
		cfg.Profiles = map[string]Profile{"staging": {
			Plugins: map[string]Connection{"db": {Set: map[string]any{"host": "h"}}},
		}}
		return cfg, true
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "output:") {
		t.Errorf("one shell's RTA_OUTPUT was written into the file for every future run:\n%s", body)
	}
}

// The lock leaves nothing behind. A sentinel that outlived its holder would
// make the next writer wait out the stale timeout for no reason.
func TestTheLockIsReleased(t *testing.T) {
	path := configAt(t)
	if err := Mutate(func(cfg Config) (Config, bool) { return cfg, true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + lockFile); !os.IsNotExist(err) {
		t.Errorf("%s survived the mutation", path+lockFile)
	}
}
