package tui

import (
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `L` on the queue opened the lock form blank: the agent's name was on the
// row and the operator retyped it, under pressure, in whatever spelling.
func TestTheLockKeyFillsTheAgentInFromTheQueueRow(t *testing.T) {
	reg := realRegistry(t)
	var lock capAction
	for _, a := range capActions(reg, "agent.pending") {
		if a.key == "L" {
			lock = a
		}
	}
	if lock.cap.ID != "lock.add" || lock.src != srcRow {
		t.Fatalf("L on the queue = %+v, want lock.add from the row", lock)
	}
	m := Model{reg: reg, row: 1}
	tbl := view.Table{
		Columns: []view.Column{{Name: "id"}, {Name: "agent"}, {Name: "capability"}},
		Rows:    [][]string{{"aaaa1111", "codex", "kv.get"}, {"bbbb2222", "claude", "kv.rm"}},
	}
	base, ok := m.actionSeed(lock, tbl)
	if !ok || base["name"] != "claude" {
		t.Fatalf("seed = %v, %v; want the agent under the cursor", base, ok)
	}
	// No agent column — an unnamed queue — leaves the name for the form
	// rather than taking the id from the first column.
	bare := view.Table{Columns: []view.Column{{Name: "id"}}, Rows: [][]string{{"aaaa1111"}, {"bbbb2222"}}}
	if base, ok := m.actionSeed(lock, bare); !ok || base["name"] != nil {
		t.Fatalf("seed without an agent column = %v, %v; want the name left open", base, ok)
	}
}

func TestTheLockKeyFillsTheAgentInFromTheDetailPage(t *testing.T) {
	reg := realRegistry(t)
	var lock capAction
	for _, a := range capActions(reg, "agent.show") {
		if a.key == "L" {
			lock = a
		}
	}
	if lock.cap.ID != "lock.add" || lock.src != srcSelf {
		t.Fatalf("L on the detail page = %+v, want lock.add from the page", lock)
	}
	m := Model{reg: reg}
	m.result = resultMsg{view: view.KeyValue{Pairs: []view.Pair{{Key: "id", Value: "aaaa1111"}, {Key: "agent", Value: "claude"}}}}
	base, ok := m.actionSeed(lock, view.Table{})
	if !ok || base["name"] != "claude" {
		t.Fatalf("seed = %v, %v; want the agent the page names", base, ok)
	}
}

// A form opened by acting on this machine's own records asked about the
// remote server the same command could aim at, and the passphrase that
// signs a request to it.
func TestAnActionsFormAsksOnlyAboutThisMachine(t *testing.T) {
	reg := realRegistry(t)
	names := func(fs []plugin.Field) map[string]bool {
		out := map[string]bool{}
		for _, f := range fs {
			out[f.Name] = true
		}
		return out
	}
	deny := names(hereOnly(mustCap(t, reg, "agent.deny").Inputs))
	if deny["server"] || deny["passphrase"] || !deny["id"] {
		t.Fatalf("agent.deny from a row asks %v", deny)
	}
	// The guard's passphrase is this machine's, and stays.
	allow := names(hereOnly(mustCap(t, reg, "grant.allow").Inputs))
	if allow["server"] || !allow["passphrase"] || !allow["target"] {
		t.Fatalf("grant.allow from a row asks %v", allow)
	}
}

// The queue showed the moment it was opened until `r`; a call that parked
// while the operator was reading did not appear.
func TestTheQueueRefreshesItselfWhileOnScreen(t *testing.T) {
	m := New(realRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.mode = modeResult
	sm.current = mustCap(t, sm.reg, "agent.pending")
	sm.tickGen = 3
	after, cmd := sm.Update(tickMsg{gen: 3})
	if cmd == nil {
		t.Fatal("a tick on the open queue did not re-run it")
	}
	if after.(Model).mode != modeResult {
		t.Fatal("the refresh took the screen away")
	}
	if _, cmd := sm.Update(tickMsg{gen: 2}); cmd != nil {
		t.Fatal("a stale tick re-ran the queue")
	}
	sm.current = mustCap(t, sm.reg, "agent.log")
	if _, cmd := sm.Update(tickMsg{gen: 3}); cmd != nil {
		t.Fatal("a view that is not live was re-run on a tick")
	}
}
