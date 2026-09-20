package eol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The shape the live API answers for debian (curled while writing this): the
// cycle is the number, and the codename sits beside it in its own field.
const canonicalDebianBody = `{
  "schema_version": "1.2.1",
  "result": {
    "name": "debian",
    "label": "Debian",
    "releases": [
      {"name": "13", "codename": "Trixie", "label": "13 (Trixie)", "releaseDate": "2025-08-09", "isEol": false,
       "eolFrom": "2028-08-09", "latest": {"name": "13.1"}},
      {"name": "12", "codename": "Bookworm", "label": "12 (Bookworm)", "releaseDate": "2023-06-10", "isEol": false,
       "eolFrom": "2026-06-10", "latest": {"name": "12.13"}}
    ]
  }
}`

func TestFindReleaseMatchesACodenameInAnyCase(t *testing.T) {
	releases := []release{{Name: "13", Codename: "Trixie"}, {Name: "12", Codename: "Bookworm"}}
	r, found := findRelease(releases, "bookworm")
	if !found || r.Name != "12" {
		t.Errorf("findRelease(bookworm) = %+v, %v; want cycle 12", r, found)
	}
}

func TestFindReleasePrefersANameOverACodenameThatSpellsIt(t *testing.T) {
	releases := []release{{Name: "3", Codename: "2"}, {Name: "2", Codename: "1"}}
	if r, _ := findRelease(releases, "2"); r.Name != "2" {
		t.Errorf("findRelease(2) = %+v, want the cycle named 2, not the one codenamed 2", r)
	}
}

func TestRunCheckAtFindsACycleByItsCodename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, canonicalDebianBody)
	}))
	t.Cleanup(srv.Close)

	v, err := runCheckAt(context.Background(), req(t, map[string]any{"product": "debian", "cycle": "bookworm"}), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	table := v.(view.Table)
	if table.Total != 1 || table.Rows[0][0] != "12" {
		t.Errorf("rows = %v, want the one cycle codenamed Bookworm", table.Rows)
	}
}

func TestSuggestCyclesOffersCodenamesAfterTheNumbers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, canonicalDebianBody)
	}))
	t.Cleanup(srv.Close)

	got := suggestCyclesAt(context.Background(), req(t, map[string]any{"product": "debian"}), srv.URL)
	want := []string{"13", "12", "Trixie", "Bookworm"}
	if len(got) != len(want) {
		t.Fatalf("suggestions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suggestion %d = %q, want %q", i, got[i], want[i])
		}
	}
}
