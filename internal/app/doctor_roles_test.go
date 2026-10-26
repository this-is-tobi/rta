package app

import (
	"os"
	"path/filepath"
	"strings"
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

// Roles defined twice are said in the order of their names, the same on every
// run. They were said in the order of a map, so two runs of doctor on the same
// files gave two different rows, and a diff of its output a change where there
// was none.
func TestDoctorNamesRolesDefinedTwiceInTheSameOrderEveryTime(t *testing.T) {
	_, configDir := isolate(t)
	t.Setenv("RTA_POLICY", "")
	t.Chdir(t.TempDir())
	roles := "roles:\n"
	for _, name := range []string{"alpha", "bravo", "charlie", "delta"} {
		roles += "  " + name + ":\n    grants: [kv.get]\n"
	}
	for _, file := range []string{"config.yaml", "policy.yaml"} {
		if err := os.WriteFile(filepath.Join(configDir, file), []byte(roles), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := `"alpha" is defined in 2 files, and ` + "`grant issue`" + ` refuses to pick; "bravo"`
	for range 20 {
		var said string
		doctorRoles(func(check, status, detail string) {
			if check == "roles" {
				said = status + ": " + detail
			}
		})
		if !strings.HasPrefix(said, "error: "+want) || strings.Index(said, `"charlie"`) > strings.Index(said, `"delta"`) {
			t.Fatalf("the roles row = %q, want the names in order", said)
		}
	}
}
