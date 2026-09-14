package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
)

// The mode of the data directory used to depend on which command a machine
// ran first. Grants, the record and the seal created it 0700; the notebook
// and the store created it 0755 on their way to writing a 0600 file into it,
// and `rta doctor` then warned about a mode rta itself had chosen. These two
// are the first commands a new machine most plausibly runs before any grant
// exists, driven against a directory that does not exist yet — which is the
// only state in which a creator's mode can be observed at all.
func TestTheFirstCommandCreatesAnOwnerOnlyDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"note add":         {"note", "add", "first"},
		"kv init":          {"kv", "init", "--generate"},
		"grant allow":      {"grant", "allow", "note", "--ttl", "5m", "--agent", "probe"},
		"profile set":      {"profile", "set", "staging", "--note", "n"},
		"plugin trust":     {"plugin", "trust"},
		"agent log (read)": {"agent", "log"},
	} {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "share", "rta")
			t.Setenv("RTA_DATA_DIR", data)
			t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
			t.Setenv("RTA_KV_PASSPHRASE", "")
			t.Setenv("RTA_KV_IDENTITY", "")
			root := NewRoot(reg, "test")
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(args)
			// The command's own outcome is not what is under test: a read
			// may legitimately create nothing, and a write may refuse for
			// its own reasons. What matters is that whatever it created is
			// owner-only.
			_ = root.ExecuteContext(context.Background())
			info, err := os.Stat(data)
			if os.IsNotExist(err) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o700 {
				t.Errorf("`rta %v` created %s with mode %04o, want 0700 — every writer must go "+
					"through paths.EnsureData (%s)", args, data, perm, paths.Data())
			}
		})
	}
}
