// Package eolapi is the endoflife.date client two built-ins share: the eol
// plugin, which is a view over it, and audit.kube.eol, which grades a
// cluster's versions against it. One fixed public host, the product and
// catalogue shapes, and the verdict on a release cycle.
//
// Under builtin/internal beside itemstore and timefmt for the reason they
// are there: two built-ins need the same code and neither may import the
// other, because a built-in package is a plugin declaration, and audit
// importing eol would pull a second plugin's capabilities into its own
// package for the sake of an HTTP call.
package eolapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// APIBase is the one endpoint anything here ever calls: a public,
// unauthenticated, CDN-cached JSON API. No key, no config section, no
// RTA_EOL_* variable — the zero-config half of the contrast with the pg
// plugin. Every function takes the base as a parameter so a test can point
// it at an httptest.Server that answers wrongly on purpose.
const APIBase = "https://endoflife.date/api/v1"

// requestTimeout bounds one call. Not a declared field the way http.get's
// timeout is: that plugin dials whatever URL the caller names, which may be
// slow or hostile, where this always dials the same fast, well-behaved host
// (consistently under a second in practice) — a fixed budget is simpler and
// nothing here needs an operator to widen it.
const requestTimeout = 10 * time.Second

// defaultRetryDelay is the wait before the one retry of a 429 that names none,
// or names one this cannot read.
const defaultRetryDelay = time.Second

// retryDelay is how long a 429's Retry-After asks the client to wait: whole
// seconds, or the date the HTTP spec also allows. A header that is missing,
// negative or neither is the default rather than no wait, because a server that
// refused a request is not asking to be asked again at once.
func retryDelay(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		return max(0, at.Sub(now))
	}
	return defaultRetryDelay
}

// maxBody bounds the read the way builtin/http does. Generous rather than
// tight: a handful of products carry a long release history, and the point
// is refusing a runaway response, not rationing a normal one.
const maxBody = 4 << 20

// TileRefresh is how often a dashboard tile of a capability that asks
// endoflife.date is re-run, for the person who names one in their config. Each
// of them is a request per product, and the site's data moves by the day: an
// end-of-life date announced this afternoon is still news two hours from now,
// and the alternative — the dashboard's every-few-seconds pace, or the thirty
// seconds a capability that reaches a cluster gets — is a public API asked the
// same question a thousand times an hour.
//
// Here rather than in either built-in because two of them ask: eol.watch and
// its siblings, and audit.kube.eol, which lists a cluster and then asks once
// per distinct product in it. A declared Refresh is a floor an operator's file
// can lengthen and never shorten.
const TileRefresh = 2 * time.Hour

// Release is the fields read from one entry in a product's "releases"
// array. Everything else the API returns (custom, …) is left for a later
// cut — encoding/json ignores what a struct does not name, so adding a
// field here later is additive, not a rewrite.
type Release struct {
	Name string `json:"name"`
	// Codename is the name a cycle is known by where the number is not what
	// people say: debian's "12" is Bookworm, macOS 26 is Tahoe. The API keeps
	// it beside the name rather than in it, so a lookup by codename has to
	// read this field — which the eol help text promised ("bookworm, Tahoe")
	// for a whole release before anything did.
	Codename    string  `json:"codename"`
	ReleaseDate string  `json:"releaseDate"`
	IsEol       bool    `json:"isEol"`
	EolFrom     *string `json:"eolFrom"`
	// IsLts is the API's own current verdict, the same trust-it-don't-
	// recompute-it call as IsEol (Grade's doc). LtsFrom is not redundant
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

// Product is "result" in the API's envelope — schema_version and
// generated_at, the other two top-level keys, name nothing anything here
// uses.
type Product struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Releases []Release `json:"releases"`
}

type productEnvelope struct {
	Result Product `json:"result"`
}

// CatalogueEntry is one product as the API's /products list describes it.
// Aliases are the whole reason eol.products exists: "postgres", "pg" and
// "psql" all name postgresql, and the way to learn that otherwise is to
// guess at eol.check until one works. They are also how audit.kube.eol
// recognises an image: `postgres:15` is the postgresql product by alias.
type CatalogueEntry struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Category string   `json:"category"`
	Aliases  []string `json:"aliases"`
	Tags     []string `json:"tags"`
}

type catalogueEnvelope struct {
	Result []CatalogueEntry `json:"result"`
}

// FetchProduct asks base about product and returns every release cycle it
// knows.
//
// An alias resolves for free: endoflife.date 301s "postgres" to
// "postgresql" and http.Client follows redirects by default, so the
// catalogue's own example — `rta eol check postgres 15` — needs no
// client-side alias table.
func FetchProduct(ctx context.Context, client *http.Client, base, product string) (*Product, *view.Error) {
	// PathEscape rather than plain concatenation: a product string carrying
	// its own "/" would otherwise change which path is requested instead of
	// failing as the unknown product it is.
	reqURL := base + "/products/" + url.PathEscape(strings.ToLower(product))
	var env productEnvelope
	status, verr := getJSON(ctx, client, reqURL, strconv.Quote(product), &env)
	if verr != nil {
		return nil, verr
	}
	// A 404 here is always HTML (the site's own not-found page), for an
	// unknown product and an unknown cycle alike — checked by status code
	// alone, before anything tries to decode the body as JSON, so the
	// failure is "no such product" rather than "invalid character '<'".
	if status == http.StatusNotFound {
		return nil, view.Errorf("eol.product.notfound", "no product named %q", product).
			WithHint("see https://endoflife.date for the full catalogue of names and aliases")
	}
	if status != http.StatusOK {
		return nil, view.Errorf("eol.request.status", "endoflife.date returned %d for %q", status, product)
	}
	return &env.Result, nil
}

// FetchCatalogue asks base for every product it knows. One request, a few
// hundred entries, CDN-cached — cheap enough to fetch on every call rather
// than cache, and a cache is the kind of state none of this has.
func FetchCatalogue(ctx context.Context, client *http.Client, base string) ([]CatalogueEntry, *view.Error) {
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

// getJSON performs a GET against the API and decodes a 200 into out. It
// returns the status rather than judging it, because a 404 means "no such
// product" on one path and "the API moved" on another, and only the caller
// knows which. what names the thing being asked about in every error, so
// "asking endoflife.date about \"postgresql\"" and "… about the catalogue"
// read the same way.
//
// A 429 is asked again once, after the wait its Retry-After names, and only if
// that wait fits in what is left of the request's own budget: the site rate
// limits a client that asks for many products in a row, which is what a cluster
// audit does, and says how long to wait. A second refusal, or a wait that would
// outlast the budget, is the answer, as any other status is.
func getJSON(ctx context.Context, client *http.Client, reqURL, what string, out any) (int, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	status, retryAfter, verr := getOnce(ctx, client, reqURL, what, out)
	if verr != nil || status != http.StatusTooManyRequests {
		return status, verr
	}
	wait := retryDelay(retryAfter, time.Now())
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= wait {
		return status, nil
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return status, nil
	}
	status, _, verr = getOnce(ctx, client, reqURL, what, out)
	return status, verr
}

// getOnce is one request, and the Retry-After of its answer.
func getOnce(ctx context.Context, client *http.Client, reqURL, what string, out any) (int, string, *view.Error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, "", view.Errorf("eol.request.invalid", "building request for %s: %v", what, err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, "", view.Errorf("eol.request.failed", "asking endoflife.date about %s: %v", what, err).
			WithHint("check network access — endoflife.date must be reachable")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, resp.Header.Get("Retry-After"), nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.StatusCode, "", view.Errorf("eol.response.read", "reading the response for %s: %v", what, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, "", view.Errorf("eol.response.invalid", "decoding the response for %s: %v", what, err)
	}
	return resp.StatusCode, "", nil
}

// Verdict is where a release cycle stands today.
type Verdict int

const (
	// Supported: not past its end-of-life date, and not within the warning
	// window of it — or with no date announced at all, which the API
	// reports for a current release whose retirement is not yet scheduled.
	Supported Verdict = iota
	// Ending: an end-of-life date is announced and falls within warnDays.
	Ending
	// Ended: the API says the cycle is past its end of life.
	Ended
)

// Grade trusts the API's own isEol verdict rather than recomputing it from
// eolFrom: endoflife.date's entire purpose is having already made that call
// correctly, and some cycles (a current release with no announced
// retirement date yet) carry no eolFrom at all — recomputing "expired" from
// a date that may not even be present would be answering a question this
// API already answered. The warning window is the one judgement made here,
// against the date the API gives.
func Grade(r Release, warnDays int, now time.Time) Verdict {
	if r.IsEol {
		return Ended
	}
	eolDate, ok := EolDate(r)
	if !ok {
		return Supported
	}
	if eolDate.Before(now.Add(time.Duration(warnDays) * 24 * time.Hour)) {
		return Ending
	}
	return Supported
}

// EolDate is the announced end-of-life date, when there is one and it
// parses; a cycle with none announced, or one the API states in a shape
// this does not read, reports false rather than a zero time.
func EolDate(r Release) (time.Time, bool) {
	if r.EolFrom == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", *r.EolFrom)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
