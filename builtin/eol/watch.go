package eol

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// maxWatch bounds one call. endoflife.date has no batch endpoint, so a
// watchlist is one request per product; twenty sequential requests to a
// CDN-cached API is a few seconds, and a list longer than that is a list
// somebody should split by team rather than wait on.
const maxWatch = 20

// eol.watch is eol.check over a list somebody wrote down once.
//
// What an operator actually checks is not "postgresql 15" on its own but
// the dozen things their platform runs, and they check it weekly. Typing
// twelve `eol check` calls is the reason nobody does. The list is
// configuration — `plugins: eol: products:` in the config file or a
// profile — so it is written once and carried by whatever the profile
// carries; `--products` on the command line is the same input typed by
// hand.
//
// Configuration, never a caller-chosen destination: every entry names a
// product on the one host this plugin ever calls, so the read stays
// ungated the way eol.check is. NoPreview on purpose, the same way
// eol.check is: an unconfigured tile would show a "nothing to watch" error
// for as long as nobody configures it, and a person who names eol.watch in
// their dashboard has already written the list it needs.
func watchCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "eol.watch",
		Summary:    "Grade every product and cycle on a configured watchlist at once",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Runs eol.check over a list of products, each optionally pinned to one " +
			"release cycle as product/cycle — postgresql/15, nodejs, debian/bookworm. " +
			"Write the list once as `plugins: eol: products:` in your config or a " +
			"profile and it is what this grades from then on. A product the API does " +
			"not know, or a cycle the product does not have, is one row saying so; the " +
			"rest of the list is still graded.",
		NoPreview: true,
		Inputs: []plugin.Field{
			{Name: "products", Type: plugin.StringSlice, Config: "products",
				Help: "product or product/cycle, repeatable — usually from `plugins: eol: products:` in your config"},
			{Name: "warn-days", Type: plugin.Int, Config: "warn-days", Default: defaultWarnDays,
				Help: "flag a cycle within this many days of its end-of-life date"},
		},
		Run: runWatch,
	}
}

func runWatch(ctx context.Context, req plugin.Request) (view.View, error) {
	return runWatchAt(ctx, req, apiBase)
}

func runWatchAt(ctx context.Context, req plugin.Request, base string) (view.View, error) {
	entries := req.StringSlice("products")
	if len(entries) == 0 {
		return nil, view.Errorf("eol.watch.empty", "nothing to watch").
			WithHint("write the list once — `plugins: eol: products: [postgresql/15, nodejs]` in your " +
				"config or a profile — or pass --products postgresql/15 --products nodejs")
	}
	if len(entries) > maxWatch {
		return nil, view.Errorf("eol.watch.toomany", "%d entries to watch, and one call grades at most %d",
			len(entries), maxWatch).
			WithHint("one request per product, so split the list — by team or by profile")
	}

	warnDays := req.Int("warn-days")
	now := time.Now()
	t := view.Table{Columns: []view.Column{
		{Name: "Product"},
		{Name: "Cycle"},
		{Name: "Released", Kind: view.KindTimestamp},
		{Name: "Latest"},
		{Name: "LTS"},
		{Name: "EOL", Kind: view.KindTimestamp},
		{Name: "In", Kind: view.KindDuration},
		{Name: "Status", Kind: view.KindStatus},
	}}
	for _, entry := range entries {
		product, cycle, _ := strings.Cut(strings.TrimSpace(entry), "/")
		if product == "" {
			return nil, view.Errorf("eol.watch.entry", "%q names no product", entry).
				WithHint("an entry is product or product/cycle — postgresql/15, nodejs")
		}
		result, verr := fetchProduct(ctx, http.DefaultClient, base, product)
		if verr != nil {
			// An unknown product is one row, not a failed call: a watchlist
			// with one typo in it is still a watchlist somebody wants graded.
			// Anything else — the host unreachable, a 500 — is about the
			// call, and the whole table would be wrong to show.
			if verr.Code == "eol.product.notfound" {
				t.Rows = append(t.Rows, missingRow(product, cycle, "not found"))
				continue
			}
			return nil, verr
		}
		releases := result.Releases
		if cycle != "" {
			r, found := findRelease(releases, cycle)
			if !found {
				t.Rows = append(t.Rows, missingRow(result.Name, cycle, "no such cycle"))
				continue
			}
			releases = []release{r}
		}
		for _, r := range releases {
			t.Rows = append(t.Rows, append([]string{result.Name}, gradeRow(r, warnDays, now)...))
		}
	}
	t.Total = len(t.Rows)
	return t, nil
}

// missingRow keeps a product on the table when the API cannot grade it, in
// the Status column where the eye already goes. The cycle cell carries what
// was asked for, so "no such cycle" says which one.
func missingRow(product, cycle, status string) []string {
	if cycle == "" {
		cycle = "-"
	}
	return []string{product, cycle, "-", "-", "-", "-", "-", status}
}
