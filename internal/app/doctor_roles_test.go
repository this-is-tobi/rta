package app

import (
	"os"
	"path/filepath"
	"testing"
)

// The starter policy caps grants at one hour a few lines above a commented
// eight-hour role; under it the one-passphrase day is twelve passphrases,
// and doctor is where that is said before anybody signs.
func TestDoctorSaysTheWindowARoleWillReallyGet(t *testing.T) {
	_, configDir := isolate(t)
	repo := t.TempDir()
	t.Setenv("RTA_POLICY", "")
	t.Chdir(repo)
	if err := os.WriteFile(filepath.Join(repo, ".rta-policy.yaml"), []byte("maxTTL: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("roles:\n  dev:\n    ttl: 8h\n    grants: [kv.get]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "roles", "ok", "dev (8h, capped to 1h by ")
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("roles:\n  dev:\n    grants: [kv.get a b]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "roles", "error", `role "dev"`)
}
