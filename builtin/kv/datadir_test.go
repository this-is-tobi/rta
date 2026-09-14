package kv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// `rta kv init` is a plausible first command on a new machine, and the two
// files it writes — the ciphertext and the recipients list — used to create
// the data directory 0755 on the way, which is the directory every grant,
// seal key and record segment then lands in. Both writers go through
// paths.EnsureData now.
func TestTheStoreCreatesAnOwnerOnlyDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	for name, write := range map[string]func() error{
		"the ciphertext": func() error {
			if verr := writeAtomic([]byte("age-encrypted bytes")); verr != nil {
				return verr
			}
			return nil
		},
		"the recipients": func() error {
			if verr := saveRecipients([]string{"age1probe"}); verr != nil {
				return verr
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			data := filepath.Join(t.TempDir(), "share", "rta")
			t.Setenv("RTA_DATA_DIR", data)
			if err := write(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(data)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o700 {
				t.Errorf("%s created %s with mode %04o, want 0700", name, data, perm)
			}
		})
	}
}
