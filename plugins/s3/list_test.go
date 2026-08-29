package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A bucket is not a directory and does not behave like one: a build-artifact
// bucket holds millions of keys, and this used to walk every one of them into
// a table in memory. What makes the bound safe to add is that the answer says
// it stopped — a listing that quietly ends at 200 reads exactly like a bucket
// with 200 things in it.

// listingServer answers ListObjects v2 with `count` generated keys, in the
// XML minio-go parses. Enough of the real protocol to drive the handler end to
// end without a live S3.
func listingServer(t *testing.T, count int, size int64) *httptest.Server {
	t.Helper()
	type object struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		Size         int64  `xml:"Size"`
		ETag         string `xml:"ETag"`
	}
	type result struct {
		XMLName     xml.Name `xml:"ListBucketResult"`
		Name        string   `xml:"Name"`
		IsTruncated bool     `xml:"IsTruncated"`
		Contents    []object `xml:"Contents"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := result{Name: "test-bucket"}
		after := r.URL.Query().Get("start-after")
		for i := range count {
			key := fmt.Sprintf("obj-%04d", i)
			if after != "" && key <= after {
				continue
			}
			res.Contents = append(res.Contents, object{
				Key: key, Size: size, ETag: `"e"`,
				LastModified: "2026-08-01T10:00:00.000Z",
			})
		}
		w.Header().Set("Content-Type", "application/xml")
		_ = xml.NewEncoder(w).Encode(res)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func listTable(t *testing.T, srv *httptest.Server, values map[string]any) view.Table {
	t.Helper()
	values["bucket"] = "test-bucket"
	v, err := runObjectList(context.Background(),
		reqFor(t, "s3.object.list", endpointOf(t, srv), values))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}
	return tbl
}

func TestAListingStopsAtTheLimitAndSaysWhereToContinue(t *testing.T) {
	srv := listingServer(t, 500, 10)

	tbl := listTable(t, srv, map[string]any{})
	if len(tbl.Rows) != listLimit {
		t.Fatalf("rows = %d, want the default limit of %d", len(tbl.Rows), listLimit)
	}
	if tbl.Page == nil || tbl.Page.Next == "" {
		t.Fatal("a bounded listing came back looking complete")
	}
	// The continuation is the last key returned, which is what --after takes.
	if got, want := tbl.Page.Next, tbl.Rows[len(tbl.Rows)-1][0]; got != want {
		t.Errorf("next = %q, want the last key shown (%q)", got, want)
	}

	// And it continues from there without repeating the boundary key.
	next := listTable(t, srv, map[string]any{"after": tbl.Page.Next})
	if next.Rows[0][0] == tbl.Rows[len(tbl.Rows)-1][0] {
		t.Error("the next page repeats the key it was told to start after")
	}
}

// A listing that fits says nothing about continuing — a cursor on a complete
// answer sends somebody looking for data that is not there.
func TestAListingThatFitsCarriesNoCursor(t *testing.T) {
	srv := listingServer(t, 5, 10)

	tbl := listTable(t, srv, map[string]any{})
	if len(tbl.Rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(tbl.Rows))
	}
	if tbl.Page != nil {
		t.Errorf("a complete listing carries a cursor: %+v", tbl.Page)
	}
}

// An exactly-full page is the case a count cannot tell apart from a truncated
// one, which is why the handler reads one past the limit rather than counting.
func TestAnExactlyFullPageIsNotReportedAsTruncated(t *testing.T) {
	srv := listingServer(t, 10, 10)

	tbl := listTable(t, srv, map[string]any{"limit": 10})
	if len(tbl.Rows) != 10 {
		t.Fatalf("rows = %d, want 10", len(tbl.Rows))
	}
	if tbl.Page != nil {
		t.Errorf("a listing that ended exactly on the limit was reported as having more: %+v", tbl.Page)
	}
}

// pkg/format exists because "the first one to show a byte count showed
// 1392640", and this was it.
func TestSizesAreReadable(t *testing.T) {
	srv := listingServer(t, 1, 1392640)

	tbl := listTable(t, srv, map[string]any{})
	if got := tbl.Rows[0][1]; got != "1.3 MiB" {
		t.Errorf("size = %q, want it in units a person reads", got)
	}
	if _, err := strconv.ParseInt(tbl.Rows[0][1], 10, 64); err == nil {
		t.Error("the size column is still a raw byte count")
	}
}
