package note

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// setup points the store at a fresh temp dir.
func setup(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func req(values map[string]any, dryRun bool) plugin.Request {
	return plugin.NewRequest(values, dryRun, false)
}

// text runs a handler and asserts it returned a Text view.
func text(t *testing.T, h plugin.Handler, values map[string]any, dryRun bool) string {
	t.Helper()
	v, err := h(context.Background(), req(values, dryRun))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	return txt.Body
}

// table runs a handler and asserts it returned a Table view.
func table(t *testing.T, h plugin.Handler, values map[string]any) view.Table {
	t.Helper()
	v, err := h(context.Background(), req(values, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}
	return tbl
}

// show runs runShow and asserts it returned the composed page.
func show(t *testing.T, id int) view.Sections {
	t.Helper()
	v, err := runShow(context.Background(), req(map[string]any{"id": id}, false))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	return s
}

// section resolves one section of a page by title.
func section(t *testing.T, s view.Sections, title string) view.View {
	t.Helper()
	var seen []string
	for _, it := range s.Items {
		if it.Title == title {
			return it.View
		}
		seen = append(seen, it.Title)
	}
	t.Fatalf("section %q not found, have %v", title, seen)
	return nil
}

// hasSection reports whether a page carries the given section.
func hasSection(s view.Sections, title string) bool {
	for _, it := range s.Items {
		if it.Title == title {
			return true
		}
	}
	return false
}

// pair resolves one metadata value by key.
func pair(t *testing.T, v view.View, key string) string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("key %q not found in %v", key, kv.Pairs)
	return ""
}

// col resolves a column name to its index, so tests survive column reordering.
func col(t *testing.T, tbl view.Table, name string) int {
	t.Helper()
	for i, c := range tbl.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("column %q not found in %v", name, tbl.Columns)
	return -1
}

func addTodo(t *testing.T, title string) {
	t.Helper()
	text(t, runAdd, map[string]any{"title": title, "todo": true}, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyClasses(t *testing.T) {
	want := map[string]plugin.Safety{
		"note.list":   plugin.Read,
		"note.show":   plugin.Read,
		"note.search": plugin.Read,
		"note.tags":   plugin.Read,
		"note.add":    plugin.Write,
		"note.edit":   plugin.Write,
		"note.toggle": plugin.Write,
		"note.done":   plugin.Write,
		"note.reopen": plugin.Write,
		"note.rm":     plugin.Destructive,
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		w, ok := want[c.ID]
		if !ok {
			t.Errorf("unexpected capability %s", c.ID)
			continue
		}
		seen[c.ID] = true
		if c.Safety != w {
			t.Errorf("%s safety = %s, want %s", c.ID, c.Safety, w)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("capability %s missing", id)
		}
	}
}

func TestAddListDoneRemoveCycle(t *testing.T) {
	setup(t)

	// Empty list greets, not errors.
	if body := text(t, runList, map[string]any{}, false); !strings.Contains(body, "Nothing here yet") {
		t.Errorf("empty list = %q", body)
	}

	addTodo(t, "write docs")
	if body := text(t, runAdd, map[string]any{"title": "ship it", "todo": true}, false); !strings.Contains(body, "added note 2") {
		t.Errorf("second add = %q", body)
	}

	tbl := table(t, runList, map[string]any{"all": false})
	statusCol, noteCol := col(t, tbl, "Status"), col(t, tbl, "Note")
	if len(tbl.Rows) != 2 || tbl.Rows[0][statusCol] != "open" {
		t.Fatalf("list rows = %v", tbl.Rows)
	}

	// Done hides from the default list, shows with --all.
	text(t, runDone, map[string]any{"id": 1}, false)
	tbl = table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 || tbl.Rows[0][noteCol] != "ship it" {
		t.Fatalf("after done, rows = %v", tbl.Rows)
	}
	tbl = table(t, runList, map[string]any{"all": true})
	if len(tbl.Rows) != 2 || tbl.Rows[0][statusCol] != "done" {
		t.Fatalf("with --all, rows = %v", tbl.Rows)
	}

	text(t, runRemove, map[string]any{"id": 2}, false)
	if tbl = table(t, runList, map[string]any{"all": true}); len(tbl.Rows) != 1 {
		t.Fatalf("after rm, rows = %v", tbl.Rows)
	}

	// IDs never recycle.
	if body := text(t, runAdd, map[string]any{"title": "new"}, false); !strings.Contains(body, "added note 3") {
		t.Errorf("id recycled: %q", body)
	}
}

// A plain note is what you get by default, it is never hidden, and it has
// nothing to be done about: `done` refuses and says what would make it a
// to-do instead.
func TestAPlainNoteIsNotAToDo(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "how the cluster is wired"}, false)

	tbl := table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Status")]; got != "note" {
		t.Errorf("status = %q, want note", got)
	}
	_, err := runDone(context.Background(), req(map[string]any{"id": 1}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.done.notatodo" || !strings.Contains(ve.Hint, "note toggle 1") {
		t.Errorf("done on a note = %+v", ve)
	}
	if got := pair(t, section(t, show(t, 1), "note"), "status"); got != "note" {
		t.Errorf("page status = %q", got)
	}
}

// The switch between the two kinds: a note becomes an open to-do, a to-do
// becomes a note and forgets it was done.
func TestToggleSwitchesNoteAndToDo(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)

	if body := text(t, runToggle, map[string]any{"id": 1}, false); !strings.Contains(body, "now a to-do") {
		t.Errorf("toggle = %q", body)
	}
	text(t, runDone, map[string]any{"id": 1}, false)
	if s, _ := load(); !s.Items[0].Todo || !s.Items[0].Done {
		t.Fatalf("after toggle+done: %+v", s.Items[0])
	}

	if body := text(t, runToggle, map[string]any{"id": 1}, false); !strings.Contains(body, "now a note") {
		t.Errorf("toggle back = %q", body)
	}
	s, _ := load()
	if s.Items[0].Todo || s.Items[0].Done || s.Items[0].DoneAt != nil {
		t.Errorf("a note still carries completion: %+v", s.Items[0])
	}
	// Back on the default list, since notes are never hidden.
	if tbl := table(t, runList, map[string]any{"all": false}); len(tbl.Rows) != 1 {
		t.Errorf("toggled-back note hidden: %v", tbl.Rows)
	}
}

func TestToggleDryRunChangesNothing(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
	if body := text(t, runToggle, map[string]any{"id": 1}, true); !strings.Contains(body, "would make note 1 a to-do") {
		t.Errorf("dry-run toggle = %q", body)
	}
	if s, _ := load(); s.Items[0].Todo {
		t.Error("a dry run toggled the note")
	}
}

// Nobody gives a date to something that cannot be done.
func TestADueDateImpliesAToDo(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "renew cert", "due": "2099-01-01"}, false)
	tbl := table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Status")]; got != "open" {
		t.Errorf("status = %q, want open", got)
	}
}

// What has a deadline comes first, soonest on top; the rest stays in the
// order it was written.
func TestListPutsDeadlinesFirst(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "a note"}, false)
	text(t, runAdd, map[string]any{"title": "later", "due": "2099-02-01"}, false)
	text(t, runAdd, map[string]any{"title": "another note"}, false)
	text(t, runAdd, map[string]any{"title": "sooner", "due": "2099-01-01"}, false)

	tbl := table(t, runList, map[string]any{"all": false})
	var titles []string
	for _, r := range tbl.Rows {
		titles = append(titles, r[col(t, tbl, "Note")])
	}
	want := []string{"sooner", "later", "a note", "another note"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v, want %v", titles, want)
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "real"}, false)

	if body := text(t, runAdd, map[string]any{"title": "phantom"}, true); !strings.Contains(body, "would add") {
		t.Errorf("dry-run add = %q", body)
	}
	if body := text(t, runRemove, map[string]any{"id": 1}, true); !strings.Contains(body, "would remove") {
		t.Errorf("dry-run rm = %q", body)
	}
	tbl := table(t, runList, map[string]any{"all": true})
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Note")] != "real" {
		t.Fatalf("dry-run mutated the store: %v", tbl.Rows)
	}
}

// The response text alone cannot tell "already done, left alone" apart from
// "marked done again": runDone's message is the title either way. Checked
// against store state instead — DoneAt unchanged across the two calls.
func TestDoneIsIdempotent(t *testing.T) {
	setup(t)
	addTodo(t, "x")
	text(t, runDone, map[string]any{"id": 1}, false)
	s, err := load()
	if err != nil {
		t.Fatal(err)
	}
	firstDoneAt := s.Items[0].DoneAt
	if firstDoneAt == nil {
		t.Fatal("the first done call did not set DoneAt")
	}

	text(t, runDone, map[string]any{"id": 1}, false)
	s, err = load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Items[0].DoneAt == nil || !s.Items[0].DoneAt.Equal(*firstDoneAt) {
		t.Errorf("DoneAt moved on a second done call: %v -> %v", firstDoneAt, s.Items[0].DoneAt)
	}
}

func TestNotFoundIsCodedWithHint(t *testing.T) {
	setup(t)
	_, err := runDone(context.Background(), req(map[string]any{"id": 99}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.notfound" || ve.Hint == "" {
		t.Errorf("want note.notfound with hint, got %+v", ve)
	}
}

func TestCorruptStoreIsCoded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	if err := os.WriteFile(dir+"/notes.json", []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runList(context.Background(), req(map[string]any{}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.store.corrupt" || ve.Hint == "" {
		t.Errorf("want note.store.corrupt with hint, got %+v", ve)
	}
}

func TestEditUpdatesTitleAndBody(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "draft"}, false)

	if body := text(t, runEdit, map[string]any{"id": 1, "title": "final", "body": "- [ ] step one"}, false); !strings.Contains(body, "updated note 1: final") {
		t.Errorf("edit = %q", body)
	}
	// List shows the new title with the body marker.
	tbl := table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Note")]; got != "final ≡" {
		t.Errorf("list cell = %q", got)
	}
	// Partial edit keeps the other field.
	text(t, runEdit, map[string]any{"id": 1, "title": "renamed"}, false)
	page := show(t, 1)
	if got := pair(t, section(t, page, "note"), "title"); got != "renamed" {
		t.Errorf("title = %q", got)
	}
	content := section(t, page, "content").(view.Text)
	if !content.Markdown {
		t.Error("content must render as markdown")
	}
	if !strings.Contains(content.Body, "- [ ] step one") {
		t.Errorf("content = %q", content.Body)
	}
}

// TestShowSeparatesMetadataFromBody pins the split that makes the page
// readable: bookkeeping lives in structured pairs, prose lives alone.
func TestShowSeparatesMetadataFromBody(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{
		"title": "ship it", "body": "the actual prose",
		"tag": []string{"release"}, "due": "2030-01-02",
	}, false)

	page := show(t, 1)
	meta := section(t, page, "note")
	if got := pair(t, meta, "status"); got != "open" {
		t.Errorf("status = %q", got)
	}
	if got := pair(t, meta, "due"); !strings.Contains(got, "2030-01-02") {
		t.Errorf("due = %q", got)
	}
	if got := pair(t, meta, "tags"); got != "#release" {
		t.Errorf("tags = %q", got)
	}
	if got := pair(t, meta, "words"); got != "3" {
		t.Errorf("words = %q", got)
	}
	// The prose section carries only the prose.
	if got := section(t, page, "content").(view.Text).Body; got != "the actual prose" {
		t.Errorf("content leaked metadata: %q", got)
	}
}

// An empty body must still say something useful — a page dedicated to one
// note should never render a blank band.
func TestShowEmptyBodyExplainsItself(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "bare"}, false)
	content := section(t, show(t, 1), "content").(view.Text)
	if !strings.Contains(content.Body, "This note is empty") || !strings.Contains(content.Body, "note edit 1") {
		t.Errorf("empty content = %q", content.Body)
	}
	if content.Markdown {
		t.Error("the placeholder is not the user's markdown")
	}
}

func TestEditNothingToChangeIsCoded(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
	_, err := runEdit(context.Background(), req(map[string]any{"id": 1, "parent": noParentChange}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.edit.nochange" || ve.Hint == "" {
		t.Errorf("want note.edit.nochange with hint, got %+v", ve)
	}
}

func TestEditDryRunTouchesNothing(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "keep"}, false)
	if body := text(t, runEdit, map[string]any{"id": 1, "title": "phantom", "parent": noParentChange}, true); !strings.Contains(body, "would update") {
		t.Errorf("dry-run edit = %q", body)
	}
	if tbl := table(t, runList, map[string]any{"all": false}); tbl.Rows[0][col(t, tbl, "Note")] != "keep" {
		t.Errorf("dry-run edit mutated the store: %v", tbl.Rows)
	}
}

// TestLegacyStoreMigrates loads a pre-body store (title under "text") and
// asserts it reads correctly and persists in the new shape on first write.
func TestLegacyStoreMigrates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	legacy := `{"nextId":2,"items":[{"id":1,"text":"old style","created":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(dir+"/notes.json", []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Note")] != "old style" {
		t.Fatalf("legacy store misread: %v", tbl.Rows)
	}

	// Any write persists the migrated shape: "text" is gone, "title" is there.
	text(t, runEdit, map[string]any{"id": 1, "body": "details", "parent": noParentChange}, false)
	raw, err := os.ReadFile(dir + "/notes.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"text"`) || !strings.Contains(string(raw), `"title": "old style"`) {
		t.Errorf("store not migrated: %s", raw)
	}
}

// Interactive edit forms open with the note's current title and body.
func TestPrefillEditReturnsCurrentContent(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "current", "body": "old body"}, false)
	got, err := prefillEdit(context.Background(), req(map[string]any{"id": 1}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got["title"] != "current" || got["body"] != "old body" {
		t.Errorf("prefill = %v", got)
	}
	if _, err := prefillEdit(context.Background(), req(map[string]any{"id": 42}, false)); err == nil {
		t.Error("prefill of unknown id must fail")
	}
}

// --- Tags -------------------------------------------------------------

func TestTagsCaptureFilterAndList(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "server work", "tag": []string{"Backend", "urgent"}}, false)
	text(t, runAdd, map[string]any{"title": "css tweak", "tag": []string{"frontend"}}, false)
	text(t, runAdd, map[string]any{"title": "untagged"}, false)

	// Tags normalize to lowercase, filter is case-insensitive and #-agnostic.
	tbl := table(t, runList, map[string]any{"tag": []string{"#BACKEND"}})
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Note")] != "server work" {
		t.Fatalf("tag filter = %v", tbl.Rows)
	}

	v, err := runTags(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	tags := v.(view.Table)
	counts := map[string]string{}
	for _, r := range tags.Rows {
		counts[r[0]] = r[1]
	}
	if counts["backend"] != "1" || counts["frontend"] != "1" || counts["urgent"] != "1" {
		t.Errorf("tag counts = %v", counts)
	}
}

func TestTagClearAndReplace(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x", "tag": []string{"a", "b"}}, false)

	text(t, runEdit, map[string]any{"id": 1, "tag": []string{"c"}, "parent": noParentChange}, false)
	got, _ := prefillEdit(context.Background(), req(map[string]any{"id": 1}, false))
	if tags := got["tag"].([]string); len(tags) != 1 || tags[0] != "c" {
		t.Fatalf("replace = %v", got["tag"])
	}

	text(t, runEdit, map[string]any{"id": 1, "tag": []string{"-"}, "parent": noParentChange}, false)
	got, _ = prefillEdit(context.Background(), req(map[string]any{"id": 1}, false))
	if tags, _ := got["tag"].([]string); len(tags) != 0 {
		t.Fatalf("clear = %v", got["tag"])
	}
}

// --- Due dates ----------------------------------------------------------

func TestDueDateCaptureAndStatus(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x", "due": "2020-01-01"}, false)

	tbl := table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Due")]; got != "OVERDUE" {
		t.Errorf("overdue due status = %q", got)
	}
}

func TestDueBadInputIsCoded(t *testing.T) {
	setup(t)
	_, err := runAdd(context.Background(), req(map[string]any{"title": "x", "due": "not-a-date"}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.add.baddue" {
		t.Errorf("want note.add.baddue, got %+v", ve)
	}
}

func TestDueClearedWithNone(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x", "due": "2099-01-01"}, false)
	text(t, runEdit, map[string]any{"id": 1, "due": "none", "parent": noParentChange}, false)
	tbl := table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Due")]; got != "" {
		t.Errorf("due not cleared: %q", got)
	}
}

// --- Sub-notes ------------------------------------------------------------

func TestSubNotesProgressAndListing(t *testing.T) {
	setup(t)
	addTodo(t, "epic")
	text(t, runAdd, map[string]any{"title": "step one", "parent": 1, "todo": true}, false)
	text(t, runAdd, map[string]any{"title": "step two", "parent": 1, "todo": true}, false)

	// Top-level list shows only the parent, with a progress suffix.
	tbl := table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 {
		t.Fatalf("top-level list should hide sub-notes: %v", tbl.Rows)
	}
	if got := tbl.Rows[0][col(t, tbl, "Note")]; got != "epic (0/2)" {
		t.Errorf("progress suffix = %q", got)
	}

	// --parent lists just the children.
	children := table(t, runList, map[string]any{"parent": 1})
	if len(children.Rows) != 2 {
		t.Fatalf("children list = %v", children.Rows)
	}

	text(t, runDone, map[string]any{"id": 2}, false)
	tbl = table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Note")]; got != "epic (1/2)" {
		t.Errorf("progress after done = %q", got)
	}
}

func TestAddBadParentIsCoded(t *testing.T) {
	setup(t)
	_, err := runAdd(context.Background(), req(map[string]any{"title": "x", "parent": 99}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.add.badparent" {
		t.Errorf("want note.add.badparent, got %+v", ve)
	}
}

func TestEditSelfParentIsCoded(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
	_, err := runEdit(context.Background(), req(map[string]any{"id": 1, "parent": 1}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.edit.selfparent" {
		t.Errorf("want note.edit.selfparent, got %+v", ve)
	}
}

// Removing a note with sub-notes moves them up rather than orphaning or
// deleting them.
func TestRemoveReparentsChildren(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "epic"}, false)
	text(t, runAdd, map[string]any{"title": "child", "parent": 1}, false)

	body := text(t, runRemove, map[string]any{"id": 1}, false)
	if !strings.Contains(body, "1 sub-note(s) moved up") {
		t.Errorf("remove message = %q", body)
	}
	tbl := table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Note")] != "child" {
		t.Fatalf("child not reparented: %v", tbl.Rows)
	}
}

// --- Show: cross-references and sub-note rendering -----------------------

func TestShowRendersSubNotesAndReferences(t *testing.T) {
	setup(t)
	addTodo(t, "epic")
	text(t, runAdd, map[string]any{"title": "child", "parent": 1, "todo": true}, false)
	text(t, runDone, map[string]any{"id": 2}, false)
	text(t, runAdd, map[string]any{"title": "related", "body": "see #1 for context"}, false)

	page := show(t, 1)
	if got := pair(t, section(t, page, "note"), "sub-notes"); got != "1 of 1 done" {
		t.Errorf("progress = %q", got)
	}
	subs := section(t, page, "sub-notes").(view.Table)
	if len(subs.Rows) != 1 || subs.Rows[0][0] != "2" || subs.Rows[0][1] != "done" {
		t.Errorf("sub-note rows = %v", subs.Rows)
	}
	refs := section(t, page, "references").(view.Table)
	if len(refs.Rows) != 1 || refs.Rows[0][0] != "← mentioned by" || refs.Rows[0][1] != "3" {
		t.Errorf("back-reference = %v", refs.Rows)
	}

	fwd := section(t, show(t, 3), "references").(view.Table)
	if len(fwd.Rows) != 1 || fwd.Rows[0][0] != "→ mentions" || fwd.Rows[0][2] != "epic" {
		t.Errorf("forward reference = %v", fwd.Rows)
	}
	// A note with neither relationship gets neither section — no empty bands.
	text(t, runAdd, map[string]any{"title": "lonely"}, false)
	if lonely := show(t, 4); hasSection(lonely, "sub-notes") || hasSection(lonely, "references") {
		t.Error("unrelated note should carry no sub-note or reference section")
	}
}

// --- Search ---------------------------------------------------------------

func TestSearchMatchesTitleBodyAndTag(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "fix login bug", "tag": []string{"backend"}}, false)
	text(t, runAdd, map[string]any{"title": "unrelated", "body": "mentions login flow"}, false)
	text(t, runAdd, map[string]any{"title": "css"}, false)

	v, err := runSearch(context.Background(), req(map[string]any{"query": "login"}, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 2 {
		t.Fatalf("search rows = %v", tbl.Rows)
	}

	v, err = runSearch(context.Background(), req(map[string]any{"query": "backend"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(view.Table).Rows; len(got) != 1 {
		t.Fatalf("tag search = %v", got)
	}
}

func TestSearchNoMatchIsFriendly(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
	v, err := runSearch(context.Background(), req(map[string]any{"query": "nope"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.(view.Text).Body, `No notes match "nope"`) {
		t.Errorf("no-match body = %v", v)
	}
}

func TestSearchMarksSubNotes(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "parent findme"}, false)
	text(t, runAdd, map[string]any{"title": "child findme", "parent": 1}, false)

	v, err := runSearch(context.Background(), req(map[string]any{"query": "findme"}, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	var sawSub bool
	for _, r := range tbl.Rows {
		if strings.HasPrefix(r[2], "↳ ") {
			sawSub = true
		}
	}
	if !sawSub || len(tbl.Rows) != 2 {
		t.Fatalf("search rows = %v", tbl.Rows)
	}
}

// A note whose body mentions its own number is writing prose, not linking to
// itself — "see #3" inside note 3 must not produce a reference to note 3.
func TestShowIgnoresSelfReferences(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "self", "body": "tracked as #1, blocked by #2"}, false)
	text(t, runAdd, map[string]any{"title": "other"}, false)

	refs := section(t, show(t, 1), "references").(view.Table)
	for _, row := range refs.Rows {
		if row[1] == "1" && row[0] == "→ mentions" {
			t.Errorf("note links to itself: %v", refs.Rows)
		}
	}
	if len(refs.Rows) != 1 || refs.Rows[0][1] != "2" {
		t.Errorf("references = %v, want only the real link", refs.Rows)
	}
}

// A list you cannot take something back out of is a list people stop
// trusting: `done` needs an undo, and it has to be safe to press twice.
func TestReopenUndoesDone(t *testing.T) {
	setup(t)
	addTodo(t, "ship it")
	text(t, runDone, map[string]any{"id": 1}, false)

	body := text(t, runReopen, map[string]any{"id": 1}, false)
	if !strings.Contains(body, "re-opened") {
		t.Errorf("reopen said %q", body)
	}
	s, _ := load()
	if s.Items[0].Done || s.Items[0].DoneAt != nil {
		t.Errorf("note still carries completion: %+v", s.Items[0])
	}
	// Re-opening an open note is a no-op, not an error.
	if _, err := runReopen(context.Background(), req(map[string]any{"id": 1}, false)); err != nil {
		t.Errorf("second reopen: %v", err)
	}
	// …and an unknown id is still an error.
	if _, err := runReopen(context.Background(), req(map[string]any{"id": 99}, false)); err == nil {
		t.Error("reopening a note that does not exist was accepted")
	}
}

func TestReopenDryRunChangesNothing(t *testing.T) {
	setup(t)
	addTodo(t, "ship it")
	text(t, runDone, map[string]any{"id": 1}, false)
	text(t, runReopen, map[string]any{"id": 1}, true)
	if s, _ := load(); !s.Items[0].Done {
		t.Error("a dry run re-opened the note")
	}
}

// The MCP bridge dispatches every tools/call in its own goroutine, so two
// pipelined `note_add` calls racing each other is the ordinary case, not an
// exotic one. Before the load-decide-save cycle was locked, both could read
// the same NextID, both append their own item, and the second Save simply
// overwrite the first — the loser's note silently gone despite being told
// it was added.
func TestConcurrentAddsDoNotLoseAWrite(t *testing.T) {
	setup(t)
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := runAdd(context.Background(),
				req(map[string]any{"title": fmt.Sprintf("note %d", i)}, false)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	s, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != n {
		t.Fatalf("%d items landed, want %d — a concurrent add lost a write", len(s.Items), n)
	}
	seen := map[int]bool{}
	for _, item := range s.Items {
		if seen[item.ID] {
			t.Fatalf("two items share id %d: %+v", item.ID, s.Items)
		}
		seen[item.ID] = true
	}
}
