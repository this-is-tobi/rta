package tui

import (
	"bytes"
	"path/filepath"
	"regexp"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// paintsColour is whether a stream selects a foreground or background colour
// anywhere: an SGR with a 3x, 4x, 9x or 10x parameter, which is how every depth
// of colour is spelled, and none of the attributes (bold, reverse) a terminal
// with no colour still takes.
func paintsColour(stream []byte) bool {
	return regexp.MustCompile(`\x1b\[(?:[0-9;:]*[;:])?(?:3[0-8]|4[0-8]|9[0-7]|10[0-7])(?:[;:][0-9;:]*)?m`).Match(stream)
}

// runOnATrueColourTerminal runs the program the way Run sets its colour up, on a
// terminal that says it takes sixteen million colours, and returns what it drew.
func runOnATrueColourTerminal(t *testing.T, m Model) []byte {
	t.Helper()
	options := append([]tea.ProgramOption{tea.WithColorProfile(colorprofile.TrueColor)}, colorOptions(m)...)
	tm := newTestModel(t, m, teatest.WithInitialTermSize(100, 30), teatest.WithProgramOptions(options...))
	var drawn []byte
	teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
		drawn = bytes.Clone(bts)
		return bytes.Contains(bts, []byte("dashboard"))
	}, teatest.WithDuration(framePatience))
	quit(t, tm)
	return drawn
}

// `--no-color` is a root flag that says "disable styled output", and the TUI read
// only the environment's NO_COLOR and TERM, so rta --no-color drew the whole
// palette. The flag reaches the program as the colour profile NO_COLOR gives it:
// the renderer takes the colour out of every frame, and the model draws the
// heavy selection border and the bracketed answers that stand in for it.
func TestTheNoColorFlagTakesTheColourOutOfTheTUI(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg := answeringRegistry(t, "alpha")

	coloured := runOnATrueColourTerminal(t, New(reg, config.Dashboard{}, nil))
	if !paintsColour(coloured) {
		t.Fatalf("the control run, on a true-colour terminal and with no flag, painted no colour:\n%q", coloured)
	}

	flagged := New(reg, config.Dashboard{}, nil, WithNoColor())
	if !flagged.noColor {
		t.Error("the model does not know it is running without colour before the terminal has told it")
	}
	if stream := runOnATrueColourTerminal(t, flagged); paintsColour(stream) {
		t.Errorf("--no-color still painted colour:\n%q", stream)
	}
}

func TestWithoutTheFlagTheProgramIsLeftToDetectTheTerminal(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	if got := colorOptions(New(answeringRegistry(t, "alpha"), config.Dashboard{}, nil)); len(got) != 0 {
		t.Errorf("an unflagged session forces %d program options on the colour", len(got))
	}
}
