package cli

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An overview whose backend is not there answers with a page and not an error,
// so that the page can still say what the configuration knows, and the line that
// says the backend is down was drawn in the plainest style beside "answering"
// and a count. A person scanning a dashboard of these read the one that had
// failed last.
func TestAnOverviewThatSaysItsBackendIsDownIsDrawnAsAFailure(t *testing.T) {
	page := view.KeyValue{Pairs: []view.Pair{
		{Key: "context", Value: "lab"},
		{Key: "cluster", Value: "did not answer — connection refused"},
		{Key: "nodes", Value: "could not be read — forbidden"},
		{Key: "namespaces", Value: "12"},
	}}
	out := renderColoured(t, page, 100)
	for _, want := range []struct{ text, style string }{
		{"did not answer — connection refused", theme.BadText.Render("did not answer — connection refused")},
		{"could not be read — forbidden", theme.WarnText.Render("could not be read — forbidden")},
	} {
		if !strings.Contains(out, want.style) {
			t.Errorf("%q is not drawn in its status colour:\n%s", want.text, out)
		}
	}
	if strings.Contains(out, theme.BadText.Render("12")) || strings.Contains(out, theme.BadText.Render("lab")) {
		t.Error("a plain value was drawn as a failure")
	}
}
