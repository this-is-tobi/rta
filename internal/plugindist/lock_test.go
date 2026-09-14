package plugindist

import (
	"os"
	"path/filepath"
	"testing"
)

// The system root's record is read for what it says and nothing else: an
// image's installs show up beside the operator's, and no system root means
// no entries rather than an error.
func TestTheSystemLockIsReadAndNeverRequired(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", "")
	if got := ReadSystemLock(); len(got) != 0 {
		t.Fatalf("no system root, yet %d entries", len(got))
	}
	root := t.TempDir()
	t.Setenv("RTA_SYSTEM_DIR", root)
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"plugins":[{"name":"kube","version":"0.3.8","digest":"abc","index":"official"}]}`
	if err := os.WriteFile(SystemLockPath(), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadSystemLock()
	if len(got) != 1 || got[0].Name != "kube" || got[0].Index != "official" {
		t.Fatalf("ReadSystemLock() = %+v, want the image's kube", got)
	}
	if len(ReadLock()) != 0 {
		t.Fatal("the operator's record picked up the image's entry")
	}
}
