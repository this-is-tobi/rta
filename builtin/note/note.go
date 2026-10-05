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
	"github.com/this-is-tobi/rta/pkg/format"
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
// Due dates are read by itemstore.ParseDue and said once, as itemstore.DueForms;
// what is offered here are the forms worth reaching for, not the grammar.
var (
	tagField = plugin.Field{Name: "tag", Type: plugin.StringSlice,
		Help:    "tags — one per entry",
		Suggest: suggestTags}
	dueField = plugin.Field{Name: "due", Type: plugin.String,
		Help:    "due date: " + itemstore.DueForms,
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
					"rest in the order they were written. Checked-off notes are hidden unless `all`. " +
					"Shows top-level notes by default; `parent` lists one note's sub-notes. " +
					"With `detail`: adds creation date and a body preview.",
				Inputs: []plugin.Field{
					{Name: "all", Type: plugin.Bool, Config: "all", Help: "include checked-off notes"},
					{Name: "tag", Type: plugin.StringSlice, Help: "only notes with any of these tags",
						Suggest: suggestTags},
					{Name: "parent", Type: plugin.Int, Default: 0, Help: "list sub-notes of this note id instead of top-level notes",
						Suggest: suggestAnyID},
				},
				// One notebook, two kinds of thing in it, and `t` is the switch between
				// them: a note becomes a to-do with a checkbox, a to-do goes back to being
				// a note. Not bare — a one-key mutation is reserved for the fail-safe
				// direction — but its only input is the id the row supplies, so nothing is
				// left to ask and it runs on the keypress all the same. `o` is the undo for
				// `d`, one key away from it: checking off the wrong note is a one-keystroke
				// mistake and should cost one keystroke to take back. `A` shows the
				// checked-off notes the list hides, without which `o` could never find a
				// row to act on.
				Actions: []plugin.Action{
					{Key: "enter", Label: "show", Target: "note.show", Source: plugin.ActionRow},
					{Key: "a", Label: "add", Target: "note.add"},
					{Key: "u", Label: "update", Target: "note.edit", Source: plugin.ActionRow},
					{Key: "t", Label: "to-do/note", Target: "note.toggle", Source: plugin.ActionRow},
					{Key: "d", Label: "done", Target: "note.done", Source: plugin.ActionRow},
					{Key: "o", Label: "re-open", Target: "note.reopen", Source: plugin.ActionRow},
					{Key: "x", Label: "remove", Target: "note.rm", Source: plugin.ActionRow},
				},
				Toggles: []plugin.Toggle{{Key: "A", Label: "show done", Input: "all"}},
				Run:     runList,
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
				// The detail page acts on the note it is already showing.
				Actions: []plugin.Action{
					{Key: "u", Label: "update", Target: "note.edit", Source: plugin.ActionSelf},
					{Key: "t", Label: "to-do/note", Target: "note.toggle", Source: plugin.ActionSelf},
					{Key: "d", Label: "done", Target: "note.done", Source: plugin.ActionSelf},
					{Key: "o", Label: "re-open", Target: "note.reopen", Source: plugin.ActionSelf},
					{Key: "x", Label: "remove", Target: "note.rm", Source: plugin.ActionSelf},
					{Key: "a", Label: "add", Target: "note.add"},
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
				Flash: true,
				Description: "A title is enough. `todo` (or a due date, which implies it) makes it " +
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
				Flash:  true,
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
				Flash: true,
				Description: "Empty fields keep their current value. A `tag` of - clears all tags; " +
					"a `due` of none clears the due date.",
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
				ID: "note.done", Summary: "Check notes off", Safety: plugin.Write, Idempotent: true,
				Flash: true,
				Description: "Done for a task, filed for a note: either way it leaves the default " +
					"list and stays findable through note.list's `all`, and in note.search. Several " +
					"ids check off several notes in one call, all or none: an id that is not a note " +
					"refuses the call before anything changes. A plain note is made a to-do and checked " +
					"off in the same call, since asking to be done with it says what it is.",
				Inputs: []plugin.Field{
					// Checking off is something you do to an open note, so the
					// done ones stay out of the way here.
					{Name: "id", Type: plugin.StringSlice, Positional: true, Required: true,
						Help: "note id, or several", Suggest: suggestOpenIDs},
				},
				Run: runDone,
			},
			{
				ID: "note.reopen", Summary: "Reopen checked notes", Safety: plugin.Write, Idempotent: true,
				Flash: true,
				Description: "The undo for `note.done`, for one id or several. Re-opening an " +
					"already-open note is a no-op, not an error.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.StringSlice, Positional: true, Required: true,
						Help: "note id, or several", Suggest: suggestDoneIDs},
				},
				Run: runReopen,
			},
			{
				ID: "note.rm", Summary: "Remove notes permanently", Safety: plugin.Destructive,
				Flash: true,
				Scope: "id",
				Description: "Sub-notes are re-parented to the removed note's parent, never deleted silently. " +
					"Several ids remove several notes in one call, all or none, each one a record a grant " +
					"or a consent has to cover.",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.StringSlice, Positional: true, Required: true,
						Help: "note id, or several", Suggest: suggestAnyID},
				},
				Run: runRemove,
			},
		},
	}
}

func load() (itemstore.Store, error) { return itemstore.Load(storeFile, ns) }
func save(s itemstore.Store) error   { return itemstore.Save(storeFile, ns, s) }
func index(s itemstore.Store, id int) (int, bool) {
	for i := range s.Items {
		if s.Items[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

// reaches reports whether walking up from note p — p, its parent, that
// note's parent — arrives at note id: whether p is id or sits somewhere under
// it. Bounded by the number of notes, so a store that already holds a cycle
// cannot hold the walk in it.
func reaches(s itemstore.Store, p, id int) bool {
	for steps := 0; p != 0 && steps <= len(s.Items); steps++ {
		if p == id {
			return true
		}
		i, ok := index(s, p)
		if !ok {
			return false
		}
		p = s.Items[i].Parent
	}
	return false
}

// rootless reports whether a note's parents never reach the top — a cycle, or
// a parent that is not there — so no listing would show it under anything.
// Notes in that state are listed at the top: an edit could make a cycle
// before it refused one, and a note shown nowhere reads as a note deleted.
func rootless(s itemstore.Store, it itemstore.Item) bool {
	p := it.Parent
	for steps := 0; p != 0; steps++ {
		i, ok := index(s, p)
		if !ok || steps > len(s.Items) {
			return true
		}
		p = s.Items[i].Parent
	}
	return false
}

// find is index for a call that cannot go on without the note, refused in
// the words of the surface asking, which is who has to find the right id.
func find(sf plugin.Surface, s itemstore.Store, id int) (int, *view.Error) {
	if i, ok := index(s, id); ok {
		return i, nil
	}
	return 0, view.Errorf("note.notfound", "no note with id %d", id).
		WithHint(sf.CapabilityWith("note.list", "all") + " lists every note")
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

// dueCell is a note's Due cell: its grade and the day it falls on. The grade
// leads because a renderer colours a status cell by its first word
// (theme.ClassifyStatus), and the day follows because a grade alone ("WARN
// soon") never said which one — the list had to be opened note by note to find
// out. A year is written only when it is not this one.
func dueCell(due *time.Time, done bool, now time.Time) string {
	grade := itemstore.DueStatus(due, done, now)
	if grade == "" {
		return ""
	}
	layout := "Jan 2"
	if due.Year() != now.Year() {
		layout = "Jan 2 2006"
	}
	return grade + " · " + due.Format(layout)
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
		// Not a note left as its own parent under itself: it is one of the
		// rootless, listed at the top, and never its own sub-note.
		under := (it.Parent == parent && it.ID != parent) || (parent == 0 && it.Parent != 0 && rootless(s, it))
		if !under || (it.Done && !includeDone) || !hasAnyTag(it, tags) {
			continue
		}
		shown = append(shown, it)
	}
	listOrder(shown)
	for _, it := range shown {
		row := []string{
			strconv.Itoa(it.ID), statusOf(it), dueCell(it.Due, it.Done, now),
			itemstore.Age(it.Created), titleCell(s, it, detail),
		}
		if detail {
			row = append(row, strings.Join(itemstore.CleanTags(it.Tags), ", "), it.Created.Format("2006-01-02 15:04"), itemstore.Preview(it.Body))
		}
		t.Rows = append(t.Rows, row)
	}
	t.Total = len(t.Rows)
	// The table even when nothing is listed, and the sentence beside it for a
	// screen: see view.Table.Empty.
	if len(t.Rows) == 0 {
		sf := req.Surface()
		t.Empty = "Nothing here yet — " + sf.CapabilityName("note.add") + " adds one"
		if parent != 0 {
			t.Empty = fmt.Sprintf("Note %d has no sub-notes yet — `%s` adds one", parent,
				sf.Call("note.add", titleArg, plugin.Arg{Name: "parent", Value: parent}))
		}
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
		if it.Parent != 0 && it.Parent != it.ID {
			title = "↳ " + title // a quiet nod that this is a sub-note
		}
		t.Rows = append(t.Rows, []string{
			strconv.Itoa(it.ID), statusOf(it), title, strings.Join(itemstore.CleanTags(it.Tags), ", "),
		})
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		t.Empty = fmt.Sprintf("No notes match %q", query)
	}
	return t, nil
}

func runTags(_ context.Context, req plugin.Request) (view.View, error) {
	s, err := load()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, it := range s.Items {
		for _, tag := range itemstore.CleanTags(it.Tags) {
			counts[tag]++
		}
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	t := view.Table{Columns: []view.Column{{Name: "Tag"}, {Name: "Notes", Kind: view.KindNumber}}}
	if len(names) == 0 {
		t.Empty = "No tags yet — `" + req.Surface().Call("note.add", titleArg,
			plugin.Arg{Name: "tag", Value: "<name>"}) + "` adds one"
	}
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
func showSections(sf plugin.Surface, s itemstore.Store, it itemstore.Item) view.Sections {
	sec := view.Sections{Items: []view.Section{
		{ID: "note", Title: "note", View: metaPairs(s, it)},
		{ID: "content", Title: "content", View: contentView(sf, it)},
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
	if own := itemstore.CleanTags(it.Tags); len(own) > 0 {
		tags := make([]string, len(own))
		for i, tg := range own {
			tags[i] = "#" + tg
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "tags", Value: strings.Join(tags, " ")})
	}
	if it.Parent != 0 && it.Parent != it.ID {
		if pi, ok := index(s, it.Parent); ok {
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
// it, rather than leaving a blank band on a page dedicated to one note, in
// the words of the surface showing it.
//
// Beside the body, not as it (view.Text.Empty): the sentence was the body,
// so -o json handed a script "This note is empty — ..." as the note's
// content, text nobody wrote in it.
func contentView(sf plugin.Surface, it itemstore.Item) view.Text {
	if strings.TrimSpace(it.Body) == "" {
		return view.Text{Empty: "This note is empty — `" + sf.Call("note.edit", idArg(it.ID),
			plugin.Arg{Name: "body", Value: "<body>"}) + "` writes it"}
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
		if i, ok := index(s, id); ok {
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
	i, verr := find(req.Surface(), s, req.Int("id"))
	if verr != nil {
		return nil, verr
	}
	return showSections(req.Surface(), s, s.Items[i]), nil
}

// applyTags interprets the --tag convention shared by add/edit: a single
// "-" clears, anything else replaces. Absent (nil) means "no change" to
// callers that check len(raw) > 0 first.
func applyTags(raw []string) []string {
	if len(raw) == 1 && raw[0] == "-" {
		return nil
	}
	return itemstore.CleanTags(raw)
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
		if _, ok := index(s, parent); !ok {
			return nil, view.Errorf("note.add.badparent", "parent note %d does not exist", parent).
				WithHint(req.Surface().CapabilityName("note.list") + " lists the notes there are")
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
	i, verr := find(req.Surface(), s, req.Int("id"))
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

// titleArg and idArg are the positional inputs a call note's pages hand over
// takes: a title the reader writes in, and the note it is about.
var titleArg = plugin.Arg{Name: "title", Value: "<title>", Positional: true}

func idArg(id int) plugin.Arg { return plugin.Arg{Name: "id", Value: id, Positional: true} }

// inputNames is each of names as the caller on sf gives it.
func inputNames(sf plugin.Surface, names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = sf.InputName(n)
	}
	return out
}

func runEdit(_ context.Context, req plugin.Request) (view.View, error) {
	title := strings.TrimSpace(req.String("title"))
	body := req.String("body")
	rawTags := req.StringSlice("tag")
	rawDue := req.String("due")
	parent := req.Int("parent")
	if title == "" && body == "" && len(rawTags) == 0 && rawDue == "" && parent == noParentChange {
		return nil, view.Errorf("note.edit.nochange", "nothing to change").
			WithHint("give the new content in one or more of " + strings.Join(inputNames(req.Surface(),
				"title", "body", "tag", "due", "parent"), ", "))
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
	i, verr := find(req.Surface(), s, id)
	if verr != nil {
		return nil, verr
	}
	if parent != noParentChange {
		if parent == id {
			return nil, view.Errorf("note.edit.selfparent", "a note cannot be its own parent")
		}
		if parent != 0 {
			if _, ok := index(s, parent); !ok {
				return nil, view.Errorf("note.edit.badparent", "parent note %d does not exist", parent)
			}
		}
		// Nor under one of its own sub-notes, however far down: the two
		// would each sit under the other, and a list shows what hangs from
		// the top, so both vanished from every listing — read by anybody as
		// deleted.
		if reaches(s, parent, id) {
			sf := req.Surface()
			return nil, view.Errorf("note.edit.cycle",
				"note %d is under note %d, so note %d cannot go under it", parent, id, id).
				WithHint("move note " + strconv.Itoa(parent) + " out first — `" +
					sf.Call("note.edit", idArg(parent), plugin.Arg{Name: "parent", Value: 0}) +
					"` puts it at the top")
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

// namedIDs reads the ids a verb that takes several was given. Each is the
// number as strconv writes it and nothing else — no "#3", no padding, no sign,
// no leading zero — so that the record a grant or a consent was judged on
// (internal/grant reads the value as it arrived) is the note the handler then
// acts on, and a spelling the gate has never seen is refused rather than read
// as a note it was not about: a grant for "3" would otherwise not cover "03",
// which would still remove note 3. Repeats are one note.
func namedIDs(sf plugin.Surface, req plugin.Request) ([]int, *view.Error) {
	var ids []int
	seen := map[int]bool{}
	for _, raw := range req.StringSlice("id") {
		n, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(n) != raw || n < 1 {
			hint := "an id is a whole number — " + sf.CapabilityWith("note.list", "all") + " shows them"
			if strings.ContainsAny(raw, ", ") {
				// "1,2" is the way several are written in most places, and the gate
				// judges each id on its own, so they are separate arguments here.
				hint = "several ids are separate arguments, not one joined: `" +
					sf.Call("note.done", plugin.Arg{Name: "id", Value: []string{"1", "2"}, Positional: true}) + "`"
			}
			return nil, view.Errorf("note.id.invalid", "%q is not a note id", raw).WithHint(hint)
		}
		if !seen[n] {
			seen[n] = true
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 {
		return nil, view.Errorf("note.id.none", "no note id given").
			WithHint("name the note, or several: " + sf.Call("note.done", plugin.Arg{Name: "id", Value: []string{"3", "4"}, Positional: true}))
	}
	return ids, nil
}

// findAll is find for every id, before anything is changed: a call over several
// notes is all or none, so one that is not there refuses it whole.
func findAll(sf plugin.Surface, s itemstore.Store, ids []int) *view.Error {
	for _, id := range ids {
		if _, verr := find(sf, s, id); verr != nil {
			return verr
		}
	}
	return nil
}

func runDone(_ context.Context, req plugin.Request) (view.View, error) {
	ids, verr := namedIDs(req.Surface(), req)
	if verr != nil {
		return nil, verr
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
	if verr := findAll(req.Surface(), s, ids); verr != nil {
		return nil, verr
	}
	var lines []string
	now := time.Now()
	for _, id := range ids {
		i, _ := index(s, id)
		it := &s.Items[i]
		converted := !it.Todo
		if req.DryRun {
			line := fmt.Sprintf("would check off note %d: %s", id, it.Title)
			if converted {
				line += " (a note, so it becomes a to-do first)"
			}
			lines = append(lines, line)
			continue
		}
		// A note asked to be done is a to-do: either the person meant it as
		// one, or "done with this" is the only thing they said about it.
		it.Todo = true
		if !it.Done {
			it.Done = true
			it.DoneAt = &now
		}
		line := "done: " + it.Title
		if converted {
			line += " (it was a note, now a checked-off to-do)"
		}
		lines = append(lines, line)
	}
	if req.DryRun {
		return view.Text{Body: strings.Join(lines, "\n")}, nil
	}
	if err := save(s); err != nil {
		return nil, err
	}
	return view.Text{Body: strings.Join(lines, "\n")}, nil
}

func runReopen(_ context.Context, req plugin.Request) (view.View, error) {
	ids, verr := namedIDs(req.Surface(), req)
	if verr != nil {
		return nil, verr
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
	if verr := findAll(req.Surface(), s, ids); verr != nil {
		return nil, verr
	}
	var lines []string
	changed := false
	for _, id := range ids {
		i, _ := index(s, id)
		it := &s.Items[i]
		if req.DryRun {
			lines = append(lines, fmt.Sprintf("would re-open note %d: %s", id, it.Title))
			continue
		}
		if it.Done {
			it.Done = false
			it.DoneAt = nil
			changed = true
		}
		lines = append(lines, "re-opened: "+it.Title)
	}
	if changed {
		if err := save(s); err != nil {
			return nil, err
		}
	}
	return view.Text{Body: strings.Join(lines, "\n")}, nil
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
	i, verr := find(req.Surface(), s, id)
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
	ids, verr := namedIDs(req.Surface(), req)
	if verr != nil {
		return nil, verr
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
	if verr := findAll(req.Surface(), s, ids); verr != nil {
		return nil, verr
	}
	var lines []string
	for _, id := range ids {
		i, _ := index(s, id)
		if req.DryRun {
			lines = append(lines, fmt.Sprintf("would remove note %d: %s", id, s.Items[i].Title))
			continue
		}
		removed, reparented := removeNote(&s, id)
		line := fmt.Sprintf("removed note %d: %s", id, removed)
		if reparented > 0 {
			line += " (" + format.CountOf(reparented, "sub-note") + " moved up)"
		}
		lines = append(lines, line)
	}
	if !req.DryRun {
		if err := save(s); err != nil {
			return nil, err
		}
	}
	return view.Text{Body: strings.Join(lines, "\n")}, nil
}

// removeNote takes note id out of s and hands its sub-notes to its parent. The
// note is looked up afresh each time, so a call removing several can name a
// note and its sub-note in either order.
func removeNote(s *itemstore.Store, id int) (title string, reparented int) {
	i, _ := index(*s, id)
	title = s.Items[i].Title
	parent := s.Items[i].Parent
	// A note an old rm left as its own parent has no parent to hand its
	// sub-notes up to but itself, which is going: they go to the top, or
	// they were left under a note that was no longer there.
	if parent == id {
		parent = 0
	}
	// Sub-notes move up to the removed note's parent — never silently
	// orphaned or deleted along with it.
	for j := range s.Items {
		// Not the note removed, which an old rm may have left as its own
		// parent: it goes, and is no sub-note moved up.
		if s.Items[j].Parent == id && j != i {
			s.Items[j].Parent = parent
			// A store an edit put in a cycle before edit refused one: the
			// removed note's parent can be its own sub-note, and moving
			// that sub-note up would make it its own parent, or one of a
			// smaller cycle. The top is where it is seen.
			if reaches(*s, parent, s.Items[j].ID) {
				s.Items[j].Parent = 0
			}
			reparented++
		}
	}
	s.Items = append(s.Items[:i], s.Items[i+1:]...)
	return title, reparented
}
