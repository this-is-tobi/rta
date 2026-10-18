package pkg

import (
	"context"
	"encoding/json"
	"errors"
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
	// tags is the process's memory of the latest tags (see latestTag).
	tags *tagCache
}

const (
	registryTimeout = 10 * time.Second
	// tagLife is how long the process believes a latest tag it was told: a
	// release lands a few times a month, and the views that ask — the tile, the
	// page it opens, an `r` on either — would each ask again.
	tagLife = 10 * time.Minute
	// registryBody caps one registry answer. PyPI's project document lists
	// every file of every release ever published, which for a popular
	// package is tens of megabytes uncompressed — measured: black and ruff
	// both cleared 4 MiB. The cap is a bound against a hostile answer, not
	// a size expectation, so it sits well above the largest honest one.
	registryBody = 64 << 20

	// The two answers after which asking about the next name would get the same
	// one (see readTools), said in one place and read back in another.
	codeRateLimited = "pkg.registry.ratelimited"
	codeUnreachable = "pkg.registry.unreachable"
)

// processTags is the one memory every registry client of the process reads
// and writes: pkg.tools, pkg.overview and the page that opens from it each
// build a client of their own, and a client's own memory would be forgotten
// by the next view.
var processTags = newTagCache(time.Now)

func newRegistryClient() *registryClient {
	return &registryClient{
		tags: processTags,
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
		return false, view.Errorf(codeUnreachable, "%s: %v", hostOf(rawURL), err).
			WithHint("the latest-version lookup needs the registry; the installed column is still right")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	case resp.StatusCode == http.StatusForbidden && strings.HasPrefix(rawURL, c.github):
		return false, view.Errorf(codeRateLimited, "%s refused: %s", hostOf(rawURL), resp.Status).
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
//
// **What it was told is kept for tagLife**, by the process and not by the
// client, so that the tile, the page that enter opens and an `r` on either
// ask once between them (see tagCache).
func (c *registryClient) latestTag(ctx context.Context, owner, repo string) (tag string, found bool, verr *view.Error) {
	return c.tags.lookup(ctx, c.web+"/"+owner+"/"+repo, func() (string, bool, *view.Error) {
		return c.askLatestTag(ctx, owner, repo)
	})
}

func (c *registryClient) askLatestTag(ctx context.Context, owner, repo string) (tag string, found bool, verr *view.Error) {
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
			return "", false, view.Errorf(codeUnreachable, "%s: %v", hostOf(target), cause(err)).
				WithHint("the latest-version lookup needs github.com; the installed column is still right")
		}
		loc := resp.Header.Get("Location")
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return "", false, nil
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		case http.StatusTooManyRequests, http.StatusForbidden:
			return "", false, view.Errorf(codeRateLimited, "%s refused: %s", hostOf(target), resp.Status).
				WithHint("wait a few minutes; github.com is asking for fewer requests")
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

// tagCache is what a process was told about the latest tags, for tagLife.
//
// An answer is kept when GitHub gave one, and "no release" is one; a refusal
// or a failure to connect says nothing about the tag and is asked again next
// time. One request is made at a time and a caller that arrives meanwhile reads
// what it found, which is what keeps two tiles that start together from asking
// twice: the gate is a channel and not a lock so that a caller whose context
// ends is not held past it by another's request.
//
// The keys are the repositories an operator's tools list names, thirty at
// most, so the memory is as bounded as the list is.
type tagCache struct {
	now  func() time.Time
	gate chan struct{}
	kept map[string]keptTag
}

type keptTag struct {
	tag   string
	found bool
	at    time.Time
}

func newTagCache(now func() time.Time) *tagCache {
	return &tagCache{now: now, gate: make(chan struct{}, 1), kept: map[string]keptTag{}}
}

func (k *tagCache) lookup(ctx context.Context, key string, ask func() (string, bool, *view.Error)) (string, bool, *view.Error) {
	select {
	case k.gate <- struct{}{}:
		defer func() { <-k.gate }()
	case <-ctx.Done():
		return "", false, view.Errorf(codeUnreachable, "%s: %v", hostOf(key), ctx.Err())
	}
	if e, ok := k.kept[key]; ok && k.now().Sub(e.at) < tagLife {
		return e.tag, e.found, nil
	}
	tag, found, verr := ask()
	if verr == nil {
		k.kept[key] = keptTag{tag: tag, found: found, at: k.now()}
	}
	return tag, found, verr
}

// cause is what went wrong without the request it went wrong for. net/http
// wraps every failure in the verb and the URL, and the URL names one
// repository, which the message of a failure that speaks for the whole list
// must not do: it is the reason every row after the first gives.
func cause(err error) error {
	var wrapped *url.Error
	if errors.As(err, &wrapped) {
		return wrapped.Err
	}
	return err
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
