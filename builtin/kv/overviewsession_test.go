package kv

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The dashboard's kv tile and the header's badge are two answers to one
// question, whether the store is unlocked, and a screen that says "store
// unlocked" in one and "no passphrase available" in the other has said both.
// The tile cannot use the session: a tile refreshes on a timer, and every use
// of a session moves its idle deadline out, so a tile that opened the store
// with it would keep it unlocked for as long as the terminal stayed open. It
// can look, which is not a use.
func tileOf(t *testing.T) plugin.Request {
	t.Helper()
	return plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceTUI)
}

func TestTheTileSaysTheStoreIsUnlockedInThisSessionWithoutUsingIt(t *testing.T) {
	setup(t)
	clock := freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")
	opened, live := SessionUntil()
	if !live {
		t.Fatal("no session to look at")
	}

	for range 3 {
		*clock = clock.Add(2 * time.Minute)
		kv := overviewPairs(t, tileOf(t))

		if got, want := pairValue(kv, "inventory"), "unlocked in this session — enter to see"; got != want {
			t.Errorf("inventory = %q, want %q", got, want)
		}
		// A tile says the word; the page enter opens says which session.
		if got, want := pairValue(kv, "unlocks here"), "yes"; got != want {
			t.Errorf("unlocks here = %q, want %q", got, want)
		}
		if got := pairValue(kv, "keys"); got != "" {
			t.Errorf("a tile counted the store with a passphrase it must not use: keys = %q", got)
		}
	}
	// What a use would have done is move this to the last refresh plus fifteen
	// minutes; it is where the unlock left it.
	if until, live := SessionUntil(); !live || !until.Equal(opened) {
		t.Errorf("the session lapses at %v after the tile, was %v: the tile used it (still live: %v)", until, opened, live)
	}
}

// A session that lapsed is not one to be told about, and a store with no session
// says what it needed before: the tile is not the badge's echo, it looks.
func TestTheTileOfALapsedSessionSaysWhatItNeeds(t *testing.T) {
	setup(t)
	clock := freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")
	*clock = clock.Add(sessionIdle + time.Second)

	kv := overviewPairs(t, tileOf(t))
	if got := pairValue(kv, "inventory"); got != "could not be read — no key at hand" {
		t.Errorf("inventory = %q", got)
	}
	if got := pairValue(kv, "unlocks here"); strings.HasPrefix(got, "yes") {
		t.Errorf("unlocks here = %q after the session lapsed", got)
	}
}

// "enter to see" is a tile's line: the page that enter opens is filled from the
// session by the TUI, and a page run without a passphrase while a session is
// open — a plugin author's test, a script — has nothing to be sent to.
func TestOnlyATileIsToldToPressEnter(t *testing.T) {
	setup(t)
	freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")

	summary := sectionByID(t, overviewPage(t, tileOf(t)), "summary").(view.KeyValue)
	if got := pairValue(summary, "inventory"); !strings.HasPrefix(got, "needs a key at hand — ") {
		t.Errorf("the page's inventory = %q", got)
	}
}
