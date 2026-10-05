package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A capability's own search words find it by the person's vocabulary, as the
// built-in commands' do: a word in neither its ID nor its summary.
func TestACapabilitysKeywordsFindItWhereAWordIsNotACommand(t *testing.T) {
	reg := tidyRegistry(t)
	c, _ := reg.Capability("tidy.done")
	c.Keywords = []string{"checkoff"}
	reg = registry.New()
	if err := reg.Register(plugin.Plugin{Name: "tidy", Summary: "tidy plugin", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, reg, "explain", "checkoff")
	var ve *view.Error
	if !errors.As(err, &ve) || !strings.Contains(ve.Hint, "tidy.done") {
		t.Errorf("rta explain checkoff: %v, want it to offer tidy.done", err)
	}
	_, _, err = run(t, reg, "tidy", "checkoff")
	if !errors.As(err, &ve) || !strings.Contains(ve.Hint+ve.Message, "rta tidy done") {
		t.Errorf("rta tidy checkoff: %v, want it to offer rta tidy done", err)
	}
}
