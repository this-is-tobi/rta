package operator

import (
	"os"
	"path/filepath"
	"testing"
)

// A server name the list does not hold is refused naming the server input,
// the one every operator call takes it in, so a form opens again on that box.
func TestAServerTheListDoesNotHoldNamesTheServerInput(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.WriteFile(RemotesPath(), []byte("servers:\n  prod:\n    url: https://rta.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, verr := ServerURL("lab"); verr == nil || verr.Code != "core.operator.server" || verr.Input != "server" {
		t.Errorf("ServerURL(lab) = %v, want core.operator.server naming the server input", verr)
	}
}
