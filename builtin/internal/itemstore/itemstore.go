// Package itemstore is the local store behind the note built-in: numbered
// items with a title and an optional markdown body, kept as plain JSON under
// the XDG data dir, written atomically. It predates the merge of the todo and
// note built-ins, which is why it is its own package rather than a file in
// note — and a second built-in over the same shape is still one import away.
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

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/view"
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
	Parent int        `json:"parent,omitempty"`
	Due    *time.Time `json:"due,omitempty"`
	// Todo is the checkbox: a note with one is something to do, and Done is
	// whether it has been. A note without one is just a note — never done,
	// never hidden, and a toggle away from becoming a task.
	Todo    bool       `json:"todo,omitempty"`
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

// Load reads the store in file (e.g. "notes.json"). ns namespaces error codes
// ("note" → note.store.corrupt).
func Load(file, ns string) (Store, error) {
	path := filepath.Join(dataDir(), file)
	var s Store
	data, err := atomicfile.ReadFile(path)
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

const (
	lockStale   = 5 * time.Second
	lockRetry   = 10 * time.Millisecond
	lockTimeout = 2 * time.Second
)

// Lock serializes a read-modify-write cycle against file, across goroutines
// and processes both.
//
// Load and Save are each atomic on their own — Save never leaves a
// half-written file, and a concurrent Load sees the whole old content or
// the whole new content, never a torn mix — but that says nothing about two
// callers each doing Load, then deciding what to write, then Save: both can
// read the same starting state, both compute their own change against it,
// and the second Save simply overwrites the first with no error to either
// caller, whichever change was made. Every write handler must hold this for
// the whole of its own load-decide-save, not just call Load and Save
// separately — this is not automatic, which is exactly how the race got in
// (grant.Mutate exists in internal/grant for the identical reason, after
// the identical bug: MCP dispatches every tools/call in its own goroutine,
// so two calls racing is the ordinary case, not an exotic one).
func Lock(file string) (release func(), err error) {
	path := filepath.Join(dataDir(), file+".lock")
	release, err = filelock.Acquire(path, lockStale, lockRetry, lockTimeout)
	if err != nil {
		return nil, fmt.Errorf("acquiring the %s lock: %w", file, err)
	}
	return release, nil
}

// Save writes the store atomically, so a crash mid-write cannot leave a
// half-written task list behind. 0600: it is one user's notes.
func Save(file, ns string, s Store) error {
	dir, err := paths.EnsureData()
	if err != nil {
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

// CleanTags normalizes each tag and drops the empty ones and the repeats,
// keeping the order they were first given in.
//
// Normalizing alone left `--tag ops --tag OPS` as two entries that were one
// tag: the note listed it twice and every count that walked its tags counted
// the note twice. It is the one place a list of tags becomes the tags of a
// note, and what a counter reads from a store written before it existed.
func CleanTags(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		n := NormalizeTag(t)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// refRe matches a cross-reference to another item: "#12". Deliberately plain
// digits only — no namespace — since every item in a store shares one ID
// space and a reference is always "the other item with this number".
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

// DueForms is the one statement of what a due date can be said as, for every
// place that has to say it: a field's help, a refusal, the chapter. The forms
// are the ones ParseDue reads and nothing more, so a form added there is added
// here in the same edit, and TestDueFormsAreAllRead keeps the two honest.
const DueForms = "today, tomorrow, a weekday (fri or friday), +3d, 2w, next-week, 10-20 or yyyy-mm-dd"

// dueShorthands are the natural-language forms accepted alongside RFC3339
// and "2006-01-02" — the same instinct as GitHub's date fields, kept tiny.
var dueShorthands = map[string]func(time.Time) time.Time{
	"today":     func(t time.Time) time.Time { return t },
	"tomorrow":  func(t time.Time) time.Time { return t.AddDate(0, 0, 1) },
	"nextweek":  func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
	"next-week": func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
}

var (
	// offsetRe is a number of days or weeks from today: 3d, +3d, 2w. Four
	// digits at most — ten years of days — so a typo cannot ask for a date the
	// calendar cannot hold.
	offsetRe = regexp.MustCompile(`^\+?(\d{1,4})([dw])$`)
	// monthDayRe is a date without its year, in the order an ISO date has them:
	// 10-20 is the twentieth of October, never the tenth of the twentieth month.
	monthDayRe = regexp.MustCompile(`^(\d{1,2})-(\d{1,2})$`)
)

// ParseDue parses a due-date input, in the forms DueForms lists. A weekday is
// the next one on or after today, today included; a month and day without a
// year is the next one on or after today, in this year or the next ones.
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
	if m := offsetRe.FindStringSubmatch(key); m != nil {
		n, _ := strconv.Atoi(m[1])
		if m[2] == "w" {
			n *= 7
		}
		d := dateOnly(now.AddDate(0, 0, n))
		return &d, nil
	}
	if m := monthDayRe.FindStringSubmatch(key); m != nil {
		if d, ok := nextMonthDay(now, m[1], m[2]); ok {
			return &d, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", raw, now.Location()); err == nil {
		return &t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("unrecognized due date %q (try %s)", raw, DueForms)
}

// nextMonthDay is the first date on or after today that falls on month and day.
// It looks a few years ahead, because the 29th of February is a real date in
// one year of four; a pair that no year holds (13-40) is not a date.
func nextMonthDay(now time.Time, month, day string) (time.Time, bool) {
	mo, _ := strconv.Atoi(month)
	dy, _ := strconv.Atoi(day)
	today := dateOnly(now)
	for year := now.Year(); year <= now.Year()+4; year++ {
		d := time.Date(year, time.Month(mo), dy, 0, 0, 0, 0, now.Location())
		if int(d.Month()) != mo || d.Day() != dy {
			continue // 31-04, or a 29th of February in a year without one
		}
		if !d.Before(today) {
			return d, true
		}
	}
	return time.Time{}, false
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// weekdays are the names a day can be given by: the whole word, its three
// letters, and the few short forms people type by habit.
var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "sun": time.Sunday,
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
}

func weekday(key string) (time.Weekday, bool) {
	d, ok := weekdays[key]
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

// Children returns the direct sub-items of parentID, in store order — the
// breakdown of a task into steps, GitHub-tasklist style.
//
// Never the item itself. A note rm from before cycles were refused moved a
// sub-note up to a parent that was the sub-note, and a store keeps such an
// item as its own parent: counted among its own sub-items, it read "(0/1)".
func Children(s Store, parentID int) []Item {
	var out []Item
	for _, it := range s.Items {
		if it.Parent == parentID && it.ID != parentID {
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

// Age renders how long ago t was, compactly: "now", "5m", "3h", "2d".
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
		for _, t := range CleanTags(it.Tags) {
			count[t]++
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
