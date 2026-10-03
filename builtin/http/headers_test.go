package http

import (
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
