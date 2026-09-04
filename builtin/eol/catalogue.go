package eol

import (
	"context"
	"net/http"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// catalogueEntry is one product as the API's /products list describes it.
// Aliases are the whole reason this capability exists: "postgres", "pg" and
// "psql" all name postgresql, and the way to learn that today is to guess
// at eol.check until one works.
type catalogueEntry struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases"`
	Tags     []string `json:"tags"`
}

type catalogueEnvelope struct {
	Result []catalogueEntry `json:"result"`
}

// fetchCatalogue asks base for every product it knows. One request, a few
// hundred entries, CDN-cached — cheap enough to fetch on every call rather
// than cache, and a cache is the kind of state this plugin has none of.
func fetchCatalogue(ctx context.Context, client *http.Client, base string) ([]catalogueEntry, *view.Error) {
	var env catalogueEnvelope
	status, verr := getJSON(ctx, client, base+"/products", "the catalogue", &env)
	if verr != nil {
		return nil, verr
	}
	if status != http.StatusOK {
		return nil, view.Errorf("eol.request.status", "endoflife.date returned %d for the catalogue", status)
	}
	return env.Result, nil
}

// eol.products answers "what is this thing called on endoflife.date".
func productsCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "eol.products",
		Summary:    "Search the endoflife.date catalogue: names, aliases and categories",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Lists the products endoflife.date tracks, with the aliases eol.check " +
			"and eol.watch accept for each. A term narrows the list to products whose " +
			"name, label or alias contains it; leave it out to see the whole catalogue.",
		NoPreview: true,
		Inputs: []plugin.Field{
			{Name: "term", Type: plugin.String, Positional: true,
				Help: "part of a name, label or alias — postgres, kube, ubuntu"},
			{Name: "category", Type: plugin.String,
				Help: "only this category — database, os, framework, lang, …"},
		},
		Run: runProducts,
	}
}

func runProducts(ctx context.Context, req plugin.Request) (view.View, error) {
	return runProductsAt(ctx, req, apiBase)
}

func runProductsAt(ctx context.Context, req plugin.Request, base string) (view.View, error) {
	entries, verr := fetchCatalogue(ctx, http.DefaultClient, base)
	if verr != nil {
		return nil, verr
	}
	term := strings.ToLower(strings.TrimSpace(req.String("term")))
	category := strings.ToLower(strings.TrimSpace(req.String("category")))

	t := view.Table{Columns: []view.Column{
		{Name: "Product"},
		{Name: "Label"},
		{Name: "Aliases"},
		{Name: "Category"},
		{Name: "Tags"},
	}}
	for _, e := range entries {
		if category != "" && strings.ToLower(e.Category) != category {
			continue
		}
		if term != "" && !matchesTerm(e, term) {
			continue
		}
		t.Rows = append(t.Rows, []string{e.Name, e.Label, strings.Join(e.Aliases, ", "),
			e.Category, strings.Join(e.Tags, ", ")})
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return nil, view.Errorf("eol.products.none", "nothing in the catalogue matches %q", req.String("term")).
			WithHint("`rta eol products` with no term lists everything endoflife.date tracks")
	}
	return t, nil
}

// matchesTerm is a substring match over the three things a person might
// know a product by. Case-insensitive because the catalogue's labels are
// not ("PostgreSQL") and nobody types them that way.
func matchesTerm(e catalogueEntry, term string) bool {
	if strings.Contains(strings.ToLower(e.Name), term) || strings.Contains(strings.ToLower(e.Label), term) {
		return true
	}
	for _, a := range e.Aliases {
		if strings.Contains(strings.ToLower(a), term) {
			return true
		}
	}
	return false
}
