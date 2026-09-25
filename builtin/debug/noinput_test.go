package debug

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The pipe is the CLI's alone, so only the CLI is told about it. The hint
// said "pass it as an argument, or pipe it: my-app | rta debug ansi" on every
// surface, which sent an agent — or a TUI tile — to a pipe it cannot have.
// codec.jwt's hint had already learned this.
func TestTheNoInputHintNamesWhatEachSurfaceCanDo(t *testing.T) {
	for surface, want := range map[plugin.Surface]string{
		plugin.SurfaceCLI: "pipe it",
		plugin.SurfaceMCP: "pass it as the input argument",
		plugin.SurfaceTUI: "paste it into the input box",
	} {
		_, err := runAnsi(context.Background(), req(map[string]any{"input": ""}).WithSurface(surface))
		verr := view.AsError(err, "debug.test")
		if verr.Code != "debug.ansi.noinput" || !strings.Contains(verr.Hint, want) {
			t.Errorf("%s: %v, want the hint %q", surface, verr, want)
		}
		if surface != plugin.SurfaceCLI && strings.Contains(verr.Hint, "pipe") {
			t.Errorf("%s: hint %q tells a surface with no pipe to use one", surface, verr.Hint)
		}
	}
}
