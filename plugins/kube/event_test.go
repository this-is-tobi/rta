package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func eventFrom(t *testing.T, body string) eventItem {
	t.Helper()
	var e eventItem
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// The core/v1 events endpoint serves two generations of the same object,
// because the API server translates the newer events.k8s.io shape rather than
// migrating it. A real cluster's answer is a mix, and reading only the legacy
// firstTimestamp/lastTimestamp pair is the easiest way to get this wrong: the
// newer rows do not error, they decode to the zero time — and being the newer
// shape they are disproportionately the recent ones, so the failure hides
// exactly what somebody opened the view to see.
//
// Walked as a table over every shape rather than spot-checking one, because
// the fallback chain has to be right at each link, not just at the end.
func TestEventTimesAreResolvedThroughEveryShapeTheAPIServes(t *testing.T) {
	cases := []struct {
		name            string
		body            string
		wantFirst       string
		wantLast        string
		wantOccurrences int
	}{
		{
			name: "legacy shape",
			body: `{"metadata":{"creationTimestamp":"2026-08-01T00:00:00Z"},
				"firstTimestamp":"2026-08-20T10:00:00Z","lastTimestamp":"2026-09-01T10:00:00Z","count":42}`,
			wantFirst: "2026-08-20T10:00:00Z", wantLast: "2026-09-01T10:00:00Z", wantOccurrences: 42,
		},
		{
			name: "newer series shape, null legacy timestamps",
			body: `{"metadata":{"creationTimestamp":"2026-08-01T00:00:00Z"},
				"firstTimestamp":null,"lastTimestamp":null,"eventTime":"2026-08-25T09:00:00Z",
				"series":{"count":7,"lastObservedTime":"2026-09-01T11:00:00Z"}}`,
			wantFirst: "2026-08-25T09:00:00Z", wantLast: "2026-09-01T11:00:00Z", wantOccurrences: 7,
		},
		{
			name: "eventTime only, no series",
			body: `{"metadata":{"creationTimestamp":"2026-08-01T00:00:00Z"},
				"firstTimestamp":null,"lastTimestamp":null,"eventTime":"2026-08-30T08:00:00Z"}`,
			wantFirst: "2026-08-30T08:00:00Z", wantLast: "2026-08-30T08:00:00Z", wantOccurrences: 1,
		},
		{
			name: "nothing but the object's own creation time",
			body: `{"metadata":{"creationTimestamp":"2026-08-15T12:00:00Z"},
				"firstTimestamp":null,"lastTimestamp":null}`,
			wantFirst: "2026-08-15T12:00:00Z", wantLast: "2026-08-15T12:00:00Z", wantOccurrences: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := eventFrom(t, c.body)
			wantFirst, err := time.Parse(time.RFC3339, c.wantFirst)
			if err != nil {
				t.Fatal(err)
			}
			wantLast, err := time.Parse(time.RFC3339, c.wantLast)
			if err != nil {
				t.Fatal(err)
			}
			if !e.firstAt().Equal(wantFirst) {
				t.Errorf("firstAt = %v, want %v", e.firstAt(), wantFirst)
			}
			if !e.lastAt().Equal(wantLast) {
				t.Errorf("lastAt = %v, want %v", e.lastAt(), wantLast)
			}
			if got := e.occurrences(); got != c.wantOccurrences {
				t.Errorf("occurrences = %d, want %d", got, c.wantOccurrences)
			}
		})
	}
}

// The consequence of the above, stated as the property that actually matters:
// a newer-shape event must not sort below an older legacy-shape one. Reading
// only lastTimestamp puts every newer row at the zero time, which is the
// bottom of a descending sort — so the rows most likely to be the reason
// somebody ran this end up underneath everything else.
func TestNewerShapeEventsDoNotSortToTheBottom(t *testing.T) {
	recent := eventFrom(t, `{"metadata":{"name":"recent","creationTimestamp":"2026-08-01T00:00:00Z"},
		"type":"Warning","firstTimestamp":null,"lastTimestamp":null,
		"series":{"count":3,"lastObservedTime":"2026-09-01T12:00:00Z"}}`)
	old := eventFrom(t, `{"metadata":{"name":"old"},"type":"Warning",
		"firstTimestamp":"2026-08-01T00:00:00Z","lastTimestamp":"2026-08-01T00:00:00Z","count":1}`)

	if !recent.lastAt().After(old.lastAt()) {
		t.Fatalf("the newer-shape event resolved to %v, which is not after the legacy one at %v",
			recent.lastAt(), old.lastAt())
	}
}

// An event that exists happened at least once. A zero in that column would
// read as a broken cluster rather than as a field this plugin failed to find.
func TestOccurrencesNeverReportsZero(t *testing.T) {
	e := eventFrom(t, `{"metadata":{"name":"e"},"count":0}`)
	if got := e.occurrences(); got != 1 {
		t.Errorf("occurrences = %d, want 1", got)
	}
}

// A message with a newline in it breaks the alignment of every renderer that
// draws a table. Collapsing whitespace is a formatting fix and must not become
// a content one: the message is frequently the whole diagnosis, so every word
// has to survive.
func TestMessagesAreFlattenedWithoutLosingAnything(t *testing.T) {
	const msg = "0/9 nodes are available:\n  3 Insufficient cpu,\n  6 node(s) had untolerated taint."
	got := oneLine(msg)
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("oneLine left a line break in %q", got)
	}
	for _, word := range []string{"0/9", "Insufficient", "cpu,", "untolerated", "taint."} {
		if !strings.Contains(got, word) {
			t.Errorf("oneLine dropped %q from the message: %q", word, got)
		}
	}
	if strings.Contains(got, "  ") {
		t.Errorf("oneLine left a run of spaces in %q", got)
	}
}

// The involved object is the column an operator scans, and an event whose
// involvedObject has no kind must not render as a bare "/name".
func TestObjectRendersWithoutAStraySeparator(t *testing.T) {
	withKind := eventFrom(t, `{"involvedObject":{"kind":"Pod","name":"api-0"}}`)
	if got := withKind.object(); got != "Pod/api-0" {
		t.Errorf("object = %q, want Pod/api-0", got)
	}
	noKind := eventFrom(t, `{"involvedObject":{"name":"api-0"}}`)
	if got := noKind.object(); got != "api-0" {
		t.Errorf("object = %q, want a bare name with no separator", got)
	}
}
