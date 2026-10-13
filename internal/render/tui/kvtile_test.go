package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The kv tile is the store's overview, and what it does on a timer is the half
// of that a tile has to get right: it counts the store from what the
// environment holds, asks nobody for anything, and leaves no store session
// behind that its own refreshes would keep warm.

const tilePassphrase = "correct horse battery staple"

// kvTile runs one refresh of the dashboard's kv tile, the way a tick does.
func kvTile(t *testing.T, env string) view.KeyValue {
	t.Helper()
	reg := realRegistry(t)
	c, ok := reg.Capability("kv.overview")
	if !ok {
		t.Fatal("kv.overview is not registered")
	}
	t.Setenv("RTA_KV_PASSPHRASE", env)
	msg := tileCmd(0, tile{cap: c}, statedConfig{}, "", nil, config.Connection{})().(tileMsg)
	if msg.err != nil {
		t.Fatalf("the kv tile errored: %v", msg.err)
	}
	kvs, ok := msg.v.(view.KeyValue)
	if !ok {
		t.Fatalf("the kv tile is a %s, want a KeyValue", view.TypeOf(msg.v))
	}
	return kvs
}

func tilePair(kvs view.KeyValue, key string) string {
	for _, p := range kvs.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

func TestTheKvTileCountsTheStoreFromTheEnvironmentAndKeepsNoSession(t *testing.T) {
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_IDENTITY", "")

	reg := realRegistry(t)
	set, _ := reg.Capability("kv.set")
	for key, value := range map[string]string{"prod/db-password": "hunter2", "api-token": "tok-1"} {
		create := plugin.NewRequest(map[string]any{"key": key, "value": value, "passphrase": tilePassphrase}, false, false)
		if _, err := set.Run(context.Background(), create); err != nil {
			t.Fatal(err)
		}
	}

	// Two refreshes, as a dashboard left open makes: the second would move a
	// session's idle deadline out if the first had started one.
	for range 2 {
		tileView := kvTile(t, tilePassphrase)
		if got := tilePair(tileView, "keys"); !strings.HasPrefix(got, "2 keys, 1 folder") {
			t.Errorf("the tile says keys = %q, want the store counted", got)
		}
		if _, held := kv.SessionPassphrase(); held {
			t.Fatal("a tile refresh started a store session: the header would say unlocked and it would never relock")
		}
	}
}

// With nothing that opens the store at hand the tile says where it is and that
// the counts need a key, in the words of the surface it is drawn on — the keys
// that reach the passphrase box, since the tile shows none — and does not wait
// for an answer.
func TestTheKvTileOfAStoreItCannotOpenSaysWhatItNeeds(t *testing.T) {
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_IDENTITY", "")

	set, _ := realRegistry(t).Capability("kv.set")
	create := plugin.NewRequest(map[string]any{"key": "a", "value": "b", "passphrase": tilePassphrase}, false, false)
	if _, err := set.Run(context.Background(), create); err != nil {
		t.Fatal(err)
	}

	tileView := kvTile(t, "")
	line := tilePair(tileView, "inventory")
	if want := "needs a key at hand — set RTA_KV_PASSPHRASE, or type the passphrase: enter, then e"; line != want {
		t.Errorf("the tile says inventory = %q, want %q", line, want)
	}
	if strings.Contains(line, "box") {
		t.Errorf("the tile names a box, and none is on it: %q", line)
	}
	if got := tilePair(tileView, "keys"); got != "" {
		t.Errorf("a store nothing opened was counted: %q", got)
	}
	if tilePair(tileView, "store") == "" || tilePair(tileView, "locked with") == "" {
		t.Errorf("the tile lost the facts about the file: %v", tileView.Pairs)
	}
}

// The header says "store unlocked" while a session is open, and the tile beside
// it says so too, rather than that no passphrase is available. It looks at the
// session and does not use it: a use moves the idle deadline, so a tile that
// refreshed on a timer with it would keep the store open for as long as the
// terminal was.
func TestTheKvTileSaysTheStoreIsUnlockedAndDoesNotKeepItSo(t *testing.T) {
	m, _ := kvModel(t)
	warmStore(t, m)
	opened, ok := kv.SessionUntil()
	if !ok {
		t.Fatal("the unlock started no session")
	}
	if !strings.Contains(m.dashboardView(), "store unlocked") {
		t.Fatal("the header does not say the store is unlocked")
	}

	for range 3 {
		tileView := kvTile(t, "")
		if got, want := tilePair(tileView, "inventory"), "unlocked in this session — enter to see"; got != want {
			t.Errorf("the tile says inventory = %q, want %q", got, want)
		}
		if got, want := tilePair(tileView, "unlocks here"), "yes — unlocked in this session"; got != want {
			t.Errorf("the tile says unlocks here = %q, want %q", got, want)
		}
	}
	if until, ok := kv.SessionUntil(); !ok || !until.Equal(opened) {
		t.Errorf("the session lapses at %v, was %v: a tile refresh moved the idle deadline", until, opened)
	}
}

// What the tile says enter does is what enter does: the page it opens counts the
// store from the session, as any other kv action in an unlocked TUI is answered
// by it, instead of asking again for what the badge says is open.
func TestEnterOnTheKvTileOpensTheStoreFromTheSession(t *testing.T) {
	m, _ := kvModel(t)
	warmStore(t, m)
	at := -1
	for i, tl := range m.tiles {
		if tl.cap.ID == "kv.overview" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the dashboard has no kv.overview tile")
	}

	_, cmd := m.openTile(at)
	var landed *resultMsg
	for _, msg := range messagesOf(cmd) {
		if r, ok := msg.(resultMsg); ok {
			landed = &r
		}
	}
	if landed == nil || landed.err != nil {
		t.Fatalf("the tile's page did not run: %+v", landed)
	}
	page, ok := landed.view.(view.Sections)
	if !ok {
		t.Fatalf("the page is a %s", view.TypeOf(landed.view))
	}
	summary, ok := page.Items[0].View.(view.KeyValue)
	if !ok {
		t.Fatalf("the page opens on a %s", view.TypeOf(page.Items[0].View))
	}
	if got := tilePair(summary, "keys"); !strings.HasPrefix(got, "1 key") {
		t.Errorf("the page says keys = %q, inventory = %q: the session did not answer", got, tilePair(summary, "inventory"))
	}
}

// A tile is a third of the screen wide at 120 columns and is cut from the
// bottom, and a security product's tile for its secret store says how it is
// locked and whether it opens before it says anything that can be cut: at the
// narrowest width it shows both lines whole, and no phrase of the history stops
// on a comma.
func TestTheKvTileShowsHowTheStoreIsLockedAtTheNarrowestWidth(t *testing.T) {
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_IDENTITY", "")

	reg := realRegistry(t)
	set, _ := reg.Capability("kv.set")
	rm, _ := reg.Capability("kv.rm")
	write := func(c plugin.Capability, values map[string]any) {
		t.Helper()
		values["passphrase"] = tilePassphrase
		if _, err := c.Run(context.Background(), plugin.NewRequest(values, false, c.Safety == plugin.Destructive)); err != nil {
			t.Fatal(err)
		}
	}
	write(set, map[string]any{"key": "prod/db/password", "value": "v"})
	write(set, map[string]any{"key": "prod/db/password", "value": "v2"})
	write(set, map[string]any{"key": "gone", "value": "v"})
	write(rm, map[string]any{"key": "gone"})

	c, _ := reg.Capability("kv.overview")
	tl := tile{cap: c, view: kvTile(t, tilePassphrase)}
	for _, width := range []int{36, 60, 80} {
		drawn := ansi.Strip(renderTile(tl, width, tileRowHeight(tl, width), false, false))
		for _, want := range []string{"locked with", "unlocks here"} {
			if !strings.Contains(drawn, want) {
				t.Errorf("width %d: the tile does not say %q:\n%s", width, want, drawn)
			}
		}
		for line := range strings.SplitSeq(drawn, "\n") {
			if inner := strings.TrimRight(strings.Trim(line, "│ "), " "); strings.HasSuffix(inner, ",") {
				t.Errorf("width %d: a line stops on a comma, a phrase cut in two: %q\n%s", width, line, drawn)
			}
		}
	}
}

// The tile says whether the store is unlocked in this TUI, and the header says
// it too, so the two change together: a tile held to its thirty-second pace
// said "needs a key" beside a badge that said "store unlocked" for the half
// minute after the unlock, and "unlocked in this session" after the session
// lapsed. It looks at the session on every tick, which costs nothing, and runs
// when what it said last is no longer so; with nothing changed the pace is kept.
func TestTheKvTileRunsAgainWhenTheStoreSessionStartsOrEnds(t *testing.T) {
	m, _ := kvModel(t)
	at := -1
	for i, tl := range m.tiles {
		if tl.cap.ID == "kv.overview" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the dashboard has no kv.overview tile")
	}
	fired := time.Now()
	soon := fired.Add(time.Second)
	tl := m.tiles[at]
	tl.lastFired, tl.storeOpen = fired, false

	if tl.due(soon) {
		t.Fatal("a tile inside its pace ran again with nothing changed")
	}
	warmStore(t, m)
	if !tl.due(soon) {
		t.Error("the store was unlocked and the tile, which said it needed a key, was not due")
	}
	tl.storeOpen = true
	if tl.due(soon) {
		t.Error("a tile that already says the store is unlocked ran again before its pace")
	}
	kv.ForgetSession()
	if !tl.due(soon) {
		t.Error("the session ended and the tile, which says it is unlocked, was not due")
	}
}

// A tile that has just run records what it saw, which is what the next tick
// compares the session with.
func TestADispatchedKvTileRemembersWhetherTheStoreWasUnlocked(t *testing.T) {
	m, _ := kvModel(t)
	warmStore(t, m)
	_ = refreshTiles(m.tiles, 1, m.pluginCfg, m.connFor)
	for _, tl := range m.tiles {
		if tl.cap.ID == "kv.overview" {
			if tl.lastFired.IsZero() || !tl.storeOpen {
				t.Errorf("the kv tile was dispatched (%v) and recorded storeOpen = %v", tl.lastFired, tl.storeOpen)
			}
			return
		}
	}
	t.Fatal("the dashboard has no kv.overview tile")
}
