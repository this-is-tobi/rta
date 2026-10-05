package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// AGENTS.md and SECURITY.md send a reader to the design reasoning of the MCP
// server, and that reasoning is on four pages: the gate, the page for a server
// that is hosted, the one for the operator channel and the one for containers.
// A pointer to the first alone sends a reader about a hosted server to a page
// that says it is somewhere else, so both documents name all four and every
// docs link either holds is a page.
func TestThePolicyDocumentsPointAtEveryPageOfTheMCPReasoning(t *testing.T) {
	root := repoRoot(t)
	link := regexp.MustCompile(`\]\((docs/[^)#\s]+)`)
	for _, doc := range []string{"AGENTS.md", "SECURITY.md"} {
		body := readDoc(t, root, doc)
		for _, page := range []string{
			"docs/30-boundary/20-mcp.md",
			"docs/30-boundary/65-hosting-a-server.md",
			"docs/30-boundary/66-operators.md",
			"docs/30-boundary/67-containers-and-images.md",
		} {
			if !strings.Contains(body, "("+page+")") {
				t.Errorf("%s does not link %s, which holds part of the MCP design reasoning", doc, page)
			}
		}
		for _, m := range link.FindAllStringSubmatch(body, -1) {
			if _, err := os.Stat(filepath.Join(root, m[1])); err != nil {
				t.Errorf("%s links %s, which is not a page", doc, m[1])
			}
		}
	}
}

// A code a page quotes is the contract a script or a SIEM rule matches on, and
// a page that quotes one that was renamed or never existed sends that rule to
// match on nothing. Every backticked `core.` code of three parts or more in
// the docs, which leaves out git's `core.worktree`, is spelled in the source as
// a string, so a code that is dropped, or typed wrong on a page, fails here
// with the page that has it.
func TestEveryCoreCodeTheDocsNameIsOneRtaRaises(t *testing.T) {
	root := repoRoot(t)
	var source strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "node_modules" || name == "docs" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			source.Write(body)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile("`(core\\.[a-z0-9-]+\\.[a-z0-9.-]*[a-z0-9])`")
	named := 0
	for _, page := range markdownPages(t, root) {
		for _, m := range code.FindAllStringSubmatch(readDoc(t, root, page), -1) {
			named++
			if !strings.Contains(source.String(), `"`+m[1]) {
				t.Errorf("%s quotes the code %s, which no part of rta raises", page, m[1])
			}
		}
	}
	if named < 20 {
		t.Fatalf("found %d codes in the docs; has the way they are quoted changed?", named)
	}
}
