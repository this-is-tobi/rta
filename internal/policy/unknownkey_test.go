package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nevr:` for `never:` used to delete a bound with nothing said, while the
// file still looked like it constrained — the vanishing ceiling
// requireRepoPolicy was written against, arriving by typo.
func TestAKeyThePolicyDoesNotHaveRefusesTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_POLICY", "")
	t.Chdir(dir)
	path := filepath.Join(dir, RepoFile)
	if err := os.WriteFile(path, []byte("maxTTL: 1h\nnevr:\n  - pg.dump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, verr := Load()
	if verr == nil || verr.Code != "policy.unknownkey" || !strings.Contains(verr.Message, `"nevr"`) {
		t.Fatalf("Load = %v, want the stray key refused by name", verr)
	}
	if err := os.WriteFile(path, []byte("maxTTL: 1h\nnever:\n  - pg.dump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(c.Never) != 1 {
		t.Fatalf("a well-formed file: %+v", c)
	}
}
