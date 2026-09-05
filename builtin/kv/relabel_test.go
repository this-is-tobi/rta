package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Changing what an entry is *called* must never require fetching and
// re-typing the secret itself: that is the operator handling a credential for
// no reason, and it is what made correcting a description feel dangerous.

func TestDescriptionAndKindChangeWithoutTouchingTheSecret(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{
		"key": "db-password", "value": "hunter2-unmistakable-marker",
		"description": "the staging database",
	}, false)

	body := text(t, runSet, map[string]any{
		"key": "db-password", "description": "the production database", "kind": "private-key",
	}, false)
	if !strings.Contains(body, "relabelled") || !strings.Contains(body, "unchanged") {
		t.Errorf("message = %q, want it to say the entry was relabelled and the secret untouched", body)
	}

	got, err := runGet(context.Background(), req(map[string]any{"key": "db-password"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if v := got.(view.Text).Body; v != "hunter2-unmistakable-marker" {
		t.Fatalf("the secret changed: %q", v)
	}
	tbl := table(t, runList, map[string]any{"detail": true})
	if d := tbl.Rows[0][col(t, tbl, "Description")]; d != "the production database" {
		t.Errorf("description = %q, want the new one", d)
	}
	if k := tbl.Rows[0][col(t, tbl, "Kind")]; k != "private-key" {
		t.Errorf("kind = %q, want the one that was asked for", k)
	}
}

// A relabel is not a rotation. `kv list`'s Updated column is how somebody
// sees that a token has been sitting untouched for fourteen months, and
// correcting its description must not reset that — the same reasoning
// kv.rename already records for a name change.
func TestRelabellingLeavesTheAgeAlone(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v", "description": "before"}, false)
	tbl := table(t, runList, map[string]any{"detail": true})
	updated := tbl.Rows[0][col(t, tbl, "Updated")]

	text(t, runSet, map[string]any{"key": "k", "description": "after"}, false)

	tbl = table(t, runList, map[string]any{"detail": true})
	if got := tbl.Rows[0][col(t, tbl, "Updated")]; got != updated {
		t.Errorf("Updated moved from %q to %q — a relabel is not a rotation", updated, got)
	}
}

// There is nothing to relabel on an entry that does not exist, and inventing
// one holding no secret would be worse than refusing.
func TestRelabellingSomethingAbsentIsRefused(t *testing.T) {
	setup(t)
	_, err := runSet(context.Background(), req(map[string]any{
		"key": "nope", "description": "whatever",
	}, false))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "kv.set.unknown" {
		t.Fatalf("err = %#v, want a coded kv.set.unknown", err)
	}
	if verr.Hint == "" {
		t.Error("the refusal should say how to create the entry instead")
	}
}

// Naming neither a value nor anything to relabel still has to be an error,
// or `kv set k` would quietly store an empty secret.
func TestSetWithNothingAtAllIsStillRefused(t *testing.T) {
	setup(t)
	_, err := runSet(context.Background(), req(map[string]any{"key": "k"}, false))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "kv.set.novalue" {
		t.Fatalf("err = %#v, want a coded kv.set.novalue", err)
	}
}

// **The property the edit form lives or dies on.** Every other Prefill in
// the codebase hands back the record's content; this one must hand back
// labels only, because a value here would be decrypted plaintext sitting in
// a form box — the one thing the kv row actions are built never to do.
func TestThePrefillNeverHandsBackTheSecret(t *testing.T) {
	setup(t)
	const marker = "hunter2-unmistakable-marker"
	text(t, runSet, map[string]any{
		"key": "db-password", "value": marker, "description": "staging", "kind": "private-key",
	}, false)

	got, err := prefillSet(context.Background(), cliReq(map[string]any{
		"key": "db-password", "passphrase": "correct horse battery staple",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := got["value"]; leaked {
		t.Fatal("the prefill returned a value field")
	}
	for field, v := range got {
		if s, ok := v.(string); ok && strings.Contains(s, marker) {
			t.Fatalf("the secret appeared in prefilled field %q", field)
		}
	}
	if got["description"] != "staging" || got["kind"] != "private-key" {
		t.Errorf("prefill = %v, want the current labels", got)
	}
}

// A store that will not open must open the form blank, not refuse to open it
// — the surface treats a Prefill error as fatal, and blocking the form would
// be worse than the blank boxes this replaced.
func TestAnUnreadableStorePrefillsNothingRatherThanFailing(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	got, err := prefillSet(context.Background(), req(map[string]any{
		"key": "k", "passphrase": "not the right passphrase at all",
	}, false))
	if err != nil {
		t.Fatalf("a locked store made the prefill fail (%v), which blocks the form", err)
	}
	if len(got) != 0 {
		t.Errorf("prefill = %v, want nothing at all from a store it could not read", got)
	}
}

// A key being invented has nothing to show, and seeding it with another
// entry's labels would be worse than blank.
func TestPrefillingAnAbsentKeyOffersNothing(t *testing.T) {
	setup(t)
	got, err := prefillSet(context.Background(), cliReq(map[string]any{
		"key": "brand-new", "passphrase": "correct horse battery staple",
	}))
	if err != nil || len(got) != 0 {
		t.Errorf("prefill = %v, %v — want nothing for a key that does not exist", got, err)
	}
}

// A kind the operator pinned by hand must survive `kv edit`. Re-detecting it
// unconditionally threw their answer away on the next edit and replaced it
// with a guess.
func TestAPinnedKindSurvivesAnEdit(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{
		"key": "k", "value": "just some text", "kind": "certificate",
	}, false)
	if got := storedEntry(t, "k").Kind; got != "certificate" {
		t.Fatalf("kind = %q before any edit, want the pinned certificate", got)
	}

	stubEditor(t, replaceWith("still just some text"))
	if _, err := cliEdit(t, "k"); err != nil {
		t.Fatal(err)
	}
	if got := storedEntry(t, "k").Kind; got != "certificate" {
		t.Errorf("kind = %q after an edit, want the pinned certificate to survive", got)
	}
}

// The other half of the same rule: a kind nobody pinned is still re-derived,
// because pasting a certificate over a string really does change what the
// entry is, and a stale label is how you fail to find it.
func TestAnUnpinnedKindIsStillRedetected(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "just some text"}, false)
	if got := storedEntry(t, "k").Kind; got != "string" {
		t.Fatalf("kind = %q, want the detected string", got)
	}

	stubEditor(t, replaceWith(`{"a":1}`))
	if _, err := cliEdit(t, "k"); err != nil {
		t.Fatal(err)
	}
	if got := storedEntry(t, "k").Kind; got != "json" {
		t.Errorf("kind = %q after pasting json, want it re-detected", got)
	}
}
