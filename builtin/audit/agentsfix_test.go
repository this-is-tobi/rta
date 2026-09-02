package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// fixBodies runs the audit with --fix and returns every section's title and
// body joined, so a test asserts on what the operator would actually read.
func fixBodies(t *testing.T) (view.View, string) {
	t.Helper()
	v, err := runAgents(t.Context(), req(map[string]any{"fix": true}).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return v, string(blob)
}

// Every failing finding with a mechanical answer gets its edit spelled out:
// the chmod, the scoped allowlist, the pinned version, the move for a
// credential — and the deny list for rta's own commands rides along, because
// a machine with Claude Code on it is a machine where that paste has a home.
func TestFixPrintsTheEditForEveryFindingThatHasOne(t *testing.T) {
	settings, _ := json.Marshal(map[string]any{
		"permissions": map[string]any{"allow": []string{"Bash"}},
		"mcpServers": map[string]any{
			"search": map[string]any{
				"command": "npx",
				"args":    []string{"mcp-server-search"},
				"env":     map[string]string{"SEARCH_API_TOKEN": tokenValue},
			},
		},
	})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".claude/settings.json": {string(settings), 0o644}})

	v, all := fixBodies(t)
	if _, ok := v.(view.Sections); !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	for _, want := range []string{
		"chmod 600",               // the file mode edit
		`\"Bash(git status:*)\"`,  // the scoped-shell shape
		"@1.2.3",                  // the pinned-version placeholder
		"rotate the value",        // a credential that sat in a file is burnt
		`\"Bash(rta grant:*)\"`,   // the deny list for rta's own commands
		`\"Bash(rta agent:*)\"`,   // …including answering its own parked calls
		"seatbelt and not a wall", // and the honesty clause beside it
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no fix carries %s", want)
		}
	}
	// The same rule the grades hold themselves to: the finding is that a
	// value is in a file, and the fix page must not spread it further.
	if strings.Contains(all, tokenValue) {
		t.Error("--fix printed the credential the audit came to report")
	}
}

// bypassPermissions short-circuits the permission grades, and the fix page
// follows: the one edit offered is the switch itself, because every other
// edit is theoretical while it is on.
func TestFixWithBypassModeOffersTheSwitchItself(t *testing.T) {
	settings, _ := json.Marshal(map[string]any{
		"permissions": map[string]any{"defaultMode": "bypassPermissions", "allow": []string{"Bash"}},
	})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".claude/settings.json": {string(settings), 0o600}})

	_, all := fixBodies(t)
	if !strings.Contains(all, "acceptEdits") {
		t.Error("no edit for the bypassPermissions switch")
	}
	if strings.Contains(all, `\"Bash(git status:*)\"`) {
		t.Error("a scoped-shell edit was offered below a switch that turns it off")
	}
}

// The deny list is a paste, and a paste needs a file to land in: a machine
// with no Claude Code configuration gets the other fixes and not an edit for
// a file that does not exist.
func TestTheDenyListIsOfferedOnlyWhereClaudeCodeExists(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithToken(), 0o600}})

	_, all := fixBodies(t)
	if !strings.Contains(all, "rotate the value") {
		t.Fatal("the credential fix disappeared with the deny list")
	}
	if strings.Contains(all, "rta grant") {
		t.Error("a Claude Code deny list was offered on a machine without Claude Code")
	}
}

// A clean machine is told so in a sentence, not with an empty page that
// reads as a check that failed to run.
func TestFixSaysNothingToPasteOnACleanMachine(t *testing.T) {
	clean, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"pinned": map[string]any{"command": "npx", "args": []string{"some-server@1.4.2"}},
	}})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {string(clean), 0o600}})

	v, all := fixBodies(t)
	if _, ok := v.(view.Text); !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	if !strings.Contains(all, "nothing to paste") {
		t.Errorf("a clean machine got: %s", all)
	}
}
