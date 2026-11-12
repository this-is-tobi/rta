package pkg

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// **A manager is asked, and upgraded, from the home directory, wherever rta
// was started.** go switched to the toolchain the go.mod of the working
// directory asked for, downloading it when it was not cached, so `go version`
// in a module that wanted 1.27.4 answered 1.27.4 for a go that was 1.27.2;
// mise answered for the tools the directory's mise.toml pinned. A dashboard
// tile drew whichever of those the TUI had been started in.
func TestAManagerIsAskedFromTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := runQuery(context.Background(), "sh", "-c", "pwd -P")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("a query ran in %q, want the home directory %q", got, want)
	}

	var upgraded strings.Builder
	if err := runUpgradeCommand(context.Background(), []string{"sh", "-c", "pwd -P"}, &upgraded); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(upgraded.String()); got != want {
		t.Errorf("an upgrade ran in %q, want the home directory %q", got, want)
	}
}
