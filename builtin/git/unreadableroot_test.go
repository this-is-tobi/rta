//go:build !windows

package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A root the server may search but not list (mode --x) opens nothing under
// it through os.Root. git's discovery took that for "no repository here",
// walked up past the root, and answered with the gate's refusal of the
// directory above it: core.mcp.path.outside, naming a directory the caller
// never gave. The root that cannot be read is named now, and a repository
// under a readable root drawn inside it is served from that one, whichever
// order --root names them in.
func TestARootThatCannotBeListedIsNamedAndARootInsideItServes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}
	machineConfig(t, "")
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	served := filepath.Join(inner, "repo")
	direct := filepath.Join(outer, "repo")
	for _, dir := range []string{served, direct} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		repo, err := gogit.PlainInit(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		commitFile(t, repo, dir, "README", "hello\n", "first")
	}
	if err := os.Chmod(outer, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outer, 0o700) })

	status := func(path string, roots ...string) error {
		t.Helper()
		g, err := pathguard.New(roots...)
		if err != nil {
			t.Fatal(err)
		}
		r := req(t, path, nil).WithConfinement(g.Check).WithBounds(g.Bounds()).WithSurface(plugin.SurfaceMCP)
		_, err = runStatus(t.Context(), r)
		return err
	}
	for _, roots := range [][]string{{outer, inner}, {inner, outer}} {
		if err := status(served, roots...); err != nil {
			t.Errorf("--root %s: git.status of a repository under the readable inner root: %v",
				strings.Join(roots, " --root "), err)
		}
	}
	err := status(direct, outer)
	if verr := view.AsError(err, "x"); verr == nil || verr.Code != "git.root.unreadable" ||
		!strings.Contains(err.Error(), filepath.Base(outer)) {
		t.Errorf("git.status of a repository under a root that cannot be listed: %v, "+
			"want git.root.unreadable naming the root", err)
	} else if !strings.Contains(verr.Hint, plugin.AskOperator("mcp serve --root <dir>")) {
		t.Errorf("the hint to a server's root that cannot be read does not hand the command to the operator: %q", verr.Hint)
	}
}
