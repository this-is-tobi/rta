// Package note is the built-in local notes: markdown documents with a title,
// tags and cross-references, captured and read at terminal speed. It shares
// the todo built-in's store shape (builtin/internal/itemstore) — a note is a
// task without a status, due date or sub-items. Editing prefills current
// content on interactive surfaces, like editing an issue.
//
// AI trajectory: exposed over MCP, so agents can capture and
// rework notes; later the ai plugin can improve wording or restructure them.
package note

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/itemstore"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const (
	storeFile = "notes.json"
	ns        = "note"
)

var tagField = plugin.Field{Name: "tag", Type: plugin.StringSlice,
	Help:    "tags, repeatable (--tag recipe --tag italian)",
	Suggest: suggestTags}

// suggestTags offers the tags this store already uses, most used first — the
// way a tag vocabulary stays consistent without anyone maintaining a list.
func suggestTags(context.Context, plugin.Request) []string {
	return itemstore.SuggestTags(storeFile, ns)
}

// suggestIDs completes a note id with its title beside it.
func suggestIDs(context.Context, plugin.Request) []string {
	return itemstore.SuggestIDs(storeFile, ns, false)
}

// Plugin returns the note plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "note",
		Summary: "Local markdown notes: capture, tag, cross-link, read, refine",
		Capabilities: []plugin.Capability{
			{
				ID: "note.list", Summary: "List notes", Safety: plugin.Read, Idempotent: true,
				Detailed:    true,
				Description: "With --detail: adds creation date and a preview of each note's body.",
				Inputs: []plugin.Field{
					{Name: "tag", Type: plugin.StringSlice, Help: "only notes with any of these tags",
						Suggest: suggestTags},
				},
				Run: runList,
			},
			{
				ID: "note.show", Summary: "Show one note: metadata, content, references",
				Safety: plugin.Read, Idempotent: true,
				Description: "Metadata (tags, size, age) stays separate from the markdown content, " +
					"which is rendered richly on human surfaces. Cross-references (\"#12\" in a " +
					"body) resolve both ways.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestIDs},
				},
				Run: runShow,
			},
			{
				ID: "note.search", Summary: "Search notes by title, body or tag", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "query", Type: plugin.String, Positional: true, Required: true, Help: "search terms"},
				},
				Run: runSearch,
			},
			{
				ID: "note.tags", Summary: "List tags in use and how many notes carry each", Safety: plugin.Read, Idempotent: true,
				Run: runTags,
			},
			{
				ID: "note.add", Summary: "Add a note", Safety: plugin.Write,
				Inputs: append([]plugin.Field{
					{Name: "title", Type: plugin.String, Positional: true, Required: true, Help: "note title"},
					{Name: "body", Type: plugin.Text, Help: "note content, markdown supported"},
				}, tagField),
				Run: runAdd,
			},
			{
				ID: "note.edit", Summary: "Edit a note's title, body or tags", Safety: plugin.Write, Idempotent: true,
				Description: "Empty fields keep their current value. --tag - clears all tags.",
				Inputs: append([]plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestIDs},
					{Name: "title", Type: plugin.String, Help: "new title (empty keeps the current one)"},
					{Name: "body", Type: plugin.Text, Help: "new body, markdown supported (empty keeps the current one)"},
				}, tagField),
				Run:     runEdit,
				Prefill: prefillEdit,
			},
			{
				ID: "note.rm", Summary: "Remove a note permanently", Safety: plugin.Destructive,
				Scope: "id",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestIDs},
				},
				Run: runRemove,
			},
		},
	}
}

func load() (itemstore.Store, error) { return itemstore.Load(storeFile, ns) }
func save(s itemstore.Store) error   { return itemstore.Save(storeFile, ns, s) }
func find(s itemstore.Store, id int) (int, *view.Error) {
	for i := range s.Items {
		if s.Items[i].ID == id {
			return i, nil
		}
	}
	return 0, view.Errorf("note.notfound", "no note with id %d", id).
		WithHint("run `rta note list` to see every note")
}

func hasAnyTag(it itemstore.Item, tags []string) bool {
	if len(tags) == 0 {
		return true
	}
	for _, t := range tags {
		if it.HasTag(t) {
			return true
		}
	}
	return false
}

// applyTags mirrors todo's --tag convention: a single "-" clears, anything
// else replaces. Kept in sync deliberately rather than shared, since the two
// packages otherwise stay independent (the low-maintenance principle
// favors one small duplication over a cross-plugin dependency here).
func applyTags(raw []string) []string {
	if len(raw) == 1 && raw[0] == "-" {
		return nil
	}
	out := make([]string, len(raw))
	for i, t := range raw {
		out[i] = itemstore.NormalizeTag(t)
	}
	return out
}

func runList(_ context.Context, req plugin.Request) (view.View, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	detail := req.Bool("detail")
	tags := req.StringSlice("tag")
	cols := []view.Column{
		{Name: "ID", Kind: view.KindNumber},
		{Name: "Age", Kind: view.KindDuration},
		{Name: "Note"},
	}
	if detail {
		cols = append(cols,
			view.Column{Name: "Tags"},
			view.Column{Name: "Created", Kind: view.KindTimestamp},
			view.Column{Name: "Content"})
	}
	t := view.Table{Columns: cols}
	for _, it := range s.Items {
		if !hasAnyTag(it, tags) {
			continue
		}
		title := it.Title
		if it.Body != "" && !detail {
			title += " ≡"
		}
		row := []string{strconv.Itoa(it.ID), itemstore.Age(it.Created), title}
		if detail {
			row = append(row, strings.Join(it.Tags, ", "), it.Created.Format("2006-01-02 15:04"), itemstore.Preview(it.Body))
		}
		t.Rows = append(t.Rows, row)
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return view.Text{Body: "No notes yet — add one with: rta note add \"...\""}, nil
	}
	return t, nil
}

func runSearch(_ context.Context, req plugin.Request) (view.View, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	query := req.String("query")
	t := view.Table{Columns: []view.Column{
		{Name: "ID", Kind: view.KindNumber},
		{Name: "Note"},
		{Name: "Tags"},
	}}
	for _, it := range s.Items {
		if !it.Matches(query) {
			continue
		}
		t.Rows = append(t.Rows, []string{strconv.Itoa(it.ID), it.Title, strings.Join(it.Tags, ", ")})
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return view.Text{Body: fmt.Sprintf("No notes match %q", query)}, nil
	}
	return t, nil
}

func runTags(_ context.Context, _ plugin.Request) (view.View, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, it := range s.Items {
		for _, tag := range it.Tags {
			counts[itemstore.NormalizeTag(tag)]++
		}
	}
	if len(counts) == 0 {
		return view.Text{Body: "No tags yet — add one with: rta note add \"...\" --tag <name>"}, nil
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	t := view.Table{Columns: []view.Column{{Name: "Tag"}, {Name: "Notes", Kind: view.KindNumber}}}
	for _, name := range names {
		t.Rows = append(t.Rows, []string{name, strconv.Itoa(counts[name])})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// crossRefs resolves "#N" mentions in the body to titles, and lists any
// other note that mentions this one back.
func crossRefs(s itemstore.Store, it itemstore.Item) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Link"},
		{Name: "ID", Kind: view.KindNumber},
		{Name: "Note"},
	}}
	for _, id := range itemstore.References(it.Body) {
		if id == it.ID {
			continue // "#3" inside note 3 is prose, not a link to itself
		}
		if i, verr := find(s, id); verr == nil {
			t.Rows = append(t.Rows, []string{"→ mentions", strconv.Itoa(id), s.Items[i].Title})
		}
	}
	for _, other := range s.Items {
		if other.ID == it.ID {
			continue
		}
		for _, id := range itemstore.References(other.Body) {
			if id == it.ID {
				t.Rows = append(t.Rows, []string{"← mentioned by", strconv.Itoa(other.ID), other.Title})
				break
			}
		}
	}
	t.Total = len(t.Rows)
	return t
}

// showSections mirrors todo's task page: metadata apart from prose, so the
// content of a note is never diluted by the bookkeeping around it.
func showSections(s itemstore.Store, it itemstore.Item) view.Sections {
	kv := view.KeyValue{Pairs: []view.Pair{
		{Key: "title", Value: it.Title},
		{Key: "id", Value: "#" + strconv.Itoa(it.ID)},
	}}
	if len(it.Tags) > 0 {
		tags := make([]string, len(it.Tags))
		for i, tg := range it.Tags {
			tags[i] = "#" + itemstore.NormalizeTag(tg)
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "tags", Value: strings.Join(tags, " ")})
	}
	kv.Pairs = append(kv.Pairs,
		view.Pair{Key: "words", Value: strconv.Itoa(len(strings.Fields(it.Body)))},
		view.Pair{Key: "created", Value: fmt.Sprintf("%s (%s)",
			it.Created.Format("2006-01-02 15:04"), itemstore.Age(it.Created))})

	body := view.Text{Body: strings.TrimRight(it.Body, "\n"), Markdown: true}
	if strings.TrimSpace(it.Body) == "" {
		body = view.Text{Body: fmt.Sprintf("This note is empty — write it with: rta note edit %d --body \"...\"", it.ID)}
	}
	sec := view.Sections{Items: []view.Section{
		{ID: "note", Title: "note", View: kv},
		{ID: "content", Title: "content", View: body},
	}}
	if refs := crossRefs(s, it); len(refs.Rows) > 0 {
		sec.Items = append(sec.Items, view.Section{ID: "references", Title: "references", View: refs})
	}
	return sec
}

func runShow(_ context.Context, req plugin.Request) (view.View, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	i, verr := find(s, req.Int("id"))
	if verr != nil {
		return nil, verr
	}
	return showSections(s, s.Items[i]), nil
}

func runAdd(_ context.Context, req plugin.Request) (view.View, error) {
	title := strings.TrimSpace(req.String("title"))
	if title == "" {
		return nil, view.Errorf("note.add.empty", "note title is empty")
	}
	// Held across the whole load-decide-save below: two calls racing this
	// (an ordinary pattern for pipelined MCP tool calls) can otherwise both
	// read the same NextID and both save, with the loser's note silently
	// gone despite being told it was added. See itemstore.Lock.
	unlock, err := itemstore.Lock(storeFile)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := load()
	if err != nil {
		return nil, err
	}
	item := itemstore.Item{
		ID: s.NextID, Title: title, Body: req.String("body"),
		Tags: applyTags(req.StringSlice("tag")), Created: time.Now(),
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would add note %d: %s", item.ID, title)}, nil
	}
	s.Items = append(s.Items, item)
	s.NextID++
	if err := save(s); err != nil {
		return nil, err
	}
	return view.Text{Body: fmt.Sprintf("added note %d: %s", item.ID, title)}, nil
}

// prefillEdit hands interactive surfaces the note's current content.
func prefillEdit(_ context.Context, req plugin.Request) (map[string]any, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	i, verr := find(s, req.Int("id"))
	if verr != nil {
		return nil, verr
	}
	return map[string]any{"title": s.Items[i].Title, "body": s.Items[i].Body, "tag": s.Items[i].Tags}, nil
}

func runEdit(_ context.Context, req plugin.Request) (view.View, error) {
	title := strings.TrimSpace(req.String("title"))
	body := req.String("body")
	rawTags := req.StringSlice("tag")
	if title == "" && body == "" && len(rawTags) == 0 {
		return nil, view.Errorf("note.edit.nochange", "nothing to change").
			WithHint("pass --title, --body and/or --tag with the new content")
	}
	// Held across the whole load-decide-save below — see itemstore.Lock.
	unlock, err := itemstore.Lock(storeFile)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := load()
	if err != nil {
		return nil, err
	}
	id := req.Int("id")
	i, verr := find(s, id)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would update note %d: %s", id, s.Items[i].Title)}, nil
	}
	if title != "" {
		s.Items[i].Title = title
	}
	if body != "" {
		s.Items[i].Body = body
	}
	if len(rawTags) > 0 {
		s.Items[i].Tags = applyTags(rawTags)
	}
	if err := save(s); err != nil {
		return nil, err
	}
	return view.Text{Body: fmt.Sprintf("updated note %d: %s", id, s.Items[i].Title)}, nil
}

func runRemove(_ context.Context, req plugin.Request) (view.View, error) {
	// Held across the whole load-decide-save below — see itemstore.Lock.
	unlock, err := itemstore.Lock(storeFile)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := load()
	if err != nil {
		return nil, err
	}
	id := req.Int("id")
	i, verr := find(s, id)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would remove note %d: %s", id, s.Items[i].Title)}, nil
	}
	removed := s.Items[i].Title
	s.Items = append(s.Items[:i], s.Items[i+1:]...)
	if err := save(s); err != nil {
		return nil, err
	}
	return view.Text{Body: fmt.Sprintf("removed note %d: %s", id, removed)}, nil
}
