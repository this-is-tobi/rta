package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fixBodies runs the audit with --fix and returns every section's title and
// body joined, so a test asserts on what the operator would actually read.
func fixBodies(t *testing.T) (view.View, string) {
	t.Helper()
	v, err := runClients(t.Context(), req(map[string]any{"fix": true}).WithSurface(plugin.SurfaceCLI), testCatalog)
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

// pnpm and pipx put a subcommand before the package, and pipx and uvx pin
// with ==. The first word after the runner was read as the package, so a
// pinned `pnpm dlx` or `pipx run --spec` was warned as fetching whatever the
// registry serves, and the fix told the operator to write "dlx@1.2.3" — an
// edit that breaks the declaration it was meant to pin.
func TestPinnedLaunchesThroughEveryRunnerAreNotFindings(t *testing.T) {
	servers := map[string]any{}
	for name, launch := range map[string][]string{
		"pnpm-pinned":    {"pnpm", "dlx", "@acme/mcp-server@1.4.2"},
		"pipx-spec":      {"pipx", "run", "--spec", "mcp-server-fetch==2025.4.7", "mcp-server-fetch"},
		"uvx-equals":     {"uvx", "mcp-server-fetch==2025.4.7"},
		"uvx-from":       {"uvx", "--from=mcp-server-fetch==2025.4.7", "mcp-server-fetch"},
		"uvx-python":     {"uvx", "--python", "3.12", "mcp-server-time@2025.4.7"},
		"npx-package":    {"npx", "-y", "--package", "@acme/tools@2.0.1", "acme-mcp"},
		"pnpm-exec":      {"pnpm", "exec", "local-server"},
		"pnpm-unpinned":  {"pnpm", "dlx", "@acme/x"},
		"pipx-unpinned":  {"pipx", "run", "mcp-server-fetch"},
		"uvx-unpinned":   {"uvx", "--python", "3.12", "mcp-server-time"},
		"npx-range":      {"npx", "-y", "some-server@^1.4.2"},
		"npx-dist-tag":   {"npx", "-y", "some-server@next"},
		"npx-tagged-pkg": {"npx", "-y", "--package=@acme/tools@latest", "acme-mcp"},
	} {
		servers[name] = map[string]any{"command": launch[0], "args": launch[1:]}
	}
	body, _ := json.Marshal(map[string]any{"mcpServers": servers})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {string(body), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	for _, pinned := range []string{"pnpm-pinned", "pipx-spec", "uvx-equals", "uvx-from", "uvx-python",
		"npx-package", "pnpm-exec"} {
		if row, found := rows[pinned]; found {
			t.Errorf("%s is pinned, or fetches nothing, and was reported: %v", pinned, row)
		}
	}
	for name, want := range map[string]string{
		"pnpm-unpinned":  "`pnpm dlx @acme/x`",
		"pipx-unpinned":  "`pipx run mcp-server-fetch`",
		"uvx-unpinned":   "`uvx mcp-server-time`",
		"npx-range":      "`npx some-server@^1.4.2`",
		"npx-dist-tag":   "`npx some-server@next`",
		"npx-tagged-pkg": "`npx @acme/tools@latest`",
	} {
		if row := rows[name]; row == nil || !strings.Contains(row[2], want) {
			t.Errorf("%s: want a row naming %s, got %v", name, want, row)
		}
	}

	_, all := fixBodies(t)
	for _, want := range []string{
		`[\"dlx\", \"@acme/x@1.2.3\"]`,
		`[\"run\", \"--spec\", \"mcp-server-fetch==1.2.3\", \"mcp-server-fetch\"]`,
		`[\"--python\", \"3.12\", \"mcp-server-time@1.2.3\"]`,
		`[\"-y\", \"some-server@1.2.3\"]`,
		`[\"-y\", \"--package=@acme/tools@1.2.3\", \"acme-mcp\"]`,
		"`npm view @acme/x version`",
		"`pip index versions mcp-server-fetch`",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no fix spells %s", want)
		}
	}
	for _, wrong := range []string{"dlx@1.2.3", "run@1.2.3", "npm view dlx", "pip index versions run", "3.12@"} {
		if strings.Contains(all, wrong) {
			t.Errorf("a fix spells %s, an edit that breaks the declaration", wrong)
		}
	}
}

// `npm exec` and `uv tool run` are npx and uvx spelled long, and fetch the
// same way; they were not read at all. `npm run` fetches nothing.
func TestTheLongFormsOfTheRunnersAreRead(t *testing.T) {
	servers := map[string]any{}
	for name, launch := range map[string][]string{
		"npm-exec":   {"npm", "exec", "--", "@acme/mcp-server"},
		"npm-x":      {"npm", "x", "-y", "@acme/mcp-server@1.4.2"},
		"uv-run":     {"uv", "tool", "run", "mcp-server-fetch"},
		"uv-pinned":  {"uv", "tool", "run", "mcp-server-fetch==2025.4.7"},
		"uv-install": {"uv", "tool", "install", "mcp-server-fetch"},
		"npm-run":    {"npm", "run", "serve"},
		"yarn-dlx":   {"yarn", "dlx", "@acme/mcp-server"},
	} {
		servers[name] = map[string]any{"command": launch[0], "args": launch[1:]}
	}
	body, _ := json.Marshal(map[string]any{"mcpServers": servers})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {string(body), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	for name, want := range map[string]string{
		"npm-exec": "`npm exec @acme/mcp-server`",
		"uv-run":   "`uv tool run mcp-server-fetch`",
		"yarn-dlx": "`yarn dlx @acme/mcp-server`",
	} {
		if row := rows[name]; row == nil || !strings.Contains(row[2], want) {
			t.Errorf("%s: want a row naming %s, got %v", name, want, row)
		}
	}
	for _, quiet := range []string{"npm-x", "uv-pinned", "uv-install", "npm-run"} {
		if row, found := rows[quiet]; found {
			t.Errorf("%s is pinned or fetches nothing, and was reported: %v", quiet, row)
		}
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
// reads as a check that failed to run — and the sentence is beside the page,
// not in place of it.
//
// It was a Text view in place of the page, so the result changed shape with
// the machine's state: `rta audit clients --fix > fix.txt` wrote the
// sentence into the file where the fixes go, and `jq '.items[]'` met a view
// with no items to iterate. The page is now empty, and the sentence is what
// a person is shown in its place (view.Sections.Empty).
func TestFixSaysNothingToPasteOnACleanMachine(t *testing.T) {
	clean, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"pinned": map[string]any{"command": "npx", "args": []string{"some-server@1.4.2"}},
	}})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {string(clean), 0o600}})

	v, all := fixBodies(t)
	page, ok := v.(view.Sections)
	if !ok || len(page.Items) != 0 {
		t.Fatalf("want an empty page of sections, got %s %s", view.TypeOf(v), all)
	}
	if !strings.Contains(page.Empty, "nothing to paste") {
		t.Errorf("a clean machine was told %q", page.Empty)
	}
	if strings.Contains(all, "nothing to paste") {
		t.Errorf("the sentence was encoded with the page: %s", all)
	}
}
