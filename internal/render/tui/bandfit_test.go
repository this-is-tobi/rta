package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A band whose name and status did not fit the pane was drawn whole and cut by
// the panel at its right edge, which is where the status is: at 44 columns a
// profile on with a deadline and invalid lost "invalid", and narrower its
// "[on …]", the marker that says whether this is production. The name gives
// way, with an ellipsis, as a section header's does, and the status is drawn
// whole.
func TestANarrowBandCutsTheNameAndNeverTheStatus(t *testing.T) {
	right := []string{"[on · 23m left]", "invalid"}
	for _, inner := range []int{40, 36} {
		out := renderBands([]band{{name: "proj1-staging", right: right}}, 0, 0, 1, inner)
		rule := plain(strings.SplitN(out, "\n", 2)[0])
		if !strings.HasSuffix(rule, "[on · 23m left] · invalid ─") {
			t.Errorf("at %d cells the band reads %q, want its whole status", inner, rule)
		}
		if w := lipgloss.Width(rule); w > inner {
			t.Errorf("at %d cells the band is %d wide", inner, w)
		}
		if !strings.Contains(rule, "PRO") || !strings.Contains(rule, "…") {
			t.Errorf("at %d cells the band reads %q, want the start of the name and a mark where it was cut", inner, rule)
		}
	}
	if rule := plain(renderBands([]band{{name: "proj1-staging", right: right}}, 0, 0, 1, 60)); strings.Contains(rule, "…") {
		t.Errorf("a band with the room for its name cut it: %q", rule)
	}
}

// The plugins pane draws its bands itself, by the same rule.
func TestANarrowPluginBandKeepsItsStatus(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 44, 30
	m.plugins = []pluginRow{{
		plugin: plugin.Plugin{Name: "a-plugin-with-a-rather-long-name"},
		origin: registry.Origin{Path: allowPath, Digest: untrustedDigest},
		failed: &pluginhost.Failed{Reason: "exited"},
	}}
	view := plain(m.pluginsView())
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "A-PLUGIN") {
			if !strings.Contains(line, "failed to start ─") {
				t.Errorf("the band reads %q, want its whole status", line)
			}
			return
		}
	}
	t.Fatalf("no band for the plugin:\n%s", view)
}
