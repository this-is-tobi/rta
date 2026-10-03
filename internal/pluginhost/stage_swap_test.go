package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pathPlugin puts a copy of the example plugin where $PATH would find it: a
// directory that is not one of rta's own.
func pathPlugin(t *testing.T) (Identity, string) {
	t.Helper()
	// Its own data directory: a copy staged by an earlier test for the same
	// digest is reused, rightly, and a launch that finds one never looks at the
	// file on disk, so a test about what happens to that file needs a launch
	// that has to make the copy. Under -shuffle the order decided it.
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	data, err := os.ReadFile(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), BinaryName("rta-plugin-staged"))
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	return id, path
}

// The digest an operator trusted is the digest of the bytes that run. A plugin
// on $PATH was hashed and then started by name, and a file that is rewritten
// in between — by anything that can write to the directory it sits in — ran
// as the artifact that had been approved. Here the file is replaced by a
// working binary of other contents after it was hashed, and the launch must
// refuse it rather than start it.
func TestAPluginRewrittenAfterItWasHashedIsNotStarted(t *testing.T) {
	id, path := pathPlugin(t)
	other := filepath.Join(t.TempDir(), BinaryName("other"))
	// -trimpath changes what is built, not whether it is a plugin: a binary
	// that would start and describe itself if it were run.
	build := exec.Command("go", "build", "-trimpath", "-o", other, "../../examples/plugin-hello")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the replacement: %v: %s", err, out)
	}
	data, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}

	if again, err := Identify(path); err != nil || again.Digest == id.Digest {
		t.Fatalf("the replacement is the same artifact: %v", err)
	}
	h := New()
	t.Cleanup(h.CloseAll)
	c, err := h.launch(context.Background(), id, DenySet{}, nil)
	if err == nil {
		c.Close()
		t.Fatal("a plugin rewritten after its digest was taken was started")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("the refusal does not say what changed: %v", err)
	}
}
