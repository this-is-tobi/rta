package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// textInTheBorderColour is every run of readable text a rendered pane paints in
// the colour of its borders and chart fills: what is left of a run once the
// box-drawing glyphs and blanks are taken off its ends.
func textInTheBorderColour(rendered string) []string {
	r, g, b, _ := theme.Faint.RGBA()
	border := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
	sgr := regexp.MustCompile(`\x1b\[([0-9;:]*)m`)
	var runs []string
	faint, at := false, 0
	flush := func(end int) {
		if !faint || end <= at {
			return
		}
		if text := strings.Trim(rendered[at:end], " ─│╭╮╰╯\n"); text != "" {
			runs = append(runs, text)
		}
	}
	for _, loc := range sgr.FindAllStringSubmatchIndex(rendered, -1) {
		flush(loc[0])
		switch params := rendered[loc[2]:loc[3]]; {
		case strings.Contains(params, border):
			faint = true
		case params == "" || params == "0" || params == "39" || strings.Contains(params, "38;"):
			faint = false
		}
		at = loc[1]
	}
	flush(len(rendered))
	return runs
}

// The border colour is for structure. Information drawn in it is 2.4:1 on black
// and 1.7:1 on Solarized dark, so "built in · 7 capabilities · no dashboard tile:
// needs to be told what to look at" and "no credential needed" were there to be
// read and could not be. Information uses the muted colour, which every palette
// keeps readable.
func TestNoPaneDrawsInformationInTheBorderColour(t *testing.T) {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "x"}, nil }
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		{Name: "home", Summary: "home summary", Capabilities: []plugin.Capability{
			{ID: "home.info", Summary: "info", Safety: plugin.Read, Idempotent: true, Run: run}}},
		// A capability that writes is never run unasked, so this plugin has no
		// tile, and its row says why on a line of its own.
		{Name: "quiet", Summary: "quiet summary", Capabilities: []plugin.Capability{
			{ID: "quiet.act", Summary: "act", Safety: plugin.Write, Run: run}}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	plugins := pluginPane(t, []pluginhost.Untrusted{{
		Name: "weather", Path: "/usr/local/bin/rta-plugin-weather", Digest: untrustedDigest,
	}})
	plugins.plugins = pluginRows(reg, plugins.dash, plugins.untrusted)

	profiles := profileModel(t, twoProfileConfig())
	profiles.profileOpen = "staging"

	panes := map[string]string{
		"plugins":         plugins.pluginsView(),
		"profiles":        profiles.profilesView(),
		"profile plugins": profiles.connsView(),
	}
	for name, rendered := range panes {
		if strings.Count(rendered, "\n") < 3 {
			t.Fatalf("the %s pane is not drawn:\n%s", name, rendered)
		}
		for _, text := range textInTheBorderColour(rendered) {
			t.Errorf("the %s pane draws %q in the border colour", name, text)
		}
	}
}
