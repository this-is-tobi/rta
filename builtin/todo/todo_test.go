package todo

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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

// show runs runShow and asserts it returned the composed task page.
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

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyClasses(t *testing.T) {
	want := map[string]plugin.Safety{
		"todo.list":   plugin.Read,
		"todo.show":   plugin.Read,
		"todo.search": plugin.Read,
		"todo.tags":   plugin.Read,
		"todo.add":    plugin.Write,
		"todo.edit":   plugin.Write,
		"todo.done":   plugin.Write,
		"todo.reopen": plugin.Write,
		"todo.rm":     plugin.Destructive,
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
	if body := text(t, runList, map[string]any{}, false); !strings.Contains(body, "Nothing to do") {
		t.Errorf("empty list = %q", body)
	}

	// Add two.
	text(t, runAdd, map[string]any{"title": "write docs"}, false)
	if body := text(t, runAdd, map[string]any{"title": "ship it"}, false); !strings.Contains(body, "added task 2") {
		t.Errorf("second add = %q", body)
	}

	// List shows both, open.
	tbl := table(t, runList, map[string]any{"all": false})
	statusCol, taskCol := col(t, tbl, "Status"), col(t, tbl, "Task")
	if len(tbl.Rows) != 2 || tbl.Rows[0][statusCol] != "open" {
		t.Fatalf("list rows = %v", tbl.Rows)
	}

	// Done hides from default list, shows with --all.
	text(t, runDone, map[string]any{"id": 1}, false)
	tbl = table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 || tbl.Rows[0][taskCol] != "ship it" {
		t.Fatalf("after done, rows = %v", tbl.Rows)
	}
	tbl = table(t, runList, map[string]any{"all": true})
	if len(tbl.Rows) != 2 || tbl.Rows[0][statusCol] != "done" {
		t.Fatalf("with --all, rows = %v", tbl.Rows)
	}

	// Remove.
	text(t, runRemove, map[string]any{"id": 2}, false)
	if tbl = table(t, runList, map[string]any{"all": true}); len(tbl.Rows) != 1 {
		t.Fatalf("after rm, rows = %v", tbl.Rows)
	}

	// IDs never recycle.
	if body := text(t, runAdd, map[string]any{"title": "new"}, false); !strings.Contains(body, "added task 3") {
		t.Errorf("id recycled: %q", body)
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
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Task")] != "real" {
		t.Fatalf("dry-run mutated the store: %v", tbl.Rows)
	}
}

// The response text alone cannot tell "already done, left alone" apart from
// "marked done again": runDone's message is the task's title either way, so
// it says nothing about whether the guard actually fired. Checked against
// store state instead — DoneAt unchanged across the two calls — the same
// way TestReopenUndoesDone already checks state rather than response text.
func TestDoneIsIdempotent(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
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
	if ve.Code != "todo.notfound" || ve.Hint == "" {
		t.Errorf("want todo.notfound with hint, got %+v", ve)
	}
}

func TestCorruptStoreIsCoded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	if err := os.WriteFile(dir+"/todo.json", []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runList(context.Background(), req(map[string]any{}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "todo.store.corrupt" || ve.Hint == "" {
		t.Errorf("want todo.store.corrupt with hint, got %+v", ve)
	}
}

func TestEditUpdatesTitleAndBody(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "draft"}, false)

	if body := text(t, runEdit, map[string]any{"id": 1, "title": "final", "body": "- [ ] step one"}, false); !strings.Contains(body, "updated task 1: final") {
		t.Errorf("edit = %q", body)
	}
	// List shows the new title with the body marker.
	tbl := table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Task")]; got != "final ≡" {
		t.Errorf("list task cell = %q", got)
	}
	// Partial edit keeps the other field.
	text(t, runEdit, map[string]any{"id": 1, "title": "renamed"}, false)
	page := show(t, 1)
	if got := pair(t, section(t, page, "task"), "title"); got != "renamed" {
		t.Errorf("title = %q", got)
	}
	desc := section(t, page, "description").(view.Text)
	if !desc.Markdown {
		t.Error("description must render as markdown")
	}
	if !strings.Contains(desc.Body, "- [ ] step one") {
		t.Errorf("description = %q", desc.Body)
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
	meta := section(t, page, "task")
	if got := pair(t, meta, "status"); got != "open" {
		t.Errorf("status = %q", got)
	}
	if got := pair(t, meta, "due"); !strings.Contains(got, "2030-01-02") {
		t.Errorf("due = %q", got)
	}
	if got := pair(t, meta, "tags"); got != "#release" {
		t.Errorf("tags = %q", got)
	}
	// The prose section carries only the prose.
	if got := section(t, page, "description").(view.Text).Body; got != "the actual prose" {
		t.Errorf("description leaked metadata: %q", got)
	}
}

// An empty body must still say something useful — a page dedicated to one
// task should never render a blank band.
func TestShowEmptyBodyExplainsItself(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "bare"}, false)
	desc := section(t, show(t, 1), "description").(view.Text)
	if !strings.Contains(desc.Body, "No description yet") || !strings.Contains(desc.Body, "todo edit 1") {
		t.Errorf("empty description = %q", desc.Body)
	}
	if desc.Markdown {
		t.Error("the placeholder is not the user's markdown")
	}
}

func TestEditNothingToChangeIsCoded(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
	_, err := runEdit(context.Background(), req(map[string]any{"id": 1, "parent": noParentChange}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "todo.edit.nochange" || ve.Hint == "" {
		t.Errorf("want todo.edit.nochange with hint, got %+v", ve)
	}
}

func TestEditDryRunTouchesNothing(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "keep"}, false)
	if body := text(t, runEdit, map[string]any{"id": 1, "title": "phantom", "parent": noParentChange}, true); !strings.Contains(body, "would update") {
		t.Errorf("dry-run edit = %q", body)
	}
	if tbl := table(t, runList, map[string]any{"all": false}); tbl.Rows[0][col(t, tbl, "Task")] != "keep" {
		t.Errorf("dry-run edit mutated the store: %v", tbl.Rows)
	}
}

// TestLegacyStoreMigrates loads a pre-body store (title under "text") and
// asserts it reads correctly and persists in the new shape on first write.
func TestLegacyStoreMigrates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	legacy := `{"nextId":2,"items":[{"id":1,"text":"old style","done":false,"created":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(dir+"/todo.json", []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Task")] != "old style" {
		t.Fatalf("legacy store misread: %v", tbl.Rows)
	}

	// Any write persists the migrated shape: "text" is gone, "title" is there.
	text(t, runEdit, map[string]any{"id": 1, "body": "details", "parent": noParentChange}, false)
	raw, err := os.ReadFile(dir + "/todo.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"text"`) || !strings.Contains(string(raw), `"title": "old style"`) {
		t.Errorf("store not migrated: %s", raw)
	}
}

// TestPrefillEditReturnsCurrentContent: interactive edit forms open with the
// task's current title and body.
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
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Task")] != "server work" {
		t.Fatalf("tag filter = %v", tbl.Rows)
	}

	// todo.tags counts usage.
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
	if ve.Code != "todo.add.baddue" {
		t.Errorf("want todo.add.baddue, got %+v", ve)
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

// --- Sub-tasks ------------------------------------------------------------

func TestSubTasksProgressAndListing(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "epic"}, false)
	text(t, runAdd, map[string]any{"title": "step one", "parent": 1}, false)
	text(t, runAdd, map[string]any{"title": "step two", "parent": 1}, false)

	// Top-level list shows only the parent, with a progress suffix.
	tbl := table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 {
		t.Fatalf("top-level list should hide sub-tasks: %v", tbl.Rows)
	}
	if got := tbl.Rows[0][col(t, tbl, "Task")]; got != "epic (0/2)" {
		t.Errorf("progress suffix = %q", got)
	}

	// --parent lists just the children.
	children := table(t, runList, map[string]any{"parent": 1})
	if len(children.Rows) != 2 {
		t.Fatalf("children list = %v", children.Rows)
	}

	text(t, runDone, map[string]any{"id": 2}, false)
	tbl = table(t, runList, map[string]any{"all": false})
	if got := tbl.Rows[0][col(t, tbl, "Task")]; got != "epic (1/2)" {
		t.Errorf("progress after done = %q", got)
	}
}

func TestAddBadParentIsCoded(t *testing.T) {
	setup(t)
	_, err := runAdd(context.Background(), req(map[string]any{"title": "x", "parent": 99}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "todo.add.badparent" {
		t.Errorf("want todo.add.badparent, got %+v", ve)
	}
}

func TestEditSelfParentIsCoded(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x"}, false)
	_, err := runEdit(context.Background(), req(map[string]any{"id": 1, "parent": 1}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "todo.edit.selfparent" {
		t.Errorf("want todo.edit.selfparent, got %+v", ve)
	}
}

// TestRemoveReparentsChildren: removing a task with sub-tasks moves them up
// rather than orphaning or deleting them.
func TestRemoveReparentsChildren(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "epic"}, false)
	text(t, runAdd, map[string]any{"title": "child", "parent": 1}, false)

	body := text(t, runRemove, map[string]any{"id": 1}, false)
	if !strings.Contains(body, "1 sub-task(s) moved up") {
		t.Errorf("remove message = %q", body)
	}
	// The child is now top-level.
	tbl := table(t, runList, map[string]any{"all": false})
	if len(tbl.Rows) != 1 || tbl.Rows[0][col(t, tbl, "Task")] != "child" {
		t.Fatalf("child not reparented: %v", tbl.Rows)
	}
}

// --- Show: cross-references and sub-task rendering -----------------------

func TestShowRendersSubTasksAndReferences(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "epic"}, false)
	text(t, runAdd, map[string]any{"title": "child", "parent": 1}, false)
	text(t, runDone, map[string]any{"id": 2}, false)
	text(t, runAdd, map[string]any{"title": "related", "body": "see #1 for context"}, false)

	page := show(t, 1)
	if got := pair(t, section(t, page, "task"), "sub-tasks"); got != "1 of 1 done" {
		t.Errorf("progress = %q", got)
	}
	subs := section(t, page, "sub-tasks").(view.Table)
	if len(subs.Rows) != 1 || subs.Rows[0][0] != "2" || subs.Rows[0][1] != "done" {
		t.Errorf("sub-task rows = %v", subs.Rows)
	}
	refs := section(t, page, "references").(view.Table)
	if len(refs.Rows) != 1 || refs.Rows[0][0] != "← mentioned by" || refs.Rows[0][1] != "3" {
		t.Errorf("back-reference = %v", refs.Rows)
	}

	fwd := section(t, show(t, 3), "references").(view.Table)
	if len(fwd.Rows) != 1 || fwd.Rows[0][0] != "→ mentions" || fwd.Rows[0][2] != "epic" {
		t.Errorf("forward reference = %v", fwd.Rows)
	}
	// A task with neither relationship gets neither section — no empty bands.
	text(t, runAdd, map[string]any{"title": "lonely"}, false)
	if lonely := show(t, 4); hasSection(lonely, "sub-tasks") || hasSection(lonely, "references") {
		t.Error("unrelated task should carry no sub-task or reference section")
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
	if !strings.Contains(v.(view.Text).Body, `No tasks match "nope"`) {
		t.Errorf("no-match body = %v", v)
	}
}

func TestSearchMarksSubTasks(t *testing.T) {
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

// A task whose body mentions its own number is writing prose, not linking to
// itself — "see #3" inside task 3 must not produce a reference to task 3.
func TestShowIgnoresSelfReferences(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "self", "body": "tracked as #1, blocked by #2"}, false)
	text(t, runAdd, map[string]any{"title": "other"}, false)

	refs := section(t, show(t, 1), "references").(view.Table)
	for _, row := range refs.Rows {
		if row[1] == "1" && row[0] == "→ mentions" {
			t.Errorf("task links to itself: %v", refs.Rows)
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
	text(t, runAdd, map[string]any{"title": "ship it"}, false)
	text(t, runDone, map[string]any{"id": 1}, false)

	body := text(t, runReopen, map[string]any{"id": 1}, false)
	if !strings.Contains(body, "re-opened") {
		t.Errorf("reopen said %q", body)
	}
	s, _ := load()
	if s.Items[0].Done || s.Items[0].DoneAt != nil {
		t.Errorf("task still carries completion: %+v", s.Items[0])
	}
	// Re-opening an open task is a no-op, not an error.
	if _, err := runReopen(context.Background(), req(map[string]any{"id": 1}, false)); err != nil {
		t.Errorf("second reopen: %v", err)
	}
	// …and an unknown id is still an error.
	if _, err := runReopen(context.Background(), req(map[string]any{"id": 99}, false)); err == nil {
		t.Error("reopening a task that does not exist was accepted")
	}
}

func TestReopenDryRunChangesNothing(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "ship it"}, false)
	text(t, runDone, map[string]any{"id": 1}, false)
	text(t, runReopen, map[string]any{"id": 1}, true)
	if s, _ := load(); !s.Items[0].Done {
		t.Error("a dry run re-opened the task")
	}
}

// The MCP bridge dispatches every tools/call in its own goroutine, so two
// pipelined `todo_add` calls racing each other is the ordinary case, not an
// exotic one. Before the load-decide-save cycle was locked, both could read
// the same NextID, both append their own item, and the second Save simply
// overwrite the first — the loser's task silently gone despite being told
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
				req(map[string]any{"title": fmt.Sprintf("task %d", i)}, false)); err != nil {
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
