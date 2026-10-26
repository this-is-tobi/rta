package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A run on a connection whose credential is a reference fetched it on the
// update loop before the running screen was drawn: a `kube:` Secret read is a
// cluster round trip, up to ten seconds against a slow API server, and a store
// held by a passphrase a second of scrypt, with no spinner, and no key
// answered, esc included, until it came back. The run fetches it now, under
// the running screen and its own context.

// resolvedRun is where a run of c with values goes and what it is handed, its
// references fetched as the run fetches them (runCmd): resolveProfile leaves
// that to the run when the standing bind has not fetched them already.
func resolvedRun(m Model, c plugin.Capability, values map[string]any,
) (string, map[string]any, config.Connection, *view.Error) {
	name, filled, conn, verr := m.resolveProfile(c, values)
	if verr != nil || name == "" || filled != nil {
		return name, filled, conn, verr
	}
	filled, verr = fillRefs(context.Background(), name, conn, c, withoutPicker(c, values))
	return name, filled, conn, verr
}

// referencedConfig is prod's database, its host a kv reference to a store this
// machine does not have, so that the fetch is refused the moment it is made.
func referencedConfig() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"prod": {Plugins: map[string]config.Connection{"db": {Secrets: map[string]string{"host": "kv:db-host"}}}},
	}}
}

// fetchAnswer is the resultMsg among what cmd answers.
func fetchAnswer(t *testing.T, cmd tea.Cmd) resultMsg {
	t.Helper()
	for _, msg := range messagesOf(cmd) {
		if r, ok := msg.(resultMsg); ok {
			return r
		}
	}
	t.Fatal("the run answered nothing")
	return resultMsg{}
}

func TestARunFetchesItsConnectionsReferencesUnderTheRunningScreen(t *testing.T) {
	m := profileModel(t, referencedConfig())
	c := dbPlugin().Capabilities[0]
	cmd := m.startRun(c, map[string]any{profileInput: "prod"}, false)
	if m.mode != modeRunning || cmd == nil {
		t.Fatalf("mode %v, a command %v: the reference was fetched before the run started", m.mode, cmd != nil)
	}
	// The fetch's refusal is the run's answer, as a forward that did not open
	// is, and lands as the result page it used to be.
	answer := fetchAnswer(t, cmd)
	if answer.err == nil || answer.seq != m.runSeq {
		t.Fatalf("the run answered %+v, want the fetch's refusal for this run", answer)
	}
	next, _ := m.Update(answer)
	if got := next.(Model); got.mode != modeResult || got.result.err == nil {
		t.Errorf("mode %v, error %v: the refusal is not on the result page", got.mode, got.result.err)
	}
}

// A tab on a live box lists what the service holds with the connection's
// credential, and read that credential before the press returned: the form
// froze for the fetch, with no key answered. It is read with the listing, and a
// refusal is the press's answer.
func TestATabPressReadsItsCredentialWithTheListing(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	profiles := map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"s3": {Secrets: map[string]string{"secret-key": "kv:s3-secret"}},
	}}}
	m, c := liveListModel(t, lr, profiles, "backups")
	nm := openLiveList(t, m, c)
	*nm.form.bindings[profileInput] = "staging"
	next, cmd := nm.Update(tabKey)
	pressed := next.(Model)
	if pressed.flash != "completing…" || cmd == nil {
		t.Fatalf("flash %q, a command %v: the press read the credential before it returned", pressed.flash, cmd != nil)
	}
	msg, ok := cmd().(completeMsg)
	if !ok || msg.err == nil {
		t.Fatalf("the press answered %+v, want the credential's refusal", msg)
	}
	if lr.calls() != 0 {
		t.Errorf("the service was asked %d times without its credential", lr.calls())
	}
	if after, _ := pressed.Update(msg); after.(Model).flashBad == "" {
		t.Error("the refusal was not said")
	}
}

// The dry run of a destructive call is a run too: its references are fetched
// under the running screen, and a refusal is the call's own failure, as it
// was, and not a question about a call that cannot be made.
func TestAPreviewFetchesItsConnectionsReferencesUnderTheRunningScreen(t *testing.T) {
	m := profileModel(t, referencedConfig())
	c := dbPlugin().Capabilities[0]
	c.ID, c.Safety = "db.drop", plugin.Destructive
	cmd := m.startConfirm(c, map[string]any{profileInput: "prod"})
	if m.mode != modeRunning || !m.previewing || cmd == nil {
		t.Fatalf("mode %v, previewing %v, a command %v: the reference was fetched before the preview started",
			m.mode, m.previewing, cmd != nil)
	}
	next, _ := m.Update(fetchAnswer(t, cmd))
	if got := next.(Model); got.mode != modeResult || got.result.err == nil {
		t.Errorf("mode %v, error %v: the refusal is not the call's failure page", got.mode, got.result.err)
	}
}

// A live view refreshed on an environment named by the pick or the tile keeps
// what it shows when the credential cannot be fetched, flashed, with the next
// refresh armed, as it keeps it for a refresh that could not resolve where to
// go. The fetch was part of resolving it until it moved into the run, and its
// refusal came back as an answer: a kv sitting that ran out put an error page
// over the rows until the next tick.
func TestALiveRefreshWhoseCredentialCannotBeFetchedKeepsItsRows(t *testing.T) {
	m := profileModel(t, referencedConfig())
	c := dbPlugin().Capabilities[0]
	c.Live = true
	shown := view.Text{Body: "the rows on screen"}
	m.mode, m.current, m.result = modeResult, c, resultMsg{cap: c, view: shown, raw: shown}
	answer := fetchAnswer(t, m.refreshInPlace(c, map[string]any{profileInput: "prod"}, false))
	if answer.err == nil || !answer.live {
		t.Fatalf("the refresh answered %+v, want the fetch's refusal, marked live", answer)
	}
	genBefore := m.tickGen
	next, cmd := m.Update(answer)
	got := next.(Model)
	if got.mode != modeResult || got.result.err != nil || got.result.view != shown {
		t.Errorf("mode %v, error %v: the refusal replaced the rows on screen", got.mode, got.result.err)
	}
	if got.flashBad == "" || got.flashBad != answer.err.Message {
		t.Errorf("the flash says %q, want the refusal", got.flashBad)
	}
	if cmd == nil || got.tickGen == genBefore {
		t.Error("the next refresh was not armed")
	}
}
