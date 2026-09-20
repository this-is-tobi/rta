// Package eol answers one question — is a product's release still supported
// — against the public endoflife.date API.
//
// Built in rather than a plugin, and the smallest case for the rule that
// decides which (docs/40-plugins/10-plugins.md, "Built in, or a plugin"): no
// credential, no configuration, nothing outside the standard library, and one
// fixed public host that no input can redirect. `rta eol check postgresql 15`
// works on a fresh install with nothing else configured. It was the smallest
// of the first-party plugins until that rule put it here.
package eol

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/eolapi"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// defaultWarnDays is further out than cert.expiry's 30 (builtin/cert):
// a TLS certificate renews in minutes once somebody notices, a database
// major-version upgrade does not, so the window to notice in has to be
// wider for the warning to be useful rather than merely early.
const defaultWarnDays = 90

// tileRefresh is how often a dashboard tile of any of these capabilities is
// re-run, for the person who names one in their config. Every one of them
// is one request per product to endoflife.date, whose data moves by the
// day: an end-of-life date announced this afternoon is still news two hours
// from now, and the alternative — the dashboard's every-few-seconds pace —
// is a public API asked the same question a thousand times an hour.
const tileRefresh = 2 * time.Hour

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "eol",
		Summary: "Support windows and end-of-life dates, via endoflife.date",
		Capabilities: []plugin.Capability{
			{
				ID:         "eol.check",
				Summary:    "Whether a product, or one of its release cycles, is still supported",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Looks up a product on endoflife.date and grades every release cycle " +
					"by how close it is to its own end-of-life date. Name one cycle to see just " +
					"that row, or a range of them — 13..16, 15.., ..16 — to see every numbered " +
					"cycle inside it; leave it out to see all of them. Aliases work — \"postgres\" " +
					"and \"postgresql\" name the same product.",
				// No dashboard tile: product is Required, so the automatic
				// picker (every Read capability that needs no input)
				// already skips this — stated anyway, the way pg states it
				// on every capability, because the reason ("reaches off the
				// box") is a property of the plugin, not an accident of one
				// field being required today.
				NoPreview: true,
				Refresh:   tileRefresh,
				Inputs: []plugin.Field{
					{Name: "product", Type: plugin.String, Positional: true, Required: true,
						Help:    "product name or alias — see https://endoflife.date for the catalogue",
						Live:    true,
						Suggest: suggestProducts},
					{Name: "cycle", Type: plugin.String, Positional: true,
						Help:    "one release cycle — 15, bookworm, 22.04 — or a range of them: 13..16, 15.., ..16; every cycle when omitted",
						Live:    true,
						Suggest: suggestCycles},
					{Name: "warn-days", Type: plugin.Int, Config: "warn-days", Default: defaultWarnDays,
						Help: "flag a cycle within this many days of its end-of-life date"},
				},
				Run: runCheck,
			},
			watchCapability(),
			productsCapability(),
		},
	}
}

// runCheck is runCheckAt against the real API — split the way builtin/audit
// splits queryOSV from queryOSVAt, so a test can point the whole capability
// at a server that answers wrongly on purpose instead of only the HTTP layer
// underneath it.
func runCheck(ctx context.Context, req plugin.Request) (view.View, error) {
	return runCheckAt(ctx, req, eolapi.APIBase)
}

func runCheckAt(ctx context.Context, req plugin.Request, base string) (view.View, error) {
	product := req.String("product")
	result, verr := eolapi.FetchProduct(ctx, http.DefaultClient, base, product)
	if verr != nil {
		return nil, verr
	}

	releases := result.Releases
	if cycle := req.String("cycle"); cycle != "" {
		sel, verr := parseSelector(cycle)
		if verr != nil {
			return nil, verr
		}
		if releases = sel.pick(releases); len(releases) == 0 {
			return nil, view.Errorf("eol.cycle.notfound", "no release %q for %q", cycle, product).
				WithHint("available cycles: " + cycleNames(result.Releases))
		}
	}

	// **A product with no release data is not a product with nothing to
	// worry about.** endoflife.date answers 200 with a valid envelope and an
	// empty releases array for something it tracks but has no cycles for
	// yet, and there was no case for it: the caller got a zero-row table and
	// a nil error, which is the same shape --warn-days filtering everything
	// out produces, from a command whose whole purpose is to say whether
	// something is past its end of life.
	if len(releases) == 0 {
		return nil, view.Errorf("eol.noreleases", "%s has no release data", product).
			WithHint("the product is tracked but has no cycles recorded — `rta eol products` lists what does")
	}

	warnDays := req.Int("warn-days")
	now := time.Now()
	t := view.Table{Columns: []view.Column{
		{Name: "Cycle"},
		{Name: "Released", Kind: view.KindTimestamp},
		{Name: "Latest"},
		{Name: "LTS"},
		{Name: "EOL", Kind: view.KindTimestamp},
		{Name: "In", Kind: view.KindDuration},
		{Name: "Status", Kind: view.KindStatus},
	}}
	for _, r := range releases {
		t.Rows = append(t.Rows, gradeRow(r, warnDays, now))
	}
	t.Total = len(t.Rows)
	return t, nil
}

// findRelease matches by name or codename, case-insensitively: cycle names
// are typed by hand, a codename (bookworm, Tahoe) is what people say for the
// products that have one, and there is no reason to make a caller get the
// case exactly right when the whole list is already in hand to check
// against. Name first across the whole list, so a codename that happens to
// spell another cycle's number can never shadow it.
func findRelease(releases []eolapi.Release, cycle string) (eolapi.Release, bool) {
	for _, r := range releases {
		if strings.EqualFold(r.Name, cycle) {
			return r, true
		}
	}
	for _, r := range releases {
		if r.Codename != "" && strings.EqualFold(r.Codename, cycle) {
			return r, true
		}
	}
	return eolapi.Release{}, false
}

func cycleNames(releases []eolapi.Release) string {
	names := make([]string, len(releases))
	for i, r := range releases {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// suggestProducts is suggestProductsAt against the real API — the same
// split runCheck/runCheckAt uses, so a test can point it at a server that
// answers wrongly on purpose instead of the real endoflife.date.
func suggestProducts(ctx context.Context, req plugin.Request) []string {
	return suggestProductsAt(ctx, req, eolapi.APIBase)
}

// suggestProductsAt offers the catalogue's names and aliases together — a
// caller reaching for eol.check usually knows "postgres", not the canonical
// "postgresql", and aliases working interchangeably is the entire point
// eol.products documents. Live: this is the one request fetchCatalogue's
// own comment already treats as cheap enough to pay on every call, run here
// on a deliberate completion press rather than every keystroke.
func suggestProductsAt(ctx context.Context, _ plugin.Request, base string) []string {
	entries, verr := eolapi.FetchCatalogue(ctx, http.DefaultClient, base)
	if verr != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		for _, name := range append([]string{e.Name}, e.Aliases...) {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// suggestCycles is suggestCyclesAt against the real API; see suggestProducts.
func suggestCycles(ctx context.Context, req plugin.Request) []string {
	return suggestCyclesAt(ctx, req, eolapi.APIBase)
}

// suggestCyclesAt offers the release cycles of the product already named:
// there is nothing to offer before that, since a product is looked up one
// at a time. Live for the same reason suggestProductsAt is — one request
// against the fixed public API, not a local computation.
func suggestCyclesAt(ctx context.Context, req plugin.Request, base string) []string {
	product := strings.TrimSpace(req.String("product"))
	if product == "" {
		return nil
	}
	result, verr := eolapi.FetchProduct(ctx, http.DefaultClient, base, product)
	if verr != nil {
		return nil
	}
	out := make([]string, 0, 2*len(result.Releases))
	for _, r := range result.Releases {
		out = append(out, r.Name)
	}
	// Codenames after every number, not interleaved: a completion list is
	// read top to bottom, and the numbers are the order the API keeps.
	for _, r := range result.Releases {
		if r.Codename != "" {
			out = append(out, r.Codename)
		}
	}
	return out
}

// gradeRow turns one release into a row. The verdict is eolapi.Grade's,
// shared with audit.kube.eol so the two never disagree about whether a
// cycle is past its end of life; the date column shows the API's own text
// and the duration is computed only from a date that parses.
func gradeRow(r eolapi.Release, warnDays int, now time.Time) []string {
	eolText, inText := "not announced", "-"
	if r.EolFrom != nil {
		eolText = *r.EolFrom
		if eolDate, ok := eolapi.EolDate(r); ok {
			inText = humanUntil(eolDate, now)
		}
	}
	return []string{r.Name, r.ReleaseDate, r.Latest.Name, ltsCell(r), eolText, inText, eolStatus(r, warnDays, now)}
}

// ltsCell has three states, not two, because LtsFrom means something
// different depending on IsLts: "18.6" (postgresql) is never LTS and never
// carries one, "24" (nodejs, already graduated) is LTS today, and "26"
// (nodejs, mid-"Current" as of this build) is not LTS yet but names the
// date it becomes so — the forward-looking case worth showing, since IsLts
// alone would report it identically to a cycle with no LTS future at all.
func ltsCell(r eolapi.Release) string {
	switch {
	case r.IsLts:
		return "yes"
	case r.LtsFrom != nil:
		return "from " + *r.LtsFrom
	default:
		return "-"
	}
}

// eolStatus is the Status cell for a verdict: the words a table reads at a
// glance, with the window named on a warning so "WARN <90d" says what it
// was measured against.
func eolStatus(r eolapi.Release, warnDays int, now time.Time) string {
	switch eolapi.Grade(r, warnDays, now) {
	case eolapi.Ended:
		return "EOL"
	case eolapi.Ending:
		return fmt.Sprintf("WARN <%dd", warnDays)
	}
	return "ok"
}

// humanUntil mirrors builtin/cert's helper of the same job, parameterized on
// now instead of calling time.Now() itself so a boundary (exactly warnDays
// out, exactly today) is a fixed input a test can hit instead of a race
// against the clock. Status already says EOL, so unlike cert's version this
// never prefixes the past case with the word "expired".
func humanUntil(t, now time.Time) string {
	d := t.Sub(now)
	if d < 0 {
		return fmt.Sprintf("%dd ago", int(-d.Hours())/24)
	}
	if days := int(d.Hours()) / 24; days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
