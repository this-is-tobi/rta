package audit

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
)

// vsCodeConfig is where VS Code keeps its user mcp.json on this machine, as
// agentFiles resolves it.
func vsCodeConfig() string {
	switch runtime.GOOS {
	case "darwin":
		return "Library/Application Support/Code/User/mcp.json"
	case "windows":
		return "AppData/Roaming/Code/User/mcp.json"
	}
	return ".config/Code/User/mcp.json"
}

// remoteWithHeader declares one remote server called with an Authorization
// header holding value, and launchedWithEnv one local server launched with an
// API_TOKEN holding it.
func remoteWithHeader(value string) string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"svc": map[string]any{
		"type": "http", "url": "https://mcp.example.com/", "headers": map[string]string{"Authorization": value},
	}}})
	return string(b)
}

func launchedWithEnv(value string) string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"svc": map[string]any{
		"command": "/usr/local/bin/svc-server", "env": map[string]string{"API_TOKEN": value},
	}}})
	return string(b)
}

// Gemini CLI names a streamable HTTP server's endpoint httpUrl rather than
// url, and a server declared that way was not found at all: its headers
// went ungraded, a plaintext token in them included, and so did a plain
// http:// endpoint. httpUrl takes precedence over url, as Gemini reads it.
func TestAGeminiServerCalledAtAnHTTPURLIsGraded(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"svc": map[string]any{
			"httpUrl": "http://mcp.example.com/",
			"headers": map[string]string{"Authorization": "Bearer " + tokenValue},
		},
		"both": map[string]any{
			"httpUrl": "http://both.example.com/", "url": "https://sse.example.com/",
			"headers": map[string]string{"X-Client": "rta-test"},
		},
	}})
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".gemini/settings.json": {string(b), 0o600}})
	failed := map[string][]string{}
	for _, row := range agentRowList(t) {
		if row[1] == findings.Fail {
			failed[row[0]] = append(failed[row[0]], row[2])
		}
	}
	svc := strings.Join(failed["svc"], "\n")
	if !strings.Contains(svc, "Authorization in its headers block, in plain text") ||
		!strings.Contains(svc, "plain http:// at mcp.example.com") {
		t.Errorf("a server declared with httpUrl was not graded: %q", failed["svc"])
	}
	if both := strings.Join(failed["both"], "\n"); !strings.Contains(both, "plain http:// at both.example.com") {
		t.Errorf("url was graded where httpUrl takes precedence: %q", failed["both"])
	}
}

// A value that only names a variable of the environment launching the client
// holds no credential, when the client expands that reference: it is the very
// move the credential fix asks for, and `rta audit clients` failed it as a
// credential in plain text. A reference the client does not expand is sent
// as the text it is, so it stays a failure, and the fix names the form the
// client reads. A reference's default is written in the file, so a default
// holding a value is that value in plain text.
func TestAReferenceToTheLaunchingEnvironmentIsGradedByWhatTheClientExpands(t *testing.T) {
	for _, tc := range []struct {
		name, file, body string
		plain            bool   // graded as a credential in the file
		fix              string // what the fix names instead, when it is one
	}{
		{"claude header", ".claude.json", remoteWithHeader("Bearer ${RTA_PAYMENTS_TOKEN}"), false, ""},
		{"claude env", ".claude.json", launchedWithEnv("${API_TOKEN}"), false, ""},
		{"claude default", ".claude.json", launchedWithEnv("${API_TOKEN:-" + tokenValue + "}"), true, "${API_TOKEN}"},
		{"claude empty default", ".claude.json", remoteWithHeader("Bearer ${API_TOKEN:-}"), false, ""},
		{"claude, cursor's form", ".claude.json", remoteWithHeader("Bearer ${env:API_TOKEN}"), true, "${API_TOKEN}"},
		{"claude, a literal beside it", ".claude.json", remoteWithHeader("Bearer sk-live-${SUFFIX}"), true, "${"},
		{"cursor header", ".cursor/mcp.json", remoteWithHeader("Bearer ${env:API_TOKEN}"), false, ""},
		{"cursor, claude's form", ".cursor/mcp.json", remoteWithHeader("Bearer ${API_TOKEN}"), true, "${env:API_TOKEN}"},
		{"vs code input", vsCodeConfig(), remoteWithHeader("Bearer ${input:api-token}"), false, ""},
		{"vs code env", vsCodeConfig(), launchedWithEnv("${env:API_TOKEN}"), false, ""},
		{"vs code, a bare reference", vsCodeConfig(), launchedWithEnv("$API_TOKEN"), true, "${env:API_TOKEN}"},
		{"gemini bare", ".gemini/settings.json", launchedWithEnv("$API_TOKEN"), false, ""},
		{"gemini header", ".gemini/settings.json", remoteWithHeader("Bearer ${API_TOKEN}"), false, ""},
		{"copilot env", ".copilot/mcp-config.json", launchedWithEnv("${API_TOKEN}"), false, ""},
		{"copilot, a bare reference", ".copilot/mcp-config.json", launchedWithEnv("$API_TOKEN"), true, "${API_TOKEN}"},
		{"codex header", ".codex/config.toml", "[mcp_servers.svc]\nurl = \"https://mcp.example.com/\"\n" +
			"http_headers = { Authorization = \"Bearer ${API_TOKEN}\" }\n", true, "bearer_token_env_var"},
		{"codex env", ".codex/config.toml", "[mcp_servers.svc]\ncommand = \"/usr/local/bin/svc-server\"\n" +
			"env = { API_TOKEN = \"${API_TOKEN}\" }\n", true, "env_vars"},
		// A header's name says nothing about which credential it carries, so
		// the variable the fix suggests is named for the server.
		{"a token", ".claude.json", remoteWithHeader("Bearer " + tokenValue), true, "${SVC_TOKEN}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeHome(t, map[string]struct {
				body string
				mode os.FileMode
			}{tc.file: {tc.body, 0o600}})
			var failed []string
			for _, row := range agentRowList(t) {
				if row[0] == "svc" && row[1] == findings.Fail {
					failed = append(failed, row[2])
				}
			}
			if plain := len(failed) > 0; plain != tc.plain {
				t.Fatalf("failed rows %q, want a credential in the file: %v", failed, tc.plain)
			}
			_, fix := fixBodies(t)
			if tc.plain && !strings.Contains(fix, tc.fix) {
				t.Errorf("the fix does not name %q:\n%s", tc.fix, fix)
			}
			if strings.Contains(strings.Join(failed, " ")+fix, tokenValue) {
				t.Error("the value reached the report")
			}
		})
	}
}

// A bare $NAME and a %NAME% are how a variable is named to a shell, and how a
// password can be spelled: "$ECRET_PASSWORD", or a token after "Bearer $".
// Read as a variable's name, the report printed the value with its first
// character taken off, in the row and in the fix. Such an entry is named by
// its key instead, and the fix suggests a variable named for the entry, as
// it does for a value held; a braced reference, which no credential is
// spelled as, is still named as written.
func TestAValueSpelledLikeABareReferenceIsNeverPrinted(t *testing.T) {
	for _, tc := range []struct {
		name, body, value, fix string
	}{
		{"env", launchedWithEnv("$ECRET_PASSWORD"), "ECRET_PASSWORD", "${API_TOKEN}"},
		{"header", remoteWithHeader("Bearer $ABCDEF123XYZ"), "ABCDEF123XYZ", "${SVC_TOKEN}"},
		{"percent", launchedWithEnv("%HUNTER_TWO%"), "HUNTER_TWO", "${API_TOKEN}"},
		{"braced", remoteWithHeader("Bearer ${env:API_TOKEN}"), "", "${API_TOKEN}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeHome(t, map[string]struct {
				body string
				mode os.FileMode
			}{".claude.json": {tc.body, 0o600}})
			var failed []string
			for _, row := range agentRowList(t) {
				if row[0] == "svc" && row[1] == findings.Fail {
					failed = append(failed, row[2])
				}
			}
			if len(failed) == 0 {
				t.Fatal("a reference Claude Code does not expand was not failed")
			}
			_, fix := fixBodies(t)
			if !strings.Contains(fix, tc.fix) {
				t.Errorf("the fix does not name %q:\n%s", tc.fix, fix)
			}
			if tc.value != "" && strings.Contains(strings.Join(failed, " ")+fix, tc.value) {
				t.Errorf("the value reached the report: %q\n%s", failed, fix)
			}
		})
	}
}
