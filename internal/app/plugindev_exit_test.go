package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// A forced exit taken while plugin dev runs the plugin it built leaves no
// build behind, unless --keep asked for it. The directory was removed by a
// deferred call only, which an exit taken without the command skips, so one
// taken while dev waited on a plugin that never reads its context left the
// directory and the binary in the temporary directory for good.
func TestAForcedExitDuringPluginDevRemovesTheBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a binary")
	}
	src := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.com/devexit\n\ngo 1.21\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, keep := range []bool{false, true} {
		binary, cleanup, err := buildPlugin(context.Background(), src, keep, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(binary)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		resume := shutdown.Settle()
		shutdown.Exiting()
		resume()
		_, statErr := os.Stat(dir)
		switch {
		case keep && statErr != nil:
			t.Errorf("with --keep, the exit removed the build: %v", statErr)
		case !keep && !errors.Is(statErr, fs.ErrNotExist):
			t.Errorf("the exit left the build at %s (%v)", dir, statErr)
		}
		cleanup()
	}
}
