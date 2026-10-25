package debug

import (
	"context"
	"errors"
	"testing"

	"github.com/this-is-tobi/rta/builtin/internal/pipein"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Nothing to explain names the input to fill (view.Error.Input), so a form
// opens again on its box and an agent knows which argument to send. The CLI is
// left out: with no argument it reads the pipe, and stdin here is go test's.
func TestNoTextNamesTheInputToFill(t *testing.T) {
	c := Plugin().Capabilities[0]
	for _, s := range []plugin.Surface{plugin.SurfaceTUI, plugin.SurfaceMCP} {
		_, err := c.Run(context.Background(), req(map[string]any{"input": ""}).WithSurface(s))
		verr := view.AsError(err, "test")
		if err == nil || verr.Code != "debug.ansi.noinput" || verr.Input != "input" {
			t.Fatalf("%s: got %v, want debug.ansi.noinput naming input", s, err)
		}
		if f, ok := c.ErrorInput(verr); !ok || f.Name != "input" {
			t.Errorf("%s: %q is not an input %s declares", s, verr.Input, c.ID)
		}
	}
}

// A pipe that holds more than debug.ansi explains is the input's value out of
// range, and names it as the argument would be named; a pipe that could not
// be read at all is no value of anyone's, and names none.
func TestAPipeTooLargeNamesTheInput(t *testing.T) {
	if verr := stdinError(pipein.ErrTooLarge); verr == nil || verr.Input != "input" {
		t.Errorf("oversized pipe = %+v, want it to name input", verr)
	}
	if verr := stdinError(errors.New("boom")); verr == nil || verr.Input != "" {
		t.Errorf("read failure = %+v, want it to name no input", verr)
	}
}
