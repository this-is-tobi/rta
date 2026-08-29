package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"strings"
)

// One question to OSV, for every dependency at once.
//
// osv.dev is the OpenSSF's aggregator: it is where GitHub advisories, the Go
// vulnerability database, PyPA, RustSec and the distributions all end up, and
// it is queryable without a key or an account. Asking it directly is what
// keeps this inside the plugin's first rule — one interaction, read-only, no
// local database to sync and no scanner reimplemented.
//
// The batch endpoint answers with vulnerability *identifiers*, not severities
// or fixed versions. That is a real limit and the report says so rather than
// working around it: pulling details would mean one request per identifier,
// which is the crawl this plugin does not do. Naming the tool that goes
// deeper — osv-scanner, trivy, grype — is the job here.

const (
	osvBatchURL = "https://api.osv.dev/v1/querybatch"
	// osvBatchMax is the endpoint's documented ceiling per request.
	osvBatchMax = 1000
)

type osvQuery struct {
	Package osvPackage `json:"package"`
	Version string     `json:"version"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

type osvVuln struct {
	ID string `json:"id"`
}

type osvResponse struct {
	Results []struct {
		Vulns []osvVuln `json:"vulns"`
	} `json:"results"`
}

// osvURL is the human-readable page for an identifier, so a finding can be
// followed rather than just cited.
func osvURL(id string) string { return "https://osv.dev/vulnerability/" + id }

// queryOSV asks about every component in one request per batch and returns
// the identifiers found, keyed by component. Results come back positionally,
// so the mapping depends on the response having exactly as many entries as
// the request — checked rather than assumed, because silently shifting
// vulnerabilities onto the wrong package is worse than reporting none.
func queryOSV(ctx context.Context, client *stdhttp.Client, comps []component) (map[string][]string, error) {
	return queryOSVAt(ctx, client, osvBatchURL, comps)
}

// queryOSVAt is queryOSV with the endpoint named, so the request shape and
// the positional matching are testable against a server that answers wrongly
// on purpose. Speaking OSV's schema incorrectly does not fail — it returns
// "no vulnerabilities" for everything, and the capability reports an
// all-clear it never checked.
func queryOSVAt(ctx context.Context, client *stdhttp.Client, endpoint string, comps []component) (map[string][]string, error) {
	found := make(map[string][]string)
	for start := 0; start < len(comps); start += osvBatchMax {
		end := min(start+osvBatchMax, len(comps))
		batch := comps[start:end]

		queries := make([]osvQuery, len(batch))
		for i, c := range batch {
			queries[i] = osvQuery{
				Package: osvPackage{Name: c.name, Ecosystem: c.ecosystem},
				Version: c.version,
			}
		}
		body, err := json.Marshal(map[string]any{"queries": queries})
		if err != nil {
			return nil, err
		}
		req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "rta-audit/1")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var parsed osvResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&parsed)
		status := resp.StatusCode
		_ = resp.Body.Close()

		if status != stdhttp.StatusOK {
			return nil, fmt.Errorf("osv.dev returned %s", strings.TrimSpace(resp.Status))
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("reading osv.dev response: %w", decodeErr)
		}
		if len(parsed.Results) != len(batch) {
			return nil, fmt.Errorf("osv.dev answered %d queries out of %d — results are positional, "+
				"so a short answer cannot be matched to its packages", len(parsed.Results), len(batch))
		}
		for i, res := range parsed.Results {
			for _, v := range res.Vulns {
				if v.ID != "" {
					found[batch[i].key()] = append(found[batch[i].key()], v.ID)
				}
			}
		}
	}
	return found, nil
}
