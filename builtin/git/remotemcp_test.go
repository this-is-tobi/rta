package git

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// **A Read capability with no grant must not reach a host the caller picked.**
//
// Every capability in this plugin is Read, which is the class that costs
// nothing to reach: it goes onto every `rta mcp serve` with no --allow-write,
// no grant and read_only_hint: true. A remote --path made that class into
// http.get with extra steps — an HTTP request to a path the caller composed,
// and the stranger's own commit messages, diffs and config coming back into a
// model's context. http.get carries NeedsGrant with Scope "url" for exactly
// those two reasons, and audit.web is gated for them under another name.
//
// Measured rather than argued: the test server records the hit, so a
// regression is a connection that happened, not a string that changed.
func TestARemoteRepositoryIsNotReachableOverMCP(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name string
		run  plugin.Handler
	}{
		{"log", runLog}, {"status", runStatus}, {"diff", runDiff},
		{"branches", runBranches}, {"config", runConfig}, {"remotes", runRemotes},
		{"blame", runBlame}, {"hooks", runHooks}, {"overview", runOverview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := plugin.NewRequest(map[string]any{
				"path": srv.URL, "limit": defaultLogLimit, "file": "a.txt",
			}, false, false).WithSurface(plugin.SurfaceMCP)
			before := hits
			_, err := tc.run(t.Context(), r)
			if hits != before {
				t.Errorf("%s reached the host: an ungated Read made an outbound request "+
					"to a URL the caller chose", tc.name)
			}
			if err == nil {
				t.Fatalf("%s allowed a remote URL over MCP", tc.name)
			}
			verr, ok := err.(*view.Error)
			if !ok {
				t.Fatalf("%s: want a view.Error, got %T", tc.name, err)
			}
			if verr.Code != "git.remote.mcp" {
				t.Errorf("%s refused for a different reason: %s — %s", tc.name, verr.Code, verr.Message)
			}
			// The refusal must not repeat the caller's own string back into a
			// model's context under rta's name.
			if strings.Contains(verr.Message+verr.Hint, srv.URL) {
				t.Errorf("%s echoed the caller-composed URL into the refusal: %s / %s",
					tc.name, verr.Message, verr.Hint)
			}
		})
	}
}

// A person at a terminal keeps the whole feature: the refusal is about who is
// asking, not about what the capability does. Without this the fix is
// indistinguishable from deleting remote support.
func TestARemoteRepositoryStillWorksForAPerson(t *testing.T) {
	bareDir := bareRepo(t)
	for _, surface := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI, plugin.SurfaceUnknown} {
		r := plugin.NewRequest(map[string]any{"path": bareDir, "limit": defaultLogLimit},
			false, false).WithSurface(surface)
		if _, err := runLog(t.Context(), r); err != nil {
			t.Errorf("surface %q was refused a local repository: %v", surface, err)
		}
	}
}
