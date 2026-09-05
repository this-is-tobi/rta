package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func set(t *testing.T, key, value string) {
	t.Helper()
	text(t, runSet, map[string]any{"key": key, "value": value}, false)
}

func get(t *testing.T, key string) string {
	t.Helper()
	return text(t, runGet, map[string]any{"key": key}, false)
}

func historyRows(t *testing.T, key string) [][]string {
	t.Helper()
	return table(t, runHistory, map[string]any{"key": key}).Rows
}

// The whole reason this exists: a paste over the wrong key is undone by
// naming the revision, and the value it replaced is not lost either.
func TestOverwritingAKeyKeepsWhatItReplacedAndRestoreBringsItBack(t *testing.T) {
	setup(t)
	set(t, "db", "first")
	set(t, "db", "second")

	rows := historyRows(t, "db")
	if len(rows) != 2 || rows[0][0] != "current" || rows[1][0] != "1" {
		t.Fatalf("history = %v, want the current value and one revision", rows)
	}
	for _, r := range rows {
		if strings.Contains(strings.Join(r, " "), "first") || strings.Contains(strings.Join(r, " "), "second") {
			t.Fatalf("history shows a value: %v", r)
		}
	}

	msg := text(t, runRestore, map[string]any{"key": "db", "revision": 1}, false)
	if !strings.Contains(msg, "revision 1") {
		t.Errorf("restore said %q", msg)
	}
	if got := get(t, "db"); got != "first" {
		t.Errorf("after restore the value is %q, want the first one back", got)
	}
	// The restore is itself undoable: what it replaced is revision 1 now.
	if rows := historyRows(t, "db"); len(rows) != 3 {
		t.Errorf("history after restore = %v, want current + 2 revisions", rows)
	}
	text(t, runRestore, map[string]any{"key": "db", "revision": 1}, false)
	if got := get(t, "db"); got != "second" {
		t.Errorf("undoing the restore gives %q, want second", got)
	}
}

func TestHistoryIsCappedSoAStoreStaysTheSizeOfItsSecrets(t *testing.T) {
	setup(t)
	for i := 0; i < maxRevisions+3; i++ {
		set(t, "k", strings.Repeat("v", i+1))
	}
	if rows := historyRows(t, "k"); len(rows) != maxRevisions+1 {
		t.Errorf("history holds %d rows, want current + %d", len(rows), maxRevisions)
	}
	// Revision 1 is the most recent replacement, not the oldest survivor.
	text(t, runRestore, map[string]any{"key": "k", "revision": 1}, false)
	if got := get(t, "k"); len(got) != maxRevisions+2 {
		t.Errorf("revision 1 restored %d bytes, want the value set just before the last", len(got))
	}
}

func TestRelabellingDoesNotRetireAValue(t *testing.T) {
	setup(t)
	set(t, "k", "v")
	text(t, runSet, map[string]any{"key": "k", "description": "what it is for"}, false)
	if rows := historyRows(t, "k"); len(rows) != 1 {
		t.Errorf("a description change made a revision: %v", rows)
	}
}

func TestRemoveKeepsTheEntryAsideUntilRestoredOrPurged(t *testing.T) {
	setup(t)
	set(t, "k", "one")
	set(t, "k", "two")
	msg := text(t, runRemove, map[string]any{"key": "k"}, false)
	if !strings.Contains(msg, "kv restore k") {
		t.Errorf("rm said %q, and does not say how to undo", msg)
	}
	if _, err := runGet(context.Background(), req(map[string]any{"key": "k"}, false)); err == nil {
		t.Fatal("a removed key still reads")
	}
	if v, err := runList(context.Background(), req(nil, false)); err != nil || view.TypeOf(v) != "text" {
		t.Errorf("a removed key still lists: %v %v", v, err)
	}
	removed := table(t, runList, map[string]any{"removed": true})
	if len(removed.Rows) != 1 || removed.Rows[0][0] != "k" {
		t.Errorf("list --removed = %v", removed.Rows)
	}

	text(t, runRestore, map[string]any{"key": "k"}, false)
	if got := get(t, "k"); got != "two" {
		t.Errorf("restored value = %q", got)
	}
	if rows := historyRows(t, "k"); len(rows) != 2 {
		t.Errorf("the restored key lost its history: %v", rows)
	}

	text(t, runRemove, map[string]any{"key": "k", "purge": true}, false)
	if _, err := runRestore(context.Background(), req(map[string]any{"key": "k"}, false)); err == nil {
		t.Fatal("a purged key was restored")
	}
	if v, _ := runList(context.Background(), req(map[string]any{"removed": true}, false)); view.TypeOf(v) != "text" {
		t.Errorf("a purged key is still listed as removed: %v", v)
	}
}

func TestPurgeFinishesOffAKeyRemovedEarlier(t *testing.T) {
	setup(t)
	set(t, "k", "v")
	text(t, runRemove, map[string]any{"key": "k"}, false)
	msg := text(t, runRemove, map[string]any{"key": "k", "purge": true}, false)
	if !strings.Contains(msg, "purged") {
		t.Errorf("purging a removed key said %q", msg)
	}
	if _, err := runRestore(context.Background(), req(map[string]any{"key": "k"}, false)); err == nil {
		t.Fatal("restored after purge")
	}
}

func TestARemovedKeyWhoseNameWasReusedIsNotMergedBack(t *testing.T) {
	setup(t)
	set(t, "k", "old")
	text(t, runRemove, map[string]any{"key": "k"}, false)
	set(t, "k", "new")
	_, err := runRestore(context.Background(), req(map[string]any{"key": "k"}, false))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "kv.restore.taken" {
		t.Fatalf("err = %v, want kv.restore.taken", err)
	}
	if got := get(t, "k"); got != "new" {
		t.Errorf("the live value was touched: %q", got)
	}
}

func TestRestoreRefusesARevisionThatDoesNotExist(t *testing.T) {
	setup(t)
	set(t, "k", "v")
	_, err := runRestore(context.Background(), req(map[string]any{"key": "k", "revision": 3}, false))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "kv.restore.norevision" {
		t.Fatalf("err = %v, want kv.restore.norevision", err)
	}
}

func TestDryRunsOfRestoreAndRemoveChangeNothing(t *testing.T) {
	setup(t)
	set(t, "k", "one")
	set(t, "k", "two")
	text(t, runRestore, map[string]any{"key": "k", "revision": 1}, true)
	if got := get(t, "k"); got != "two" {
		t.Errorf("a dry-run restore changed the value to %q", got)
	}
	text(t, runRemove, map[string]any{"key": "k", "purge": true}, true)
	if got := get(t, "k"); got != "two" {
		t.Errorf("a dry-run purge removed the key")
	}
}

func TestTheTreeGroupsKeysByTheFoldersTheyShare(t *testing.T) {
	setup(t)
	set(t, "staging/db/password", "a")
	set(t, "staging/api-token", "b")
	set(t, "prod/db/password", "c")
	set(t, "loose", "d")
	v, err := runTree(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	tree := v.(view.Tree)
	labels := make([]string, 0, len(tree.Roots))
	for _, n := range tree.Roots {
		labels = append(labels, n.Label)
	}
	if strings.Join(labels, " ") != "loose prod/ staging/" {
		t.Errorf("roots = %v", labels)
	}
	staging := tree.Roots[2]
	if staging.Detail != "2 keys" || len(staging.Children) != 2 {
		t.Errorf("staging/ = %+v", staging)
	}
	if db := staging.Children[1]; db.Label != "db/" || db.Children[0].Label != "password" || db.Children[0].Detail == "" {
		t.Errorf("staging/db/ = %+v", db)
	}
	for _, n := range tree.Roots {
		if strings.Contains(n.Label+n.Detail, "a") && n.Label == "loose" && n.Detail == "a" {
			t.Fatal("the tree shows a value")
		}
	}
}
