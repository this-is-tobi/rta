package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
)

// The root's --version is its own row of the root's help. A command's own
// --version is a flag like the rest: `plugin manifest` takes the version to
// claim, its example shows it, and FLAGS left it out because it shares the
// root's name.
func TestACommandsOwnVersionFlagIsListed(t *testing.T) {
	out, _, err := run(t, registry.New(), "plugin", "manifest", "--help")
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(out, "FLAGS")
	if at < 0 {
		t.Fatalf("plugin manifest's help has no FLAGS:\n%s", out)
	}
	flags := out[at:]
	if !strings.Contains(flags, "--version") || !strings.Contains(flags, "Version to claim") {
		t.Errorf("plugin manifest's FLAGS do not list its --version:\n%s", flags)
	}
}
