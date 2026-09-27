package audit

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Codex's config.toml was listed and never opened: a server holding a
// plaintext token and launched through an unpinned npx got the file-mode row
// alone, and the overall read "no issues found". The same server written as
// JSON failed for the token and warned for the launch, and so does this.
func TestACodexServerIsGradedLikeAJSONOne(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".codex/config.toml": {`model = "o3"

[mcp_servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
env = { GITHUB_PERSONAL_ACCESS_TOKEN = "` + tokenValue + `" }
`, 0o600}})

	var got []string
	for _, row := range agentRowList(t) {
		if row[0] == "github" {
			got = append(got, row[1]+" "+row[2])
		}
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, findings.Fail+" launched with GITHUB_PERSONAL_ACCESS_TOKEN") {
		t.Errorf("the plaintext token in config.toml was not graded: %q", got)
	}
	if !strings.Contains(joined, findings.Warn+" launched with `npx @modelcontextprotocol/server-github`") {
		t.Errorf("the unpinned launch in config.toml was not graded: %q", got)
	}
	if strings.Contains(joined, tokenValue) {
		t.Error("the token's value reached the report")
	}
}

// The env block written as its own table, and the remote form Codex spells
// with http_headers.
func TestCodexSubtablesAndRemoteServersAreRead(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".codex/config.toml": {`# servers
[mcp_servers."local.one"]
command = "/usr/local/bin/one"
args = [
  "--flag", # a comment inside the array
  'literal',
]

[mcp_servers."local.one".env]
API_KEY = "` + tokenValue + `"

[mcp_servers.remote]
url = "https://mcp.example.com/v1"
http_headers = { "Authorization" = "Bearer ` + remoteTokenValue + `" }

[mcp_servers.cleartext]
url = "http://mcp.example.com/v1"
bearer_token_env_var = "MCP_TOKEN"
`, 0o600}})

	rows := map[string]string{}
	for _, row := range agentRowList(t) {
		rows[row[0]] += row[1] + " " + row[2] + "\n"
	}
	if !strings.Contains(rows["local.one"], "launched with API_KEY") {
		t.Errorf("a token in an env subtable was not graded: %q", rows)
	}
	if !strings.Contains(rows["remote"], "called with Authorization") {
		t.Errorf("a header in http_headers was not graded: %q", rows)
	}
	// Codex names the variable a bearer token is read from, and writes no
	// type: read by headers alone the server was never found, and its token
	// crossed the network in clear under a clean report.
	if !strings.Contains(rows["cleartext"], findings.Fail+" called over plain http://") {
		t.Errorf("a token sent over http:// was not graded: %q", rows)
	}
}

// What the reader cannot parse is said, so a file whose servers went unread
// is never a quiet part of a clean report.
func TestACodexConfigThisCannotReadSaysSo(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".codex/config.toml": {"[mcp_servers.github\ncommand = \"npx\"\n", 0o600}})
	for _, row := range agentRowList(t) {
		if strings.Contains(row[2], "servers and credentials were not graded") {
			return
		}
	}
	t.Error("an unreadable config.toml left no row saying its servers went ungraded")
}

// Each level of nesting is a frame of recursion, and ten million of them
// overflowed the stack — which ends the process, not the parse. Past the
// bound it is a file this does not read, said so like any other.
func TestTOMLNestingIsBounded(t *testing.T) {
	deep := "a = " + strings.Repeat("[", maxTOMLNesting+1) + strings.Repeat("]", maxTOMLNesting+1)
	if _, err := tomlTree(deep); err == nil {
		t.Error("nesting past the bound was read")
	}
	inline := "a = " + strings.Repeat("{ b = ", maxTOMLNesting+1) + "1" + strings.Repeat(" }", maxTOMLNesting+1)
	if _, err := tomlTree(inline); err == nil {
		t.Error("inline tables nested past the bound were read")
	}
	within := "a = " + strings.Repeat("[", maxTOMLNesting) + strings.Repeat("]", maxTOMLNesting) +
		"\nb = [ { c = [1, 2] } ]\n"
	if _, err := tomlTree(within); err != nil {
		t.Errorf("nesting within the bound was refused: %v", err)
	}
	// A dotted key is a table per part, which the walk for servers descends
	// one frame at a time: two million of them read in half a second and
	// were never walked.
	dotted := strings.Repeat("a.", maxTOMLNesting) + "a"
	for _, text := range []string{"[" + dotted + "]\nb = 1\n", dotted + " = 1\n", "t = { " + dotted + " = 1 }\n"} {
		if _, err := tomlTree(text); err == nil {
			t.Errorf("a dotted key past the bound was read: %.40q", text)
		}
	}
	if _, err := tomlTree("[" + strings.Repeat("a.", maxTOMLNesting-1) + "a]\n"); err != nil {
		t.Errorf("a dotted key within the bound was refused: %v", err)
	}
}

func TestTOMLTreeReadsTheConstructsAnAgentConfigUses(t *testing.T) {
	got, err := tomlTree(`a = "x\ty"
b.c = 'lit\eral'
arr = [1, "two", ['n']]
tbl = { k = "v", nested = { deep = true } }
multi = """
line one
line two"""

[s."quoted.key"]
v = 1

[[list]]
n = "first"
[[list]]
n = "second"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a":     "x\ty",
		"b":     map[string]any{"c": `lit\eral`},
		"arr":   []any{"1", "two", []any{"n"}},
		"tbl":   map[string]any{"k": "v", "nested": map[string]any{"deep": "true"}},
		"multi": "line one\nline two",
		"s":     map[string]any{"quoted.key": map[string]any{"v": "1"}},
		"list":  []any{map[string]any{"n": "first"}, map[string]any{"n": "second"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tomlTree =\n%#v\nwant\n%#v", got, want)
	}
	for _, bad := range []string{`a = "open`, "[t\n", "k =", `a = "x" b = 1`, "a = [1, 2"} {
		if _, err := tomlTree(bad); err == nil {
			t.Errorf("tomlTree(%q) read something it should have refused", bad)
		}
	}
}

// A hand-written reader over a file anything on the machine may have written:
// whatever the bytes, it answers a tree or an error, and never a panic that
// takes the audit down with it.
func FuzzTOMLTree(f *testing.F) {
	for _, seed := range []string{
		"", "a = 1", "[a]\nb = 2", "[[a]]\n[[a]]\nb = 'x'", "a = []\n[a.b]", "a = 1\n[a]",
		`a = "` + string(rune(0x5c)) + `u00e9 ` + string(rune(0x5c)) + `U0001F600"`, `a = "\x"`, "a = \"\"\"\nx\\\n  y\"\"\"", "a = '''\nx'''",
		"a = { b = { c = [1, [2, {d = 3}]] } }", "[mcp_servers.x]\ncommand = \"npx\"\nenv.K = \"v\"",
		"a = [\n1,\n# c\n2,\n]", "=", "[", "[[", "a.b.c", `"a" = 1`, "a = \"\\",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if got, err := tomlTree(text); err == nil && got == nil {
			t.Fatalf("tomlTree(%q) answered neither a tree nor an error", text)
		}
	})
}

func agentRowList(t *testing.T) [][]string {
	t.Helper()
	v, err := runClients(t.Context(), req(map[string]any{}).WithSurface(plugin.SurfaceCLI), testCatalog)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want a Table, got %s", view.TypeOf(v))
	}
	return tbl.Rows
}
