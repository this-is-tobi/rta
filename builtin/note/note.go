// Package note is the built-in local notebook: things you write down, some
// of which you will do. Every item is a markdown document with a title, tags
// and cross-references; any of them can also carry a due date, sit under a
// parent, and be checked off. It used to be two built-ins — todo, the task
// list, and note, "a task without a status, due date or sub-items" — and the
// difference was two namespaces, two stores and two tag vocabularies for one
// shape: a task is a note with a checkbox, and a note you are done with is a
// note you check off. One namespace means one list on the dashboard, one
// `d`, and nothing to decide before writing something down.
//
// It is also the first mutating built-in, so it exercises the full safety
// model for real: write capabilities, a destructive remove, and honest
// --dry-run support. Editing prefills the current content on interactive
// surfaces (Capability.Prefill), like editing an issue. Every capability is
// exposed over MCP — `rta grant allow note --ttl 8h` lets an agent capture
// and complete notes for you.
package note

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/itemstore"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	storeFile = "notes.json"
	ns        = "note"
	// noParentChange is the sentinel default for --parent on edit: -1 means
	// "leave as-is" so it can be distinguished from 0 ("make top-level").
	noParentChange = -1
)

// tagField and dueField are shared between add and edit.
//
// Both complete rather than enumerate. Tags are open by nature — the point of
// a tag is that you invent it — so the suggestions are the ones already in
// use, which is how a vocabulary stays consistent without anyone policing it.
// Due dates accept far more than the shorthands offered; the list is the
// forms worth remembering, not the grammar.
var (
	tagField = plugin.Field{Name: "tag", Type: plugin.StringSlice,
		Help:    "tags, repeatable (--tag backend --tag urgent)",
		Suggest: suggestTags}
	dueField = plugin.Field{Name: "due", Type: plugin.String,
		Help:    "due date: today, tomorrow, a weekday, or 2006-01-02",
		Suggest: suggestDue}
)

// suggestTags offers the tags this store already uses, most used first.
func suggestTags(context.Context, plugin.Request) []string {
	return itemstore.SuggestTags(storeFile, ns)
}

// suggestDue offers the shorthands, and today's actual date so the literal
// form is one keystroke away rather than a calendar lookup.
func suggestDue(context.Context, plugin.Request) []string {
	now := time.Now()
	return []string{
		"today", "tomorrow", "next-week",
		strings.ToLower(now.AddDate(0, 0, 2).Format("Monday")),
		now.AddDate(0, 0, 7).Format("2006-01-02"),
	}
}

// suggestOpenIDs completes an id with the title beside it — the id is what
// gets typed, the title is what makes it the right id.
func suggestOpenIDs(context.Context, plugin.Request) []string {
	return itemstore.SuggestIDs(storeFile, ns, true)
}

// suggestAnyID includes checked-off notes: removing or looking at one is a
// thing you do to a note that is already done.
func suggestAnyID(context.Context, plugin.Request) []string {
	return itemstore.SuggestIDs(storeFile, ns, false)
}

// suggestDoneIDs offers only what can actually be re-opened.
func suggestDoneIDs(context.Context, plugin.Request) []string {
	s, err := load()
	if err != nil {
		return nil
	}
	var out []string
	for _, it := range s.Items {
		if it.Done {
			out = append(out, fmt.Sprintf("%d\t%s", it.ID, it.Title))
		}
	}
	return out
}

// Plugin returns the note plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "note",
		Summary: "Local notebook: capture, tag, cross-link, schedule, break down, check off",
		Capabilities: []plugin.Capability{
			{
				ID: "note.list", Summary: "List notes", Safety: plugin.Read, Idempotent: true,
				Detailed: true,
				Description: "Open notes, the ones with a due date first — soonest on top — then the " +
					"rest in the order they were written. Checked-off notes are hidden until --all. " +
					"Shows top-level notes by default; pass --parent to list one note's sub-notes. " +
					"With --detail: adds creation date and a body preview.",
				Inputs: []plugin.Field{
					{Name: "all", Type: plugin.Bool, Config: "all", Help: "include checked-off notes"},
					{Name: "tag", Type: plugin.StringSlice, Help: "only notes with any of these tags",
						Suggest: suggestTags},
					{Name: "parent", Type: plugin.Int, Default: 0, Help: "list sub-notes of this note id instead of top-level notes",
						Suggest: suggestAnyID},
				},
				Run: runList,
			},
			{
				ID: "note.show", Summary: "Show one note: metadata, content, sub-notes, references",
				Safety: plugin.Read, Idempotent: true,
				Description: "A composed page rather than one blob: structured metadata (status, due, " +
					"tags, parent, progress) stays separate from the markdown content, so both " +
					"stay readable and machine callers can read a due date without parsing prose. " +
					"Sub-notes and cross-references (\"#12\" in a body, resolved both ways) follow.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestAnyID},
				},
				Run: runShow,
			},
			{
				ID: "note.search", Summary: "Search notes by title, body or tag", Safety: plugin.Read, Idempotent: true,
				Description: "Searches every note regardless of parent/child, done or open.",
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
				Description: "A title is enough. --todo (or a due date, which implies it) makes it " +
					"something to do; a parent makes it part of something bigger; markdown in the " +
					"body is rendered on human surfaces.",
				Inputs: append([]plugin.Field{
					{Name: "title", Type: plugin.String, Positional: true, Required: true, Help: "note title"},
					{Name: "body", Type: plugin.Text, Help: "note content, markdown supported"},
					{Name: "todo", Type: plugin.Bool, Help: "make it a to-do: something to check off"},
					{Name: "parent", Type: plugin.Int, Help: "make this a sub-note of the given note id",
						Suggest: suggestOpenIDs},
				}, tagField, dueField),
				Run: runAdd,
			},
			{
				ID: "note.toggle", Summary: "Turn a note into a to-do, or a to-do back into a note",
				Safety: plugin.Write,
				Description: "The switch between the two kinds of thing in the notebook. A note " +
					"becomes an open to-do; a to-do becomes a note and forgets whether it was done, " +
					"which is the point — a note has nothing to be done about.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestAnyID},
				},
				Run: runToggle,
			},
			{
				ID: "note.edit", Summary: "Edit a note's title, body, tags, due date or parent", Safety: plugin.Write, Idempotent: true,
				Description: "Empty fields keep their current value. --tag - clears all tags; " +
					"--due none clears the due date.",
				Inputs: append([]plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true,
						Suggest: suggestAnyID, Help: "note id"},
					{Name: "title", Type: plugin.String, Help: "new title (empty keeps the current one)"},
					{Name: "body", Type: plugin.Text, Help: "new body, markdown supported (empty keeps the current one)"},
					{Name: "parent", Type: plugin.Int, Default: noParentChange, Suggest: suggestOpenIDs,
						Help: "move it under this note id (0 makes it top-level)"},
				}, tagField, dueField),
				Run:     runEdit,
				Prefill: prefillEdit,
			},
			{
				ID: "note.done", Summary: "Check a note off", Safety: plugin.Write, Idempotent: true,
				Description: "Done for a task, filed for a note: either way it leaves the default " +
					"list and stays findable under --all and in search.",
				Inputs: []plugin.Field{
					// Checking off is something you do to an open note, so the
					// done ones stay out of the way here.
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestOpenIDs},
				},
				Run: runDone,
			},
			{
				ID: "note.reopen", Summary: "Reopen a checked note", Safety: plugin.Write, Idempotent: true,
				Description: "The undo for `note done`. Checking off the wrong note is a " +
					"one-keystroke mistake, and a list you cannot take something back out of is a " +
					"list people stop trusting. Re-opening an already-open note is a no-op, not an " +
					"error.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestDoneIDs},
				},
				Run: runReopen,
			},
			{
				ID: "note.rm", Summary: "Remove a note permanently", Safety: plugin.Destructive,
				Scope:       "id",
				Description: "Sub-notes are re-parented to the removed note's parent, never deleted silently.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "note id",
						Suggest: suggestAnyID},
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
		WithHint("run `rta note list --all` to see every note")
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

// statusOf is the one word a row carries about what kind of thing it is:
// a plain note, an open to-do, or a done one.
func statusOf(it itemstore.Item) string {
	switch {
	case !it.Todo:
		return "note"
	case it.Done:
		return "done"
	default:
		return "open"
	}
}

// titleCell renders one note's title cell, decorated with what a compact view
// has no room for a separate column for: sub-note progress and a body marker.
func titleCell(s itemstore.Store, it itemstore.Item, detail bool) string {
	title := it.Title
	if done, total := itemstore.Progress(s, it.ID); total > 0 {
		title += fmt.Sprintf(" (%d/%d)", done, total)
	}
	if it.Body != "" && !detail {
		title += " ≡" // a quiet marker that `note show <id>` has more to tell
	}
	return title
}

// listOrder puts what has a deadline on top, soonest first, and leaves the
// rest in the order it was written.
//
// A stable sort on one key rather than a full ordering: the undated notes
// are the notebook, and a notebook reads in the order you wrote it. Only the
// dated ones are a to-do list, and a to-do list reads by when.
func listOrder(items []itemstore.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Due, items[j].Due
		switch {
		case a == nil || b == nil:
			return a != nil && b == nil
		default:
			return a.Before(*b)
		}
	})
}

func runList(_ context.Context, req plugin.Request) (view.View, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	includeDone := req.Bool("all")
	detail := req.Bool("detail")
	tags := req.StringSlice("tag")
	parent := req.Int("parent")

	cols := []view.Column{
		{Name: "ID", Kind: view.KindNumber},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Due", Kind: view.KindStatus},
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
	now := time.Now()
	var shown []itemstore.Item
	for _, it := range s.Items {
		if it.Parent != parent || (it.Done && !includeDone) || !hasAnyTag(it, tags) {
			continue
		}
		shown = append(shown, it)
	}
	listOrder(shown)
	for _, it := range shown {
		row := []string{
			strconv.Itoa(it.ID), statusOf(it), itemstore.DueStatus(it.Due, it.Done, now),
			itemstore.Age(it.Created), titleCell(s, it, detail),
		}
		if detail {
			row = append(row, strings.Join(it.Tags, ", "), it.Created.Format("2006-01-02 15:04"), itemstore.Preview(it.Body))
		}
		t.Rows = append(t.Rows, row)
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		if parent != 0 {
			return view.Text{Body: fmt.Sprintf("Note %d has no sub-notes yet — add one with: rta note add \"...\" --parent %d", parent, parent)}, nil
		}
		return view.Text{Body: "Nothing here yet — add one with: rta note add \"...\""}, nil
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
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Note"},
		{Name: "Tags"},
	}}
	for _, it := range s.Items {
		if !it.Matches(query) {
			continue
		}
		title := it.Title
		if it.Parent != 0 {
			title = "↳ " + title // a quiet nod that this is a sub-note
		}
		t.Rows = append(t.Rows, []string{
			strconv.Itoa(it.ID), statusOf(it), title, strings.Join(it.Tags, ", "),
		})
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

// showSections composes the page from parts, the way an issue page is laid
// out: structured metadata first (aligned keys, scannable), then the prose,
// then the relationships. Splitting them is what makes both readable —
// metadata folded into the markdown body drowns in it, and every renderer has
// to re-parse prose to find a due date.
func showSections(s itemstore.Store, it itemstore.Item) view.Sections {
	sec := view.Sections{Items: []view.Section{
		{ID: "note", Title: "note", View: metaPairs(s, it)},
		{ID: "content", Title: "content", View: contentView(it)},
	}}
	if children := itemstore.Children(s, it.ID); len(children) > 0 {
		t := view.Table{Columns: []view.Column{
			{Name: "ID", Kind: view.KindNumber},
			{Name: "Status", Kind: view.KindStatus},
			{Name: "Sub-note"},
		}}
		for _, c := range children {
			t.Rows = append(t.Rows, []string{strconv.Itoa(c.ID), statusOf(c), c.Title})
		}
		t.Total = len(t.Rows)
		sec.Items = append(sec.Items, view.Section{ID: "sub-notes", Title: "sub-notes", View: t})
	}
	if refs := crossRefs(s, it); len(refs.Rows) > 0 {
		sec.Items = append(sec.Items, view.Section{ID: "references", Title: "references", View: refs})
	}
	return sec
}

// metaPairs is everything about a note that is not its prose.
func metaPairs(s itemstore.Store, it itemstore.Item) view.KeyValue {
	kv := view.KeyValue{Pairs: []view.Pair{
		{Key: "title", Value: it.Title},
		{Key: "id", Value: "#" + strconv.Itoa(it.ID)},
		{Key: "status", Value: statusOf(it)},
	}}
	if it.Due != nil {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "due", Value: fmt.Sprintf("%s (%s)",
			it.Due.Format("2006-01-02"), itemstore.DueStatus(it.Due, it.Done, time.Now()))})
	}
	if len(it.Tags) > 0 {
		tags := make([]string, len(it.Tags))
		for i, tg := range it.Tags {
			tags[i] = "#" + itemstore.NormalizeTag(tg)
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "tags", Value: strings.Join(tags, " ")})
	}
	if it.Parent != 0 {
		if pi, verr := find(s, it.Parent); verr == nil {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: "part of",
				Value: fmt.Sprintf("#%d %s", it.Parent, s.Items[pi].Title)})
		}
	}
	if done, total := itemstore.Progress(s, it.ID); total > 0 {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "sub-notes",
			Value: fmt.Sprintf("%d of %d done", done, total)})
	}
	kv.Pairs = append(kv.Pairs,
		view.Pair{Key: "words", Value: strconv.Itoa(len(strings.Fields(it.Body)))},
		view.Pair{Key: "created", Value: fmt.Sprintf("%s (%s)",
			it.Created.Format("2006-01-02 15:04"), itemstore.Age(it.Created))})
	if it.DoneAt != nil {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "completed",
			Value: fmt.Sprintf("%s (%s)", it.DoneAt.Format("2006-01-02 15:04"), itemstore.Age(*it.DoneAt))})
	}
	return kv
}

// contentView renders the prose. An empty body says so and says how to fill
// it, rather than leaving a blank band on a page dedicated to one note.
func contentView(it itemstore.Item) view.Text {
	if strings.TrimSpace(it.Body) == "" {
		return view.Text{Body: fmt.Sprintf("This note is empty — write it with: rta note edit %d --body \"...\"", it.ID)}
	}
	return view.Text{Body: strings.TrimRight(it.Body, "\n"), Markdown: true}
}

// crossRefs resolves "#N" mentions in the body to titles, and lists any
// other note that mentions this one back — a lightweight, local version of
// GitHub's issue cross-linking.
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

// applyTags interprets the --tag convention shared by add/edit: a single
// "-" clears, anything else replaces. Absent (nil) means "no change" to
// callers that check len(raw) > 0 first.
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
	parent := req.Int("parent")
	if parent != 0 {
		if _, verr := find(s, parent); verr != nil {
			return nil, view.Errorf("note.add.badparent", "parent note %d does not exist", parent).
				WithHint("run `rta note list` to see valid note ids")
		}
	}
	due, err := itemstore.ParseDue(req.String("due"), time.Now())
	if err != nil {
		return nil, view.Errorf("note.add.baddue", "%v", err)
	}
	item := itemstore.Item{
		ID: s.NextID, Title: title, Body: req.String("body"),
		Tags: applyTags(req.StringSlice("tag")), Parent: parent, Due: due, Created: time.Now(),
		// A deadline on a note is a to-do whatever the flag said: nobody
		// gives a date to something that cannot be done.
		Todo: req.Bool("todo") || due != nil,
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

// prefillEdit hands interactive surfaces the note's current content, so the
// edit form opens like editing an issue — not a blank slate.
func prefillEdit(_ context.Context, req plugin.Request) (map[string]any, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	i, verr := find(s, req.Int("id"))
	if verr != nil {
		return nil, verr
	}
	it := s.Items[i]
	due := ""
	if it.Due != nil {
		due = it.Due.Format("2006-01-02")
	}
	return map[string]any{
		"title": it.Title, "body": it.Body, "tag": it.Tags, "due": due, "parent": it.Parent,
	}, nil
}

func runEdit(_ context.Context, req plugin.Request) (view.View, error) {
	title := strings.TrimSpace(req.String("title"))
	body := req.String("body")
	rawTags := req.StringSlice("tag")
	rawDue := req.String("due")
	parent := req.Int("parent")
	if title == "" && body == "" && len(rawTags) == 0 && rawDue == "" && parent == noParentChange {
		return nil, view.Errorf("note.edit.nochange", "nothing to change").
			WithHint("pass --title, --body, --tag, --due and/or --parent with the new content")
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
	if parent != noParentChange {
		if parent == id {
			return nil, view.Errorf("note.edit.selfparent", "a note cannot be its own parent")
		}
		if parent != 0 {
			if _, verr := find(s, parent); verr != nil {
				return nil, view.Errorf("note.edit.badparent", "parent note %d does not exist", parent)
			}
		}
	}
	var due *time.Time
	dueChanged := false
	if strings.EqualFold(strings.TrimSpace(rawDue), "none") || strings.EqualFold(strings.TrimSpace(rawDue), "clear") {
		dueChanged = true // due stays nil: clears it
	} else if rawDue != "" {
		due, err = itemstore.ParseDue(rawDue, time.Now())
		if err != nil {
			return nil, view.Errorf("note.edit.baddue", "%v", err)
		}
		dueChanged = true
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
	if dueChanged {
		s.Items[i].Due = due
	}
	if parent != noParentChange {
		s.Items[i].Parent = parent
	}
	if err := save(s); err != nil {
		return nil, err
	}
	return view.Text{Body: fmt.Sprintf("updated note %d: %s", id, s.Items[i].Title)}, nil
}

func runDone(_ context.Context, req plugin.Request) (view.View, error) {
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
	if !s.Items[i].Todo {
		return nil, view.Errorf("note.done.notatodo", "note %d is not a to-do: %s", id, s.Items[i].Title).
			WithHint(fmt.Sprintf("`rta note toggle %d` makes it one, then check it off", id))
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would check off note %d: %s", id, s.Items[i].Title)}, nil
	}
	if !s.Items[i].Done {
		now := time.Now()
		s.Items[i].Done = true
		s.Items[i].DoneAt = &now
		if err := save(s); err != nil {
			return nil, err
		}
	}
	return view.Text{Body: fmt.Sprintf("done: %s", s.Items[i].Title)}, nil
}

func runReopen(_ context.Context, req plugin.Request) (view.View, error) {
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
		return view.Text{Body: fmt.Sprintf("would re-open note %d: %s", id, s.Items[i].Title)}, nil
	}
	if s.Items[i].Done {
		s.Items[i].Done = false
		s.Items[i].DoneAt = nil
		if err := save(s); err != nil {
			return nil, err
		}
	}
	return view.Text{Body: fmt.Sprintf("re-opened: %s", s.Items[i].Title)}, nil
}

func runToggle(_ context.Context, req plugin.Request) (view.View, error) {
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
	it := &s.Items[i]
	became := "a to-do"
	if it.Todo {
		became = "a note"
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would make note %d %s: %s", id, became, it.Title)}, nil
	}
	it.Todo = !it.Todo
	if !it.Todo {
		it.Done = false
		it.DoneAt = nil
	}
	if err := save(s); err != nil {
		return nil, err
	}
	return view.Text{Body: fmt.Sprintf("note %d is now %s: %s", id, became, it.Title)}, nil
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
	parent := s.Items[i].Parent
	// Sub-notes move up to the removed note's parent — never silently
	// orphaned or deleted along with it.
	reparented := 0
	for j := range s.Items {
		if s.Items[j].Parent == id {
			s.Items[j].Parent = parent
			reparented++
		}
	}
	s.Items = append(s.Items[:i], s.Items[i+1:]...)
	if err := save(s); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("removed note %d: %s", id, removed)
	if reparented > 0 {
		msg += fmt.Sprintf(" (%d sub-note(s) moved up)", reparented)
	}
	return view.Text{Body: msg}, nil
}
