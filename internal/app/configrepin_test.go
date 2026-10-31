package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// writeConfig puts body in the config file repinSession pointed RTA_CONFIG at.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(config.Path(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// After an upgrade the section pinned to the old build is moved onto the
// installed one in one command, every key under it kept.
func TestConfigRepinMovesAStaleSectionToTheInstalledBuild(t *testing.T) {
	run := repinSession(t)
	writeConfig(t, "plugins:\n  pg@111111111111:\n    host: db.internal\n    port: 6432\n")
	reg := pgRegistryAt(t, pgNewDigest)

	out, errOut, err := run(reg, "config", "repin", "--plugin", "pg", "--dry-run")
	if err != nil || !strings.Contains(out, "would write") || !strings.Contains(configOf(t), "pg@111111111111:") {
		t.Fatalf("the dry run (%v %q) wrote, or said nothing:\n%s\n%s", err, errOut, out, configOf(t))
	}

	out, errOut, err = run(reg, "config", "repin", "--plugin", "pg")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "plugins.pg@111111111111 repinned to pg@222222222222") {
		t.Errorf("the receipt does not say what moved where: %q", out)
	}
	after := configOf(t)
	if strings.Contains(after, "pg@111111111111") || !strings.Contains(after, "pg@222222222222:") ||
		!strings.Contains(after, "host: db.internal") || !strings.Contains(after, "port: 6432") {
		t.Errorf("the section was not moved whole:\n%s", after)
	}

	out, _, err = run(reg, "config", "repin", "--plugin", "pg")
	if err != nil || !strings.Contains(out, "names the installed build — nothing written") {
		t.Errorf("a second repin (%v) did not say there was nothing to do: %q", err, out)
	}
}

// A heading with no pin is pinned the same way, and a plugin with no section
// is told so rather than given one.
func TestConfigRepinPinsAnUnpinnedHeadingAndWritesNoNewOne(t *testing.T) {
	run := repinSession(t)
	reg := pgRegistryAt(t, pgNewDigest)
	out, _, err := run(reg, "config", "repin", "--plugin", "pg")
	if err != nil || !strings.Contains(out, "no plugins: section") {
		t.Errorf("a plugin with no section (%v): %q", err, out)
	}
	if _, statErr := os.Stat(config.Path()); statErr == nil {
		t.Errorf("a repin with nothing to do wrote a file:\n%s", configOf(t))
	}

	writeConfig(t, "plugins:\n  pg:\n    host: db.internal\n")
	if _, errOut, err := run(reg, "config", "repin", "--plugin", "pg"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if after := configOf(t); !strings.Contains(after, "pg@222222222222:") || !strings.Contains(after, "host: db.internal") {
		t.Errorf("the unpinned heading was not pinned:\n%s", after)
	}
}

// Two sections for one plugin would land on one heading, and which one's keys
// survive is the operator's to say: refused, and nothing written.
func TestConfigRepinRefusesToFoldTwoSectionsTogether(t *testing.T) {
	run := repinSession(t)
	body := "plugins:\n  pg@111111111111:\n    host: old.internal\n  pg@222222222222:\n    host: db.internal\n"
	writeConfig(t, body)
	_, errOut, err := run(pgRegistryAt(t, pgNewDigest), "config", "repin", "--plugin", "pg")
	if err == nil || !strings.Contains(errOut, "core.config.repin.collision") {
		t.Errorf("two sections were folded together (%v): %q", err, errOut)
	}
	if configOf(t) != body {
		t.Errorf("a refused repin wrote:\n%s", configOf(t))
	}
}

// Nothing to repin for a built-in or a plugin that is not installed, and
// nothing for a name that is not a plugin's: an instance label and a pin are a
// profile's and a heading's.
func TestConfigRepinRefusesWhatHasNoPin(t *testing.T) {
	run := repinSession(t)
	reg := pgRegistryAt(t, pgNewDigest)
	if err := reg.Register(plugin.Plugin{Name: "sys", Summary: "sys", Capabilities: []plugin.Capability{{
		ID: "sys.disk", Summary: "disks", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ plugin, code string }{
		{"sys", "core.config.pinned"},
		{"nope", "core.config.unknownplugin"},
		{"pg/analytics", "core.config.repin.plugin"},
		{"pg@111111111111", "core.config.repin.plugin"},
	} {
		if _, errOut, err := run(reg, "config", "repin", "--plugin", tc.plugin); err == nil || !strings.Contains(errOut, tc.code) {
			t.Errorf("--plugin %s (%v): %q, want %s", tc.plugin, err, errOut, tc.code)
		}
	}
}

// A section kept in a drop-in is repinned in that drop-in. Placed by its new
// heading alone it went to the config file, which states nothing of it.
func TestConfigRepinKeepsASectionInTheFileItWasIn(t *testing.T) {
	run := repinSession(t)
	writeConfig(t, "output: json\n")
	dropIn := filepath.Join(config.DropInDir(), "databases.yaml")
	if err := os.MkdirAll(filepath.Dir(dropIn), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropIn, []byte("plugins:\n  pg@111111111111:\n    host: db.internal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(pgRegistryAt(t, pgNewDigest), "config", "repin", "--plugin", "pg")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, dropIn) {
		t.Errorf("the receipt names another file than the drop-in: %q", out)
	}
	moved, err := os.ReadFile(dropIn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moved), "pg@222222222222:") || strings.Contains(configOf(t), "pg@") {
		t.Errorf("the section left its drop-in:\ndrop-in:\n%s\nconfig file:\n%s", moved, configOf(t))
	}
}
