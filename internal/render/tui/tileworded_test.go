package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// wordedCap is a connection plugin's capability as a tile sees it: settings a
// profile or the config states, a credential, and an input of the call's own.
func wordedCap(run plugin.Handler) plugin.Capability {
	c := plugin.Capability{ID: "demo.status", Summary: "status", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "ca-file", Type: plugin.String, Config: "ca-file", Local: true, Help: "CA"},
			{Name: "cert-file", Type: plugin.String, Config: "cert-file", Local: true, Help: "cert"},
			{Name: "key-file", Type: plugin.String, Config: "key-file", Local: true, Help: "key"},
			{Name: "file", Type: plugin.String, Config: "file", Local: true, Help: "a file"},
			{Name: "mode", Type: plugin.String, Config: "mode", Local: true, Options: []string{"on", "off"}, Help: "mode"},
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
			{Name: "limit", Type: plugin.Int, Help: "rows"},
		},
		Run: run,
	}
	return c
}

// A refusal a tile shows names each setting where the tile's value is stated,
// not as a form's box: a tile has none, and its settings are in the profile it
// runs against, the config's section, its entry's with:, or a credential's
// variable.
func TestATilesRefusalNamesWhereEachSettingIsStated(t *testing.T) {
	c := wordedCap(nil)
	refusal := func(hint string) *view.Error { return view.Errorf("demo.tls", "refused").WithHint(hint) }
	for _, tc := range []struct {
		name, profile, section string
		with                   map[string]any
		hint, want             string
	}{
		{"a setting under a profile", "ohmlab", "", nil,
			"the ca-file box names a copy",
			"profile ohmlab's `ca-file` names a copy"},
		{"two settings one profile states", "ohmlab", "", nil,
			"pair with the cert-file and key-file boxes",
			"pair with profile ohmlab's `cert-file` and `key-file`"},
		{"an input the tile's entry states beside a profile's setting", "ohmlab", "", map[string]any{"limit": 5},
			"the limit and ca-file boxes",
			"the tile's `limit` and profile ohmlab's `ca-file`"},
		{"a setting the tile's entry states over the profile", "ohmlab", "", map[string]any{"ca-file": "/srv/ca.crt"},
			"the ca-file box names a copy",
			"the tile's `ca-file` names a copy"},
		{"an input of the call's own under a profile", "ohmlab", "", nil,
			"raise the limit box",
			"raise the tile's `limit`"},
		{"a setting of a pinned section, no profile", "", "demo@1a2b3c4d", nil,
			"clear the ca-file box",
			"clear the config's `plugins.demo@1a2b3c4d.ca-file`"},
		{"a setting of a built-in's section", "", "", nil,
			"clear the ca-file box",
			"clear the config's `plugins.demo.ca-file`"},
		{"a credential with no profile", "", "", nil,
			"check the password box",
			"check `RTA_DEMO_PASSWORD`"},
		{"a credential under a profile", "ohmlab", "", nil,
			"check the password box",
			"check profile ohmlab's `password`"},
		{"an input whose name ends another's", "ohmlab", "", nil,
			"the file box, not the ca-file box",
			"profile ohmlab's `file`, not profile ohmlab's `ca-file`"},
		{"a box naming no input of the capability", "ohmlab", "", nil,
			"the search box is elsewhere",
			"the search box is elsewhere"},
	} {
		got := tileWorded(refusal(tc.hint), c, tc.with, tc.profile, tc.section)
		if got.Hint != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got.Hint, tc.want)
		}
	}
}

// Through a tile's run: a plugin worded its refusal for the surface the request
// came through, which is the TUI's, and the tile shows where the setting it
// names is stated.
func TestATilesRunNamesTheProfileItsSettingIsIn(t *testing.T) {
	c := wordedCap(func(_ context.Context, req plugin.Request) (view.View, error) {
		return nil, view.Errorf("demo.tls.ca.expired", "the CA expired").
			WithHint(req.Surface().SettingName("ca-file") + " names a copy: export it again").WithInput("ca-file")
	})
	c.Run = plugin.GuardInputs(c)
	tm := tileCmd(0, tile{cap: c}, statedConfig{}, "ohmlab",
		map[string]any{"ca-file": "/srv/ca.crt"}, config.Connection{})().(tileMsg)
	if tm.err == nil || tm.err.Hint != "profile ohmlab's `ca-file` names a copy: export it again" {
		t.Fatalf("got %+v", tm.err)
	}
}

// The refusals a tile meets before its handler runs are worded for it too: the
// profile's fence, held to what the tile's entry states, and the forward,
// refusing a TLS statement it cannot carry.
func TestATilesRefusalBeforeItsRunNamesItsEntry(t *testing.T) {
	ran := false
	c := wordedCap(func(context.Context, plugin.Request) (view.View, error) {
		ran = true
		return view.Text{Body: "ran"}, nil
	})
	c.Inputs = append(c.Inputs, plugin.Field{Name: "tls", Type: plugin.Bool, Local: true,
		Config: "tls", Endpoint: plugin.EndpointTLS, Help: "TLS"})
	c.Run = plugin.GuardInputs(c)

	fenced := config.Connection{Only: map[string]any{"mode": []any{"on"}}}
	tm := tileCmd(0, tile{cap: c, values: map[string]any{"mode": "off"}}, statedConfig{}, "ohmlab", nil, fenced)().(tileMsg)
	if ran || tm.err == nil || strings.Contains(tm.err.Message+tm.err.Hint, "box") ||
		!strings.Contains(tm.err.Message+tm.err.Hint, "the tile's `mode`") {
		t.Errorf("fence: %+v (ran %v)", tm.err, ran)
	}

	forwarded := config.Connection{Kube: "lab/db/svc/pg:5432"}
	tm = tileCmd(0, tile{cap: c, values: map[string]any{"tls": true}}, statedConfig{}, "ohmlab", nil, forwarded)().(tileMsg)
	if ran || tm.err == nil || tm.err.Code != "core.profile.tls.forward" ||
		strings.Contains(tm.err.Message+tm.err.Hint, "box") || !strings.Contains(tm.err.Message, "the tile's `tls` set to true") {
		t.Errorf("forward: %+v (ran %v)", tm.err, ran)
	}
}

// The host's own refusal of a value a profile states offers a run of one to
// override it from a form. A tile has no such run, so that clause goes and the
// rest — where the value is set — stays.
func TestATilesRefusalOfAProfilesValueOffersNoRunOfOne(t *testing.T) {
	ran := false
	c := wordedCap(func(context.Context, plugin.Request) (view.View, error) {
		ran = true
		return view.Text{Body: "ran"}, nil
	})
	c.Run = plugin.GuardInputs(c)
	tm := tileCmd(0, tile{cap: c}, statedConfig{}, "ohmlab",
		map[string]any{"mode": "sideways"}, config.Connection{})().(tileMsg)
	if ran || tm.err == nil || tm.err.Code != "core.input.option" {
		t.Fatalf("got %+v (ran %v)", tm.err, ran)
	}
	if strings.Contains(tm.err.Message+tm.err.Hint, "box") || strings.Contains(tm.err.Hint, "for one run") ||
		!strings.Contains(tm.err.Message, "profile ohmlab's `mode`") ||
		!strings.Contains(tm.err.Hint, "`rta profile show ohmlab` shows the block it is set in") {
		t.Errorf("got %q / %q", tm.err.Message, tm.err.Hint)
	}
}
