package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// U on the dashboard unlocks the encrypted store for the session, for the length
// the person chooses, and locks it again. The store session itself and what
// makes it safe are builtin/kv's (session.go, startsession_test.go); these hold
// the screen's half: what the key does in each state, that the action exists
// nowhere but here, and that answering it puts the person back on the tiles.

const unlockPassphrase = "correct horse battery staple"

func lockedStore(t *testing.T, m Model) {
	t.Helper()
	set, ok := m.reg.Capability("kv.set")
	if !ok {
		t.Fatal("kv.set is not registered")
	}
	create := plugin.NewRequest(map[string]any{"key": "db-password", "value": "hunter2", "passphrase": unlockPassphrase}, false, false)
	if _, err := set.Run(context.Background(), create); err != nil {
		t.Fatal(err)
	}
}

func TestUWithNoStoreSaysThereIsNothingToUnlock(t *testing.T) {
	m, _ := kvModel(t)
	next, _, handled := m.dashboardKeys(keyMsg("U"))
	if !handled {
		t.Fatal("U was not taken by the dashboard")
	}
	got := next.(Model)
	if got.mode != modeDashboard || !strings.Contains(got.flash, "nothing to unlock") {
		t.Errorf("mode = %v, flash = %q", got.mode, got.flash)
	}
}

func TestUOnALockedStoreOpensAMaskedFormForThePassphraseAndTheLength(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)

	next, _, _ := m.dashboardKeys(keyMsg("U"))
	got := next.(Model)
	if got.mode != modeForm || got.form == nil || got.form.cap.ID != unlockID {
		t.Fatalf("mode = %v, form = %+v", got.mode, got.form)
	}
	var names []string
	for _, f := range got.form.fields {
		names = append(names, f.Name)
		if f.Name == "passphrase" && (f.Type != plugin.Secret || !strings.Contains(f.Help, "store's passphrase")) {
			t.Errorf("the passphrase box is %+v, want a masked one that says whose passphrase", f)
		}
		if f.Name == "for" && (f.Type != plugin.Duration || f.Default != "1h") {
			t.Errorf("the length box is %+v", f)
		}
	}
	if strings.Join(names, ",") != "passphrase,for" {
		t.Errorf("fields = %v", names)
	}
}

func TestTheUnlockActionIsOnNoSurfaceButThisScreen(t *testing.T) {
	m, _ := kvModel(t)
	if _, ok := m.reg.Capability(unlockID); ok {
		t.Error("kv.unlock is in the registry, which puts it on the CLI and on every MCP tool list")
	}
}

func TestTheUnlockActionRefusesAWrongPassphraseAndStartsASessionForARightOne(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)
	c := unlockCapability("the store's passphrase")

	bad := plugin.NewRequest(map[string]any{"passphrase": "not it", "for": "30m"}, false, false)
	if _, err := c.Run(context.Background(), bad); err == nil {
		t.Fatal("a wrong passphrase unlocked the store")
	}
	if _, ok := kv.SessionUntil(); ok {
		t.Fatal("a wrong passphrase started a session")
	}

	good := plugin.NewRequest(map[string]any{"passphrase": unlockPassphrase, "for": "30m"}, false, false)
	v, err := c.Run(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	until, ok := kv.SessionUntil()
	if !ok || time.Until(until) > 30*time.Minute || time.Until(until) < 29*time.Minute {
		t.Errorf("session until %v, %v; want about thirty minutes", until, ok)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "30m") || strings.Contains(body, unlockPassphrase) {
		t.Errorf("answer = %q", body)
	}
}

// The answer goes back to the tiles, which run again at once, and says so in the
// footer; a result page would hold them until it was closed.
func TestAnsweringTheUnlockFormReturnsToTheDashboardWithAFlash(t *testing.T) {
	m, _ := kvModel(t)
	m.mode = modeResult
	m.runSeq = 3
	next, cmd := m.Update(resultMsg{
		cap: unlockCapability("x"), view: view.Text{Body: "store unlocked for 1h — U locks it again"}, seq: 3,
	})
	got := next.(Model)
	if got.mode != modeDashboard || !strings.Contains(got.flash, "store unlocked") {
		t.Errorf("mode = %v, flash = %q", got.mode, got.flash)
	}
	if cmd == nil {
		t.Error("nothing re-ran the tiles that were failing")
	}
}

func TestUWhileUnlockedLocksTheStoreAgain(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)
	if verr := kv.StartSession(unlockPassphrase, time.Hour); verr != nil {
		t.Fatal(verr)
	}
	if item := m.unlockItem(); item.label != "lock store" {
		t.Errorf("the footer says %q while the store is open", item.label)
	}
	next, _, _ := m.dashboardKeys(keyMsg("U"))
	asked := next.(Model)
	if asked.dialog == nil {
		t.Fatal("U locked the store without asking")
	}
	if _, ok := kv.SessionUntil(); !ok {
		t.Fatal("U shut the store before the question was answered")
	}
	got := press(t, asked, "enter")
	if _, ok := kv.SessionUntil(); ok {
		t.Error("confirming left the store unlocked")
	}
	if !strings.Contains(got.flash, "locked") || got.dialog != nil {
		t.Errorf("flash = %q, dialog open = %v", got.flash, got.dialog != nil)
	}
	if item := got.unlockItem(); item.label != "unlock store" {
		t.Errorf("the footer says %q once it is shut", item.label)
	}
}

// The key box of a kv action walks the store's keys once U has unlocked it. The
// box offered nothing: the unlock pair has left the form, the completion request
// is stripped of every secret, and the passphrase the person typed is held where
// only a run could reach it. Locked again, the box is empty again.
func TestAKeyBoxBrowsesTheStoreOnceItIsUnlockedWithU(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)
	set, _ := m.reg.Capability("kv.set")
	more := plugin.NewRequest(map[string]any{"key": "api-token", "value": "t", "passphrase": unlockPassphrase}, false, false)
	if _, err := set.Run(context.Background(), more); err != nil {
		t.Fatal(err)
	}
	rm, _ := m.reg.Capability("kv.rm")

	open := func() (Model, *capForm) {
		model, _ := m.startForm(rm, nil)
		next := model.(Model)
		next.form.form = startedForm(next.form)
		return next, next.form
	}
	locked, cf := open()
	_ = pressKey(t, locked, downKey)
	if got := *cf.bindings["key"]; got != "" {
		t.Fatalf("a locked store filled the key box with %q", got)
	}

	if verr := kv.StartSession(unlockPassphrase, time.Hour); verr != nil {
		t.Fatal(verr)
	}
	unlocked, cf := open()
	if _, asked := cf.bindings["passphrase"]; asked {
		t.Fatal("the unlock pair is still in the form, so this is not the case the unlock is for")
	}
	unlocked = pressKey(t, unlocked, downKey)
	if got := *cf.bindings["key"]; got != "api-token" {
		t.Fatalf("down on the key box holds %q, want the first key of the store", got)
	}
	_ = pressKey(t, unlocked, downKey)
	if got := *cf.bindings["key"]; got != "db-password" {
		t.Errorf("second down holds %q, want the next key", got)
	}
}

// The screen that opens with every tile failing for want of the store has the
// search line selected, and U has to reach the unlock from there like every
// other letter does.
func TestUReachesTheUnlockWhileTheSearchLineHoldsTheSelection(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)
	m = filled(t, New(m.reg, m.dash, nil), 100, 30)
	m.selected = 0
	next, _, _ := m.dashboardKeys(keyMsg("U"))
	got := next.(Model)
	if got.searchEditing || got.query != "" {
		t.Errorf("U started a search: editing=%v query %q", got.searchEditing, got.query)
	}
	if got.mode != modeForm || got.current.ID != unlockID {
		t.Errorf("U opened mode %v for %q, want the unlock form", got.mode, got.current.ID)
	}
}

// What a tile shows while the store is shut names the key, and an agent's reader
// is not handed one.
func TestATilesRefusalNamesTheKeyThatUnlocksTheStore(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)
	_, verr := kv.RevealAs(plugin.SurfaceTUI)("db-password")
	if verr == nil || !strings.Contains(verr.Hint, "press U") {
		t.Fatalf("verr = %+v", verr)
	}
}

// The store refuses a session's length without knowing which box asked for it,
// and the unlock form names its own, so it opens again on the for box rather
// than on the passphrase the store names for its other refusals.
func TestAnUnlockForTooShortATimeNamesTheForBox(t *testing.T) {
	c := unlockCapability("the store's passphrase")
	_, err := c.Run(context.Background(), plugin.NewRequest(map[string]any{"passphrase": "pp", "for": "10s"}, false, false))
	if verr := view.AsError(err, "x"); verr == nil || verr.Code != "kv.session.ttl" || verr.Input != "for" {
		t.Errorf("a ten-second session = %+v, want kv.session.ttl naming for", verr)
	}
}

// U is a key of the dashboard, and its form closed to whichever screen the
// last one to open a form had left behind: open a capability from the
// catalogue, go back to the dashboard, press U and then esc, and the catalogue
// was on screen, with the dashboard's refresh not started again. It closes to
// the dashboard it was opened on.
func TestLeavingTheUnlockFormGoesBackToTheDashboard(t *testing.T) {
	m, _ := kvModel(t)
	m.mode, m.origin = modeDashboard, modeBrowse
	next, _ := m.openUnlock("the store's passphrase")
	form := next.(Model)
	if form.mode != modeForm || form.form == nil || form.form.cap.ID != unlockID {
		t.Fatalf("mode = %v, form = %+v", form.mode, form.form)
	}
	gen := form.tickGen
	back, cmd := form.Update(keyMsg("esc"))
	if got := back.(Model); got.mode != modeDashboard || got.tickGen == gen || cmd == nil {
		t.Errorf("esc on the unlock form went to %v (refresh restarted %v), want the dashboard", got.mode, got.tickGen != gen)
	}
}
