package pipein

import (
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

func TestReadFromReturnsWhatWasWritten(t *testing.T) {
	got, err := ReadFrom(strings.NewReader("piped text"), 64)
	if err != nil || got != "piped text" {
		t.Errorf("got %q, %v", got, err)
	}
}

// Exactly the limit is whole; one byte more is not. The two must not look
// alike, because a pipe cut at the limit and handed on would be a truncated
// token or seed phrase read as a complete one.
func TestReadFromTellsAFullPipeFromAnOverflowingOne(t *testing.T) {
	if got, err := ReadFrom(strings.NewReader("abcd"), 4); err != nil || got != "abcd" {
		t.Errorf("at the limit: got %q, %v", got, err)
	}
	if _, err := ReadFrom(strings.NewReader("abcde"), 4); !errors.Is(err, ErrTooLarge) {
		t.Errorf("past the limit: err = %v, want ErrTooLarge", err)
	}
}

func TestReadFromPassesAReadFailureOn(t *testing.T) {
	boom := errors.New("boom")
	if _, err := ReadFrom(iotest.ErrReader(boom), 64); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the reader's own", err)
	}
}

// No assertion beyond "returns at once with nothing": had Read touched
// stdio.Real() on these surfaces, whatever `go test` handed the process as
// stdin would decide the outcome instead of the surface check — which is the
// coupling this guards against, and on MCP that stream is the agent's
// requests.
func TestReadNeverTouchesStdinOffTheCLI(t *testing.T) {
	for _, s := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI} {
		got, err := Read(plugin.NewRequest(nil, false, false).WithSurface(s), 64)
		if err != nil || got != "" {
			t.Errorf("%v: got %q, %v; want nothing", s, got, err)
		}
	}
}
