package tui

import (
	"testing"
)

// **Tab has to mean one thing on every field: complete if there is something
// to complete, otherwise move on.**
//
// It meant that everywhere except on a completing field, which is the seam
// somebody reports as "tab goes to the next field here and completes there".
// A completing field binds Next to enter alone so a coordinate can be taken a
// segment at a time — right, and unchanged — but at the end of the
// coordinate there is no segment left, and every press refetched the same
// text and stayed. The key that leaves a form was one key on most fields and
// a different one on these.
//
// The fixture's Suggest answers with a fixed list, so the second fetch for
// "backups" brings back nothing that extends it. That is the dead end: the
// press after it must leave.
func TestTabMovesOnOnceCompletionHasNothingLeft(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveModel(t, lr, nil)
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	bucket := c.Inputs[0].Name

	nm = fetchFromCluster(t, nm)                // empty box: fetch
	nm.form.form = typeInto(nm.form.form, "ba") // "backups" now extends it
	next, _ := nm.Update(tabKey)                // …so this tab accepts
	nm = next.(Model)
	if got := *nm.form.bindings[bucket]; got != "backups" {
		t.Fatalf("tab did not accept the offer: %q", got)
	}
	nm = fetchFromCluster(t, nm) // nothing extends "backups": fetch deeper
	if lr.calls() != 2 {
		t.Fatalf("want two fetches by now, got %d", lr.calls())
	}

	// The dead end. Nothing on offer extends the box and the service has
	// already been asked about exactly this text, so there is nothing left
	// for tab to do here.
	before := nm.form.form.GetFocusedField()
	next, cmd := nm.Update(tabKey)
	nm = next.(Model)
	if lr.calls() != 2 {
		t.Errorf("tab asked the service again for text it had already asked about (%d calls)", lr.calls())
	}
	if cmd == nil {
		t.Fatal("tab at the end of completion did nothing at all — the dead key this exists to remove")
	}
	// huh's nextFieldMsg is unexported, so the assertion is on what it does:
	// drive the command back in, which is what the event loop does, and the
	// cursor must have left the field.
	next, _ = nm.Update(cmd())
	nm = next.(Model)
	if nm.form.form.GetFocusedField() == before {
		t.Error("tab at the end of completion stayed in the field — on every other field it advances")
	}
}

// The rhythm the field exists for is unchanged: while a segment is still on
// offer, tab takes it and stays. Only the dead end moves.
func TestTabStillStaysWhileThereIsSomethingToTake(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveModel(t, lr, nil)
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)

	nm = fetchFromCluster(t, nm)
	before := nm.form.form.GetFocusedField()
	nm.form.form = typeInto(nm.form.form, "ba")
	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if nm.form.form.GetFocusedField() != before {
		t.Error("the accept moved off the field — a coordinate is taken a segment at a time")
	}
	if got := *nm.form.bindings[c.Inputs[0].Name]; got != "backups" {
		t.Errorf("the accept did not happen: %q", got)
	}
}

// A service that cannot answer costs one press, not every press. Recording
// the value before the fetch rather than after it is what makes a broken
// completer a thing you tab past instead of a field you cannot leave.
func TestAFailingCompleterDoesNotTrapTheCursor(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveModel(t, lr, nil)
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	// Answer nothing at all, the shape of a completer that is failing or has
	// simply run out.
	nm.form.suggested[c.Inputs[0].Name] = nil
	nm.form.fetchedFor[c.Inputs[0].Name] = ""

	before := nm.form.form.GetFocusedField()
	next, cmd := nm.Update(tabKey)
	nm = next.(Model)
	if cmd == nil {
		t.Fatal("tab did nothing on a field whose completer answered nothing")
	}
	next, _ = nm.Update(cmd())
	nm = next.(Model)
	if nm.form.form.GetFocusedField() == before {
		t.Error("the cursor is stuck on a field whose completer has nothing to say")
	}
	if lr.calls() != 0 {
		t.Errorf("the service was asked again about text it had already been asked about (%d calls)", lr.calls())
	}
}

// The same dead end on the cluster path — the profile editor's coordinate
// field, which completes from kubectl rather than from a plugin.
//
// Two paths reach tab (completeFromCluster for a coordinate or a secret
// reference, completeFromService for a field a plugin marks Live) and both
// have to answer it the same way, or the rule holds on one kind of field and
// not the other, which is the bug this is about. The fake answers a fixed
// list, so once the box holds a context that nothing extends there is
// nothing left to fetch.
func TestTabMovesOnAtTheEndOfACoordinate(t *testing.T) {
	noHistory(t)
	log := clusterFake(t)
	nm := connFormOnKubeField(t)

	nm = fetchFromCluster(t, nm) // empty box: fetch the contexts
	nm.form.form = typeInto(nm.form.form, "homelab/databases/svc/postgres:5432")
	nm = fetchFromCluster(t, nm) // nothing on offer extends that: one fetch
	askedOnce := askedFake(t, log)

	before := nm.form.form.GetFocusedField()
	next, cmd := nm.Update(tabKey)
	nm = next.(Model)
	if askedFake(t, log) != askedOnce {
		t.Error("tab asked the cluster again about text it had already asked about")
	}
	if cmd == nil {
		t.Fatal("tab at the end of a coordinate did nothing")
	}
	next, _ = nm.Update(cmd())
	if next.(Model).form.form.GetFocusedField() == before {
		t.Error("tab at the end of a coordinate stayed — on every other field it advances")
	}
}
