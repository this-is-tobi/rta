// Command rta-plugin-eol answers one question — is a product's release
// still supported — against the public endoflife.date API.
//
// It exists as the small counterpart to plugins/pg:
// where pg needs a connection, a credential and a config section before its
// first call, eol needs none of that — no auth, no local state, one public
// GET request per call. Build it and put it on $PATH as rta-plugin-eol
//
//	cd plugins/eol && go build -o ~/.local/bin/rta-plugin-eol .
//
// and `rta eol check postgresql 15` works with nothing else configured.
package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// defaultWarnDays is further out than cert.expiry's 30 (builtin/cert):
// a TLS certificate renews in minutes once somebody notices, a database
// major-version upgrade does not, so the window to notice in has to be
// wider for the warning to be useful rather than merely early.
const defaultWarnDays = 90

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "eol",
		Summary: "End-of-life and support-window checks via endoflife.date",
		Version: "0.1.0",
		Capabilities: []plugin.Capability{
			{
				ID:         "eol.check",
				Summary:    "Whether a product, or one of its release cycles, is still supported",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Looks up a product on endoflife.date and grades every release cycle " +
					"by how close it is to its own end-of-life date. Name one cycle to see just " +
					"that row; leave it out to see all of them. Aliases work — \"postgres\" and " +
					"\"postgresql\" name the same product.",
				// No dashboard tile: product is Required, so the automatic
				// picker (every Read capability that needs no input, §4.3)
				// already skips this — stated anyway, the way pg states it
				// on every capability, because the reason ("reaches off the
				// box") is a property of the plugin, not an accident of one
				// field being required today.
				NoPreview: true,
				Inputs: []plugin.Field{
					{Name: "product", Type: plugin.String, Positional: true, Required: true,
						Help: "product name or alias — see https://endoflife.date for the catalogue"},
					{Name: "cycle", Type: plugin.String, Positional: true,
						Help: "one release cycle, e.g. 15, bookworm, 22.04 — every cycle is shown when omitted"},
					{Name: "warn-days", Type: plugin.Int, Default: defaultWarnDays,
						Help: "flag a cycle within this many days of its end-of-life date"},
				},
				Run: runCheck,
			},
		},
	}
}

// runCheck is runCheckAt against the real API — split the way builtin/audit
// splits queryOSV from queryOSVAt, so a test can point the whole capability
// at a server that answers wrongly on purpose instead of only the HTTP layer
// underneath it.
func runCheck(ctx context.Context, req plugin.Request) (view.View, error) {
	return runCheckAt(ctx, req, apiBase)
}

func runCheckAt(ctx context.Context, req plugin.Request, base string) (view.View, error) {
	product := req.String("product")
	result, verr := fetchProduct(ctx, http.DefaultClient, base, product)
	if verr != nil {
		return nil, verr
	}

	releases := result.Releases
	if cycle := req.String("cycle"); cycle != "" {
		r, found := findRelease(releases, cycle)
		if !found {
			return nil, view.Errorf("eol.cycle.notfound", "no release %q for %q", cycle, product).
				WithHint("available cycles: " + cycleNames(releases))
		}
		releases = []release{r}
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

// findRelease matches by name, case-insensitively: cycle names are typed by
// hand and are sometimes a codename (bookworm, Tahoe) rather than a number,
// and there is no reason to make a caller get the case exactly right when
// the whole list is already in hand to check against.
func findRelease(releases []release, cycle string) (release, bool) {
	for _, r := range releases {
		if strings.EqualFold(r.Name, cycle) {
			return r, true
		}
	}
	return release{}, false
}

func cycleNames(releases []release) string {
	names := make([]string, len(releases))
	for i, r := range releases {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// gradeRow turns one release into a row, trusting the API's own isEol
// verdict rather than recomputing it from eolFrom: endoflife.date's entire
// purpose is having already made that call correctly, and some cycles (a
// current release with no announced retirement date yet) carry no eolFrom
// at all — recomputing "expired" from a date that may not even be present
// would be answering a question this API already answered.
func gradeRow(r release, warnDays int, now time.Time) []string {
	eolText, inText := "not announced", "-"
	if r.EolFrom != nil {
		eolText = *r.EolFrom
		if eolDate, err := time.Parse("2006-01-02", *r.EolFrom); err == nil {
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
func ltsCell(r release) string {
	switch {
	case r.IsLts:
		return "yes"
	case r.LtsFrom != nil:
		return "from " + *r.LtsFrom
	default:
		return "-"
	}
}

func eolStatus(r release, warnDays int, now time.Time) string {
	if r.IsEol {
		return "EOL"
	}
	if r.EolFrom == nil {
		return "ok"
	}
	eolDate, err := time.Parse("2006-01-02", *r.EolFrom)
	if err != nil {
		return "ok"
	}
	if eolDate.Before(now.Add(time.Duration(warnDays) * 24 * time.Hour)) {
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
