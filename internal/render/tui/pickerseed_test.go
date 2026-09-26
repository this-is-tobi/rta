package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// builtinConfig is a PluginConfig stating one section for whichever
// namespace asks, under that namespace's own heading — a built-in's.
type builtinConfig map[string]any

func (b builtinConfig) For(string) map[string]any { return b }
func (b builtinConfig) Section(ns string) string  { return ns }

// A config value naming none of a closed set's options is refused by the
// CLI, naming the key. The run form seeds from the same config, and huh,
// finding no option matching the seed, moved the picker to the first one and
// wrote it into the binding — so the form handed that option back as the
// caller's choice and the run went ahead with it: `sslmode: verify_full`, a
// typo for verify-full, ran as disable, with nothing on screen to say the
// file was wrong. The picker keeps the value now, marked, and the run is
// refused as the CLI's is.
func TestAPickerKeepsAConfigValueOutsideItsOptions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)

	var ran []string
	record := func(_ context.Context, req plugin.Request) (view.View, error) {
		ran = append(ran, req.String("sslmode")+"|"+strings.Join(req.StringSlice("kinds"), ","))
		return view.Text{Body: "ok"}, nil
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{{
		ID: "db.status", Summary: "status", Safety: plugin.Read, Run: record,
		Inputs: []plugin.Field{
			{Name: "sslmode", Type: plugin.String, Default: "prefer", Config: "sslmode", Local: true,
				Options: []string{"disable", "prefer", "require", "verify-ca", "verify-full"}, Help: "tls"},
			{Name: "kinds", Type: plugin.StringSlice, Config: "kinds",
				Options: []string{"table", "view"}, Help: "kinds"},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Capability("db.status")

	for _, tc := range []struct {
		cfg   map[string]any
		input string
		want  string
	}{
		{map[string]any{"sslmode": "verify_full"}, "sslmode", `not "verify_full", which the config's plugins.db.sslmode sets`},
		{map[string]any{"kinds": []any{"table", "index"}}, "kinds", `not "index", which the config's plugins.db.kinds sets`},
	} {
		ran = nil
		m := New(reg, config.Dashboard{}, builtinConfig(tc.cfg))
		m.width, m.height = 100, 40
		model, _ := m.startForm(c, nil)
		nm := model.(Model)
		// Through every field and back, the way a person tabbing through
		// does: huh writes a picker's value back as it is focused and left.
		nm.form.form.NextField()
		nm.form.form.PrevField()
		values := nm.form.values()
		if v, given := values[tc.input]; given {
			t.Errorf("%s: an untouched picker handed back %v as the caller's", tc.input, v)
		}
		rm := runCmd(context.Background(), 1, c, withoutPicker(c, values), false,
			nm.configFor(c), "", nil, config.Connection{}, false)().(resultMsg)
		if rm.err == nil || rm.err.Code != "core.input.option" || !strings.Contains(rm.err.Message, tc.want) {
			t.Errorf("%s: run = %+v, want core.input.option ending %q", tc.input, rm.err, tc.want)
		}
		if len(ran) != 0 {
			t.Errorf("%s: the handler ran with %v", tc.input, ran)
		}
	}
}

// And the value is on screen, marked, rather than a first option standing in
// for it — while a seed in another case is shown as the option it names.
func TestAPickerShowsWhatItWasSeededWith(t *testing.T) {
	cf := newCapForm(plugin.Capability{ID: "db.status"}, []plugin.Field{
		{Name: "sslmode", Type: plugin.String, Options: []string{"disable", "verify-full"}},
		{Name: "mode", Type: plugin.String, Options: []string{"fast", "safe"}},
		{Name: "kinds", Type: plugin.StringSlice, Options: []string{"table", "view"}},
	}, map[string]any{"sslmode": "verify_full", "mode": "SAFE", "kinds": []string{"Table", "index"}}, true, nil)
	if got := *cf.bindings["sslmode"]; got != "verify_full" {
		t.Errorf("sslmode binding = %q", got)
	}
	if got := *cf.bindings["mode"]; got != "safe" {
		t.Errorf("mode binding = %q, want the declared spelling", got)
	}
	if got := *cf.slices["kinds"]; strings.Join(got, ",") != "table,index" {
		t.Errorf("kinds = %v", got)
	}
	cf.form.Init()
	shown := cf.form.WithWidth(100).WithHeight(40).View()
	if !strings.Contains(shown, "verify_full (not an option)") {
		t.Errorf("the seeded value is not on screen:\n%s", shown)
	}
}

// An unlisted element is drawn as an option, and a list from YAML reaches the
// picker uncleaned, so its label is cleaned before it is drawn.
func TestAnUnlistedElementIsDrawnClean(t *testing.T) {
	esc := string(rune(0x1b))
	cf := newCapForm(plugin.Capability{ID: "db.status"}, []plugin.Field{
		{Name: "sslmode", Type: plugin.String, Options: []string{"disable", "verify-full"}},
		{Name: "kinds", Type: plugin.StringSlice, Options: []string{"table", "view"}},
	}, map[string]any{"sslmode": []any{"x" + esc + "]0;owned"}, "kinds": []any{"table", "x" + esc + "[2J"}}, true, nil)
	cf.form.Init()
	shown := cf.form.WithWidth(100).WithHeight(40).View()
	for _, raw := range []string{esc + "[2J", esc + "]0;"} {
		if strings.Contains(shown, raw) {
			t.Errorf("a seeded element reached the screen raw: %q", shown)
		}
	}
	if !strings.Contains(shown, notAnOption) {
		t.Errorf("the unlisted element is not on screen:\n%s", shown)
	}
}
