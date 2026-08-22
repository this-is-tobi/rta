package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// apiBase is the one endpoint this plugin ever calls: a public,
// unauthenticated, CDN-cached JSON API. No key, no config section, no
// RTA_EOL_* variable — the zero-config half of the contrast with plugins/pg.
const apiBase = "https://endoflife.date/api/v1"

// requestTimeout bounds one call. Not a declared field the way http.get's
// timeout is: that plugin dials whatever URL the caller names, which may be
// slow or hostile, where this always dials the same fast, well-behaved host
// (consistently under a second in practice) — a fixed budget is simpler and
// nothing here needs an operator to widen it.
const requestTimeout = 10 * time.Second

// maxBody bounds the read the way builtin/http does. Generous rather than
// tight: a handful of products carry a long release history, and the point
// is refusing a runaway response, not rationing a normal one.
const maxBody = 4 << 20

// release is the fields this plugin reads from one entry in a product's
// "releases" array. Everything else the API returns (codename, custom, …)
// is left for a later cut — encoding/json ignores what a struct does not
// name, so adding a field here later is additive, not a rewrite.
type release struct {
	Name        string  `json:"name"`
	ReleaseDate string  `json:"releaseDate"`
	IsEol       bool    `json:"isEol"`
	EolFrom     *string `json:"eolFrom"`
	// IsLts is the API's own current verdict, the same trust-it-don't-
	// recompute-it call as IsEol (gradeRow's doc). LtsFrom is not redundant
	// with it: a cycle can carry a *future* LtsFrom while IsLts is still
	// false — nodejs.org runs its releases through a "Current" phase before
	// they graduate to LTS, and endoflife.date's LtsFrom names that
	// scheduled date rather than the release date.
	IsLts   bool    `json:"isLts"`
	LtsFrom *string `json:"ltsFrom"`
	Latest  struct {
		Name string `json:"name"`
	} `json:"latest"`
}

// productResult is "result" in the API's envelope — schema_version and
// generated_at, the other two top-level keys, name nothing this plugin uses.
type productResult struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Releases []release `json:"releases"`
}

type productEnvelope struct {
	Result productResult `json:"result"`
}

// fetchProduct asks base about product and returns every release cycle it
// knows. base is a parameter rather than always apiBase so a test can point
// it at an httptest.Server that answers wrongly on purpose — the same reason
// builtin/audit's queryOSVAt takes its endpoint as an argument.
//
// An alias resolves for free: endoflife.date 301s "postgres" to
// "postgresql" and http.Client follows redirects by default, so the
// catalogue's own example — `rta eol check postgres 15` — needs no
// client-side alias table.
func fetchProduct(ctx context.Context, client *http.Client, base, product string) (*productResult, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// PathEscape rather than plain concatenation: a product string carrying
	// its own "/" would otherwise change which path is requested instead of
	// failing as the unknown product it is.
	reqURL := base + "/products/" + url.PathEscape(strings.ToLower(product))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, view.Errorf("eol.request.invalid", "building request for %q: %v", product, err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, view.Errorf("eol.request.failed", "asking endoflife.date about %q: %v", product, err).
			WithHint("check network access — endoflife.date must be reachable")
	}
	defer func() { _ = resp.Body.Close() }()

	// A 404 here is always HTML (the site's own not-found page), for an
	// unknown product and an unknown cycle alike — checked by status code
	// alone, before anything tries to decode the body as JSON, so the
	// failure is "no such product" rather than "invalid character '<'".
	if resp.StatusCode == http.StatusNotFound {
		return nil, view.Errorf("eol.product.notfound", "no product named %q", product).
			WithHint("see https://endoflife.date for the full catalogue of names and aliases")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, view.Errorf("eol.request.status", "endoflife.date returned %s for %q", resp.Status, product)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, view.Errorf("eol.response.read", "reading the response for %q: %v", product, err)
	}
	var env productEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, view.Errorf("eol.response.invalid", "decoding the response for %q: %v", product, err)
	}
	return &env.Result, nil
}
