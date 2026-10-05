//go:build linux || darwin

package main_test

import (
	"strings"
	"testing"
)

// A plugin that was approved and cannot start is said at startup, and then it
// was in no inventory: `rta plugin list` showed what had loaded, so a broken
// plugin looked exactly like one never installed. main is what carries what
// discovery found to the commands that list plugins.
func TestAPluginThatCannotStartHasARowInPluginList(t *testing.T) {
	env := installed(t, map[string]string{"broken": "echo no handshake here >&2; exit 1"})

	r := runIn(t, env, "plugin", "list")
	if r.code != 0 {
		t.Fatalf("exit %d, want 0 (stderr %q)", r.code, r.stderr)
	}
	var row string
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.Contains(line, "broken") {
			row = line
		}
	}
	if !strings.Contains(row, "failed to start") || !strings.Contains(row, "no handshake here") {
		t.Errorf("plugin list has no row saying broken failed to start and why:\n%s", r.stdout)
	}
	if !strings.Contains(row, "`rta plugin untrust broken`") {
		t.Errorf("the row does not carry the way out: %q", row)
	}
	if !strings.Contains(r.stderr, "`rta plugin untrust broken`") {
		t.Errorf("the startup report does not end with the way out: %q", r.stderr)
	}
}
