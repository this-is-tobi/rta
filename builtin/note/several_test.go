package note

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func ids(values ...string) map[string]any { return map[string]any{"id": values} }

func addNotes(t *testing.T, titles ...string) {
	t.Helper()
	for _, title := range titles {
		text(t, runAdd, map[string]any{"title": title, "todo": true}, false)
	}
}

func openIDs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, row := range table(t, runList, map[string]any{"all": false}).Rows {
		out = append(out, row[0])
	}
	return out
}

// done takes as many ids as it is given, and the answer says what happened to
// each, one line apiece in the order they were named.
func TestDoneTakesSeveralIDs(t *testing.T) {
	setup(t)
	addNotes(t, "a", "b", "c")

	if got := text(t, runDone, ids("1", "3"), false); got != "done: a\ndone: c" {
		t.Errorf("done 1 3 = %q", got)
	}
	if got := strings.Join(openIDs(t), " "); got != "2" {
		t.Errorf("open after done 1 3 = %q, want only 2", got)
	}
	if got := text(t, runReopen, ids("1", "3"), false); got != "re-opened: a\nre-opened: c" {
		t.Errorf("reopen 1 3 = %q", got)
	}
	if got := strings.Join(openIDs(t), " "); got != "1 2 3" {
		t.Errorf("open after reopen = %q", got)
	}
}

// A call over several notes is all or none: one id that is not a note refuses
// it before anything changes, so a typo in the last id does not leave the first
// ones checked off.
func TestSeveralIDsAreAllOrNone(t *testing.T) {
	setup(t)
	addNotes(t, "a", "b")

	for name, run := range map[string]plugin.Handler{"done": runDone, "reopen": runReopen, "rm": runRemove} {
		_, err := run(context.Background(), req(ids("1", "2", "9"), false))
		if ve := view.AsError(err, "x"); ve.Code != "note.notfound" || !strings.Contains(ve.Message, "9") {
			t.Errorf("%s 1 2 9 = %+v, want note.notfound for 9", name, ve)
		}
	}
	if s, _ := load(); len(s.Items) != 2 || s.Items[0].Done || s.Items[1].Done {
		t.Errorf("a refused call changed the notebook: %+v", s.Items)
	}
}

// Ids are whole numbers as strconv writes them. A spelling the grant gate judges
// as a record of its own ("#3", " 3", "03") is refused rather than read as note
// 3, so the note a grant covers is the note acted on; a repeat is one note.
func TestIDsAreWrittenAsNumbersOrRefused(t *testing.T) {
	setup(t)
	addNotes(t, "a", "b")

	for _, bad := range []string{"#1", " 1", "1 ", "+1", "-1", "01", "007", "0", "one", "1.5", "", "0x1"} {
		_, err := runDone(context.Background(), req(ids(bad), false))
		if ve := view.AsError(err, "x"); ve.Code != "note.id.invalid" || ve.Hint == "" {
			t.Errorf("id %q = %+v, want note.id.invalid with a hint", bad, ve)
		}
	}
	_, err := runDone(context.Background(), req(map[string]any{}, false))
	if ve := view.AsError(err, "x"); ve.Code != "note.id.none" {
		t.Errorf("no id = %+v, want note.id.none", ve)
	}
	if got := text(t, runDone, ids("2", "2"), false); got != "done: b" {
		t.Errorf("done 2 2 = %q, want one line for one note", got)
	}
}

// A dry run says what each note would get, and changes none.
func TestSeveralIDsDryRun(t *testing.T) {
	setup(t)
	addNotes(t, "a", "b")

	if got := text(t, runRemove, ids("1", "2"), true); got != "would remove note 1: a\nwould remove note 2: b" {
		t.Errorf("rm --dry-run = %q", got)
	}
	if got := text(t, runDone, ids("1", "2"), true); got != "would check off note 1: a\nwould check off note 2: b" {
		t.Errorf("done --dry-run = %q", got)
	}
	if s, _ := load(); len(s.Items) != 2 || s.Items[0].Done {
		t.Errorf("a dry run changed the notebook: %+v", s.Items)
	}
}

// rm of a note and its sub-note in one call lands the same whichever is named
// first: the sub-note is handed up before it goes, or goes before it is.
func TestRemoveSeveralIncludingAParentAndItsChild(t *testing.T) {
	for name, order := range map[string][]string{"parent first": {"1", "2"}, "child first": {"2", "1"}} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			text(t, runAdd, map[string]any{"title": "parent"}, false)
			text(t, runAdd, map[string]any{"title": "child", "parent": 1}, false)
			text(t, runAdd, map[string]any{"title": "grandchild", "parent": 2}, false)
			text(t, runAdd, map[string]any{"title": "other"}, false)

			text(t, runRemove, ids(order...), false)
			s, _ := load()
			if len(s.Items) != 2 || s.Items[0].Title != "grandchild" || s.Items[1].Title != "other" {
				t.Fatalf("after rm: %+v", s.Items)
			}
			if s.Items[0].Parent != 0 {
				t.Errorf("the grandchild was left under a note that is gone: parent %d", s.Items[0].Parent)
			}
		})
	}
}

// One note answers as it always did: the same line, nothing added for a list of
// one.
func TestOneIDAnswersAsItAlwaysDid(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "parent"}, false)
	text(t, runAdd, map[string]any{"title": "child", "parent": 1}, false)

	if got := text(t, runRemove, ids("1"), false); got != "removed note 1: parent (1 sub-note moved up)" {
		t.Errorf("rm 1 = %q", got)
	}
}
