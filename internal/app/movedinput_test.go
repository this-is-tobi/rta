package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta kv status --detail` was a command, and the one that answers it now is
// another: a script, an alias or a habit was told "unknown flag" under a hint to
// read the help of a command that no longer says anything of it.
func TestAFlagThatMovedSaysWhereItWent(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(kv.Plugin()); err != nil {
		t.Fatal(err)
	}
	run := session(t, reg)
	_, _, err := run("kv", "status", "--detail")
	var verr *view.Error
	if !errors.As(err, &verr) {
		t.Fatalf("a flag the command no longer has gave %v, want a refusal", err)
	}
	if verr.Code != CodeUsage || !strings.Contains(verr.Message, "unknown flag: --detail") {
		t.Errorf("the refusal is %s %q, want the usage mistake it is", verr.Code, verr.Message)
	}
	if !strings.Contains(verr.Hint, "--detail moved to `rta kv overview`") {
		t.Errorf("hint = %q, want where --detail went", verr.Hint)
	}
}
