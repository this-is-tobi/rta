package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A manifest whose only build is one nobody runs rta on, so an install that
// reaches it is refused for the platform: deterministic, offline, and proof
// that the install went on past the attach and read the index.
var noBuildHerePG = "name: pg\nversion: 0.1.0\nsummary: PostgreSQL toolkit\nplatforms:\n  - os: freebsd\n    arch: amd64\n" +
	"    url: https://example.test/pg_freebsd_amd64\n    sha256: " + strings.Repeat("b", 64) +
	"\ncapabilities:\n  - id: pg.status\n    safety: read\n"

// offerSession is a session in which the first-party index is a directory
// written rather than a repository cloned, and the person at the terminal is
// whatever the test says.
type offerSession struct {
	run      func(args ...string) (string, string, error)
	terminal bool
	answer   bool
	asked    []string
	attached int
}

func newOfferSession(t *testing.T) *offerSession {
	t.Helper()
	s := &offerSession{run: session(t, registry.New())}
	savedTerm, savedAsk, savedAttach := indexOfferTerminal, askToAttach, attachFirstPartyIndex
	t.Cleanup(func() { indexOfferTerminal, askToAttach, attachFirstPartyIndex = savedTerm, savedAsk, savedAttach })
	indexOfferTerminal = func() bool { return s.terminal }
	askToAttach = func(url string) (bool, error) {
		s.asked = append(s.asked, url)
		return s.answer, nil
	}
	attachFirstPartyIndex = func(context.Context) *view.Error {
		s.attached++
		return writeIndex("official", noBuildHerePG)
	}
	return s
}

func writeIndex(name, manifest string) *view.Error {
	dir := filepath.Join(paths.Indexes(), name, "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("test.index", "%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pg.yaml"), []byte(manifest), 0o644); err != nil {
		return view.Errorf("test.index", "%v", err)
	}
	return nil
}

// `rta plugin install pg` on a machine with no index attached named the
// command that attaches one, and the person copied it, ran it and typed the
// install again. At a terminal it now asks, once, with the URL in the question,
// and goes on.
func TestInstallingAFirstPartyPluginOffersTheFirstPartyIndexAtATerminal(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, true

	_, errOut, err := s.run("plugin", "install", "pg")
	if got := refusalCode(t, err); got != "plugin.install.platform" {
		t.Fatalf("code = %s (%v), want the install to have gone on to read the index it attached", got, err)
	}
	url, _ := plugindist.KnownIndexURL(plugindist.FirstPartyIndex)
	if len(s.asked) != 1 || s.asked[0] != url {
		t.Errorf("asked %v, want one question naming %s", s.asked, url)
	}
	if s.attached != 1 {
		t.Errorf("attached %d times, want once", s.attached)
	}
	for _, want := range []string{"pg is a first-party plugin", "attached official"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr = %q, want it to say %q", errOut, want)
		}
	}
	if _, ok := plugindist.IndexByName(plugindist.FirstPartyIndex); !ok {
		t.Error("the index is not attached after a yes")
	}
}

func TestADeclinedOfferLeavesTheRefusalAsItWas(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, false

	_, _, err := s.run("plugin", "install", "pg")
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "plugin.index.none" ||
		!strings.Contains(verr.Hint, "`rta plugin index add official`") {
		t.Fatalf("err = %v, want today's refusal with the command that fixes it", err)
	}
	if len(s.asked) != 1 || s.attached != 0 {
		t.Errorf("asked %v and attached %d times, want one question and no attach", s.asked, s.attached)
	}
	if _, ok := plugindist.IndexByName(plugindist.FirstPartyIndex); ok {
		t.Error("a no attached the index")
	}
}

// Nothing reaches a network destination on a question nobody could have seen:
// a script, a pipe, and a --dry-run, which changes nothing an attach included,
// all get the refusal and are never asked.
func TestNobodyIsAskedWhereNobodyCanAnswer(t *testing.T) {
	for _, c := range []struct {
		name     string
		terminal bool
		args     []string
	}{
		{"no terminal", false, []string{"plugin", "install", "pg"}},
		{"no terminal, --yes", false, []string{"plugin", "install", "pg", "--yes"}},
		{"a dry run at a terminal", true, []string{"plugin", "install", "pg", "--dry-run"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newOfferSession(t)
			s.terminal, s.answer = c.terminal, true
			_, _, err := s.run(c.args...)
			if got := refusalCode(t, err); got != "plugin.index.none" {
				t.Errorf("code = %s, want plugin.index.none", got)
			}
			if len(s.asked) != 0 || s.attached != 0 {
				t.Errorf("asked %v and attached %d times, want neither", s.asked, s.attached)
			}
		})
	}
}

// --yes is the answer to the question at a terminal; it does not conjure a
// terminal and it does not make the question unnecessary elsewhere.
func TestYesAnswersTheOfferAtATerminal(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, false

	_, errOut, err := s.run("plugin", "install", "pg", "--yes")
	if got := refusalCode(t, err); got != "plugin.install.platform" {
		t.Fatalf("code = %s (%v), want the install to have gone on", got, err)
	}
	if len(s.asked) != 0 || s.attached != 1 {
		t.Errorf("asked %v and attached %d times, want the flag to have answered", s.asked, s.attached)
	}
	if !strings.Contains(errOut, "pg is a first-party plugin") {
		t.Errorf("stderr = %q, want the reason for the attach said", errOut)
	}
}

// The offer is for the first-party index and a first-party plugin's name, and
// for nothing else a person might type after `install`.
func TestOnlyAFirstPartyNameEarnsTheOffer(t *testing.T) {
	for _, c := range []struct {
		spec string
		want string
	}{
		{"mongo", "plugin.index.none"},
		{"community/pg", "plugin.index.unknown"},
		{"pkg", "plugin.index.none"},
		{"PG", "plugin.install.spec"},
	} {
		s := newOfferSession(t)
		s.terminal, s.answer = true, true
		_, _, err := s.run("plugin", "install", c.spec)
		if got := refusalCode(t, err); got != c.want {
			t.Errorf("install %s: code = %s, want %s", c.spec, got, c.want)
		}
		if len(s.asked) != 0 || s.attached != 0 {
			t.Errorf("install %s: asked %v and attached %d times", c.spec, s.asked, s.attached)
		}
	}
}

func TestTheOfficialSpellingEarnsTheOfferToo(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, true
	_, _, err := s.run("plugin", "install", "official/pg")
	if got := refusalCode(t, err); got != "plugin.install.platform" || s.attached != 1 {
		t.Errorf("code = %s, attached %d times, want the offer taken and the install gone on", got, s.attached)
	}
}

// Another index is attached and does not carry the plugin: the first-party one
// might, and the person is asked. The first-party index attached and not
// carrying it has nothing more to offer.
func TestTheOfferIsForAnIndexThatIsNotAttachedYet(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, true
	if err := os.MkdirAll(filepath.Join(paths.Indexes(), "community", "index"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.run("plugin", "install", "pg")
	if got := refusalCode(t, err); got != "plugin.install.platform" || s.attached != 1 {
		t.Fatalf("code = %s, attached %d times, want the offer made past an index that lacks the plugin", got, s.attached)
	}

	s.asked, s.attached = nil, 0
	if err := os.Remove(filepath.Join(paths.Indexes(), "official", "index", "pg.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.run("plugin", "install", "pg")
	if got := refusalCode(t, err); got != "plugin.install.unknown" {
		t.Errorf("code = %s, want plugin.install.unknown", got)
	}
	if len(s.asked) != 0 || s.attached != 0 {
		t.Errorf("asked %v and attached %d times with the first-party index already attached", s.asked, s.attached)
	}
}

// No index carries "postgres", and the refusal said so; the answer is the name
// the plugin goes by.
func TestAnAliasIsAnsweredWithTheNameItGoesBy(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, true
	_, _, err := s.run("plugin", "install", "postgres")
	var verr *view.Error
	if !errors.As(err, &verr) || !strings.Contains(verr.Hint, "postgres is the first-party plugin pg") ||
		!strings.Contains(verr.Hint, "`rta plugin install pg`") {
		t.Fatalf("err = %v, want it to name pg", err)
	}
	if len(s.asked) != 0 {
		t.Errorf("asked %v about a name no index can carry", s.asked)
	}
}

// A machine without git is told so before it is asked to attach something it
// cannot.
func TestTheOfferIsNotMadeWhereTheAttachCannotWork(t *testing.T) {
	s := newOfferSession(t)
	s.terminal, s.answer = true, true
	t.Setenv("PATH", t.TempDir())

	_, _, err := s.run("plugin", "install", "pg")
	if got := refusalCode(t, err); got != "plugin.index.git" {
		t.Errorf("code = %s (%v), want the missing git said before the question", got, err)
	}
	if len(s.asked) != 0 {
		t.Errorf("asked %v on a machine that cannot attach", s.asked)
	}
}

func TestAnAnswerIsYesOnlyWhenItSaysSo(t *testing.T) {
	for in, want := range map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, " YES \n": true, "y": true,
		"n\n": false, "N\n": false, "\n": false, "": false, "maybe\n": false, "yep\n": false, "no\n": false,
	} {
		var out strings.Builder
		got, err := askYesNo(strings.NewReader(in), &out, "Attach? [y/N] ")
		if err != nil || got != want {
			t.Errorf("answer %q = %v, %v, want %v", in, got, err, want)
		}
		if !strings.HasPrefix(out.String(), "Attach? [y/N] ") {
			t.Errorf("the question was not written first: %q", out.String())
		}
	}
}
