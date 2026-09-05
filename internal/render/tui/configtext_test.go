package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// **The config file's own strings are data from somewhere else, and they were
// the one kind this app rendered raw.**
//
// A plugin's declared text is checked at registration — control characters,
// bidi overrides, invisibles and a forged authorship frame are all refused
// (pkg/plugin checkText) — and a capability's *results* are run through
// textclean on the way to every surface. Between the two sat the config file:
// profile names, notes, plugin keys and `set:` values, which pass no such
// check and are printed straight into a pane rta draws.
//
// `ESC [ 2 J` in a profile name cleared the screen from inside the profiles
// pane. Nobody but the file's owner can put it there, which is what keeps this
// a defect rather than a vulnerability — but a file people are told to edit,
// share in dotfiles and generate from scripts is not a place to be relying on
// nobody having done it.

// hostileText is what a terminal acts on: a colour change, a clear-screen, and
// a bell. Every one of them survives a %s into a rendered line.
const hostileText = "eviL\x1b[31m\x1b[2Jpwned\x07"

func hostileProfileModel(t *testing.T) Model {
	t.Helper()
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		hostileText: {Note: hostileText, Plugins: map[string]config.Connection{
			"db": {Set: map[string]any{"host": hostileText}},
		}},
	}})
	m.profiles = m.profileRows()
	m.width, m.height = 100, 24
	return m
}

// assertNoTerminalControl fails with the offending byte named, because "output
// contains an escape" is not a thing anybody can act on.
func assertNoTerminalControl(t *testing.T, where, out string) {
	t.Helper()
	for _, bad := range []struct {
		seq, what string
	}{
		{"\x1b[2J", "a clear-screen"},
		{"\x07", "a bell"},
		{"\x1b]", "an OSC introducer"},
	} {
		if strings.Contains(out, bad.seq) {
			t.Errorf("%s renders %s from the config file", where, bad.what)
		}
	}
}

func TestAProfilePaneNeverRendersWhatTheConfigFileHolds(t *testing.T) {
	m := hostileProfileModel(t)

	m.mode = modeProfiles
	assertNoTerminalControl(t, "the profiles pane", m.profilesView())

	m.profileOpen = hostileText
	m.mode = modeProfilePlugins
	assertNoTerminalControl(t, "the plugins-in-a-profile pane", m.connsView())
}

// The two renderers every pane in this app goes through, asserted directly, so
// a pane added later inherits the guarantee instead of needing its own test.
//
// Both take plain text and style it themselves — that is what their own
// comments have always said the fields are for — which is exactly why the
// cleaning belongs in them: a caller that pre-styled could not be cleaned
// afterwards without stripping the colour it had just applied.
func TestTheBandAndPanelRenderersCleanWhatTheyAreGiven(t *testing.T) {
	assertNoTerminalControl(t, "a band's name",
		renderBands([]band{{name: hostileText, right: []string{"ok"}, detail: []string{"a", "b"}}}, 0, 0, 1, 80))

	assertNoTerminalControl(t, "a panel's head",
		panel(panelHead{Title: hostileText, Note: hostileText, Right: hostileText}, "body", 80, 6, false))
}

// And the name still reads, because stripping is not the same as blanking:
// what is left is the text a person can recognise their own profile by.
func TestCleaningLeavesTheNameReadable(t *testing.T) {
	out := plain(renderBands([]band{{name: hostileText, detail: []string{"", ""}}}, 0, 0, 1, 80))
	if !strings.Contains(out, "EVIL") || !strings.Contains(out, "PWNED") {
		t.Errorf("the band dropped the readable part of the name too:\n%s", out)
	}
}
