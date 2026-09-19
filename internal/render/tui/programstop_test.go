package tui

import (
	"testing"
	"time"

	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// A test that ends must leave no program rendering behind it.
//
// **Because the one that does is blamed on somebody else.** teatest starts a
// real Bubble Tea program in a goroutine and registers no cleanup of its
// own, so a test that fails before it reaches quit() — a waitFor whose frame
// never came on a loaded machine, a t.Fatal two lines earlier — leaves that
// program running for the rest of the binary. It goes on rendering, and
// every View in this package reads the theme package's shared styles;
// TestSaveThemeWritesToDiskAndAppliesLive then calls theme.Apply, which
// rewrites exactly those vars. Under -race that is a reported write/read
// race between two tests that no one ever meant to overlap, and it appears
// only when the machine is loaded enough for the first one to time out — so
// the report names the theme test and the cause is three files away.
//
// Nothing in this package runs t.Parallel, which is what makes the
// diagnosis stick: two tests can only overlap here if one of them outlived
// its own test function.
//
// A test-only hazard, and worth saying so, because the obvious reading of
// that race report is that theme needs a mutex. It does not: Bubble Tea
// calls View from p.render inside the event loop, the same goroutine that
// ran Update, so themeform's live theme.Apply and every View it affects are
// already serialised by the loop itself, and the only other caller applies
// the config before any program exists. The unsynchronised pair exists
// because a test calls Apply from the test goroutine — so the fix belongs
// where the overlap is invented, not in the palette.
func TestAProgramIsStoppedEvenWhenItsTestNeverQuitsIt(t *testing.T) {
	stopped := make(chan struct{})
	// Registered before the program exists, so LIFO runs it after the
	// cleanup that stops it: what this checks is exactly what that cleanup
	// is for.
	t.Cleanup(func() {
		select {
		case <-stopped:
		case <-time.After(framePatience):
			t.Error("the program was still running once its test had finished — it keeps " +
				"rendering, and its View reads the theme styles another test in this " +
				"binary rewrites")
		}
	})

	tm := newTestModel(t, New(testRegistry(t), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	waitFor(t, tm, "dashboard")
	// Started once a frame has arrived, which is proof Run is past the line
	// that creates the channel Wait blocks on.
	go func() {
		tm.GetProgram().Wait()
		close(stopped)
	}()
	// Deliberately no quit(): the abandoned program is the case under test.
}
