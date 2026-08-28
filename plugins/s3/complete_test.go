package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The recipes behind live completion, proven against a server that records
// what was asked: names only come back, only GETs go out, the delimiter
// walks one segment, and every failure is silence rather than an error.

// recordingS3 serves canned XML and records every request line.
func recordingS3(t *testing.T, body string) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		asked []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

const bucketsXML = `<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Owner><ID>me</ID></Owner>
  <Buckets>
    <Bucket><Name>media</Name><CreationDate>2024-03-01T00:00:00.000Z</CreationDate></Bucket>
    <Bucket><Name>backups</Name><CreationDate>2024-03-01T00:00:00.000Z</CreationDate></Bucket>
  </Buckets>
</ListAllMyBucketsResult>`

const keysXML = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>backups</Name>
  <Prefix>db/</Prefix>
  <Delimiter>/</Delimiter>
  <KeyCount>2</KeyCount>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>db/dump.sql</Key><Size>10</Size>
    <LastModified>2024-03-01T00:00:00.000Z</LastModified></Contents>
  <CommonPrefixes><Prefix>db/daily/</Prefix></CommonPrefixes>
</ListBucketResult>`

func TestSuggestBucketsListsNamesOnlyThroughGETs(t *testing.T) {
	srv, asked := recordingS3(t, bucketsXML)
	r := reqFor(t, "s3.bucket.list", endpointOf(t, srv), map[string]any{})

	got := suggestBuckets(context.Background(), r)
	if len(got) != 2 || got[0] != "backups" || got[1] != "media" {
		t.Fatalf("suggestBuckets = %v, want the two names, sorted", got)
	}
	for _, line := range asked() {
		if !strings.HasPrefix(line, "GET ") {
			t.Errorf("a completion sent %q — a listing must never be anything but a GET", line)
		}
	}
}

func TestSuggestKeysWalksOneSegmentAndKeepsTheSeparator(t *testing.T) {
	srv, asked := recordingS3(t, keysXML)
	r := reqFor(t, "s3.object.list", endpointOf(t, srv), map[string]any{
		"bucket": "backups", "prefix": "db/",
	})

	got := suggestKeys("prefix")(context.Background(), r)
	want := map[string]bool{"db/dump.sql": true, "db/daily/": true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("suggestKeys = %v, want the key and the common prefix with its trailing slash — "+
			"the slash is what lets the next press fetch deeper", got)
	}
	segmentWalked := false
	for _, line := range asked() {
		if !strings.HasPrefix(line, "GET ") {
			t.Errorf("a completion sent %q, want GETs only", line)
		}
		if strings.Contains(line, "delimiter=%2F") && strings.Contains(line, "prefix=db%2F") {
			segmentWalked = true
		}
	}
	if !segmentWalked {
		t.Errorf("no request pinned delimiter=/ with the typed prefix; asked: %v — without the "+
			"delimiter this walks the whole bucket", asked())
	}
}

// A million-object bucket answers with a screenful, and the listing stops
// there rather than draining the rest for nothing.
func TestAListingIsCapped(t *testing.T) {
	var keys strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&keys, "<Contents><Key>k-%03d</Key><Size>1</Size>"+
			"<LastModified>2024-03-01T00:00:00.000Z</LastModified></Contents>", i)
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>big</Name><KeyCount>200</KeyCount><IsTruncated>false</IsTruncated>` +
		keys.String() + `</ListBucketResult>`
	srv, _ := recordingS3(t, body)
	r := reqFor(t, "s3.object.list", endpointOf(t, srv), map[string]any{"bucket": "big"})

	got := suggestKeys("prefix")(context.Background(), r)
	if len(got) != completionCap {
		t.Errorf("listing returned %d entries, want it capped at %d", len(got), completionCap)
	}
}

// Every failure is silence: a completion that cannot answer must slow nobody
// down, and the run that follows classifies the same failure with a code and
// a hint.
func TestSuggestionsAreSilentOnEveryFailure(t *testing.T) {
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(denied.Close)

	cases := []struct {
		name     string
		endpoint string
	}{
		{"denied", endpointOf(t, denied)},
		{"nothing listening", "127.0.0.1:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := reqFor(t, "s3.object.list", tc.endpoint, map[string]any{"bucket": "b"})
			if got := suggestBuckets(context.Background(), r); got != nil {
				t.Errorf("suggestBuckets = %v, want silence", got)
			}
			if got := suggestKeys("prefix")(context.Background(), r); got != nil {
				t.Errorf("suggestKeys = %v, want silence", got)
			}
		})
	}
}

// The wiring is part of the contract: every input whose values exist
// server-side declares Live with a Suggest, so a refactor cannot silently
// put a service listing back on the keystroke channel — or drop the
// completion — without failing here.
func TestServerSideInputsDeclareLive(t *testing.T) {
	wantLive := map[string]bool{"bucket": true, "key": true, "prefix": true, "dest-bucket": true}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if wantLive[f.Name] {
				seen[f.Name] = true
				if !f.Live || f.Suggest == nil {
					t.Errorf("%s: input %q is not Live with a Suggest", c.ID, f.Name)
				}
			} else if f.Live {
				t.Errorf("%s: input %q declares Live, and nothing here lists it server-side", c.ID, f.Name)
			}
		}
	}
	for name := range wantLive {
		if !seen[name] {
			t.Errorf("no capability declares input %q — the wiring this test pins moved", name)
		}
	}
}
