package eol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The selector reaches eol.check through the cycle argument: a range grades
// every numbered cycle inside it, in the API's own order.
func TestRunCheckAtGradesEveryCycleInARange(t *testing.T) {
	srv := newEolServer(t, `
		{"name":"4","releaseDate":"2025-01-01","isEol":false,"latest":{"name":"4.0"}},
		{"name":"3","releaseDate":"2024-01-01","isEol":false,"latest":{"name":"3.2"}},
		{"name":"2","releaseDate":"2023-01-01","isEol":true,"eolFrom":"2025-01-01","latest":{"name":"2.9"}}`)

	v, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "cycle": "..3"}), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	table := v.(view.Table)
	if table.Total != 2 || table.Rows[0][0] != "3" || table.Rows[1][0] != "2" {
		t.Fatalf("got %+v, want cycles 3 and 2 for ..3", table.Rows)
	}
}

func TestRunCheckAtRefusesARangeNothingFallsInside(t *testing.T) {
	srv := newEolServer(t, `{"name":"3","releaseDate":"2024-01-01","latest":{"name":"3.2"}}`)

	_, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "cycle": "10..12"}), srv.URL)
	verr := view.AsError(err, "eol.test")
	if verr == nil || verr.Code != "eol.cycle.notfound" {
		t.Fatalf("got %v, want eol.cycle.notfound", err)
	}
	if !strings.Contains(verr.Hint, "3") {
		t.Errorf("hint %q should name the cycle that does exist", verr.Hint)
	}
}

func TestRunCheckAtRefusesAMalformedSelectorBeforeGrading(t *testing.T) {
	srv := newEolServer(t, `{"name":"3","releaseDate":"2024-01-01","latest":{"name":"3.2"}}`)

	_, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "cycle": "4..3"}), srv.URL)
	verr := view.AsError(err, "eol.test")
	if verr == nil || verr.Code != "eol.cycle.selector" {
		t.Fatalf("got %v, want eol.cycle.selector", err)
	}
}

// A watch entry's `@` part takes the same selector, so one entry can pin a
// product to the cycles a platform actually runs rather than one or all.
func TestWatchGradesARangeOfCyclesFromOneEntry(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := watchTable(t, srv.URL, map[string]any{"products": []any{"nodejs@24..25", "postgresql@..13"}})

	if len(tbl.Rows) != 3 {
		t.Fatalf("got %d rows, want nodejs 24 and 25 plus postgresql 13:\n%v", len(tbl.Rows), tbl.Rows)
	}
	if tbl.Rows[0][0] != "nodejs" || tbl.Rows[0][1] != "24" || tbl.Rows[1][1] != "25" {
		t.Errorf("nodejs rows = %v, want cycles 24 then 25 in the API's order", tbl.Rows[:2])
	}
	if tbl.Rows[2][0] != "postgresql" || tbl.Rows[2][1] != "13" {
		t.Errorf("postgresql row = %v, want cycle 13", tbl.Rows[2])
	}
}

func TestWatchReportsARangeNothingFallsInsideAsARow(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := watchTable(t, srv.URL, map[string]any{"products": []any{"postgresql@14..17"}})

	if len(tbl.Rows) != 1 || tbl.Rows[0][1] != "14..17" || tbl.Rows[0][7] != "no such cycle" {
		t.Errorf("rows = %v, want one row for 14..17 with Status \"no such cycle\"", tbl.Rows)
	}
}

// A list written for the release that spelled entries product/cycle is
// refused with the new spelling, before any request: graded as it stood it
// was one "not found" row per entry, which reads as a typo in the product's
// name rather than as the grammar having moved.
func TestWatchRefusesTheOldProductSlashCycleSpellingWithTheNewOne(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := runWatchAt(context.Background(),
		reqFor(t, "eol.watch", map[string]any{"products": []any{"nodejs", "postgresql/15"}}), srv.URL)
	verr := view.AsError(err, "eol.test")
	if verr == nil || verr.Code != "eol.watch.entry" {
		t.Fatalf("got %v, want eol.watch.entry", err)
	}
	if !strings.Contains(verr.Message, "product/cycle") || verr.Hint != "write it as postgresql@15" {
		t.Errorf("refusal = %q / hint %q, want the old spelling named and the entry rewritten", verr.Message, verr.Hint)
	}
	if requests != 0 {
		t.Errorf("%d requests were made before the list was refused", requests)
	}
}

// A malformed entry is refused before anything is fetched, so the whole
// list is checked and the refusal names the part that is wrong.
func TestWatchRefusesAMalformedSelectorBeforeAnyRequest(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := runWatchAt(context.Background(),
		reqFor(t, "eol.watch", map[string]any{"products": []any{"postgresql@18", "nodejs@26..24"}}), srv.URL)
	verr := view.AsError(err, "eol.test")
	if verr == nil || verr.Code != "eol.cycle.selector" {
		t.Fatalf("got %v, want eol.cycle.selector", err)
	}
	if requests != 0 {
		t.Errorf("%d requests were made before the list was refused", requests)
	}
}
