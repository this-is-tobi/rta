package eol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A refusal of one input names it (view.Error.Input), so a form opens again
// on that box and an agent knows which argument to send again. A cycle typed
// into the product box as product@cycle is refused on the product box, which
// is where it is; a watch entry's cycle, in the products list that holds it;
// and an empty listing, on the one filter that narrowed it. Two inputs that
// conflict, an answer that reads like a refusal and a host that failed name
// none.
func TestRefusalsNameTheInputTheyAreAbout(t *testing.T) {
	srv := newCatalogueServer(t)
	noReleases := newEolServer(t, "")
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)
	tooMany := make([]any, maxWatch+1)
	for i := range tooMany {
		tooMany[i] = "postgresql"
	}
	runs := map[string]func(context.Context, plugin.Request, string) (view.View, error){
		"eol.check": runCheckAt, "eol.watch": runWatchAt, "eol.products": runProductsAt,
	}

	for _, tc := range []struct {
		name, capID string
		values      map[string]any
		base        string
		code, input string
	}{
		{"an unknown product", "eol.check", map[string]any{"product": "nosuchthing"}, "",
			"eol.product.notfound", "product"},
		{"an unknown product before an @", "eol.check", map[string]any{"product": "nosuchthing@15"}, "",
			"eol.product.notfound", "product"},
		{"a cycle the product does not have", "eol.check", map[string]any{"product": "postgresql", "cycle": "9.6"}, "",
			"eol.cycle.notfound", "cycle"},
		{"a cycle the product does not have, after an @", "eol.check", map[string]any{"product": "postgresql@9.6"}, "",
			"eol.cycle.notfound", "product"},
		{"a range that runs backwards", "eol.check", map[string]any{"product": "postgresql", "cycle": "18..13"}, "",
			"eol.cycle.selector", "cycle"},
		{"a range that runs backwards, after an @", "eol.check", map[string]any{"product": "postgresql@18..13"}, "",
			"eol.cycle.selector", "product"},
		{"a cycle given twice", "eol.check", map[string]any{"product": "postgresql@18", "cycle": "13"}, "",
			"eol.cycle.twice", ""},
		{"a product with no release data", "eol.check", map[string]any{"product": "demo"}, noReleases.URL,
			"eol.noreleases", ""},
		{"the API failing", "eol.check", map[string]any{"product": "postgresql"}, down.URL,
			"eol.request.status", ""},
		{"nothing to watch", "eol.watch", nil, "",
			"eol.watch.empty", "products"},
		{"more than one call grades", "eol.watch", map[string]any{"products": tooMany}, "",
			"eol.watch.toomany", "products"},
		{"an entry that names no product", "eol.watch", map[string]any{"products": []any{"nodejs", "@15"}}, "",
			"eol.watch.entry", "products"},
		{"an entry in the old spelling", "eol.watch", map[string]any{"products": []any{"postgresql/15"}}, "",
			"eol.watch.entry", "products"},
		{"an entry whose range runs backwards", "eol.watch", map[string]any{"products": []any{"nodejs@26..24"}}, "",
			"eol.cycle.selector", "products"},
		{"a term nothing matches", "eol.products", map[string]any{"term": "zzz"}, "",
			"eol.products.none", "term"},
		{"a category nothing is in", "eol.products", map[string]any{"category": "zzz"}, "",
			"eol.products.none", "category"},
		{"a term and a category nothing matches together", "eol.products",
			map[string]any{"term": "postgres", "category": "os"}, "",
			"eol.products.none", ""},
	} {
		base := tc.base
		if base == "" {
			base = srv.URL
		}
		_, err := runs[tc.capID](context.Background(), reqFor(t, tc.capID, tc.values), base)
		verr := view.AsError(err, "eol.test")
		if verr == nil || verr.Code != tc.code {
			t.Errorf("%s: got %v, want %s", tc.name, err, tc.code)
			continue
		}
		if verr.Input != tc.input {
			t.Errorf("%s: %s names the input %q, want %q", tc.name, tc.code, verr.Input, tc.input)
		}
		if _, declared := capability(t, tc.capID).ErrorInput(verr); tc.input != "" && !declared {
			t.Errorf("%s: %s names %q, which %s does not declare", tc.name, tc.code, tc.input, tc.capID)
		}
	}
}

func capability(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}
