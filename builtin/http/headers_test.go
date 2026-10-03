package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The command line and the form cut a list at commas, so a header with a comma
// in its value arrives in pieces and is put back together.
func TestWholeHeadersRejoinsOnTheSurfacesThatCut(t *testing.T) {
	in := []string{"Accept: text/html", " application/json"}
	want := []string{"Accept: text/html, application/json"}
	for _, s := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		if got := wholeHeaders(s, in); !slices.Equal(got, want) {
			t.Errorf("%v: got %q, want %q", s, got, want)
		}
	}
}

// Over MCP the list arrives as the caller wrote it, so a piece that is no header
// is the caller's mistake and is not glued onto the header before it.
func TestWholeHeadersLeavesAnMCPListAlone(t *testing.T) {
	in := []string{"Accept: text/html", " application/json"}
	if got := wholeHeaders(plugin.SurfaceMCP, in); !slices.Equal(got, in) {
		t.Errorf("got %q, want %q", got, in)
	}
}

// And over MCP a header with a comma in its value needs no re-joining: nothing
// cut it, a bare string is one entry (never split on commas, as the CLI's flag
// does), and the server receives it whole. A structured map input would add
// nothing for the caller and a change to the frozen wire.
func TestAnMCPHeaderWithACommaReachesTheServerWhole(t *testing.T) {
	var got string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		got = r.Header.Get("Accept")
	}))
	defer srv.Close()
	for name, header := range map[string]any{
		"an array": []string{"Accept: text/html, application/json"},
		"a string": "Accept: text/html, application/json",
	} {
		got = ""
		r := req(map[string]any{"url": srv.URL, "header": header}).WithSurface(plugin.SurfaceMCP)
		if _, err := doRequest(context.Background(), "GET", r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != "text/html, application/json" {
			t.Errorf("%s: the server saw Accept %q, want it whole", name, got)
		}
	}
}
