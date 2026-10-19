package kv

import (
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func keysOfPairs(kv view.KeyValue) []string {
	out := make([]string, len(kv.Pairs))
	for i, p := range kv.Pairs {
		out[i] = p.Key
	}
	return out
}

// onTheTUI is r as the dashboard asks for it: the TUI's surface, no detail.
func onTheTUI(r plugin.Request) plugin.Request { return r.WithSurface(plugin.SurfaceTUI) }

// The first line of the overview is the verdict, in a word the theme draws: ok
// when the store is there and opens, and the worst thing found when it is not.
func TestTheOverviewLeadsWithAVerdictInAWordTheThemeDraws(t *testing.T) {
	seeded(t)
	for name, tc := range map[string]struct {
		r      plugin.Request
		want   string
		colour theme.StatusKind
	}{
		"a store this call opens":     {req(nil, false), "ok", theme.StatusGood},
		"a store no key is at hand":   {plugin.NewRequest(nil, false, false), "warn — locked", theme.StatusWarn},
		"a key the store refuses":     {req(map[string]any{"passphrase": "not the passphrase"}, false), "warn — locked", theme.StatusWarn},
		"the same, as a tile":         {onTheTUI(plugin.NewRequest(nil, false, false)), "warn — locked", theme.StatusWarn},
		"a store opened, as a tile":   {onTheTUI(req(nil, false)), "ok", theme.StatusGood},
		"a store opened, over an MCP": {req(nil, false).WithSurface(plugin.SurfaceMCP), "ok", theme.StatusGood},
	} {
		kv := overviewPairs(t, tc.r)
		if kv.Pairs[0].Key != "status" || kv.Pairs[0].Value != tc.want {
			t.Errorf("%s: first row = %v, want status %q", name, kv.Pairs[0], tc.want)
		}
		if got := theme.ClassifyStatus(kv.Pairs[0].Value); got != tc.colour {
			t.Errorf("%s: the theme draws %q as %v, want %v", name, kv.Pairs[0].Value, got, tc.colour)
		}
	}
}

func TestTheOverviewBeforeThereIsAStoreIsOkAndSaysSo(t *testing.T) {
	setup(t)
	kv := overviewPairs(t, req(nil, false))
	if got := pairValue(kv, "status"); got != "ok — no store yet" {
		t.Errorf("status = %q", got)
	}
	if want := []string{"status", "store", "state"}; !slices.Equal(keysOfPairs(kv), want) {
		t.Errorf("rows = %v, want %v", keysOfPairs(kv), want)
	}
	// A tile has no use for where the file would be: the path is the CLI's.
	tile := overviewPairs(t, onTheTUI(req(nil, false)))
	if want := []string{"status", "state"}; !slices.Equal(keysOfPairs(tile), want) {
		t.Errorf("tile rows = %v, want %v", keysOfPairs(tile), want)
	}
}

// A store the file system will not let this process look at is not an error for
// the whole page: it is the verdict, and the row that says what was refused.
func TestAStoreThatCannotBeLookedAtIsAFailOnTheVerdict(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	dir := seeded2(t)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	for _, r := range []plugin.Request{req(nil, false), onTheTUI(req(nil, false))} {
		kv := overviewPairs(t, r)
		if got := pairValue(kv, "status"); got != "fail — unreadable store" || theme.ClassifyStatus(got) != theme.StatusBad {
			t.Errorf("%v: status = %q", r.Surface(), got)
		}
		if got := pairValue(kv, "store"); !strings.HasPrefix(got, "could not be read — ") {
			t.Errorf("%v: store = %q, want it to say what could not be read", r.Surface(), got)
		}
	}
}

// seeded2 is a store with one key in a data directory the caller may take away,
// which is returned.
func seeded2(t *testing.T) string {
	t.Helper()
	dir := setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	return dir
}

// A tile is a third of the screen wide, so it says "yes" where the page says
// which identity and which file: the sentence, with the path and the hint, is
// for the CLI, an agent and the page enter opens.
func TestOnATileTheLockIsOneWordAndTheSentenceStaysOnThePage(t *testing.T) {
	seeded(t)
	open := req(nil, false)
	if got := pairValue(overviewPairs(t, onTheTUI(open)), "unlocks here"); got != "yes" {
		t.Errorf("tile: unlocks here = %q", got)
	}
	if got := pairValue(overviewPairs(t, onTheTUI(open)), "locked with"); got != "a passphrase" {
		t.Errorf("tile: locked with = %q", got)
	}
	if got := pairValue(overviewPairs(t, open.WithSurface(plugin.SurfaceCLI)), "unlocks here"); !strings.HasPrefix(got, "yes — passphrase ") {
		t.Errorf("CLI: unlocks here = %q, want the sentence", got)
	}
	page := sectionByID(t, overviewPage(t, onTheTUI(open)), "summary").(view.KeyValue)
	if got := pairValue(page, "unlocks here"); !strings.HasPrefix(got, "yes — passphrase ") {
		t.Errorf("the page enter opens: unlocks here = %q, want the sentence", got)
	}

	// The call above gave the TUI's surface a passphrase, which opens its session.
	forgetSession()
	locked := onTheTUI(plugin.NewRequest(nil, false, false))
	tile := overviewPairs(t, locked)
	if got := pairValue(tile, "unlocks here"); got != "no passphrase available" {
		t.Errorf("tile: unlocks here = %q", got)
	}
	// The count's place says it could not be had, in the words every overview
	// uses, and not in the sentence that tells the CLI how to supply the key.
	if got := pairValue(tile, "inventory"); got != "could not be read — no key at hand" || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("tile: inventory = %q", got)
	}
	cli := overviewPairs(t, plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceCLI))
	if got := pairValue(cli, "inventory"); !strings.HasPrefix(got, "needs a key at hand — set RTA_KV_PASSPHRASE") {
		t.Errorf("CLI: inventory = %q, want the sentence", got)
	}
}

func TestOnATileTheFileIsOneRowWithNoPath(t *testing.T) {
	seeded(t)
	tile := overviewPairs(t, onTheTUI(req(nil, false)))
	got := pairValue(tile, "store")
	if !strings.Contains(got, ", changed ") || strings.Contains(got, "/") {
		t.Errorf("tile: store = %q, want its size and when it changed, and no path", got)
	}
	for _, gone := range []string{"size", "changed"} {
		if v := pairValue(tile, gone); v != "" {
			t.Errorf("tile: %s = %q, which the store row says", gone, v)
		}
	}
	cli := overviewPairs(t, req(nil, false))
	for _, key := range []string{"store", "size", "changed"} {
		if pairValue(cli, key) == "" {
			t.Errorf("CLI: no %s row: %v", key, keysOfPairs(cli))
		}
	}
	if got := pairValue(cli, "store"); !strings.Contains(got, "kv.age") {
		t.Errorf("CLI: store = %q, want the path", got)
	}
}

func TestATilesRowsAreInTheOrderOfWhatIsWorthActingOn(t *testing.T) {
	seeded(t)
	opened := keysOfPairs(overviewPairs(t, onTheTUI(req(nil, false))))
	if want := []string{"status", "keys", "locked with", "unlocks here", "kinds", "last set", "history", "store"}; !slices.Equal(opened, want) {
		t.Errorf("an open store reads %v, want %v", opened, want)
	}
	locked := keysOfPairs(overviewPairs(t, onTheTUI(plugin.NewRequest(nil, false, false))))
	if want := []string{"status", "inventory", "locked with", "unlocks here", "store"}; !slices.Equal(locked, want) {
		t.Errorf("a store nothing opened reads %v, want %v", locked, want)
	}
	if len(opened) > 8 {
		t.Errorf("a tile of an open store has %d rows", len(opened))
	}
}

func TestATilesValuesFitTheRoomATileLeavesBesideAKey(t *testing.T) {
	seeded(t)
	for name, r := range map[string]plugin.Request{
		"an open store":   onTheTUI(req(nil, false)),
		"a locked store":  onTheTUI(plugin.NewRequest(nil, false, false)),
		"a refused key":   onTheTUI(req(map[string]any{"passphrase": "not the passphrase"}, false)),
		"the same, wrong": onTheTUI(req(map[string]any{"passphrase": "also wrong"}, false)),
	} {
		for _, p := range overviewPairs(t, r).Pairs {
			if n := utf8.RuneCountInString(p.Value); n > 42 {
				t.Errorf("%s: %s = %q is %d characters, past what a third of a 120-column screen leaves", name, p.Key, p.Value, n)
			}
		}
	}
}

func TestOnATileTheCountIsTheShortOne(t *testing.T) {
	seeded(t)
	tile := overviewPairs(t, onTheTUI(req(nil, false)))
	if got, want := pairValue(tile, "keys"), "5 keys, 3 folders"; got != want {
		t.Errorf("tile: keys = %q, want %q", got, want)
	}
	if got, want := pairValue(tile, "history"), "1 earlier, 2 removed"; got != want {
		t.Errorf("tile: history = %q, want %q", got, want)
	}
	cli := overviewPairs(t, req(nil, false))
	if got := pairValue(cli, "keys"); !strings.HasPrefix(got, "5 keys, 3 folders (2 at the top), ") || !strings.HasSuffix(got, " of values") {
		t.Errorf("CLI: keys = %q, want the whole line", got)
	}
}

// A tile that only looks at a session another part of the program holds open
// is not locked, and does not call itself so.
func TestTheVerdictOfAStoreHeldBySessionIsOk(t *testing.T) {
	setup(t)
	freezeClock(t)
	unlockedFromTheTUI(t, "correct horse battery staple")
	kv := overviewPairs(t, tileOf(t))
	if got := pairValue(kv, "status"); got != "ok — unlocked in this session" {
		t.Errorf("status = %q", got)
	}
	if got := pairValue(kv, "unlocks here"); got != "yes" {
		t.Errorf("unlocks here = %q", got)
	}
}

// The numbers carry whether the store opened, which the verdict was made from.
func TestTheOverviewNumbersSayWhetherTheStoreOpened(t *testing.T) {
	seeded(t)
	if got := overviewPairs(t, req(nil, false)).Numbers["unlocked"]; got != 1 {
		t.Errorf("unlocked = %v for a store this call opened", got)
	}
	n := overviewPairs(t, plugin.NewRequest(nil, false, false)).Numbers
	if v, ok := n["unlocked"]; !ok || v != 0 {
		t.Errorf("unlocked = %v (present %v) for a store no key opened", v, ok)
	}
	setup(t)
	if _, ok := overviewPairs(t, req(nil, false)).Numbers["unlocked"]; ok {
		t.Error("a store that is not there says whether it opened")
	}
}
