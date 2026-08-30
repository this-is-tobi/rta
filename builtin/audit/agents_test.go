package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// fakeHome writes an agent configuration and points HOME at it.
func fakeHome(t *testing.T, files map[string]struct {
	body string
	mode os.FileMode
}) string {
	t.Helper()
	home := t.TempDir()
	for rel, f := range files {
		full := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, f.mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_URL", "OPENAI_BASE_URL",
		"OPENAI_API_BASE", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(k, "")
	}
	return home
}

func agentRows(t *testing.T, surface plugin.Surface) map[string][]string {
	t.Helper()
	v, err := runAgents(t.Context(), req(map[string]any{}).WithSurface(surface))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want a Table, got %s", view.TypeOf(v))
	}
	out := map[string][]string{}
	for _, r := range tbl.Rows {
		out[r[0]] = r
	}
	return out
}

const tokenValue = "ghp_thisisarealtokenvalue0123456789"

func configWithToken() string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"github": map[string]any{
			"command": "npx",
			"args":    []string{"-y", "@modelcontextprotocol/server-github"},
			"env":     map[string]string{"GITHUB_PERSONAL_ACCESS_TOKEN": tokenValue},
		},
		"pinned": map[string]any{
			"command": "npx", "args": []string{"-y", "some-server@1.4.2"},
		},
	}})
	return string(b)
}

// **The audit must not do the thing it is warning about.**
//
// The finding is that a credential is sitting in a world-readable file. Its
// value going onto a terminal, into `-o json` piped somewhere, and into the
// issue somebody pastes this into would be the tool spreading the exposure it
// came to report. The *name* is what makes the row actionable; the value adds
// nothing to it.
func TestTheCredentialValueIsNeverPrinted(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithToken(), 0o600}})

	for _, detail := range []bool{false, true} {
		v, err := runAgents(t.Context(), req(map[string]any{"detail": detail}).WithSurface(plugin.SurfaceCLI))
		if err != nil {
			t.Fatal(err)
		}
		blob, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(blob), tokenValue) {
			t.Errorf("--detail=%v printed the credential it was reporting", detail)
		}
		if !strings.Contains(string(blob), "GITHUB_PERSONAL_ACCESS_TOKEN") {
			t.Errorf("--detail=%v did not name the variable, so the row is not actionable", detail)
		}
	}
}

// **The subject of this audit is the agent asking for it.**
//
// The findings are "your shell is unrestricted", "this server holds a token",
// "here is where each one keeps its credential" — a map of the machine, read
// out to the thing it is a map of.
func TestTheAgentAuditIsRefusedOverMCP(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithToken(), 0o600}})

	_, err := runAgents(t.Context(), req(map[string]any{}).WithSurface(plugin.SurfaceMCP))
	if err == nil {
		t.Fatal("an agent read its own configuration audit")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a view.Error, got %T", err)
	}
	if verr.Code != "audit.agents.mcp" {
		t.Errorf("code = %s", verr.Code)
	}
	// …and a person still gets it.
	if rows := agentRows(t, plugin.SurfaceCLI); len(rows) == 0 {
		t.Error("the refusal took the capability away from the operator too")
	}
}

// An unrestricted shell is the setting that decides whether anything else rta
// does is enforceable, so it is a failing row and not a note.
func TestAnUnrestrictedShellIsAFailingFinding(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".claude/settings.json": {`{"permissions":{"allow":["Bash","Read"]}}`, 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	shell, ok := rows["shell"]
	if !ok {
		t.Fatalf("unrestricted Bash produced no finding: %v", rows)
	}
	if shell[1] != stFail {
		t.Errorf("graded %q, want fail: %s", shell[1], shell[2])
	}
}

// …and a shell somebody actually scoped is a decision, not a finding. Without
// this the check reports every configuration and stops meaning anything.
func TestAScopedShellIsNotAFinding(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".claude/settings.json": {`{"permissions":{"allow":["Bash(git status:*)","Read"]}}`, 0o600}})

	if _, found := agentRows(t, plugin.SurfaceCLI)["shell"]; found {
		t.Error("a scoped Bash rule was reported as unrestricted")
	}
}

// bypassPermissions is every gate below it turned off at once, so it outranks
// the individual rules and says so instead of them.
func TestBypassModeIsReportedInsteadOfTheRules(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".claude/settings.json": {`{"permissions":{"defaultMode":"bypassPermissions","allow":["Bash"]}}`, 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	mode, ok := rows["permission mode"]
	if !ok {
		t.Fatalf("bypassPermissions produced no finding: %v", rows)
	}
	if mode[1] != stFail {
		t.Errorf("graded %q, want fail", mode[1])
	}
}

// A pinned package is a decision somebody made; a bare name or @latest is the
// registry deciding at launch. Reporting both is reporting neither.
func TestOnlyAnUnpinnedLaunchIsASupplyChainFinding(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithToken(), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	if _, found := rows["pinned"]; found {
		t.Error("a version-pinned server was reported as unpinned")
	}
	if got := rows["github"]; got == nil {
		t.Error("an unpinned npx launch produced no finding")
	}
}

// A file anybody on the machine can read is a different weakness from a
// credential being in it, with a different fix, so it is its own row.
func TestFilePermissionsAreGraded(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {`{"mcpServers":{}}`, 0o644}})

	var worldReadable bool
	for _, row := range agentRows(t, plugin.SurfaceCLI) {
		if strings.Contains(row[2], "world-readable") && row[1] == stFail {
			worldReadable = true
		}
	}
	if !worldReadable {
		t.Error("a 0644 agent config was not reported as world-readable")
	}
}

// The row that says what rta actually bounds. It is informational and it is
// the most important one on the page: an agent with a shell can run the rta
// binary, grant itself, and call capabilities without the MCP record.
func TestTheBoundaryIsStatedOutLoud(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{})
	row, ok := agentRows(t, plugin.SurfaceCLI)["rta itself"]
	if !ok {
		t.Fatal("nothing states what rta bounds and what it does not")
	}
	// Asserted on the *clipped* cell, which is what people read: this row's
	// whole job is to survive a 96-character table, and a first draft led with
	// the executable path and lost the sentence to it.
	for _, want := range []string{"run rta itself", "grant"} {
		if !strings.Contains(row[2], want) {
			t.Errorf("the boundary row loses %q to the clip: %s", want, row[2])
		}
	}
}
