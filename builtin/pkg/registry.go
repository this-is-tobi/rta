package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// registryClient asks the fixed public registries for the latest version of
// a name this machine already has installed.
//
// Every host here is a constant. The name in the path comes from a manager's
// own installed list — never from a caller — so this is a read to a
// destination nobody at the keyboard or over MCP chose, which is what keeps
// the read tier ungated (builtin/http's rule: a caller-chosen destination
// cannot be a free read). Tests point the bases at httptest servers.
type registryClient struct {
	http   *http.Client
	pypi   string
	npm    string
	crates string
	gomod  string
	github string
}

const (
	registryTimeout = 10 * time.Second
	// registryBody caps one registry answer. PyPI's project document lists
	// every file of every release ever published, which for a popular
	// package is tens of megabytes uncompressed — measured: black and ruff
	// both cleared 4 MiB. The cap is a bound against a hostile answer, not
	// a size expectation, so it sits well above the largest honest one.
	registryBody = 64 << 20
)

func newRegistryClient() *registryClient {
	return &registryClient{
		http:   &http.Client{Timeout: registryTimeout},
		pypi:   "https://pypi.org",
		npm:    "https://registry.npmjs.org",
		crates: "https://crates.io",
		gomod:  "https://proxy.golang.org",
		github: "https://api.github.com",
	}
}

// getJSON fetches one document and decodes it. A 404 is reported as absent
// rather than as a failure: a package that a registry does not know is a
// row that says so, not a call that fails.
func (c *registryClient) getJSON(ctx context.Context, rawURL string, out any) (found bool, verr *view.Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, view.Errorf("pkg.registry.request", "%v", err)
	}
	req.Header.Set("Accept", "application/json")
	// crates.io refuses requests without a User-Agent naming the tool, and
	// naming it everywhere is the polite default.
	req.Header.Set("User-Agent", "rta-pkg (+https://github.com/this-is-tobi/rta)")
	if strings.HasPrefix(rawURL, c.github) {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, view.Errorf("pkg.registry.unreachable", "%s: %v", hostOf(rawURL), err).
			WithHint("the latest-version lookup needs the registry; the installed column is still right")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	case resp.StatusCode == http.StatusForbidden && strings.HasPrefix(rawURL, c.github):
		return false, view.Errorf("pkg.registry.ratelimited", "%s refused: %s", hostOf(rawURL), resp.Status).
			WithHint("the unauthenticated GitHub API allows 60 calls an hour from one address; wait, or keep the tools list short")
	case resp.StatusCode != http.StatusOK:
		return false, view.Errorf("pkg.registry.status", "%s answered %s", hostOf(rawURL), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, registryBody+1))
	if err != nil {
		return false, view.Errorf("pkg.registry.read", "%s: %v", hostOf(rawURL), err)
	}
	if len(body) > registryBody {
		return false, view.Errorf("pkg.registry.read", "%s answered more than %d MB", hostOf(rawURL), registryBody>>20)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return false, view.Errorf("pkg.registry.read", "%s did not answer JSON: %v", hostOf(rawURL), err)
	}
	return true, nil
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// latestPyPI is the version PyPI calls current for a project.
func (c *registryClient) latestPyPI(ctx context.Context, name string) (string, *view.Error) {
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	found, verr := c.getJSON(ctx, c.pypi+"/pypi/"+url.PathEscape(name)+"/json", &doc)
	if verr != nil || !found {
		return "", verr
	}
	return doc.Info.Version, nil
}

// latestNPM is the dist-tag "latest" of a package. Scoped names keep their
// slash on the registry path, which is why this escapes only the "@".
func (c *registryClient) latestNPM(ctx context.Context, name string) (string, *view.Error) {
	var doc struct {
		Version string `json:"version"`
	}
	path := strings.ReplaceAll(name, "@", "%40")
	found, verr := c.getJSON(ctx, c.npm+"/"+path+"/latest", &doc)
	if verr != nil || !found {
		return "", verr
	}
	return doc.Version, nil
}

// latestCrate is crates.io's highest stable version of a crate.
func (c *registryClient) latestCrate(ctx context.Context, name string) (string, *view.Error) {
	var doc struct {
		Crate struct {
			MaxStable string `json:"max_stable_version"`
			MaxVer    string `json:"max_version"`
		} `json:"crate"`
	}
	found, verr := c.getJSON(ctx, c.crates+"/api/v1/crates/"+url.PathEscape(name), &doc)
	if verr != nil || !found {
		return "", verr
	}
	if doc.Crate.MaxStable != "" {
		return doc.Crate.MaxStable, nil
	}
	return doc.Crate.MaxVer, nil
}

// latestGoModule is the Go module proxy's @latest for a module path. The
// proxy's escaping rule: every uppercase letter becomes "!" plus lowercase.
func (c *registryClient) latestGoModule(ctx context.Context, module string) (string, *view.Error) {
	var doc struct {
		Version string `json:"Version"`
	}
	found, verr := c.getJSON(ctx, c.gomod+"/"+escapeModulePath(module)+"/@latest", &doc)
	if verr != nil || !found {
		return "", verr
	}
	return doc.Version, nil
}

func escapeModulePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// release is what the GitHub API says about a repository's latest release:
// the tag, and every asset with the digest the API has published on assets
// since 2025 — the cheapest verification there is, when it is there.
type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// latestRelease is GET /repos/{owner}/{repo}/releases/latest.
func (c *registryClient) latestRelease(ctx context.Context, owner, repo string) (release, bool, *view.Error) {
	var rel release
	found, verr := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.github, url.PathEscape(owner), url.PathEscape(repo)), &rel)
	return rel, found, verr
}
