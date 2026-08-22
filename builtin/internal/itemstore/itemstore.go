// Package itemstore is the shared local store behind the todo and note
// built-ins: numbered items with a title and an optional markdown body, kept
// as plain JSON under the XDG data dir, written atomically. One store, two
// plugins — todos add a done status on top, notes do not.
package itemstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Item is one stored record. Tags, parents and due dates are what turn a
// flat list into something usable past a dozen entries.
type Item struct {
	ID    int      `json:"id"`
	Title string   `json:"title"`
	Body  string   `json:"body,omitempty"`
	Tags  []string `json:"tags,omitempty"`
	// Parent is the enclosing item's ID, 0 for top level. Sub-items let a
	// task be broken down without inventing a second store.
	Parent  int        `json:"parent,omitempty"`
	Due     *time.Time `json:"due,omitempty"`
	Done    bool       `json:"done,omitempty"`
	Created time.Time  `json:"created"`
	DoneAt  *time.Time `json:"doneAt,omitempty"`
	// LegacyText decodes pre-body stores where the title lived under "text".
	// Load migrates it into Title; it is never written back.
	LegacyText string `json:"text,omitempty"`
}

// HasTag reports whether the item carries tag, case-insensitively.
func (i Item) HasTag(tag string) bool {
	tag = NormalizeTag(tag)
	for _, t := range i.Tags {
		if NormalizeTag(t) == tag {
			return true
		}
	}
	return false
}

// Matches reports whether the item satisfies a free-text query across title,
// body and tags — the cheat-style "find that thing I wrote down" search.
func (i Item) Matches(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	hay := strings.ToLower(i.Title + "\n" + i.Body + "\n" + strings.Join(i.Tags, " "))
	// Every whitespace-separated term must appear: narrowing, like a search bar.
	for _, term := range strings.Fields(q) {
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

// Store is the on-disk shape.
type Store struct {
	NextID int    `json:"nextId"`
	Items  []Item `json:"items"`
}

// DataDir resolves where local stores live. Exported so sibling built-ins
// with their own store shape (kv's encrypted blob is not an itemstore.Store)
// still share one directory and one env var.
func DataDir() string { return dataDir() }

func dataDir() string { return paths.Data() }

// Load reads the store in file (e.g. "todo.json"). ns namespaces error codes
// ("todo" → todo.store.corrupt).
func Load(file, ns string) (Store, error) {
	path := filepath.Join(dataDir(), file)
	var s Store
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Store{NextID: 1}, nil
	}
	if err != nil {
		return s, view.Errorf(ns+".store.unreadable", "reading %s: %v", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, view.Errorf(ns+".store.corrupt", "parsing %s: %v", path, err).
			WithHint("fix or remove the file; it is plain JSON")
	}
	if s.NextID < 1 {
		s.NextID = 1
	}
	// Migrate pre-body stores: "text" becomes the title, silently and once —
	// the next save persists the new shape.
	for i := range s.Items {
		if s.Items[i].Title == "" && s.Items[i].LegacyText != "" {
			s.Items[i].Title = s.Items[i].LegacyText
		}
		s.Items[i].LegacyText = ""
	}
	return s, nil
}

// Save writes the store atomically, so a crash mid-write cannot leave a
// half-written task list behind. 0600: it is one user's notes.
func Save(file, ns string, s Store) error {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf(ns+".store.mkdir", "creating %s: %v", dir, err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return view.Errorf(ns+".store.encode", "encoding store: %v", err)
	}
	if err := atomicfile.Write(filepath.Join(dir, file), data, 0o600); err != nil {
		return view.Errorf(ns+".store.write", "writing store: %v", err)
	}
	return nil
}

// NormalizeTag lowercases and strips a leading "#", so "#Backend", "backend"
// and "BACKEND" are the same tag.
func NormalizeTag(tag string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
}

// refRe matches a cross-reference to another item: "#12". Deliberately plain
// digits only — no namespace — since todo and note share one ID space per
// store file and a reference is always "the other item with this number".
var refRe = regexp.MustCompile(`#(\d+)`)

// References extracts the item IDs mentioned in text ("see #12 for context"),
// deduplicated, in first-seen order.
func References(text string) []int {
	seen := map[int]bool{}
	var out []int
	for _, m := range refRe.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// dueShorthands are the natural-language forms accepted alongside RFC3339
// and "2006-01-02" — the same instinct as GitHub's date fields, kept tiny.
var dueShorthands = map[string]func(time.Time) time.Time{
	"today":     func(t time.Time) time.Time { return t },
	"tomorrow":  func(t time.Time) time.Time { return t.AddDate(0, 0, 1) },
	"nextweek":  func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
	"next-week": func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
}

// ParseDue parses a due-date input: "today", "tomorrow", "next-week",
// "2026-08-25", or a weekday name ("friday", nearest one on or after today).
// Empty input clears the due date (returns nil, nil).
func ParseDue(raw string, now time.Time) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	key := strings.ToLower(strings.ReplaceAll(raw, " ", ""))
	if shift, ok := dueShorthands[key]; ok {
		d := dateOnly(shift(now))
		return &d, nil
	}
	if wd, ok := weekday(key); ok {
		d := dateOnly(nextWeekday(now, wd))
		return &d, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, now.Location()); err == nil {
		return &t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("unrecognized due date %q (try today, tomorrow, a weekday, or 2006-01-02)", raw)
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func weekday(key string) (time.Weekday, bool) {
	names := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
		"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
		"saturday": time.Saturday,
	}
	d, ok := names[key]
	return d, ok
}

func nextWeekday(from time.Time, target time.Weekday) time.Time {
	days := (int(target) - int(from.Weekday()) + 7) % 7
	return from.AddDate(0, 0, days)
}

// DueStatus grades a due date against now, in the shared status vocabulary.
func DueStatus(due *time.Time, done bool, now time.Time) string {
	if due == nil {
		return ""
	}
	if done {
		return "done"
	}
	d := dateOnly(*due).Sub(dateOnly(now))
	switch {
	case d < 0:
		return "OVERDUE"
	case d == 0:
		return "WARN today"
	case d <= 2*24*time.Hour:
		return "WARN soon"
	default:
		return "ok"
	}
}

// Preview condenses a markdown body to one scannable line — headings and
// list markers stripped, whitespace collapsed, clipped.
func Preview(body string) string {
	if body == "" {
		return ""
	}
	fields := strings.FieldsFunc(body, func(r rune) bool { return r == '\n' || r == '\r' })
	var parts []string
	for _, line := range fields {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>-*+ \t")
		if line != "" {
			parts = append(parts, line)
		}
	}
	out := strings.Join(parts, " · ")
	const max = 60
	r := []rune(out)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return out
}

// Age renders how long ago t was, compactly: "now", "5m", "3h", "2d".
// Children returns the direct sub-items of parentID, in store order — the
// breakdown of a task into steps, GitHub-tasklist style.
func Children(s Store, parentID int) []Item {
	var out []Item
	for _, it := range s.Items {
		if it.Parent == parentID {
			out = append(out, it)
		}
	}
	return out
}

// Progress reports done/total across an item's direct sub-items. total==0
// means the item has no sub-items.
func Progress(s Store, parentID int) (done, total int) {
	for _, it := range Children(s, parentID) {
		total++
		if it.Done {
			done++
		}
	}
	return done, total
}

func Age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

// --- Completion ---------------------------------------------------------
//
// Suggestions come from the store because that is where the answer is: the
// tags worth offering are the ones already in use, and the ids worth offering
// are the ones that exist. They are called on a keystroke, so a store that
// cannot be read yields nothing rather than an error — a completion that
// cannot answer should slow nobody down.

// SuggestTags returns the tags in use, most used first, then alphabetically.
// Frequency order matters: the tag you reach for is usually the one you
// reached for last time.
func SuggestTags(file, ns string) []string {
	s, err := Load(file, ns)
	if err != nil {
		return nil
	}
	count := map[string]int{}
	for _, it := range s.Items {
		for _, t := range it.Tags {
			count[NormalizeTag(t)]++
		}
	}
	tags := make([]string, 0, len(count))
	for t := range count {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool {
		if count[tags[i]] != count[tags[j]] {
			return count[tags[i]] > count[tags[j]]
		}
		return tags[i] < tags[j]
	})
	return tags
}

// SuggestIDs returns "id\ttitle" entries — the id is what gets typed, the
// title is what makes it the right id. openOnly drops completed items, which
// is what you want when completing something to work on and not when
// completing something to remove.
func SuggestIDs(file, ns string, openOnly bool) []string {
	s, err := Load(file, ns)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(s.Items))
	for _, it := range s.Items {
		if openOnly && it.Done {
			continue
		}
		out = append(out, fmt.Sprintf("%d\t%s", it.ID, it.Title))
	}
	return out
}
