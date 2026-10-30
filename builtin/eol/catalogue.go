package eol

import (
	"context"
	"net/http"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/eolapi"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// eol.products answers "what is this thing called on endoflife.date".
func productsCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "eol.products",
		Summary:    "Search the endoflife.date catalogue: names, aliases and categories",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Lists the products endoflife.date tracks, with the aliases `eol.check` " +
			"and `eol.watch` accept for each. A term narrows the list to products whose " +
			"name, label or alias contains it. Without one the first products are listed and the " +
			"total says how many there are; `limit` shows more.",
		NoPreview: true,
		Refresh:   eolapi.TileRefresh,
		Inputs: []plugin.Field{
			{Name: "term", Type: plugin.String, Positional: true,
				Help: "part of a name, label or alias — postgres, kube, ubuntu"},
			{Name: "category", Type: plugin.String,
				Help: "only this category — database, os, framework, lang, …"},
			// Bounded because the catalogue is some five hundred products, 35 KB
			// of rows, and a model that leaves the term out to see what there
			// is pays for all of it, in tokens, to read the first screen of it.
			{Name: "limit", Type: plugin.Int, Config: "catalogue.limit", Default: 50, Min: 1, Max: 1000,
				Help: "how many products to list"},
		},
		Run: runProducts,
	}
}

func runProducts(ctx context.Context, req plugin.Request) (view.View, error) {
	return runProductsAt(ctx, req, eolapi.APIBase)
}

func runProductsAt(ctx context.Context, req plugin.Request, base string) (view.View, error) {
	entries, verr := eolapi.FetchCatalogue(ctx, http.DefaultClient, base)
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
	if limit := req.Int("limit"); limit > 0 && len(t.Rows) > limit {
		t.Rows = t.Rows[:limit]
	}
	if len(t.Rows) == 0 {
		return nil, noProducts(req, term, category)
	}
	return t, nil
}

// noProducts is the refusal of a listing nothing in the catalogue fills, worded
// by the filters that narrowed it and naming the one input to change when one
// filter did: both is a choice between them. It quoted the term whatever was
// given, so a category nothing is in read `nothing in the catalogue matches ""`,
// about a term nobody gave, under a hint about the term.
func noProducts(req plugin.Request, term, category string) *view.Error {
	sf := req.Surface()
	termText, categoryText := strings.TrimSpace(req.String("term")), strings.TrimSpace(req.String("category"))
	switch {
	case term != "" && category != "":
		return view.Errorf("eol.products.none", "nothing in the category %q matches %q", categoryText, termText).
			WithHint("drop one of them; " + sf.CapabilityName("eol.products") + " with neither lists what endoflife.date tracks")
	case category != "":
		return view.Errorf("eol.products.none", "nothing in the catalogue is in the category %q", categoryText).
			WithHint(sf.CapabilityName("eol.products") + " with no category lists every product beside its own").
			WithInput("category")
	case term != "":
		return view.Errorf("eol.products.none", "nothing in the catalogue matches %q", termText).
			WithHint(sf.CapabilityName("eol.products") + " with no term lists what endoflife.date tracks").
			WithInput("term")
	}
	return view.Errorf("eol.products.none", "endoflife.date sent an empty catalogue")
}

// matchesTerm is a substring match over the three things a person might
// know a product by. Case-insensitive because the catalogue's labels are
// not ("PostgreSQL") and nobody types them that way.
func matchesTerm(e eolapi.CatalogueEntry, term string) bool {
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
