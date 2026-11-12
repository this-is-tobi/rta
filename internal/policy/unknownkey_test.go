package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nevr:` for `never:` used to delete a bound with nothing said, while the
// file still looked like it constrained — the vanishing ceiling
// require-repo-policy was written against, arriving by typo.
func TestAKeyThePolicyDoesNotHaveRefusesTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_POLICY", "")
	t.Chdir(dir)
	path := filepath.Join(dir, RepoFile)
	if err := os.WriteFile(path, []byte("max-ttl: 1h\nnevr:\n  - pg.dump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, verr := Load()
	if verr == nil || verr.Code != "policy.unknownkey" || !strings.Contains(verr.Message, `"nevr"`) {
		t.Fatalf("Load = %v, want the stray key refused by name", verr)
	}
	if err := os.WriteFile(path, []byte("max-ttl: 1h\nnever:\n  - pg.dump\n"), 0o600); err != nil {
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

// A key spelled as an older rta spelled it is refused like any key nothing
// reads, since the bound it carried would otherwise be gone, and told its
// spelling now and that the team sharing the file moves together; any other
// stray key is told the key it was likely meant as.
func TestAnOldSpellingIsToldItsNewOneAndATypoItsNearestKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_POLICY", "")
	t.Chdir(dir)
	path := filepath.Join(dir, RepoFile)
	for body, hint := range map[string]string{
		"maxTTL: 1h\n":                   "`maxTTL` is spelled `max-ttl` now, like every key rta reads; a team that shares this file upgrades rta together",
		"requireRepoPolicy: true\n":      "`requireRepoPolicy` is spelled `require-repo-policy` now",
		"max-ttl: 1h\nnevr: [pg.dump]\n": "did you mean `never`?",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, verr := Load(); verr == nil || verr.Code != "policy.unknownkey" || !strings.Contains(verr.Hint, hint) {
			t.Errorf("%q: %+v, want a hint saying %q", body, verr, hint)
		}
	}
}
