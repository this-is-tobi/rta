package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	src := devModule(t)
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

// --keep says where it left the build. The flag promised to and said
// nothing, so a run with a command after `--` — whose output is that
// command's alone — left the binary under a name nobody was told.
func TestPluginDevKeepSaysWhereTheBuildIs(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a binary")
	}
	var stderr bytes.Buffer
	binary, cleanup, err := buildPlugin(context.Background(), devModule(t), true, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(binary)) })
	cleanup()
	if !strings.Contains(stderr.String(), binary) {
		t.Errorf("--keep said %q, which does not name the build at %s", stderr.String(), binary)
	}
}

// devModule is a Go module with nothing in it but a main, which plugin
// dev's build compiles as it would a plugin's.
func devModule(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.com/devexit\n\ngo 1.21\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return src
}
