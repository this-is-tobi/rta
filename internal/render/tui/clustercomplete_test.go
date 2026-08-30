package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
)

// Cluster completion, driven the way a person drives it: tab — the app's one
// completion key — through the real Update path. The tunnel package proves
// what each listing asks and answers; these prove the key reaches it with the
// *field's* coordinates, which no tunnel-level test can see, and that one key
// carries the whole flow: fetch when nothing is left to accept, accept when
// something is.

// clusterFake answers the listings completion makes and records each argv.
func clusterFake(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := `#!/bin/sh
printf '%s ' "$@" >> ` + log + `; printf '\n' >> ` + log + `
case "$*" in
  *"config get-contexts"*) printf 'kind-kind\nhomelab\n' ;;
  *"get namespaces"*) printf 'namespace/monitoring\nnamespace/databases\n' ;;
  *"get secrets"*) printf 'secret/pg-creds\nsecret/api-token\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func askedFake(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return ""
	}
	return string(b)
}

var tabKey = tea.KeyPressMsg{Code: tea.KeyTab}

// connFormOnKubeField opens the connection editor and moves focus onto the
// coordinate field, the way a person tabs to it.
func connFormOnKubeField(t *testing.T) Model {
	t.Helper()
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}})
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	nm = pressTab(t, nm) // off the plugin picker, which has nothing left to offer
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs[profileKubeField]) {
		t.Fatal("one tab from the top did not land on the coordinate field")
	}
	return nm
}

// fetchFromCluster presses tab expecting the fetch half of the key, and lands
// the answer, the way a session would: keypress, background command, message,
// apply.
func fetchFromCluster(t *testing.T, m Model) Model {
	t.Helper()
	// The production timeout is an operator-patience bound; against a fake
	// kubectl inside a loaded full-suite run it is a flake generator — the
	// fetch's fork alone can lose a three-second race the feature never
	// races. Raised here, not in any single test, so every fetch in the
	// package measures the code.
	saved := completeTimeout
	completeTimeout = 30 * time.Second
	t.Cleanup(func() { completeTimeout = saved })
	next, cmd := m.Update(tabKey)
	nm := next.(Model)
	if nm.flash != "completing…" {
		t.Fatalf("tab did not start a fetch (flash %q)", nm.flash)
	}
	if cmd == nil {
		t.Fatal("the fetch returned no command to run")
	}
	msg, ok := cmd().(completeMsg)
	if !ok {
		t.Fatal("the fetch did not produce a completeMsg")
	}
	next, _ = nm.Update(msg)
	return next.(Model)
}

// One key walks the coordinate: tab fetches when the segment is empty,
// accepts when a suggestion extends what is typed, and fetches the next
// segment the moment the accept leaves the cursor on a separator — the shell
// rhythm, with no second key to learn.
//
// The two flashes are two states of the same feature: bubbles matches
// suggestions only against non-empty text, so on an empty box there is no
// ghost yet and the names themselves are what the screen owes; after that,
// the ghost is on screen and the affordances are.
func TestTabAloneWalksTheCoordinate(t *testing.T) {
	noHistory(t)
	log := clusterFake(t)
	nm := connFormOnKubeField(t)

	nm = fetchFromCluster(t, nm) // empty box: fetch contexts
	if nm.flash != "kube contexts: homelab, kind-kind" {
		t.Errorf("an empty box's fetch flashed %q, want the context names themselves", nm.flash)
	}

	nm.form.form = typeInto(nm.form.form, "h")
	next, _ := nm.Update(tabKey) // "homelab/" extends "h": this tab accepts
	nm = next.(Model)
	if got := *nm.form.bindings[profileKubeField]; got != "homelab/" {
		t.Fatalf("tab with a suggestion on offer did not accept it (field %q)", got)
	}
	if nm.flash == "completing…" {
		t.Fatal("tab fetched over a suggestion it should have accepted")
	}

	nm = fetchFromCluster(t, nm) // cursor on the separator: fetch namespaces
	if !strings.Contains(askedFake(t, log), "--context homelab get namespaces -o name") {
		t.Errorf("the namespace listing was not pinned to the typed context:\n%s", askedFake(t, log))
	}
	if nm.flash != "2 namespaces in homelab — ↓ cycles, tab completes" {
		t.Errorf("flash = %q", nm.flash)
	}
}

// Accepting stays in the field.
//
// The form-wide keymap makes tab accept *and* advance — right for a
// whole-value suggestion (complete_test.go pins it), wrong for a value built
// four segments deep, where it would cost a shift+tab per segment. This is
// the test that fails if the per-field keymap override is dropped: the
// binding keeps its old value and focus lands on the next field.
func TestTabAcceptsASegmentAndStaysInTheField(t *testing.T) {
	noHistory(t)
	clusterFake(t)
	nm := connFormOnKubeField(t)
	nm.form.form = typeInto(nm.form.form, "homelab/")
	nm = fetchFromCluster(t, nm)

	// pressTab, not a tab into the form: the press has to run the command it
	// produces, or a NextField this field should not emit — the exact
	// regression this test exists for — is returned unrun and the focus never
	// moves. A first version of this test asserted against a dropped keymap
	// override and passed for that reason.
	nm = pressTab(t, nm)
	if got := *nm.form.bindings[profileKubeField]; got != "homelab/databases/" {
		t.Errorf("after tab the field holds %q, want the first suggestion accepted in place", got)
	}
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs[profileKubeField]) {
		t.Error("tab left the coordinate field — accepting a segment must not cost the focus")
	}
}

// On every other field, tab is exactly what it was: the form's own key. On
// the plugin picker it completes locally or moves on — never a socket.
func TestTabOnAnUncompletableFieldIsTheFormsTab(t *testing.T) {
	noHistory(t)
	log := clusterFake(t)
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}})
	m.profileOpen = "staging"
	next, _ := m.startConnForm("db")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form) // focus is on the plugin field

	if _, _, ok := nm.completionTarget(); ok {
		t.Fatal("the plugin picker was offered cluster completion")
	}
	after, _ := nm.Update(tabKey)
	nm = after.(Model)
	if nm.flash == "completing…" {
		t.Error("tab on the plugin picker started a fetch")
	}
	if got := askedFake(t, log); got != "" {
		t.Errorf("kubectl ran for a field completion does not cover: %s", got)
	}
}

// The credential form's reference completes against the connection's own
// coordinate — context and namespace from the `kube:` line the operator
// already wrote, the same boundary the read at call time enforces.
func TestTheCredentialReferenceCompletesAgainstTheConnectionsCoordinate(t *testing.T) {
	log := clusterFake(t)
	m := credentialModel(t, "password")
	for i, row := range m.profiles {
		for j := range row.conns {
			m.profiles[i].conns[j].conn.Kube = "homelab/databases/svc/postgres:5432"
		}
	}
	next, _ := m.startCredentialForm()
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)

	// To the source picker, choose the cluster (store → env → kube), and the
	// implied follow-up appears focused: the reference field.
	nm.form.form = settleForm(nm.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	nm.form.form = settleForm(nm.form.form, tea.KeyPressMsg{Code: tea.KeyDown})
	nm.form.form = settleForm(nm.form.form, tea.KeyPressMsg{Code: tea.KeyDown})
	nm.form.form = settleForm(nm.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})

	field, coord, ok := nm.completionTarget()
	if !ok || field != credKubeField {
		t.Fatalf("focus did not land on the reference field (field %q, ok %v)", field, ok)
	}
	if coord != "homelab/databases/svc/postgres:5432" {
		t.Fatalf("the reference would complete against %q, not the connection's coordinate", coord)
	}

	nm = fetchFromCluster(t, nm)
	if !strings.Contains(askedFake(t, log), "--context homelab --namespace databases get secrets -o name") {
		t.Errorf("the Secret listing was not pinned to the coordinate:\n%s", askedFake(t, log))
	}
	if nm.flash != "Secrets in databases: api-token, pg-creds" {
		t.Errorf("flash = %q", nm.flash)
	}
}

// An answer for a form that is gone is dropped, not applied to whichever
// same-named field a later form happens to hold. The fetch is asynchronous;
// esc is not.
func TestAFetchForAClosedFormIsDropped(t *testing.T) {
	noHistory(t)
	clusterFake(t)
	nm := connFormOnKubeField(t)
	_, cmd := nm.Update(tabKey)
	msg := cmd().(completeMsg)

	// The operator closed the form and opened another before the answer came.
	reopened, _ := nm.startConnForm("db")
	rm := reopened.(Model)
	after, _ := rm.Update(msg)
	if flash := after.(Model).flash; strings.Contains(flash, "kube contexts") {
		t.Errorf("a stale fetch landed on a fresh form (flash %q)", flash)
	}
}

// needsFetch is tab's whole disambiguation, so both directions of its one
// question are pinned. The pair that matters most is the same string with
// different offers on the table: "homelab/" against the context list is the
// moment after an accept (equal is not an extension — fetch the next
// segment), and against the namespace list it is the moment after a fetch
// (the ghost is up — this tab takes it). A first draft decided by trailing
// separator instead, which cannot tell those two moments apart, and the walk
// test caught it refetching over the ghost.
func TestNeedsFetchTellsAcceptFromFetch(t *testing.T) {
	contexts := []string{"homelab/", "kind-kind/"}
	namespaces := []string{"homelab/databases/", "homelab/monitoring/"}
	for _, tc := range []struct {
		value   string
		offered []string
		want    bool
		why     string
	}{
		{"", nil, true, "an empty box has nothing to accept and nothing yet offered"},
		{"", contexts, true, "an empty box fetches even over stale offers — the widget cannot accept against empty text"},
		{"h", contexts, false, "homelab/ extends h, so this tab is the accept"},
		{"HOME", contexts, false, "matching is case-insensitive, as the widget's is"},
		{"homelab/", contexts, true, "the box equals the accepted offer: fetch the next segment"},
		{"homelab/", namespaces, false, "a fetch landed for this segment: this tab takes the ghost"},
		{"zzz", contexts, true, "nothing on offer extends it, so re-fetch for the segment"},
	} {
		if got := needsFetch(tc.value, tc.offered); got != tc.want {
			t.Errorf("needsFetch(%q, %v) = %v — %s", tc.value, tc.offered, got, tc.why)
		}
	}
}
