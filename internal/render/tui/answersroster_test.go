package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// twoGrants issues two grants to one agent in a fresh data directory and
// returns the roster as a screen, its cursor on the first row.
func twoGrants(t *testing.T) Model {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := realRegistry(t)
	allow := mustCap(t, reg, "grant.allow")
	for _, target := range []string{"note.add", "net.hosts.add"} {
		req := plugin.NewRequest(map[string]any{"target": target, "ttl": "1h", "agent": "claude"}, false, false).
			WithSurface(plugin.SurfaceTUI)
		if _, err := allow.Run(context.Background(), req); err != nil {
			t.Fatalf("issuing %s: %v", target, err)
		}
	}
	roster := screenOf(t, "grant.list", nil)
	if tbl, _, ok := rowTable(roster.result.raw); !ok || len(tbl.Rows) != 2 {
		t.Fatalf("the roster has %v rows, want the two grants", len(tbl.Rows))
	}
	return roster
}

func standing(t *testing.T) []string {
	t.Helper()
	v, err := mustCap(t, realRegistry(t), "grant.list").Run(context.Background(),
		plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	tbl, _, _ := rowTable(v)
	var out []string
	for _, row := range tbl.Rows {
		out = append(out, row[0])
	}
	return out
}

// `x` on a grant row took nine boxes to say what the row already said.
func TestXOnAGrantRowRevokesThatGrantAndNoOther(t *testing.T) {
	roster := twoGrants(t)
	tbl, _, _ := rowTable(roster.result.raw)
	picked := tbl.Rows[roster.row][0]
	ran := press(t, roster, "x")
	if ran.mode != modeRunning || ran.current.ID != "grant.revoke" || ran.form != nil {
		t.Fatalf("x left %v on %q (form %v), want the revoke running on the row", ran.mode, ran.current.ID, ran.form)
	}
	if ran.lastValues["exact"] != true || ran.lastValues["target"] != picked || ran.lastValues["all"] != nil {
		t.Errorf("revoking with %v, want exactly %s", ran.lastValues, picked)
	}
	_, err := ran.current.Run(context.Background(), plugin.NewRequest(ran.lastValues, false, false).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatalf("the revoke x ran: %v", err)
	}
	if left := standing(t); len(left) != 1 || left[0] == picked {
		t.Errorf("after x on %s the roster holds %v, want only the other grant", picked, left)
	}
}

// A row whose cells could not be read back names no single grant, and a
// revoke on it would take back every grant of that target: it asks first.
func TestXOnARowThatNamesNoSingleGrantOpensTheFormInstead(t *testing.T) {
	roster := twoGrants(t)
	var revoke capAction
	for _, a := range capActions(roster.reg, rosterCapability) {
		if a.key == "x" {
			revoke = a
		}
	}
	tbl, _, _ := rowTable(roster.result.raw)
	base, ok := roster.actionSeed(revoke, tbl)
	if !ok || base["exact"] != true {
		t.Fatalf("the roster row seeds %v, %v", base, ok)
	}
	delete(base, "exact")
	if _, _, took := roster.answer(revoke, base); took {
		t.Error("the shortcut took a row that does not name exactly one grant")
	}
}

func TestACapitalXRevokesEveryGrantBehindItsOwnDryRun(t *testing.T) {
	roster := twoGrants(t)
	got := press(t, roster, "X")
	if got.mode != modeRunning || !got.previewing || got.current.ID != "grant.revoke" {
		t.Fatalf("X left %v previewing %v on %q, want the dry run that is the confirmation", got.mode, got.previewing, got.current.ID)
	}
	if got.lastValues["all"] != true {
		t.Errorf("X asked for %v", got.lastValues)
	}
	if left := standing(t); len(left) != 2 {
		t.Errorf("X revoked before the confirmation: %v", left)
	}
	remote := roster
	remote.lastValues = map[string]any{"server": "lab"}
	if refused := press(t, remote, "X"); refused.mode != modeResult || !strings.Contains(refused.flash, "another server's") {
		t.Errorf("X on a remote roster left %v with %q", refused.mode, refused.flash)
	}
}

func TestTheRosterAdvertisesTheKeysItAnswers(t *testing.T) {
	bar := barOf(twoGrants(t))
	for _, want := range []string{"x revoke", "X revoke all"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the roster's bar lacks %q:\n%s", want, bar)
		}
	}
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	if bar := barOf(screenOf(t, "agent.pending", nil)); !strings.Contains(bar, "A allow longer") {
		t.Errorf("the queue's bar lacks the capital A:\n%s", bar)
	}
}

// A row action has no row on a tile; the page whose rows they are does.
func TestARowKeyOnATileOpensThePageAndSaysWhatToDo(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	tile := -1
	for i, tl := range m.tiles {
		if !tl.search && tl.cap.ID == rosterCapability {
			tile = i
		}
	}
	if tile < 0 {
		t.Fatal("no grant roster tile in this catalogue")
	}
	m.selected = tile
	got := press(t, m, "x")
	if got.mode == modeForm || got.current.ID != rosterCapability {
		t.Fatalf("x on the roster tile left %v on %q, want the roster itself", got.mode, got.current.ID)
	}
	if !strings.Contains(got.flash, "pick a row, then x revoke") {
		t.Errorf("flash %q does not say what to do next", got.flash)
	}
}
