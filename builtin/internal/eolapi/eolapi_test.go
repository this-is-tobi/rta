package eolapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
