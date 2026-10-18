package pkg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// toolsOnPath is a tools list of n binaries that are all on $PATH and all
// print 1.0.0, with the entries that list them.
func toolsOnPath(t *testing.T, names ...string) []string {
	t.Helper()
	f := &fake{bins: map[string]bool{}, answers: map[string]fakeAnswer{}}
	var raw []string
	for _, n := range names {
		f.bins[n] = true
		f.answers[n+" --version"] = fakeAnswer{out: "1.0.0\n"}
		raw = append(raw, n+"=github:o/"+n)
	}
	install(t, f)
	return raw
}

// **The first refusal that the rest of the list would meet too ends the
// asking.** Seven tools against an address GitHub had stopped answering took
// seven requests, each refused the same way, or seven ten-second timeouts
// against a network that was down. The rows after the refusal say they were
// not checked, with the reason the first one gave, and cost no request; what is
// installed is still read for every one.
func TestARefusalStopsTheRestOfAToolsList(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.web = srv.URL
	c.tags = newTagCache(time.Now)
	raw := toolsOnPath(t, "a", "b", "c")

	states, verr := readTools(context.Background(), c, raw)
	if verr != nil {
		t.Fatal(verr)
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("a refused list of 3 asked %d times, want the one", n)
	}
	reason := hostOf(srv.URL) + " refused: 403 Forbidden"
	if !strings.HasPrefix(states[0].Note, reason) || !strings.Contains(states[0].Note, "wait a few minutes") {
		t.Errorf("the refused row = %q, want the refusal and the first clause of its remedy", states[0].Note)
	}
	for _, s := range states[1:] {
		if want := "not checked — " + reason; s.Note != want {
			t.Errorf("%s: note = %q, want %q", s.tool.Bin, s.Note, want)
		}
		if s.Installed != "1.0.0" {
			t.Errorf("%s: installed = %q, want the version the binary prints, which costs no request", s.tool.Bin, s.Installed)
		}
	}
	if status := toolsTable(states).Rows[2][4]; !strings.HasPrefix(status, "info not checked — ") {
		t.Errorf("status = %q, want an info row that says it was not checked", status)
	}
}

// refusingTransport is a network that is down: every request fails to connect,
// and it counts them.
type refusingTransport struct{ n atomic.Int32 }

func (r *refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.n.Add(1)
	return nil, errors.New("dial tcp: connection refused")
}

// A host that cannot be reached is the other answer every further tool would
// wait ten seconds to hear.
func TestAnUnreachableHostStopsTheRestOfAToolsList(t *testing.T) {
	down := &refusingTransport{}
	c := newRegistryClient()
	c.noFollow = &http.Client{Transport: down}
	c.tags = newTagCache(time.Now)
	raw := toolsOnPath(t, "a", "b", "c")

	states, verr := readTools(context.Background(), c, raw)
	if verr != nil {
		t.Fatal(verr)
	}
	if n := down.n.Load(); n != 1 {
		t.Errorf("a list of 3 against a host that is down asked %d times, want the one", n)
	}
	for _, s := range states[1:] {
		if want := "not checked — github.com: dial tcp: connection refused"; s.Note != want {
			t.Errorf("%s: note = %q, want %q, which names no repository", s.tool.Bin, s.Note, want)
		}
	}
}

// Only the answers that every further request would get too end the asking:
// a repository GitHub does not know, or a status that is its own, says
// nothing about the next one.
func TestARowOnlyItsOwnFailureDoesNotStopTheList(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.web = srv.URL
	c.tags = newTagCache(time.Now)
	raw := toolsOnPath(t, "a", "b", "c")

	if _, verr := readTools(context.Background(), c, raw); verr != nil {
		t.Fatal(verr)
	}
	if n := asked.Load(); n != 3 {
		t.Errorf("three tools answered 502 each asked %d times, want 3", n)
	}
}

// requestLog is github.com for the tests that count what is asked: it
// answers a link for the repositories in tags, a 404 for any other, and a 403
// for the ones in refused, and keeps a count per path.
type requestLog struct {
	mu      sync.Mutex
	asked   map[string]int
	refused map[string]bool
	URL     string
}

func newRequestLog(t *testing.T, tags map[string]string, refused ...string) *requestLog {
	t.Helper()
	l := &requestLog{asked: map[string]int{}, refused: map[string]bool{}}
	for _, r := range refused {
		l.refused[r] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/releases/latest")
		l.mu.Lock()
		l.asked[repo]++
		denied := l.refused[repo]
		l.mu.Unlock()
		if denied {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if tag, ok := tags[repo]; ok {
			http.Redirect(w, r, "/"+repo+"/releases/tag/"+tag, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	l.URL = srv.URL
	return l
}

func (l *requestLog) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, v := range l.asked {
		n += v
	}
	return n
}

// **A latest tag is asked once and kept for ten minutes by the process.** The
// tile, the page it opens on enter and an `r` on either each ran the whole list
// again, so a TUI that looked at pkg.tools three times spent three times the
// requests. Every registry client of a process shares the one memory, so
// pkg.overview and pkg.tools read each other's answers. A repository with no
// release is an answer and is kept; a refusal says nothing about the tag and is
// asked again.
func TestALatestTagIsKeptForTenMinutesAndSharedByEveryClient(t *testing.T) {
	logged := newRequestLog(t, map[string]string{"o/a": "v1.0.0", "o/flaky": "v2.0.0"}, "o/flaky")
	now := time.Now()
	shared := newTagCache(func() time.Time { return now })
	newClient := func() *registryClient {
		c := newRegistryClient()
		c.web = logged.URL
		c.tags = shared
		return c
	}
	raw := toolsOnPath(t, "a", "none", "flaky")

	read := func() []toolState {
		t.Helper()
		states, verr := readTools(context.Background(), newClient(), raw)
		if verr != nil {
			t.Fatal(verr)
		}
		return states
	}
	states := read()
	if states[0].Latest != "1.0.0" || states[1].Note != "no release on GitHub" || states[2].Note == "" {
		t.Fatalf("first pass = %+v", states)
	}
	if n := logged.total(); n != 3 {
		t.Fatalf("the first pass asked %d times, want 3", n)
	}

	now = now.Add(9 * time.Minute)
	logged.mu.Lock()
	delete(logged.refused, "o/flaky")
	logged.mu.Unlock()
	states = read()
	if states[0].Latest != "1.0.0" || states[1].Note != "no release on GitHub" {
		t.Errorf("the second pass = %+v, want the kept answers", states)
	}
	if states[2].Latest != "2.0.0" {
		t.Errorf("the repository that had refused = %+v, want it read now that it answers", states[2])
	}
	if logged.asked["o/a"] != 1 || logged.asked["o/none"] != 1 {
		t.Errorf("a kept tag and a kept 'no release' were asked again: %v", logged.asked)
	}
	if logged.asked["o/flaky"] != 2 {
		t.Errorf("the refused repository was asked %d times, want 2: a refusal is not kept", logged.asked["o/flaky"])
	}

	now = now.Add(2 * time.Minute)
	read()
	if logged.asked["o/a"] != 2 || logged.asked["o/none"] != 2 {
		t.Errorf("after eleven minutes: %v, want every answer asked again", logged.asked)
	}
}

// The memory is the process's, not a client's: pkg.tools, pkg.overview and the
// page each build their own client.
func TestEveryRegistryClientOfAProcessSharesOneTagMemory(t *testing.T) {
	first, second := newRegistryClient(), newRegistryClient()
	if first.tags != second.tags {
		t.Error("two registry clients keep their own latest tags, so each view asks again")
	}
}

// Callers that arrive together share one request, and one whose context ends
// is not held past it by another's.
func TestCallersThatArriveTogetherShareOneRequest(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		time.Sleep(50 * time.Millisecond)
		http.Redirect(w, r, "/o/r/releases/tag/v1.0.0", http.StatusFound)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.web = srv.URL
	c.tags = newTagCache(time.Now)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tag, found, verr := c.latestTag(context.Background(), "o", "r"); verr != nil || !found || tag != "v1.0.0" {
				t.Errorf("latestTag = %q, %v, %v", tag, found, verr)
			}
		}()
	}
	wg.Wait()
	if n := asked.Load(); n != 1 {
		t.Errorf("four callers asked %d times, want the one", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.tags.gate <- struct{}{}
	cancel()
	if _, _, verr := c.latestTag(ctx, "o", "other"); verr == nil || verr.Code != "pkg.registry.unreachable" {
		t.Errorf("a caller whose context ended while waiting got %+v, want pkg.registry.unreachable", verr)
	}
	<-c.tags.gate
}
