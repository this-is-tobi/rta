package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

func refusalCode(t *testing.T, err error) string {
	t.Helper()
	var verr *view.Error
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	return verr.Code
}

// `plugin rm` is `plugin remove`: every other namespace removes with rm.
func TestPluginRmIsPluginRemove(t *testing.T) {
	run := session(t, registry.New())
	_, _, err := run("plugin", "rm", "ghost", "--dry-run")
	if got := refusalCode(t, err); got != "plugin.remove.unknown" {
		t.Errorf("code = %s (%v), want remove's own refusal", got, err)
	}
}

// With nothing attached, the empty listing names the command that works.
func TestTheEmptyIndexListingNamesOfficial(t *testing.T) {
	run := session(t, registry.New())
	onATerminal(t)
	out, errOut, err := run("plugin", "index", "list", "--no-color")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{"no index is attached", "`rta plugin index add official`", "https://github.com/this-is-tobi/rta-plugins"} {
		if !strings.Contains(out, want) {
			t.Errorf("out = %q, want %q", out, want)
		}
	}
}
