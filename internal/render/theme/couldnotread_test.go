package theme

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A fact a page could not read is graded amber in the SDK's words for it,
// which the plugins and rta's own overviews use so that it is graded at all.
func TestCouldNotReadIsGradedAmber(t *testing.T) {
	for _, line := range []string{view.CouldNotRead("needs the nodes"), view.CouldNotRead("")} {
		if got := ClassifyStatus(line); got != StatusWarn {
			t.Errorf("ClassifyStatus(%q) = %v, want amber", line, got)
		}
	}
}
