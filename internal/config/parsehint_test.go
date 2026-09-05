package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The duplicate-key message is the one parse failure whose general advice is
// actively harmful: "re-create it with `rta init`" would throw away every
// profile in the file to fix one repeated line. It is also the failure a
// person is most likely to hit, because a second connection for the same
// plugin looks like it should be a second block and is not.
func TestARepeatedPluginKeyExplainsItselfInsteadOfSuggestingRtaInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	// Two connections for one plugin, the shape somebody reaches by copying a
	// block to add a second database.
	body := strings.Join([]string{
		"profiles:",
		"  lab:",
		"    plugins:",
		"      pg@f334a1a8e088:",
		"        kube: homelab/one/svc/db-r:5432",
		"      pg@f334a1a8e088:",
		"        kube: homelab/two/svc/db-r:5432",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFile()
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("err = %#v, want a *view.Error", err)
	}
	if verr.Code != "config.invalid" {
		t.Errorf("code = %q, want config.invalid", verr.Code)
	}
	if strings.Contains(verr.Hint, "rta init") {
		t.Errorf("hint tells them to re-create the file, which discards every profile: %q", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "one connection per plugin") {
		t.Errorf("hint = %q, want it to name the rule that was broken", verr.Hint)
	}
}

// Every other parse failure keeps the general advice, which is right for a
// file that really is malformed.
func TestAnOrdinarilyBrokenFileKeepsTheGeneralAdvice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	if err := os.WriteFile(path,
		[]byte("profiles:\n  lab:\n   plugins: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile()
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("err = %#v, want a *view.Error", err)
	}
	if !strings.Contains(verr.Hint, "rta init") {
		t.Errorf("hint = %q, want the general advice for a genuinely malformed file", verr.Hint)
	}
}
