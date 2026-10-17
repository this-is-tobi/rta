package eolapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func date(s string) *string { return &s }

func TestGradeTrustsIsEolOverAFarFutureDate(t *testing.T) {
	r := Release{IsEol: true, EolFrom: date("2099-01-01")}
	if got := Grade(r, 90, time.Now()); got != Ended {
		t.Errorf("Grade = %v, want Ended: the API's own verdict wins over the date", got)
	}
}

func TestGradeCallsANotAnnouncedDateSupported(t *testing.T) {
	if got := Grade(Release{}, 90, time.Now()); got != Supported {
		t.Errorf("Grade = %v, want Supported for a cycle with no date announced", got)
	}
}

func TestGradeWarnsInsideTheWindowAndNotOutsideIt(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	inside := Release{EolFrom: date("2026-10-01")}
	outside := Release{EolFrom: date("2027-10-01")}
	if got := Grade(inside, 90, now); got != Ending {
		t.Errorf("11 days out with a 90-day window: %v, want Ending", got)
	}
	if got := Grade(outside, 90, now); got != Supported {
		t.Errorf("a year out with a 90-day window: %v, want Supported", got)
	}
	if got := Grade(inside, 5, now); got != Supported {
		t.Errorf("11 days out with a 5-day window: %v, want Supported", got)
	}
}

func TestEolDateReportsFalseForAShapeItCannotRead(t *testing.T) {
	if _, ok := EolDate(Release{EolFrom: date("next year")}); ok {
		t.Error("an unparseable date was reported as one")
	}
	if d, ok := EolDate(Release{EolFrom: date("2027-02-28")}); !ok || d.Year() != 2027 {
		t.Errorf("EolDate = %v, %v", d, ok)
	}
}

func TestFetchProductClassifiesA404AsProductNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<html>not found</html>")
	}))
	defer srv.Close()

	_, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "nosuchthing")
	if verr == nil || verr.Code != "eol.product.notfound" {
		t.Fatalf("got %v, want eol.product.notfound — judged by status before any decode", verr)
	}
}

func TestFetchProductClassifiesA500AsARequestStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "postgresql")
	if verr == nil || verr.Code != "eol.request.status" {
		t.Fatalf("got %v, want eol.request.status", verr)
	}
}

// endoflife.date answers 429 with a Retry-After when a client asks for many
// products in a row, which is what audit.kube.eol does. One more try, after the
// wait it names, is what a polite client does; a second refusal is the answer.
func TestRetriesOnceOn429(t *testing.T) {
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		if asked == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"result":{"name":"x","releases":[]}}`)
	}))
	defer srv.Close()

	p, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "x")
	if verr != nil || p == nil {
		t.Fatalf("FetchProduct = %v, %v; want the page the second request returned", p, verr)
	}
	if asked != 2 {
		t.Errorf("asked %d times, want 2", asked)
	}
}

func TestA429ThatKeepsComingIsRetriedOnceAndThenReported(t *testing.T) {
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "x")
	if verr == nil || verr.Code != "eol.request.status" || !strings.Contains(verr.Message, "429") {
		t.Fatalf("got %v, want eol.request.status naming the 429", verr)
	}
	if asked != 2 {
		t.Errorf("asked %d times, want 2: one retry and no more", asked)
	}
}

// The wait is inside the request's own budget. A Retry-After of an hour is not
// waited for: the call would be cancelled at the request timeout, a transport
// error where the server said something plainer.
func TestARetryAfterBeyondTheRequestBudgetIsNotWaitedFor(t *testing.T) {
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	start := time.Now()
	_, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "x")
	if verr == nil || verr.Code != "eol.request.status" {
		t.Fatalf("got %v, want eol.request.status", verr)
	}
	if asked != 1 || time.Since(start) > requestTimeout/2 {
		t.Errorf("asked %d times in %s: a wait that does not fit is not made", asked, time.Since(start))
	}
}

func TestRetryDelayReadsSecondsADateAndNothing(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for header, want := range map[string]time.Duration{
		"":                              defaultRetryDelay,
		"0":                             0,
		"3":                             3 * time.Second,
		"-5":                            defaultRetryDelay,
		"soon":                          defaultRetryDelay,
		"Sat, 17 Oct 2026 12:00:07 GMT": 7 * time.Second,
		"Sat, 17 Oct 2026 11:00:00 GMT": 0,
	} {
		if got := retryDelay(header, now); got != want {
			t.Errorf("retryDelay(%q) = %s, want %s", header, got, want)
		}
	}
}

func TestFetchProductRejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result": {"name": "x", "releases": [`)
	}))
	defer srv.Close()

	_, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "x")
	if verr == nil || verr.Code != "eol.response.invalid" {
		t.Fatalf("got %v, want eol.response.invalid", verr)
	}
}

func TestFetchProductEscapesTheProductIntoOnePathSegment(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.EscapedPath()
		fmt.Fprint(w, `{"result":{"name":"x","releases":[]}}`)
	}))
	defer srv.Close()

	if _, verr := FetchProduct(context.Background(), srv.Client(), srv.URL, "Post/Gres"); verr != nil {
		t.Fatal(verr)
	}
	if asked != "/products/post%2Fgres" {
		t.Errorf("asked %q, want the product lowercased and kept to one path segment", asked)
	}
}

func TestFetchCatalogueDecodesAliases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result":[{"name":"postgresql","label":"PostgreSQL","category":"database","aliases":["postgres","pg"]}]}`)
	}))
	defer srv.Close()

	entries, verr := FetchCatalogue(context.Background(), srv.Client(), srv.URL)
	if verr != nil {
		t.Fatal(verr)
	}
	if len(entries) != 1 || len(entries[0].Aliases) != 2 || entries[0].Aliases[0] != "postgres" {
		t.Errorf("entries = %+v", entries)
	}
}
