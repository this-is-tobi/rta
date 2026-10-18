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
	// web is github.com itself, whose "latest release" link answers with the
	// tag and spends none of the API's budget (see latestTag).
	web string
	// noFollow is the client that reads a redirect instead of following it.
	noFollow *http.Client
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
		http: &http.Client{Timeout: registryTimeout},
		noFollow: &http.Client{Timeout: registryTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		web:    "https://github.com",
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
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// latestTag is the tag of a repository's latest release, read from where
// github.com's own "latest release" link points: GET /{owner}/{repo}/releases/latest
// answers 302 with a Location ending /releases/tag/{tag}, and the rest of the
// site is not the API.
//
// **The API is the wrong door for asking after a tag.** Unauthenticated, it
// allows 60 calls an hour from one address, and the address is shared with
// everything else on the machine and the network that asks it (mise, gh, a
// CI runner behind one NAT); a tools list of seven spent seven of them on
// every `pkg.tools`, every tile refresh and every agent call, and the sixty
// were gone before the hour was. A conditional request does not help: measured,
// a 304 is counted like any answer. This door costs none of the budget, and
// what the API adds, a release's assets and the digests published on them,
// only an upgrade needs, so that one call (latestRelease) stays where the
// upgrade is.
//
// The redirect is read, never followed, and what it may say is narrow. A
// renamed repository answers with a redirect to its new name's own link, which
// is followed at most twice and only to the same host; the answer for the
// latest release is a /releases/tag/ path on that host; a repository with no
// release answers with its /releases page, and that is "no release", the
// same as a 404. Anything else, another host included, is an answer rta does
// not know how to read.
func (c *registryClient) latestTag(ctx context.Context, owner, repo string) (tag string, found bool, verr *view.Error) {
	base, err := url.Parse(c.web)
	if err != nil {
		return "", false, view.Errorf("pkg.registry.request", "%v", err)
	}
	target := fmt.Sprintf("%s/%s/%s/releases/latest", c.web, url.PathEscape(owner), url.PathEscape(repo))
	for hop := 0; hop <= 2; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return "", false, view.Errorf("pkg.registry.request", "%v", err)
		}
		req.Header.Set("User-Agent", "rta-pkg (+https://github.com/this-is-tobi/rta)")
		resp, err := c.noFollow.Do(req)
		if err != nil {
			return "", false, view.Errorf("pkg.registry.unreachable", "%s: %v", hostOf(target), err).
				WithHint("the latest-version lookup needs github.com; the installed column is still right")
		}
		loc := resp.Header.Get("Location")
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return "", false, nil
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		case http.StatusTooManyRequests, http.StatusForbidden:
			return "", false, view.Errorf("pkg.registry.ratelimited", "%s refused: %s", hostOf(target), resp.Status).
				WithHint("github.com is asking for fewer requests; wait a few minutes")
		default:
			return "", false, view.Errorf("pkg.registry.status", "%s answered %s", hostOf(target), resp.Status)
		}
		next, err := req.URL.Parse(loc)
		if err != nil || next.Host != base.Host {
			return "", false, view.Errorf("pkg.registry.read", "%s sent the latest release of %s/%s somewhere rta does not follow",
				hostOf(target), owner, repo)
		}
		parts := strings.Split(strings.Trim(next.EscapedPath(), "/"), "/")
		switch {
		case len(parts) >= 5 && parts[2] == "releases" && parts[3] == "tag":
			// A tag may hold slashes (release/1.4), which the link leaves as they are.
			tag, err = url.PathUnescape(strings.Join(parts[4:], "/"))
			if err != nil || !plainTag(tag) {
				return "", false, view.Errorf("pkg.registry.read", "%s/%s has a latest release whose tag rta cannot show", owner, repo)
			}
			return tag, true, nil
		case len(parts) == 3 && parts[2] == "releases":
			return "", false, nil
		case len(parts) == 4 && parts[2] == "releases" && parts[3] == "latest":
			target = next.String()
		default:
			return "", false, view.Errorf("pkg.registry.read", "%s sent the latest release of %s/%s somewhere rta does not follow",
				hostOf(target), owner, repo)
		}
	}
	return "", false, view.Errorf("pkg.registry.read", "%s/%s redirects more than it should", owner, repo)
}

// plainTag is whether a tag is something a table can show as it is: printable
// ASCII, no space, nothing a terminal could take for a command.
func plainTag(tag string) bool {
	if tag == "" || len(tag) > 200 {
		return false
	}
	for _, r := range tag {
		if r <= ' ' || r >= 0x7f {
			return false
		}
	}
	return true
}

// latestRelease is GET /repos/{owner}/{repo}/releases/latest.
func (c *registryClient) latestRelease(ctx context.Context, owner, repo string) (release, bool, *view.Error) {
	var rel release
	found, verr := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.github, url.PathEscape(owner), url.PathEscape(repo)), &rel)
	return rel, found, verr
}
