package pluginhost

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// The describe cache is written on the first invocation that launches a
// trusted plugin, which on a machine that installed a plugin before it
// issued a grant makes this the writer that creates the data directory —
// and it created it 0755. Through paths.EnsureData now, like every other
// writer under it.
func TestTheDescribeCacheCreatesAnOwnerOnlyDataDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	data := filepath.Join(t.TempDir(), "share", "rta")
	t.Setenv("RTA_DATA_DIR", data)
	writeCache("0123456789abcdef0123456789abcdef", &rtav1.Plugin{Name: "hello", Version: "1"})
	if _, ok := readCache("0123456789abcdef0123456789abcdef"); !ok {
		t.Fatal("the cache entry was not written")
	}
	info, err := os.Stat(data)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("the describe cache created %s with mode %04o, want 0700", data, perm)
	}
}
