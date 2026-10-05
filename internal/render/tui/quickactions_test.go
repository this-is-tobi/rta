package tui

import (
	"testing"

	operatorid "github.com/this-is-tobi/rta/internal/operator"
)

// `g` on the agent tile asked six questions about filters before it showed the
// record; the record is what it is for, and `e` on it is where the filters are.
func TestGOnTheAgentTileShowsTheRecordAtOnce(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	tile := -1
	for i, tl := range m.tiles {
		if !tl.search && tl.cap.ID == "agent.overview" {
			tile = i
		}
	}
	if tile < 0 {
		t.Fatal("no agent tile in this catalogue")
	}
	m.selected = tile
	got := press(t, m, "g")
	if got.mode != modeRunning || got.current.ID != "agent.log" || got.form != nil {
		t.Errorf("g on the tile left %v on %q (form %v), want the record running", got.mode, got.current.ID, got.form != nil)
	}
}

func TestGOnTheOpenedAgentPageShowsTheRecordAtOnce(t *testing.T) {
	page := screenOf(t, "agent.overview", nil)
	got := press(t, page, "g")
	if got.mode != modeRunning || got.current.ID != "agent.log" || got.form != nil {
		t.Errorf("g on the page left %v on %q (form %v), want the record running", got.mode, got.current.ID, got.form != nil)
	}
	opened := press(t, screenOf(t, "agent.log", nil), "e")
	if opened.mode != modeForm || !formFields(opened)["refused"] {
		t.Errorf("e on the record left %v with %v, want its filters", opened.mode, formFields(opened))
	}
}

// grant.list takes its operator passphrase for a roster on another server.
// From the search bar it asked for it all the same.
func TestARosterRunsFromTheSearchBarWithoutAskingForAServersPassphrase(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m = press(t, m, "/")
	for _, r := range "grant.list" {
		m = press(t, m, string(r))
	}
	got := press(t, m, "enter")
	if got.mode != modeRunning || got.current.ID != "grant.list" {
		t.Errorf("enter on grant.list left %v on %q, want it running", got.mode, got.current.ID)
	}
}

func TestACredentialIsReadOnlyAcrossAServerWhenItIsTheOperatorKeys(t *testing.T) {
	reg := realRegistry(t)
	roster := mustCap(t, reg, "grant.list")
	var pass = roster.Inputs[len(roster.Inputs)-1]
	if pass.Name != "passphrase" {
		t.Fatalf("the roster's last input is %q, want its passphrase", pass.Name)
	}
	if pass.Help != operatorid.PassphraseField.Help {
		t.Fatalf("the roster's passphrase is %q, not the operator key's", pass.Help)
	}
	if !readsOnlyAcrossAServer(roster, pass, map[string]any{}) {
		t.Error("the operator passphrase is asked for a roster read on this machine")
	}
	if readsOnlyAcrossAServer(roster, pass, map[string]any{"server": "lab"}) {
		t.Error("the operator passphrase is not asked for a roster read on a server")
	}
	unlock := mustCap(t, reg, "kv.list")
	for _, f := range unlock.Inputs {
		if f.Name == "passphrase" && readsOnlyAcrossAServer(unlock, f, nil) {
			t.Error("the store's own passphrase was taken for one that only a server reads")
		}
	}
}
