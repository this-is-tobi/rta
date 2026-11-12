package note

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The Due cell says which day as well as how soon, with the grade first so the
// renderer still colours it, and writes the year only when it is not this one.
func TestDueCellNamesTheDay(t *testing.T) {
	now := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	at := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &v
	}
	for _, tc := range []struct {
		name string
		due  *time.Time
		done bool
		want string
	}{
		{"none", nil, false, ""},
		{"overdue", at(2026, 9, 1), false, "OVERDUE · Sep 1"},
		{"today", at(2026, 10, 5), false, "WARN today · Oct 5"},
		{"soon", at(2026, 10, 6), false, "WARN soon · Oct 6"},
		{"later this year", at(2026, 12, 25), false, "ok · Dec 25"},
		{"next year", at(2027, 1, 4), false, "ok · Jan 4 2027"},
		{"done keeps its day", at(2026, 10, 2), true, "done · Oct 2"},
	} {
		if got := dueCell(tc.due, tc.done, now); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// **The day a --due was read as is said back.** "10-20" typed in November is
// next year's, and the receipt said only "added note 4": the person learned
// the day from the list. Adding, editing and their dry runs name it.
func TestADueDateIsSaidBackAsTheDayItWasReadAs(t *testing.T) {
	setup(t)
	const want = " — due Tue Jan 15 2030"
	for _, call := range []struct {
		h      func(context.Context, plugin.Request) (view.View, error)
		values map[string]any
		dryRun bool
	}{
		{runAdd, map[string]any{"title": "renew the domain", "due": "2030-01-15"}, true},
		{runAdd, map[string]any{"title": "renew the domain", "due": "2030-01-15"}, false},
		{runEdit, map[string]any{"id": 1, "due": "2030-01-15"}, true},
		{runEdit, map[string]any{"id": 1, "due": "2030-01-15"}, false},
	} {
		if got := text(t, call.h, call.values, call.dryRun); !strings.HasSuffix(got, want) {
			t.Errorf("%v (dry run %v) answered %q, want it to end %q", call.values, call.dryRun, got, want)
		}
	}
	if got := text(t, runEdit, map[string]any{"id": 1, "title": "renew it"}, false); strings.Contains(got, "due") {
		t.Errorf("an edit that set no date answered %q", got)
	}
}

// Every form of a date the field's help lists is accepted by the verb that
// takes it, and the refusal for one that is not carries the same list.
func TestAddTakesTheDueFormsItsHelpLists(t *testing.T) {
	setup(t)
	for i, form := range []string{"mon", "+3d", "3d", "1w", "10-20", "next week", "tomorrow", "2099-01-01"} {
		text(t, runAdd, map[string]any{"title": form, "due": form}, false)
		if s, _ := load(); s.Items[i].Due == nil || !s.Items[i].Todo {
			t.Errorf("%q: not taken as a due date: %+v", form, s.Items[i])
		}
	}
	_, err := runAdd(context.Background(), req(map[string]any{"title": "x", "due": "whenever"}, false))
	if ve := view.AsError(err, "x"); ve.Code != "note.add.baddue" || !strings.Contains(ve.Message, "+3d") {
		t.Errorf("a due date that is none = %+v", ve)
	}
	for _, c := range Plugin().Capabilities {
		if c.ID != "note.add" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "due" && !strings.Contains(f.Help, "+3d") {
				t.Errorf("the due field's help = %q, want the grammar", f.Help)
			}
		}
	}
}
