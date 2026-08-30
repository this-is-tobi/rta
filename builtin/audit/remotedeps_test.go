package audit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/file"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/gitclone"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// **The same rule builtin/git draws, drawn once.**
//
// audit.deps is Read with no grant, which is the class that lands on every
// `rta mcp serve` with no flag and read_only_hint: true. Reading a
// repository somebody named by URL is an outbound request to a host the
// caller chose and a stranger's file contents coming back — http.get, gated,
// under a different name. A person at a terminal keeps it.
func TestAuditingARepositoryURLIsRefusedOverMCP(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  plugin.Handler
		args map[string]any
	}{
		{"deps", runDeps, map[string]any{"path": "https://127.0.0.1:1/x.git", "timeout": 5}},
		{"why", runWhy, map[string]any{"package": "lodash", "path": "https://127.0.0.1:1/x.git"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := plugin.NewRequest(tc.args, false, false).WithSurface(plugin.SurfaceMCP)
			_, err := tc.run(t.Context(), r)
			if err == nil {
				t.Fatal("a repository URL was accepted over MCP")
			}
			verr, ok := err.(*view.Error)
			if !ok {
				t.Fatalf("want a view.Error, got %T", err)
			}
			if verr.Code != "git.remote.mcp" {
				t.Errorf("refused for a different reason: %s — %s", verr.Code, verr.Message)
			}
			if strings.Contains(verr.Message+verr.Hint, "127.0.0.1") {
				t.Errorf("the caller's URL was echoed back: %s / %s", verr.Message, verr.Hint)
			}
		})
	}
}

// And a person gets the whole thing, through the real routing: a URL-shaped
// path is recognised as remote, cloned, and read back — none of which a test
// against a plain directory touches, as the first draft of this file found
// out by passing while reading the working copy off the disk beside it.
func TestAPersonCanAuditARepositoryTheyHaveNotClonedOut(t *testing.T) {
	repo := gitURL(t, gitFixture(t, map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.24\n\nrequire github.com/a/b v1.0.0\n",
	}))
	if !gitclone.IsRemote(repo) {
		t.Fatalf("%s is not routed as remote, so this tests the local path", repo)
	}

	for _, surface := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		v, err := runDeps(t.Context(), plugin.NewRequest(map[string]any{
			"path": repo, "offline": true, "timeout": 5,
		}, false, false).WithSurface(surface))
		if err != nil {
			t.Fatalf("surface %q: %v", surface, err)
		}
		tbl, ok := v.(view.Table)
		if !ok {
			t.Fatalf("want a Table, got %s", view.TypeOf(v))
		}
		var sawInventory bool
		for _, row := range tbl.Rows {
			if strings.Contains(strings.Join(row, " "), "Go 1") {
				sawInventory = true
			}
		}
		if !sawInventory {
			t.Errorf("surface %q read no dependencies out of the repository: %v", surface, tbl.Rows)
		}
	}
}

// A manifest read out of a clone is named by its path inside the repository.
// An absolute path from a memory filesystem would name a file on this machine
// that does not exist, which is worse than useless in a security finding —
// somebody would go looking for it.
func TestAManifestFromACloneIsNamedByItsPathInTheRepository(t *testing.T) {
	repo := gitURL(t, gitFixture(t, map[string]string{
		"services/api/go.mod": "module example.com/api\n\ngo 1.24\n\nrequire github.com/a/b v1.0.0\n",
	}))
	proj, verr := openProject(t.Context(), req(map[string]any{}), repo)
	if verr != nil {
		t.Fatal(verr)
	}
	_, shown, _, err := proj.manifests(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 {
		t.Fatalf("manifests = %v, want one", shown)
	}
	if shown[0] != "services/api/go.mod" {
		t.Errorf("manifest named %q, want the path inside the repository", shown[0])
	}
}

// gitURL makes a local repository addressable the way a remote one is.
//
// go-git resolves a clone URL's scheme through a registry, and its own "file"
// client is registered there exactly as http and ssh are — so a scheme nobody
// else uses, pointed at that client, is a genuine remote address as far as
// every layer above the transport is concerned. That is what lets the routing
// decision, the clone and the read all be exercised with no network and no
// git daemon.
func gitURL(t *testing.T, dir string) string {
	t.Helper()
	installTestProtocol()
	return testScheme + "://" + filepath.ToSlash(dir)
}

const testScheme = "rtafile"

var installOnce sync.Once

func installTestProtocol() {
	installOnce.Do(func() { client.InstallProtocol(testScheme, file.DefaultClient) })
}

// gitFixture builds a repository on disk and returns its path.
func gitFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=rta", "GIT_AUTHOR_EMAIL=rta@example.invalid",
			"GIT_COMMITTER_NAME=rta", "GIT_COMMITTER_EMAIL=rta@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "fixture")
	return dir
}
