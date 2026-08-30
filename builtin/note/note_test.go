package note

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func setup(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func req(values map[string]any, dryRun bool) plugin.Request {
	return plugin.NewRequest(values, dryRun, false)
}

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

// show runs runShow and asserts it returned the composed note page.
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

func TestNotesHaveNoStatus(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "idea"}, false)
	v, err := runList(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.(view.Table).Columns {
		if c.Kind == view.KindStatus {
			t.Errorf("notes must not have a status column: %+v", c)
		}
	}
}

func TestAddShowEditRemoveCycle(t *testing.T) {
	setup(t)

	if body := text(t, runList, nil, false); !strings.Contains(body, "No notes yet") {
		t.Errorf("empty list = %q", body)
	}

	text(t, runAdd, map[string]any{"title": "meeting", "body": "## Agenda\n\n- topic"}, false)

	page := show(t, 1)
	if got := pair(t, section(t, page, "note"), "title"); got != "meeting" {
		t.Errorf("title = %q", got)
	}
	content := section(t, page, "content").(view.Text)
	if !content.Markdown || !strings.Contains(content.Body, "## Agenda") {
		t.Errorf("content = %+v", content)
	}

	if body := text(t, runEdit, map[string]any{"id": 1, "title": "standup"}, false); !strings.Contains(body, "updated note 1: standup") {
		t.Errorf("edit = %q", body)
	}

	// Prefill hands back current content for edit-in-place forms.
	got, err := prefillEdit(context.Background(), req(map[string]any{"id": 1}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got["title"] != "standup" || got["body"] != "## Agenda\n\n- topic" {
		t.Errorf("prefill = %v", got)
	}

	text(t, runRemove, map[string]any{"id": 1}, false)
	if body := text(t, runList, nil, false); !strings.Contains(body, "No notes yet") {
		t.Errorf("after rm = %q", body)
	}
}

func TestNotesAndTodosAreSeparateStores(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "only a note"}, false)
	// The note landed in notes.json, not todo.json: the todo namespace's
	// store file stays untouched (its own tests cover the todo side).
	v, err := runList(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	if total := v.(view.Table).Total; total != 1 {
		t.Errorf("note store total = %d", total)
	}
}

func TestNotFoundIsCodedWithHint(t *testing.T) {
	setup(t)
	_, err := runShow(context.Background(), req(map[string]any{"id": 9}, false))
	ve := view.AsError(err, "x")
	if ve.Code != "note.notfound" || ve.Hint == "" {
		t.Errorf("want note.notfound with hint, got %+v", ve)
	}
}

// --- Tags -------------------------------------------------------------

func TestTagsCaptureFilterAndList(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "carbonara", "tag": []string{"Recipe", "italian"}}, false)
	text(t, runAdd, map[string]any{"title": "todo app idea", "tag": []string{"project"}}, false)

	v, err := runList(context.Background(), req(map[string]any{"tag": []string{"#RECIPE"}}, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 1 || tbl.Rows[0][2] != "carbonara" {
		t.Fatalf("tag filter = %v", tbl.Rows)
	}

	v, err = runTags(context.Background(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]string{}
	for _, r := range v.(view.Table).Rows {
		counts[r[0]] = r[1]
	}
	if counts["recipe"] != "1" || counts["italian"] != "1" || counts["project"] != "1" {
		t.Errorf("tag counts = %v", counts)
	}
}

func TestTagClearAndReplace(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "x", "tag": []string{"a", "b"}}, false)

	text(t, runEdit, map[string]any{"id": 1, "tag": []string{"c"}}, false)
	got, _ := prefillEdit(context.Background(), req(map[string]any{"id": 1}, false))
	if tags := got["tag"].([]string); len(tags) != 1 || tags[0] != "c" {
		t.Fatalf("replace = %v", got["tag"])
	}

	text(t, runEdit, map[string]any{"id": 1, "tag": []string{"-"}}, false)
	got, _ = prefillEdit(context.Background(), req(map[string]any{"id": 1}, false))
	if tags, _ := got["tag"].([]string); len(tags) != 0 {
		t.Fatalf("clear = %v", got["tag"])
	}
}

// --- References ------------------------------------------------------------

func TestShowRendersCrossReferences(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "base idea"}, false)
	text(t, runAdd, map[string]any{"title": "follow-up", "body": "builds on #1"}, false)

	back := section(t, show(t, 1), "references").(view.Table)
	if len(back.Rows) != 1 || back.Rows[0][0] != "← mentioned by" || back.Rows[0][2] != "follow-up" {
		t.Errorf("back-reference = %v", back.Rows)
	}
	fwd := section(t, show(t, 2), "references").(view.Table)
	if len(fwd.Rows) != 1 || fwd.Rows[0][0] != "→ mentions" || fwd.Rows[0][2] != "base idea" {
		t.Errorf("forward reference = %v", fwd.Rows)
	}
}

// An empty note must say how to fill itself rather than render blank.
func TestShowEmptyNoteExplainsItself(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "stub"}, false)
	content := section(t, show(t, 1), "content").(view.Text)
	if !strings.Contains(content.Body, "empty") || !strings.Contains(content.Body, "note edit 1") {
		t.Errorf("empty content = %q", content.Body)
	}
}

// --- Search ---------------------------------------------------------------

func TestSearchMatchesTitleBodyAndTag(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "pasta night", "tag": []string{"recipe"}}, false)
	text(t, runAdd, map[string]any{"title": "unrelated", "body": "mentions pasta somewhere"}, false)
	text(t, runAdd, map[string]any{"title": "css"}, false)

	v, err := runSearch(context.Background(), req(map[string]any{"query": "pasta"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(view.Table).Rows; len(got) != 2 {
		t.Fatalf("search rows = %v", got)
	}

	v, err = runSearch(context.Background(), req(map[string]any{"query": "recipe"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(view.Table).Rows; len(got) != 1 {
		t.Fatalf("tag search = %v", got)
	}
}

// The same for notes: "#1" inside note 1 is prose, not a self-link.
func TestShowIgnoresSelfReferences(t *testing.T) {
	setup(t)
	text(t, runAdd, map[string]any{"title": "self", "body": "this is note #1, see also #2"}, false)
	text(t, runAdd, map[string]any{"title": "other"}, false)

	refs := section(t, show(t, 1), "references").(view.Table)
	if len(refs.Rows) != 1 || refs.Rows[0][1] != "2" {
		t.Errorf("references = %v, want only the real link", refs.Rows)
	}
}

// The MCP bridge dispatches every tools/call in its own goroutine — see the
// identical test and comment in builtin/todo.
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
}
