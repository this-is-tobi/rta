// Package gitignore finds and prints the templates in github/gitignore, the
// repository GitHub itself offers when somebody creates a repository: one file
// for each language, tool, operating system or editor, each a .gitignore that
// people maintain and many projects start from.
//
// Built in rather than a plugin, by the rule docs/40-plugins/10-plugins.md
// gives: no credential, no configuration, nothing outside the standard
// library, and two fixed public hosts of one provider that no input can
// redirect. A name a caller gives is looked up in the listing the first host
// returns and is put into a URL only when it is in that listing.
//
// Why this repository and not one of the web services that compose templates
// for you. The best known of them is no longer maintained, and the repository
// its templates came from was archived and has since gone, so what it serves
// stops where upstream stopped while it still answers every request. What
// composing is worth is done here, in the open, and nothing is rewritten on
// the way: a section of the answer is an upstream file as it is, which is what
// makes it reviewable against upstream and what a hash can check.
package gitignore

import (
	"context"
	"crypto/sha1" //nolint:gosec // git's own object id, compared with the one GitHub lists for the file; not used as a security hash
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	// apiBase answers one question, which files the repository holds. It is
	// the host with a quota (sixty requests an hour for an address that gives
	// no credential), which is why it is asked once per run and the files
	// themselves come from rawBase.
	apiBase = "https://api.github.com"
	rawBase = "https://raw.githubusercontent.com"

	repo = "github/gitignore"
	ref  = "main"

	userAgent = "rta (https://github.com/this-is-tobi/rta)"

	// requestTimeout bounds one request. A fixed budget, like eol's: both hosts
	// are fast and well behaved, and nothing here needs an operator to widen it.
	requestTimeout = 10 * time.Second

	// maxListing and maxTemplate refuse a runaway response rather than ration a
	// normal one. The listing is about eighty kilobytes and the largest
	// template about thirty.
	maxListing  = 2 << 20
	maxTemplate = 128 << 10

	// listingLife is how long a listing is trusted by a process that lives
	// longer than one call, which is a server or the TUI. A command line asks
	// once and exits.
	listingLife = 10 * time.Minute

	// maxRedirects is how many redirects one request follows.
	maxRedirects = 3

	// maxSpent bounds how long a host's word that an address has used its share
	// is believed without asking again. GitHub's share renews within the hour,
	// so a header that names a later time is not one to stop asking for until.
	maxSpent = time.Hour

	// Codes that are said in one place and read back in another.
	codeTemplateChanged = "gitignore.template.changed"
	codeNotFound        = "gitignore.response.notfound"
	codeRateLimited     = "gitignore.ratelimited"
)

// source is where templates come from. The hosts are fields so that a test can
// point the whole capability at a server that answers wrongly on purpose.
type source struct {
	api, raw string
	client   *http.Client

	mu  sync.Mutex
	cat *catalogue
	at  time.Time
	// asking is closed when the request for the listing that is in flight is
	// over, and is nil when none is. Callers that arrive meanwhile wait on it
	// rather than ask too, and give up when their own context does.
	asking chan struct{}
	// spent is, for each host that has said an address has used its share, when
	// it says the share is renewed.
	spent map[string]time.Time
}

// upstream is the one source anything outside a test uses.
var upstream = &source{api: apiBase, raw: rawBase, client: &http.Client{CheckRedirect: sameHostOnly}}

// sameHostOnly lets a request follow a redirect to the host it started at, over
// https, at most maxRedirects times, and nowhere else. GitHub does redirect a
// renamed repository, and that is the whole of what is worth following: a
// response that sends the listing to another host is not one to read a listing
// from.
func sameHostOnly(req *http.Request, via []*http.Request) error {
	// via holds every request so far, the first included, so it is one longer
	// than the number of redirects already followed.
	if len(via) > maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if from := via[0].URL; req.URL.Scheme != "https" || req.URL.Host != from.Host {
		return fmt.Errorf("refusing a redirect from %s://%s to %s://%s", from.Scheme, from.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// catalogue is the repository's listing, kept for listingLife, and whether it
// is one that was kept rather than read for this call.
//
// One request is made at a time and the callers that arrive while it is in
// flight share its answer, which is what keeps sixty requests an hour from
// being spent by sixty calls that start together. They wait for it in a select
// and not on a lock, so a caller whose context ends is not held past it.
func (s *source) catalogue(ctx context.Context) (cat *catalogue, cached bool, verr *view.Error) {
	waited := false
	for {
		s.mu.Lock()
		if s.cat != nil && time.Since(s.at) < listingLife {
			cat = s.cat
			s.mu.Unlock()
			// A listing another caller read while this one waited was read for
			// this call as much as for that one, and is not a kept one.
			return cat, !waited, nil
		}
		if s.asking == nil {
			s.asking = make(chan struct{})
			s.mu.Unlock()
			cat, verr = s.read(ctx)
			return cat, false, verr
		}
		inFlight := s.asking
		s.mu.Unlock()
		select {
		case <-inFlight:
			// The caller that was asking is done. What it found is read from
			// the top again, and a failure of its own is not this one's: this
			// caller asks next, with its own context.
			waited = true
		case <-ctx.Done():
			return nil, false, requestFailed("the template listing", ctx.Err())
		}
	}
}

// read asks for the listing and wakes the callers waiting on it, whatever the
// answer.
func (s *source) read(ctx context.Context) (cat *catalogue, verr *view.Error) {
	defer func() {
		s.mu.Lock()
		if verr == nil {
			s.cat, s.at = cat, time.Now()
		}
		close(s.asking)
		s.asking = nil
		s.mu.Unlock()
	}()
	body, verr := s.get(ctx, s.api+"/repos/"+repo+"/git/trees/"+ref+"?recursive=1",
		"application/vnd.github+json", maxListing, "the template listing")
	if verr != nil {
		return nil, verr
	}
	var tree treeResponse
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, view.Errorf("gitignore.response.invalid", "reading the template listing: %v", err)
	}
	// A listing cut short would present a repository with templates missing as
	// the whole of it, and every "no such template" after that would be wrong.
	if tree.Truncated {
		return nil, view.Errorf("gitignore.response.truncated", "GitHub cut the template listing short")
	}
	cat = newCatalogue(tree)
	if len(cat.Entries) == 0 {
		return nil, view.Errorf("gitignore.response.empty", "the template listing holds no templates")
	}
	return cat, nil
}

// forget drops the listing a call found out of date, so that the next call
// reads it again, unless one read since is already in place of it.
func (s *source) forget(cat *catalogue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cat == cat {
		s.cat = nil
	}
}

// template is one file's bytes, held to the id the listing gave it.
//
// The id is git's own: the SHA-1 of "blob <length>", a zero byte and the
// content. Checking it ties the bytes to the listing that named them, which
// is what keeps a file read a moment after a commit landed from being passed
// off as part of the listing it did not belong to, and what notices a body
// that was damaged on the way. It is not a defence against somebody who
// controls both hosts; the transport is TLS for that.
func (s *source) template(ctx context.Context, e entry) ([]byte, *view.Error) {
	what := "the " + e.Ref + " template"
	body, verr := s.get(ctx, s.raw+"/"+repo+"/"+ref+"/"+escapePath(e.Path), "text/plain", maxTemplate, what)
	if verr != nil {
		return nil, verr
	}
	if blobID(body) != e.SHA {
		changed := view.Errorf(codeTemplateChanged,
			"%s is not the file the listing named: the repository changed while it was being read", e.Ref).
			WithHint("ask again, and if it is refused again wait a few minutes: the host the files come from can lag the listing")
		changed.Retryable = true
		return nil, changed
	}
	// Bytes somebody else wrote are checked before they are handed back as
	// text (pkg/format says why): a .gitignore is text, and one that is not is
	// not one to print.
	if !plainTemplate(body) {
		return nil, view.Errorf("gitignore.template.notplain",
			"%s is not plain text (it holds a control character other than a tab, a line feed or a carriage return), so it is not printed", e.Ref)
	}
	return body, nil
}

// plainTemplate is format.PlainText held to what a .gitignore is written with:
// no control character but a tab, a line feed and a carriage return.
// PlainText answers "text or binary" and lets an escape, a form feed and a
// vertical tab through, on the ground that what a renderer shows of them is the
// renderer's business. Here the renderer's business is to remove them from the
// file on its way to the person, and a template that says something else once
// they are gone is not one to print as if it were whole. The C1 controls count,
// since a terminal acts on those as well. A carriage return is let through
// because templates do hold one, inside a pattern and at the end of a line, and
// `-o json` carries it; the other formats leave it out, which the capability's
// description says. No template in github/gitignore holds any other control
// character.
func plainTemplate(body []byte) bool {
	if !format.PlainText(body) {
		return false
	}
	for _, r := range string(body) {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

func blobID(b []byte) string {
	h := sha1.New() //nolint:gosec // see the import
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(b))
	_, _ = h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// escapePath escapes each segment of a repository path and keeps the slashes.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (s *source) get(ctx context.Context, rawURL, accept string, limit int64, what string) ([]byte, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, view.Errorf("gitignore.request.invalid", "building the request for %s: %v", what, err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	// A host that has said this address used its share is not asked again until
	// it says the share is back: the answer is the same, and a caller that
	// honours "retryable" would otherwise ask once a call for as long as it
	// takes, which GitHub's own guidance says not to do.
	if until, spent := s.spentUntil(req.URL.Host); spent {
		return nil, quotaError(until, true)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, requestFailed(what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && quotaSpent(resp) {
			until, known := quotaResetsAt(resp)
			if known {
				s.spendQuota(req.URL.Host, until)
			}
			return nil, quotaError(until, known)
		}
		return nil, statusError(resp, what)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, view.Errorf("gitignore.response.read", "reading %s: %v", what, err)
	}
	if int64(len(body)) > limit {
		return nil, view.Errorf("gitignore.response.toolarge", "%s is larger than the %s this reads", what, format.Bytes(limit))
	}
	return body, nil
}

// requestFailed is a request that did not get an answer, which is retryable
// whatever kept it from one.
func requestFailed(what string, err error) *view.Error {
	failed := view.Errorf("gitignore.request.failed", "asking GitHub for %s: %v", what, err).
		WithHint("check network access: api.github.com and raw.githubusercontent.com must be reachable")
	failed.Retryable = true
	return failed
}

// quotaError is a spent quota, which is cured by waiting and says until when
// when the host said.
func quotaError(until time.Time, known bool) *view.Error {
	msg := "GitHub limits what an address can ask without signing in, and this one has used its share"
	if known {
		msg += " until " + format.Clock(until)
	}
	spent := view.Errorf(codeRateLimited, "%s", msg).
		WithHint("wait until then and ask again; each call here costs one such request at most")
	spent.Retryable = true
	return spent
}

// spentUntil is when a host that has refused this address for a spent quota
// says it will answer again, while that is still ahead.
func (s *source) spentUntil(host string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.spent[host]
	if ok && !time.Now().Before(until) {
		delete(s.spent, host)
		return time.Time{}, false
	}
	return until, ok
}

// spendQuota remembers that a host refused this address for a spent quota,
// until the time it named or maxSpent from now, whichever comes first.
func (s *source) spendQuota(host string, until time.Time) {
	if latest := time.Now().Add(maxSpent); until.After(latest) {
		until = latest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.spent == nil {
		s.spent = map[string]time.Time{}
	}
	s.spent[host] = until
}

// statusError words a response that was not a 200 and was not a spent quota.
func statusError(resp *http.Response, what string) *view.Error {
	if resp.StatusCode == http.StatusNotFound {
		return view.Errorf(codeNotFound, "GitHub does not have %s", what)
	}
	bad := view.Errorf("gitignore.request.status", "GitHub answered %d for %s", resp.StatusCode, what)
	bad.Retryable = resp.StatusCode >= 500
	return bad
}

func quotaSpent(resp *http.Response) bool {
	return resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""
}

func quotaResetsAt(resp *http.Response) (time.Time, bool) {
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if sec, err := strconv.ParseInt(v, 10, 64); err == nil && sec > 0 {
			return time.Unix(sec, 0), true
		}
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec >= 0 {
			return time.Now().Add(time.Duration(sec) * time.Second), true
		}
	}
	return time.Time{}, false
}

// treeResponse is the part of GitHub's tree listing this reads. A field it does
// not name is ignored, so a field GitHub adds later changes nothing here.
type treeResponse struct {
	SHA       string `json:"sha"`
	Truncated bool   `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"tree"`
}
