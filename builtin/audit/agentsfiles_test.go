package audit

import (
	"path/filepath"
	"strings"
	"testing"
)

// **Run the audit from your home directory and every finding arrived twice.**
//
// Four of the paths this reads are the same relative name under the home
// directory and under the working directory — `.cursor/mcp.json`,
// `.claude/settings.json`, `.claude/settings.local.json`, `.mcp.json`. When
// those are the same directory the list held one file under two labels, and
// every check ran on it twice: two rows for one weak permission, two rows for
// one credential, two of every container finding. A duplicated finding is
// worse than a noisy one — it makes a reader wonder which of the two they
// already fixed.
func TestTheSameFileIsNeverListedTwice(t *testing.T) {
	home := t.TempDir()
	seen := map[string]string{}
	for _, f := range agentFiles(home, home) {
		if prev, dup := seen[f.path]; dup {
			t.Errorf("%s is listed under both %q and %q", f.path, prev, f.label)
		}
		seen[f.path] = f.label
	}
}

// And the ordinary case keeps both, because they really are two files: a user
// config and a project one, graded separately because each names its own path
// in its own finding.
func TestAProjectFileIsStillItsOwnEntry(t *testing.T) {
	home := t.TempDir()
	wd := filepath.Join(t.TempDir(), "project")
	files := agentFiles(home, wd)

	var underHome, underWd int
	for _, f := range files {
		switch {
		case strings.HasPrefix(f.path, home):
			underHome++
		case strings.HasPrefix(f.path, wd):
			underWd++
		}
	}
	if underHome == 0 || underWd == 0 {
		t.Fatalf("want entries under both roots, got %d home and %d project", underHome, underWd)
	}
}

// The first label wins, so a shared path is reported as the user-scope config
// it is rather than as "(this project)" — which would be true only by accident
// of where the command was run.
func TestASharedPathKeepsItsUserScopeLabel(t *testing.T) {
	home := t.TempDir()
	for _, f := range agentFiles(home, home) {
		if strings.HasSuffix(f.path, filepath.Join(".cursor", "mcp.json")) &&
			strings.Contains(f.label, "this project") {
			t.Errorf("a shared path kept the project label: %s -> %s", f.path, f.label)
		}
	}
}
