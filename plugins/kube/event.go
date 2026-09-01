package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.event.list: what the cluster has been complaining about, and for how
// long.
//
// The obvious version of this capability is a bad one, and it is worth saying
// why before reading the code, because every decision below is a reaction to
// it. `kubectl get events` sorted by time gives a wall of rows dominated by
// whichever operator is chattiest — on a real cluster, hundreds of rows where
// the great majority are Normal churn from one storage controller reconciling
// happily. That view is technically complete and practically unreadable, and
// somebody who runs it once does not run it again.
//
// Two facts about the Events API make a better view possible, and both are
// counter-intuitive enough that the naive version gets them wrong.
//
// **An Event is not a log line, it is a counter.** A recurring problem does
// not append a new Event; the existing one is *updated*, its count
// incremented and its last-seen time refreshed. So the interesting payload is
// firstTimestamp and count together — an event first seen eleven days ago with
// thirteen thousand occurrences is a completely different signal from a one-off
// thirty seconds ago, and last-seen alone renders the two identically.
//
// **The retention window runs from last-seen, not from first-seen.** The
// default one-hour TTL is measured against the moment the event was last
// updated, so a problem that recurs steadily is never collected: its
// firstTimestamp can be days or weeks old. "Events only go back an hour" is
// true only for problems that stopped.

// eventItem is a core/v1 Event.
//
// The timestamp fields are the awkward part and are all carried deliberately.
// The original Event type used firstTimestamp/lastTimestamp/count; the newer
// events.k8s.io shape uses eventTime plus a series{count,lastObservedTime}
// block, and the core/v1 endpoint serves *both*, because the API server
// translates rather than migrating. So a real cluster's answer is a mix, and a
// large minority of rows carry a null lastTimestamp. Reading only the legacy
// pair is the single easiest way to get this wrong: those rows do not error,
// they decode to the zero time, sort to the very bottom, and vanish under
// everything else — and being the newer shape, they are disproportionately the
// recent ones.
type eventItem struct {
	Metadata       meta `json:"metadata"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	Reason         string    `json:"reason"`
	Message        string    `json:"message"`
	Type           string    `json:"type"`
	Count          int       `json:"count"`
	FirstTimestamp time.Time `json:"firstTimestamp"`
	LastTimestamp  time.Time `json:"lastTimestamp"`
	EventTime      time.Time `json:"eventTime"`
	Series         *struct {
		Count            int       `json:"count"`
		LastObservedTime time.Time `json:"lastObservedTime"`
	} `json:"series,omitempty"`
}

// firstAt and lastAt resolve the two times through every shape the API serves,
// falling back to the object's own creation timestamp — which is always set,
// and for a single-occurrence event is exactly when it happened.
func (e eventItem) firstAt() time.Time {
	if !e.FirstTimestamp.IsZero() {
		return e.FirstTimestamp
	}
	if !e.EventTime.IsZero() {
		return e.EventTime
	}
	return e.Metadata.CreationTimestamp
}

func (e eventItem) lastAt() time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp
	}
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		return e.Series.LastObservedTime
	}
	if !e.EventTime.IsZero() {
		return e.EventTime
	}
	return e.Metadata.CreationTimestamp
}

// occurrences is how many times this event has fired. Both shapes again, and
// the floor is 1 rather than 0: an event that exists happened at least once,
// and a "0" in that column would read as a bug in the cluster rather than in
// the field this plugin failed to find.
func (e eventItem) occurrences() int {
	if e.Count > 0 {
		return e.Count
	}
	if e.Series != nil && e.Series.Count > 0 {
		return e.Series.Count
	}
	return 1
}

// object names what the event is about, which is the column an operator scans.
func (e eventItem) object() string {
	if e.InvolvedObject.Kind == "" {
		return e.InvolvedObject.Name
	}
	return e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name
}

// oneLine flattens a message for a table cell.
//
// Not truncated, deliberately: an event's message is frequently the entire
// diagnosis — "0/9 nodes are available: 3 Insufficient cpu, 6 node(s) had
// untolerated taint" is the answer, and a clipped version of it sends somebody
// to `kubectl describe` for the half that was cut. Newlines are collapsed
// because a cell containing one breaks the alignment of every renderer that
// draws a table, which is a formatting concern rather than a content one.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func fetchEvents(ctx context.Context, s selection) (list[eventItem], *view.Error) {
	var out list[eventItem]
	if verr := getJSON(ctx, s, "events", &out); verr != nil {
		return out, verr
	}
	return out, nil
}

func runEventList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	events, verr := fetchEvents(ctx, s)
	if verr != nil {
		return nil, verr
	}
	items := events.Items
	// Warnings only unless asked otherwise. This is the difference between a
	// view somebody reads and one they close: Normal events are the routine
	// narration of a cluster working — pulled an image, scheduled a pod,
	// reconciled a volume — and on any cluster running an active operator they
	// outnumber the Warnings by an order of magnitude and say nothing. The
	// full set stays one flag away rather than being unavailable, because
	// "which Normal event stopped happening" is occasionally the question.
	if !req.Bool("normal") {
		kept := items[:0]
		for _, e := range items {
			if e.Type == "Warning" {
				kept = append(kept, e)
			}
		}
		items = kept
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].lastAt().After(items[j].lastAt())
	})
	return eventTable(items, s.AllNS), nil
}

func eventTable(events []eventItem, withNS bool) view.Table {
	cols := []view.Column{}
	if withNS {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	cols = append(cols,
		view.Column{Name: "last seen", Kind: view.KindDuration},
		view.Column{Name: "first seen", Kind: view.KindDuration},
		view.Column{Name: "count", Kind: view.KindNumber},
		view.Column{Name: "type", Kind: view.KindStatus},
		view.Column{Name: "object"},
		view.Column{Name: "reason"},
		view.Column{Name: "message"},
	)
	rows := make([][]string, 0, len(events))
	for _, e := range events {
		row := []string{}
		if withNS {
			row = append(row, e.Metadata.Namespace)
		}
		rows = append(rows, append(row,
			age(e.lastAt()), age(e.firstAt()), fmt.Sprintf("%d", e.occurrences()),
			e.Type, e.object(), e.Reason, oneLine(e.Message)))
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}
}
