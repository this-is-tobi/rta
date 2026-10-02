package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/tunnel"
)

// landing is the answer a fetch brings back for the live bucket field.
func landing(m Model) completeMsg {
	return completeMsg{form: m.form, field: "bucket", c: tunnel.Completion{
		Items: []string{"backups", "media/"}, Names: []string{"backups", "media/"}, What: "buckets",
	}}
}

// answers is what running cmd and everything it batches says, clocks left
// alone as resolveCmd leaves them.
func answers(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg, ok := resolveCmd(cmd)
	if !ok || msg == nil {
		return nil
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, sub := range batch {
		out = append(out, answers(sub)...)
	}
	return out
}

// A keystroke queues a reload of the field's suggestions, which huh runs on a
// goroutine of its own and which calls the field's suggestion function when it
// does. A fetch that lands in between must leave that function where it is:
// Input.Suggestions clears it, so a landing that went through the widget left
// the reload to call nil, in a process that is somebody's terminal.
//
// The interleaving is forced rather than waited for: the reload is held, the
// fetch lands, and the reload runs after.
func TestAFetchLandingBetweenAKeystrokeAndItsReloadLeavesTheReloadItsFunction(t *testing.T) {
	noHistory(t)
	m, c := liveModel(t, &liveRecorder{}, nil)
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)

	_, held := nm.form.form.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if held == nil {
		t.Fatal("the keystroke left no command to hold, so this proves nothing about landing a fetch")
	}
	nm.applyCompletion(landing(nm))
	if got := answers(held); len(got) == 0 {
		t.Fatal("the held command answered nothing, so no reload ran after the landing")
	}
}

// What a landed fetch is for is the box showing it, without another keystroke:
// "ba" is typed, nothing is on offer yet, and the answer arrives.
func TestALandedFetchReachesTheBoxWithoutAnotherKeystroke(t *testing.T) {
	noHistory(t)
	m, c := liveModel(t, &liveRecorder{}, nil)
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	nm.form.form = typeInto(nm.form.form, "ba")
	if strings.Contains(plain(nm.form.form.View()), "backups") {
		t.Fatal("the box shows backups before any fetch, so this proves nothing about landing one")
	}

	next, cmd := nm.applyCompletion(landing(nm))
	nm = next.(Model)
	if cmd == nil {
		t.Fatal("landing a fetch gave the form nothing to be told with")
	}
	if msg, ok := resolveCmd(cmd); ok {
		nm.form.form = settleForm(nm.form.form, msg)
	}

	if !strings.Contains(plain(nm.form.form.View()), "backups") {
		t.Errorf("the box does not show the landed answer:\n%s", plain(nm.form.form.View()))
	}
}
