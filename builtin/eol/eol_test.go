package eol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// req builds a resolved request the way the host would, mirroring the pg
// plugin's own helper of the same name.
func req(t *testing.T, values map[string]any) plugin.Request {
	t.Helper()
	c := Plugin().Capabilities[0]
	return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
}

// --- eolStatus / humanUntil: the grading boundary ---

func TestEolStatusTrustsIsEolOverAFarFutureDate(t *testing.T) {
	// A malformed or stale eolFrom next to isEol:true should not talk the
	// status back down to "ok" — isEol is the API's own verdict, and this
	// plugin does not second-guess it (see gradeRow's doc).
	future := time.Now().AddDate(5, 0, 0).Format("2006-01-02")
	r := release{IsEol: true, EolFrom: &future}
	if got := eolStatus(r, 90, time.Now()); got != "EOL" {
		t.Errorf("isEol:true with a far-future eolFrom graded %q, want EOL", got)
	}
}

func TestEolStatusGradesNoAnnouncedDateAsOk(t *testing.T) {
	r := release{IsEol: false, EolFrom: nil}
	if got := eolStatus(r, 90, time.Now()); got != "ok" {
		t.Errorf("no eolFrom graded %q, want ok", got)
	}
}

func TestEolStatusIgnoresAnUnparseableDate(t *testing.T) {
	bad := "not-a-date"
	r := release{IsEol: false, EolFrom: &bad}
	if got := eolStatus(r, 90, time.Now()); got != "ok" {
		t.Errorf("unparseable eolFrom graded %q, want the safe fallback ok", got)
	}
}

// The boundary itself: eolStatus compares with Before, so a date exactly
// warnDays out is not yet inside the window and one moment closer is —
// matching builtin/cert's own "the case the two callers disagreed on" test.
func TestEolStatusBoundaryIsExclusive(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	exactly := now.AddDate(0, 0, 90).Format("2006-01-02")
	oneCloser := now.AddDate(0, 0, 89).Format("2006-01-02")

	if got := eolStatus(release{EolFrom: &exactly}, 90, now); got != "ok" {
		t.Errorf("exactly warnDays out graded %q, want ok", got)
	}
	if got := eolStatus(release{EolFrom: &oneCloser}, 90, now); got != "WARN <90d" {
		t.Errorf("one day inside the window graded %q, want WARN <90d", got)
	}
}

func TestEolStatusGradesAFarFutureDateAsOk(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	farOut := now.AddDate(3, 0, 0).Format("2006-01-02")
	if got := eolStatus(release{EolFrom: &farOut}, 90, now); got != "ok" {
		t.Errorf("three years out graded %q, want ok", got)
	}
}

func TestHumanUntilFormatsAFutureDateInDays(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := humanUntil(now.AddDate(0, 0, 42), now); got != "42d" {
		t.Errorf("got %q, want 42d", got)
	}
}

// Status already carries the word EOL, so the "In" column for a past date
// must not repeat it — this is the one behavior that deliberately differs
// from builtin/cert's humanUntil ("expired %dd ago").
func TestHumanUntilFormatsAPastDateWithoutTheWordExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := humanUntil(now.AddDate(0, 0, -10), now)
	if got != "10d ago" {
		t.Errorf("got %q, want %q", got, "10d ago")
	}
	if strings.Contains(got, "expired") {
		t.Errorf("got %q, redundant with the Status column", got)
	}
}

func TestHumanUntilFormatsASameDayFutureInHours(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if got := humanUntil(now.Add(6*time.Hour), now); got != "6h" {
		t.Errorf("got %q, want 6h", got)
	}
}

// --- findRelease / cycleNames ---

func TestFindReleaseMatchesCaseInsensitively(t *testing.T) {
	releases := []release{{Name: "bookworm"}, {Name: "bullseye"}}
	r, found := findRelease(releases, "BOOKWORM")
	if !found || r.Name != "bookworm" {
		t.Errorf("got %+v, %v; want bookworm, true", r, found)
	}
}

func TestFindReleaseReportsNotFound(t *testing.T) {
	if _, found := findRelease([]release{{Name: "15"}}, "999"); found {
		t.Error("999 should not have matched")
	}
}

func TestCycleNamesJoinsInOrder(t *testing.T) {
	releases := []release{{Name: "18"}, {Name: "17"}, {Name: "16"}}
	if got := cycleNames(releases); got != "18, 17, 16" {
		t.Errorf("got %q", got)
	}
}

// --- gradeRow: the full row shape ---

func TestGradeRowShowsNotAnnouncedWhenThereIsNoEolFromDate(t *testing.T) {
	r := release{Name: "26", ReleaseDate: "2025-09-15"}
	r.Latest.Name = "26.6.2"
	row := gradeRow(r, 90, time.Now())
	want := []string{"26", "2025-09-15", "26.6.2", "-", "not announced", "-", "ok"}
	for i := range want {
		if row[i] != want[i] {
			t.Errorf("column %d: got %q, want %q (row=%v)", i, row[i], want[i], row)
		}
	}
}

func TestGradeRowShowsTheDateAndDurationWhenAnnounced(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	eol := "2027-11-11"
	r := release{Name: "15", ReleaseDate: "2022-10-13", EolFrom: &eol, IsLts: true}
	r.Latest.Name = "15.19"
	row := gradeRow(r, 90, now)
	if row[3] != "yes" {
		t.Errorf("LTS column: got %q, want yes", row[3])
	}
	if row[4] != "2027-11-11" {
		t.Errorf("EOL column: got %q", row[4])
	}
	if row[5] == "-" || row[5] == "" {
		t.Errorf("In column should be a computed duration, got %q", row[5])
	}
	if row[6] != "ok" {
		t.Errorf("Status column: got %q, want ok", row[6])
	}
}

// --- ltsCell: the three states, matching nodejs's own "Current" phase ---

func TestLtsCellReportsYesWhenIsLtsIsTrue(t *testing.T) {
	from := "2020-01-01"
	// IsLts wins even against an LtsFrom that looks stale — same
	// trust-the-verdict call as eolStatus makes for IsEol.
	if got := ltsCell(release{IsLts: true, LtsFrom: &from}); got != "yes" {
		t.Errorf("got %q, want yes", got)
	}
}

func TestLtsCellReportsTheScheduledDateWhenNotLtsYet(t *testing.T) {
	from := "2026-10-28"
	if got := ltsCell(release{IsLts: false, LtsFrom: &from}); got != "from 2026-10-28" {
		t.Errorf("got %q, want %q", got, "from 2026-10-28")
	}
}

func TestLtsCellReportsADashWhenLtsWillNeverApply(t *testing.T) {
	if got := ltsCell(release{IsLts: false, LtsFrom: nil}); got != "-" {
		t.Errorf("got %q, want -", got)
	}
}

// --- fetchProduct: the HTTP + decode + error-classification layer ---

// A trimmed, real response shape (curled from the live API), so this test
// also catches a wrong json tag — a struct-to-struct round trip through the
// plugin's own (possibly wrong) tags could not.
const canonicalPostgresBody = `{
  "schema_version": "1.2.1",
  "result": {
    "name": "postgresql",
    "label": "PostgreSQL",
    "releases": [
      {"name": "18", "releaseDate": "2025-09-25", "isEol": false, "eolFrom": "2030-11-14",
       "isMaintained": true, "latest": {"name": "18.6", "date": "2026-08-11"}},
      {"name": "13", "releaseDate": "2020-09-24", "isEol": true, "eolFrom": "2025-11-13",
       "isMaintained": false, "latest": {"name": "13.23", "date": "2025-11-10"}}
    ]
  }
}`

func TestFetchProductParsesTheRealResponseShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, canonicalPostgresBody)
	}))
	defer srv.Close()

	result, verr := fetchProduct(context.Background(), srv.Client(), srv.URL, "postgresql")
	if verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	if len(result.Releases) != 2 {
		t.Fatalf("got %d releases, want 2", len(result.Releases))
	}
	if result.Releases[0].Name != "18" || result.Releases[0].Latest.Name != "18.6" {
		t.Errorf("release[0] decoded wrong: %+v", result.Releases[0])
	}
	if result.Releases[1].EolFrom == nil || *result.Releases[1].EolFrom != "2025-11-13" {
		t.Errorf("release[1].eolFrom decoded wrong: %+v", result.Releases[1])
	}
}

// nodejs, not postgresql: it is the real product where isLts and ltsFrom
// disagree in the interesting way (curled from the live API), because
// releases spend time in a "Current" phase before graduating to LTS.
const canonicalNodejsBody = `{
  "schema_version": "1.2.1",
  "result": {
    "name": "nodejs",
    "releases": [
      {"name": "26", "releaseDate": "2026-05-05", "isEol": false, "eolFrom": "2029-04-30",
       "isLts": false, "ltsFrom": "2026-10-28", "latest": {"name": "26.1.0"}},
      {"name": "24", "releaseDate": "2025-05-06", "isEol": false, "eolFrom": "2028-04-30",
       "isLts": true, "ltsFrom": "2025-10-28", "latest": {"name": "24.9.0"}},
      {"name": "25", "releaseDate": "2025-10-15", "isEol": true, "eolFrom": "2026-06-01",
       "isLts": false, "ltsFrom": null, "latest": {"name": "25.2.0"}}
    ]
  }
}`

func TestFetchProductDecodesTheNotYetLtsCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, canonicalNodejsBody)
	}))
	defer srv.Close()

	result, verr := fetchProduct(context.Background(), srv.Client(), srv.URL, "nodejs")
	if verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	r26 := result.Releases[0]
	if r26.IsLts || r26.LtsFrom == nil || *r26.LtsFrom != "2026-10-28" {
		t.Errorf("release 26 decoded wrong: %+v", r26)
	}
	if got := ltsCell(r26); got != "from 2026-10-28" {
		t.Errorf("release 26 LTS cell: got %q", got)
	}
	r24 := result.Releases[1]
	if !r24.IsLts {
		t.Errorf("release 24 decoded wrong: %+v", r24)
	}
	if got := ltsCell(r24); got != "yes" {
		t.Errorf("release 24 LTS cell: got %q", got)
	}
	r25 := result.Releases[2]
	if r25.IsLts || r25.LtsFrom != nil {
		t.Errorf("release 25 decoded wrong: %+v", r25)
	}
	if got := ltsCell(r25); got != "-" {
		t.Errorf("release 25 LTS cell: got %q", got)
	}
}

func TestFetchProductClassifiesA404AsProductNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<html>not found</html>") // the real API's 404 body is HTML, not JSON
	}))
	defer srv.Close()

	_, verr := fetchProduct(context.Background(), srv.Client(), srv.URL, "not-a-real-product")
	if verr == nil {
		t.Fatal("expected an error")
	}
	if verr.Code != "eol.product.notfound" {
		t.Errorf("code = %q, want eol.product.notfound", verr.Code)
	}
	if verr.Hint == "" {
		t.Error("expected a hint pointing somewhere useful")
	}
}

func TestFetchProductClassifiesA500AsARequestStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, verr := fetchProduct(context.Background(), srv.Client(), srv.URL, "postgresql")
	if verr == nil || verr.Code != "eol.request.status" {
		t.Errorf("got %v, want code eol.request.status", verr)
	}
}

func TestFetchProductRejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	_, verr := fetchProduct(context.Background(), srv.Client(), srv.URL, "postgresql")
	if verr == nil || verr.Code != "eol.response.invalid" {
		t.Errorf("got %v, want code eol.response.invalid", verr)
	}
}

// A product string containing "/" must not be able to turn one path
// segment into two — checked by inspecting exactly what the server saw.
func TestFetchProductEscapesTheProductNameInThePath(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.EscapedPath()
		fmt.Fprint(w, `{"result":{"releases":[]}}`)
	}))
	defer srv.Close()

	if _, verr := fetchProduct(context.Background(), srv.Client(), srv.URL, "a/b"); verr != nil {
		t.Fatalf("unexpected error: %v", verr)
	}
	if strings.Contains(seenPath, "/a/b") || !strings.Contains(seenPath, "%2F") {
		t.Errorf("server saw path %q — the slash was not escaped", seenPath)
	}
}

// --- runCheckAt: the whole capability, wired end to end, no network ---

func newEolServer(t *testing.T, releases string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"result":{"name":"demo","releases":[%s]}}`, releases)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunCheckAtListsEveryCycleWhenNoneIsNamed(t *testing.T) {
	srv := newEolServer(t, `
		{"name":"3","releaseDate":"2024-01-01","isEol":false,"latest":{"name":"3.2"}},
		{"name":"2","releaseDate":"2023-01-01","isEol":true,"eolFrom":"2025-01-01","latest":{"name":"2.9"}}`)

	v, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo"}), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("got %T, want view.Table", v)
	}
	if table.Total != 2 || len(table.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 (table=%+v)", len(table.Rows), table)
	}
}

func TestRunCheckAtFiltersToTheNamedCycle(t *testing.T) {
	srv := newEolServer(t, `
		{"name":"3","releaseDate":"2024-01-01","isEol":false,"latest":{"name":"3.2"}},
		{"name":"2","releaseDate":"2023-01-01","isEol":true,"eolFrom":"2025-01-01","latest":{"name":"2.9"}}`)

	v, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "cycle": "2"}), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	table := v.(view.Table)
	if table.Total != 1 || table.Rows[0][0] != "2" {
		t.Fatalf("got %+v, want exactly the cycle named 2", table)
	}
}

func TestRunCheckAtHintsAtTheAvailableCyclesWhenTheNamedOneIsMissing(t *testing.T) {
	srv := newEolServer(t, `{"name":"3","releaseDate":"2024-01-01","latest":{"name":"3.2"}}`)

	_, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "cycle": "999"}), srv.URL)
	if err == nil {
		t.Fatal("expected an error for an unknown cycle")
	}
	verr := view.AsError(err, "eol.test")
	if verr.Code != "eol.cycle.notfound" {
		t.Errorf("code = %q", verr.Code)
	}
	if !strings.Contains(verr.Hint, "3") {
		t.Errorf("hint %q should name the cycle that does exist", verr.Hint)
	}
}

// warn-days has to reach eolStatus through the request, not just through a
// direct call to eolStatus itself — this is the wiring a unit test on
// eolStatus alone cannot see.
func TestRunCheckAtPassesWarnDaysThroughToTheStatusColumn(t *testing.T) {
	eol := time.Now().AddDate(0, 0, 40).Format("2006-01-02")
	srv := newEolServer(t, fmt.Sprintf(
		`{"name":"1","releaseDate":"2020-01-01","isEol":false,"eolFrom":%q,"latest":{"name":"1.0"}}`, eol))

	narrow, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "warn-days": 10}), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := narrow.(view.Table).Rows[0][6]; got != "ok" {
		t.Errorf("warn-days=10 against a 40-day-out release: got %q, want ok", got)
	}

	wide, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "demo", "warn-days": 90}), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := wide.(view.Table).Rows[0][6]; got != "WARN <90d" {
		t.Errorf("warn-days=90 against a 40-day-out release: got %q, want WARN <90d", got)
	}
}
