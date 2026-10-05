package app

import (
	"strings"
	"testing"
)

// A comment or an error message that says where the reasoning lives names a
// page, and splitting a chapter moves the reasoning without touching either.
// The pointer then still resolves — the old page keeps a row for it — and sends
// whoever follows it to a page that does not hold what the comment says it
// does. That a cited page exists is held by TestEveryDocsPathCitedFromCodeExists;
// this holds that each pointer that has moved before names the page that holds
// its topic today.
func TestAPointerThatMovedNamesThePageThatHoldsItsTopic(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct {
		source, page, topic string
	}{
		{"builtin/kv/store.go", "docs/20-using/50-secrets.md", "RTA_KV_PASSPHRASE"},
		{"internal/config/profile.go", "docs/20-using/42-reaching-private-services.md", "tls-server-name"},
		{"internal/plugindist/install.go", "docs/40-plugins/24-testing-and-publishing.md", "a path on this machine"},
		{"internal/mcp/remote.go", "docs/30-boundary/65-hosting-a-server.md", "world-readable files are refused"},
		{"internal/mcp/bridge_test.go", "docs/40-plugins/23-safety-and-credentials.md", "HostSpecific"},
		{"internal/app/mcp.go", "docs/30-boundary/66-operators.md", "agent allow"},
		{"builtin/audit/agentscontainer.go", "docs/30-boundary/67-containers-and-images.md", "docker run"},
		{"internal/guard/guard.go", "docs/30-boundary/10-the-boundary.md", "running as you"},
		{"internal/operator/key.go", "docs/30-boundary/50-team-policy.md", "no subjects and no allow rules"},
		{"internal/mcp/secretslice_test.go", "docs/30-boundary/40-audit-trail.md", "ship the record somewhere durable"},
		{"builtin/eol/eol.go", "docs/40-plugins/10-plugins.md", "## Built in, or a plugin"},
	} {
		src := readDoc(t, root, c.source)
		if !strings.Contains(src, c.page) {
			t.Errorf("%s no longer names %s, which holds %q", c.source, c.page, c.topic)
			continue
		}
		if !strings.Contains(strings.ToLower(readDoc(t, root, c.page)), strings.ToLower(c.topic)) {
			t.Errorf("%s is named by %s for %q and does not hold it", c.page, c.source, c.topic)
		}
	}
}
