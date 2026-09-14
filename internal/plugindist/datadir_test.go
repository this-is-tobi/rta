package plugindist

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
)

// The lockfile and the store are written under the data directory, and both
// used to create it 0755 when they were the first thing on a machine to need
// it — a plugin install before any grant left the names of every later grant,
// record segment and parked request listable by any account. Every writer
// here goes through paths.EnsureData now, and this is what keeps it so.
func TestWritingUnderTheDataDirectoryCreatesItOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	for name, write := range map[string]func(t *testing.T){
		"the lockfile": func(t *testing.T) {
			if verr := mutateLock(func(e []LockEntry) []LockEntry { return e }); verr != nil {
				t.Fatal(verr)
			}
		},
		"the store": func(t *testing.T) {
			staged := filepath.Join(t.TempDir(), "staged")
			if err := os.WriteFile(staged, []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, verr := place("probe", "0123456789abcdef", staged); verr != nil {
				t.Fatal(verr)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "share", "rta")
			t.Setenv("RTA_DATA_DIR", data)
			write(t)
			for _, p := range []string{data, filepath.Dir(paths.Data())} {
				info, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				if perm := info.Mode().Perm(); perm != 0o700 {
					t.Errorf("%s created %s with mode %04o, want 0700", name, p, perm)
				}
			}
		})
	}
}
