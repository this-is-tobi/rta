package pkg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// latestTags is github.com as far as a latest-release link goes: for each
// "owner/repo" it knows, /owner/repo/releases/latest answers 302 to
// /owner/repo/releases/tag/<tag>; anything else is a 404. The base to put in
// registryClient.web.
func latestTags(t *testing.T, tags map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/releases/latest")
		if tag, ok := tags[repo]; ok && strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, "/"+repo+"/releases/tag/"+tag, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// **A tools list spends none of the GitHub API's budget.** Unauthenticated,
// the API allows sixty calls an hour from one address, which a machine that
// also runs mise or gh shares with them; a list of seven tools spent seven on
// every call and every tile refresh, and every row then read "api.github.com
// refused: 403 Forbidden". The tag is read from github.com's own link
// instead, and the API is not asked.
func TestAToolsListIsNeverAskedOfTheAPI(t *testing.T) {
	var apiCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer api.Close()
	c := newRegistryClient()
	c.github = api.URL
	c.web = latestTags(t, map[string]string{"junegunn/fzf": "v0.55.0"})
	f := &fake{bins: map[string]bool{"fzf": true}, answers: map[string]fakeAnswer{
		"fzf --version": {out: "0.50.0\n"},
	}}
	install(t, f)

	states, verr := readTools(context.Background(), c, []string{"fzf=github:junegunn/fzf"})
	if verr != nil {
		t.Fatal(verr)
	}
	if states[0].Note != "" || states[0].Latest != "0.55.0" || !states[0].behind() {
		t.Errorf("fzf = %+v, want it read as behind 0.55.0 with no note", states[0])
	}
	if n := apiCalls.Load(); n != 0 {
		t.Errorf("the API was asked %d times for a list of tools, want none", n)
	}
}

func TestALatestTagIsReadFromTheLink(t *testing.T) {
	for _, tc := range []struct {
		name, location, tag string
		found               bool
	}{
		{"a plain tag", "/o/r/releases/tag/v1.2.3", "v1.2.3", true},
		{"a tag with a slash", "/o/r/releases/tag/release/1.4", "release/1.4", true},
		{"an escaped tag", "/o/r/releases/tag/v1.2.3%2Brc1", "v1.2.3+rc1", true},
		{"a repository with no release", "/o/r/releases", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tc.location)
				w.WriteHeader(http.StatusFound)
			}))
			defer srv.Close()
			c := newRegistryClient()
			c.web = srv.URL
			tag, found, verr := c.latestTag(context.Background(), "o", "r")
			if verr != nil {
				t.Fatal(verr)
			}
			if tag != tc.tag || found != tc.found {
				t.Errorf("latestTag = %q, %v; want %q, %v", tag, found, tc.tag, tc.found)
			}
		})
	}
}

func TestAnUnknownRepositoryHasNoRelease(t *testing.T) {
	c := newRegistryClient()
	c.web = latestTags(t, nil)
	if _, found, verr := c.latestTag(context.Background(), "no", "such"); verr != nil || found {
		t.Errorf("latestTag = %v, %v; want no release and no error", found, verr)
	}
}

// A renamed or transferred repository redirects its link to the new name's,
// and the new name's link names the tag.
func TestARenamedRepositoryIsFollowedToItsNewLink(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/old/name/releases/latest":
			http.Redirect(w, r, "/new/name/releases/latest", http.StatusMovedPermanently)
		case "/new/name/releases/latest":
			http.Redirect(w, r, "/new/name/releases/tag/v3.0.0", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.web = srv.URL
	tag, found, verr := c.latestTag(context.Background(), "old", "name")
	if verr != nil || !found || tag != "v3.0.0" {
		t.Fatalf("latestTag = %q, %v, %v; want v3.0.0 through the rename (asked %v)", tag, found, verr, asked)
	}
}

func TestARedirectLoopIsGivenUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.web = srv.URL
	if _, found, verr := c.latestTag(context.Background(), "o", "r"); verr == nil || found || verr.Code != "pkg.registry.read" {
		t.Errorf("latestTag = %v, %+v; want pkg.registry.read", found, verr)
	}
}

// **What the link may say is narrow.** It is a read of a destination the
// operator's tools list names, but the answer is the other side's: a
// Location that leaves the host, or a tag that holds a control sequence, is
// not something to carry into a table.
func TestALinkThatLeavesTheHostOrCarriesAControlIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, location string }{
		{"another host", "https://evil.example/o/r/releases/tag/v1"},
		{"another path", "/somewhere/else"},
		{"an escape in the tag", "/o/r/releases/tag/v1%1B%5B2J"},
		{"a space in the tag", "/o/r/releases/tag/v1%20x"},
		{"an empty tag", "/o/r/releases/tag/"},
		{"a non-ASCII tag", "/o/r/releases/tag/v1%E2%80%AE"},
		{"a very long tag", "/o/r/releases/tag/" + strings.Repeat("a", 300)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tc.location)
				w.WriteHeader(http.StatusFound)
			}))
			defer srv.Close()
			c := newRegistryClient()
			c.web = srv.URL
			tag, found, verr := c.latestTag(context.Background(), "o", "r")
			if verr == nil || found || tag != "" {
				t.Errorf("latestTag = %q, %v, %v; want a refusal", tag, found, verr)
			}
		})
	}
}

func TestAnAnsweredRefusalIsSaidWithItsRemedy(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		c := newRegistryClient()
		c.web = srv.URL
		_, found, verr := c.latestTag(context.Background(), "o", "r")
		srv.Close()
		if verr == nil || found || verr.Code != "pkg.registry.ratelimited" || verr.Hint == "" {
			t.Errorf("status %d: latestTag = %v, %+v; want pkg.registry.ratelimited with a hint", status, found, verr)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.web = srv.URL
	if _, _, verr := c.latestTag(context.Background(), "o", "r"); verr == nil || verr.Code != "pkg.registry.status" {
		t.Errorf("a 502 gave %+v, want pkg.registry.status", verr)
	}
}
