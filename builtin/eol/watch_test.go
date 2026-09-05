package eol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// reqFor builds a resolved request for the named capability, the way the
// host would — req above serves eol.check; these serve the other two.
func reqFor(t *testing.T, id string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Request{}
}

// newCatalogueServer answers /products with a small catalogue and
// /products/<name> with the canonical bodies, so one server serves every
// capability the way the real API would.
func newCatalogueServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/products":
			fmt.Fprint(w, `{"result":[
				{"name":"postgresql","label":"PostgreSQL","category":"database","aliases":["postgres","pg"],"tags":["database"]},
				{"name":"nodejs","label":"Node.js","category":"lang","aliases":["node"],"tags":["javascript-runtime","lang"]},
				{"name":"debian","label":"Debian","category":"os","aliases":[],"tags":["linux-distribution","os"]}]}`)
		case "/products/postgresql":
			fmt.Fprint(w, canonicalPostgresBody)
		case "/products/nodejs":
			fmt.Fprint(w, canonicalNodejsBody)
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "<html>not found</html>")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func asTable(t *testing.T, v view.View, err error) view.Table {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("view is %T, want view.Table", v)
	}
	return tbl
}

func watchTable(t *testing.T, base string, values map[string]any) view.Table {
	t.Helper()
	v, err := runWatchAt(context.Background(), reqFor(t, "eol.watch", values), base)
	return asTable(t, v, err)
}

func productsTable(t *testing.T, base string, values map[string]any) view.Table {
	t.Helper()
	v, err := runProductsAt(context.Background(), reqFor(t, "eol.products", values), base)
	return asTable(t, v, err)
}

// --- eol.watch ---

func TestWatchGradesEveryEntryAndPinsACycleWhenNamed(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := watchTable(t, srv.URL, map[string]any{"products": []any{"postgresql/18", "nodejs"}})

	// postgresql pinned to one cycle is one row; nodejs unpinned is every
	// cycle the fixture carries.
	if len(tbl.Rows) < 3 {
		t.Fatalf("got %d rows, want the pinned postgresql row plus every nodejs cycle:\n%v", len(tbl.Rows), tbl.Rows)
	}
	if tbl.Rows[0][0] != "postgresql" || tbl.Rows[0][1] != "18" {
		t.Errorf("first row = %v, want postgresql cycle 18", tbl.Rows[0])
	}
	for _, r := range tbl.Rows[1:] {
		if r[0] != "nodejs" {
			t.Errorf("row %v is not a nodejs cycle", r)
		}
	}
	if tbl.Total != len(tbl.Rows) {
		t.Errorf("Total = %d, rows = %d", tbl.Total, len(tbl.Rows))
	}
}

func TestWatchKeepsAnUnknownProductAsARowRatherThanFailingTheCall(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := watchTable(t, srv.URL, map[string]any{"products": []any{"nosuchthing", "postgresql/18"}})

	if len(tbl.Rows) != 2 {
		t.Fatalf("got %d rows, want 2:\n%v", len(tbl.Rows), tbl.Rows)
	}
	if tbl.Rows[0][0] != "nosuchthing" || tbl.Rows[0][7] != "not found" {
		t.Errorf("unknown product row = %v, want Status \"not found\"", tbl.Rows[0])
	}
	if tbl.Rows[1][0] != "postgresql" {
		t.Errorf("the entry after the typo was not graded: %v", tbl.Rows[1])
	}
}

func TestWatchReportsACycleTheProductDoesNotHave(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := watchTable(t, srv.URL, map[string]any{"products": []any{"postgresql/9.6"}})

	if len(tbl.Rows) != 1 || tbl.Rows[0][1] != "9.6" || tbl.Rows[0][7] != "no such cycle" {
		t.Errorf("rows = %v, want one row for cycle 9.6 with Status \"no such cycle\"", tbl.Rows)
	}
}

// The list the help text and the docs point at — `plugins: eol: products:` —
// reaches the capability through the same Config path as every other
// config-backed input. It did not, once: the input never declared a key, so
// a configured list was silently ignored and the call refused as empty.
func TestWatchReadsTheListFromTheConfigWhenNobodyPassedOne(t *testing.T) {
	srv := newCatalogueServer(t)
	var c plugin.Capability
	for _, cap := range Plugin().Capabilities {
		if cap.ID == "eol.watch" {
			c = cap
		}
	}
	req := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{
		Config: map[string]any{"products": []any{"postgresql/18"}},
	}), false, false)
	v, err := runWatchAt(context.Background(), req, srv.URL)
	tbl := asTable(t, v, err)
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "postgresql" {
		t.Errorf("rows = %v, want the configured product graded", tbl.Rows)
	}
}

func TestWatchRefusesAnEmptyListWithTheConfigHint(t *testing.T) {
	srv := newCatalogueServer(t)
	_, err := runWatchAt(context.Background(), reqFor(t, "eol.watch", nil), srv.URL)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "eol.watch.empty" {
		t.Fatalf("err = %v, want eol.watch.empty", err)
	}
	if !strings.Contains(verr.Hint, "plugins: eol: products:") {
		t.Errorf("hint = %q, want it to show the config key", verr.Hint)
	}
}

func TestWatchCapsTheListAndSaysSo(t *testing.T) {
	srv := newCatalogueServer(t)
	var many []any
	for i := 0; i <= maxWatch; i++ {
		many = append(many, "postgresql")
	}
	_, err := runWatchAt(context.Background(), reqFor(t, "eol.watch", map[string]any{"products": many}), srv.URL)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "eol.watch.toomany" {
		t.Fatalf("err = %v, want eol.watch.toomany", err)
	}
}

func TestWatchFailsTheWholeCallWhenTheAPIIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := runWatchAt(context.Background(),
		reqFor(t, "eol.watch", map[string]any{"products": []any{"postgresql"}}), srv.URL)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "eol.request.status" {
		t.Fatalf("err = %v, want eol.request.status — a 500 is about the call, not one product", err)
	}
}

// --- eol.products ---

func TestProductsMatchesAnAliasCaseInsensitively(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := productsTable(t, srv.URL, map[string]any{"term": "PG"})
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "postgresql" {
		t.Errorf("rows = %v, want postgresql alone, found through its alias", tbl.Rows)
	}
	if !strings.Contains(tbl.Rows[0][2], "pg") {
		t.Errorf("aliases cell = %q, want it to carry the alias that matched", tbl.Rows[0][2])
	}
}

func TestProductsListsEverythingWithoutATerm(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := productsTable(t, srv.URL, nil)
	if len(tbl.Rows) != 3 || tbl.Total != 3 {
		t.Errorf("rows = %v, want the whole three-entry catalogue", tbl.Rows)
	}
}

func TestProductsNarrowsByCategory(t *testing.T) {
	srv := newCatalogueServer(t)
	tbl := productsTable(t, srv.URL, map[string]any{"category": "os"})
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "debian" {
		t.Errorf("rows = %v, want debian alone", tbl.Rows)
	}
}

func TestProductsSaysWhenNothingMatches(t *testing.T) {
	srv := newCatalogueServer(t)
	_, err := runProductsAt(context.Background(), reqFor(t, "eol.products", map[string]any{"term": "zzz"}), srv.URL)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "eol.products.none" {
		t.Fatalf("err = %v, want eol.products.none", err)
	}
}

func TestFetchCatalogueReportsAnUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, verr := fetchCatalogue(context.Background(), srv.Client(), srv.URL)
	if verr == nil || verr.Code != "eol.request.status" {
		t.Fatalf("verr = %v, want eol.request.status", verr)
	}
}
