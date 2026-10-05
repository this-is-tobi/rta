package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/pkg/view"
)

// initOfferSession is an init on a machine with nothing on it but git, which
// the attach's preflight asks for, and no index attached.
func initOfferSession(t *testing.T) *offerSession {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	s := newOfferSession(t)
	initMachine(t, nil)
	t.Setenv("PATH", filepath.Dir(git))
	return s
}

func pluginsPair(t *testing.T, out string) string {
	t.Helper()
	for _, p := range answerPairsList(t, out) {
		if p.Key == "plugins" {
			return p.Value
		}
	}
	return ""
}

// init printed the command that attaches the first-party index and left a
// person to copy, run and come back. At a terminal it asks the question the
// install asks, and attaches on a yes.
func TestInitAttachesTheFirstPartyIndexOnAYesAtATerminal(t *testing.T) {
	s := initOfferSession(t)
	s.terminal, s.answer = true, true
	out, _, err := s.run("init", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	url, _ := plugindist.KnownIndexURL(plugindist.FirstPartyIndex)
	if len(s.asked) != 1 || s.asked[0] != url || s.attached != 1 {
		t.Errorf("asked %v and attached %d times, want one question naming %s and one attach", s.asked, s.attached, url)
	}
	if got := pluginsPair(t, out); !strings.Contains(got, "is attached") || strings.Contains(got, "index add") {
		t.Errorf("plugins = %q, want it to say the index is attached and not to name the command", got)
	}
}

// A no changes nothing and keeps the command; so do a run with nobody to ask
// and a dry run, which must not reach a network destination on a prompt nobody
// could have seen.
func TestInitKeepsTheCommandWhenTheIndexIsNotAttached(t *testing.T) {
	for name, tc := range map[string]struct {
		terminal, answer bool
		args             []string
	}{
		"a no":        {true, false, []string{"init", "-o", "json"}},
		"no terminal": {false, true, []string{"init", "-o", "json"}},
		"a dry run":   {true, true, []string{"init", "--dry-run", "-o", "json"}},
		"yes, no tty": {false, true, []string{"init", "--yes", "-o", "json"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := initOfferSession(t)
			s.terminal, s.answer = tc.terminal, tc.answer
			out, _, err := s.run(tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if s.attached != 0 {
				t.Errorf("the index was attached %d times", s.attached)
			}
			if got := pluginsPair(t, out); !strings.Contains(got, "`rta plugin index add official`") {
				t.Errorf("plugins = %q, want the command that attaches it", got)
			}
		})
	}
}

// --yes answers the question at a terminal, as it does for the install, and
// does not make a terminal of a script.
func TestInitYesAnswersTheIndexQuestionAtATerminal(t *testing.T) {
	s := initOfferSession(t)
	s.terminal = true
	if _, _, err := s.run("init", "--yes", "-o", "json"); err != nil {
		t.Fatal(err)
	}
	if len(s.asked) != 0 || s.attached != 1 {
		t.Errorf("asked %v and attached %d times, want no question and one attach", s.asked, s.attached)
	}
}

// A yes to an attach that then fails is an init that failed at what it was
// asked, and says so with the attach's own words.
func TestInitFailsWhenTheAttachSaidYesToFails(t *testing.T) {
	s := initOfferSession(t)
	s.terminal, s.answer = true, true
	attachFirstPartyIndex = func(context.Context) *view.Error {
		return view.Errorf("plugin.index.clone", "git clone failed")
	}
	out, _, err := s.run("init", "-o", "json")
	verr := codedError(t, err)
	if verr.Code != "plugin.index.clone" {
		t.Errorf("err = %v, want the attach's refusal", err)
	}
	if got := pluginsPair(t, out); !strings.Contains(got, "could not be attached — git clone failed") {
		t.Errorf("plugins = %q, want the reason", got)
	}
}
