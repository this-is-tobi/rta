package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

const remoteTokenValue = "sk-remote-thisisarealtokenvalue0123456789"

// configWithRemoteServer declares an MCP server the remote way — no command to
// launch, a URL to call and headers to call it with.
func configWithRemoteServer(url string) string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"remote": map[string]any{
			"type":    "http",
			"url":     url,
			"headers": map[string]string{"Authorization": "Bearer " + remoteTokenValue},
		},
	}})
	return string(b)
}

// configWithHeadersOnlyRemoteServer is the remote form with no `type` field at
// all — the shape a client that only ever writes one remote transport, or one
// this file has not seen the spelling of yet, produces. remoteTransports names
// today's spellings; it is not the only way to say "this is a server".
func configWithHeadersOnlyRemoteServer(url string) string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"remote": map[string]any{
			"url":     url,
			"headers": map[string]string{"Authorization": "Bearer " + remoteTokenValue},
		},
	}})
	return string(b)
}

// configWithPlaintextRemoteServer is the remote form with a header that does
// not look like a credential, so a fixture built from it can only ever trip
// the plaintext-endpoint finding — never the header-credential one too. Using
// configWithRemoteServer for the plaintext tests would leave them asserting on
// whichever of the two findings gradeServers happens to add last for the same
// server name, which is a property of source order and not of the behaviour
// the test means to pin down.
func configWithPlaintextRemoteServer(url string) string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"remote": map[string]any{
			"type":    "http",
			"url":     url,
			"headers": map[string]string{"X-Client-Name": "rta-test"},
		},
	}})
	return string(b)
}

// **A remote server is a server.**
//
// collectServers recognised an object by its `command` string, so a server
// declared with a url and headers was not merely graded leniently — it was
// invisible: not counted, not named, its credential never looked at. The same
// file failing for a token in `env` said nothing about a bearer token in
// `headers`, which is the more exposed of the two: it is a live credential
// some other machine accepts, sitting in a file this audit was already
// reading.
func TestARemoteServersCredentialIsGradedLikeALocalOnes(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithRemoteServer("https://mcp.example.com/v1"), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	row := rows["remote"]
	if row == nil {
		t.Fatalf("a remote server with a bearer token produced no finding; rows: %v", keysOf(rows))
	}
	if !strings.Contains(strings.Join(row, " "), "Authorization") {
		t.Errorf("the finding did not name the header, so it is not actionable: %v", row)
	}
}

// The same rule the local path already obeys: the name is what makes the row
// actionable, and printing the value would be the audit spreading the exposure
// it came to report.
func TestARemoteServersCredentialValueIsNeverPrinted(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithRemoteServer("https://mcp.example.com/v1"), 0o600}})

	for _, detail := range []bool{false, true} {
		v, err := runClients(t.Context(), req(map[string]any{"detail": detail}).WithSurface(plugin.SurfaceCLI), testCatalog)
		if err != nil {
			t.Fatal(err)
		}
		blob, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(blob), remoteTokenValue) {
			t.Errorf("--detail=%v printed the credential it was reporting", detail)
		}
	}
}

// On this transport the header is the whole credential and it crosses the wire
// as it is. rta's own remotes.yaml refuses plain http for anything but
// loopback, for the same reason, and a client config pointed at one deserves
// the same answer.
func TestAPlaintextRemoteServerIsAFinding(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithPlaintextRemoteServer("http://mcp.example.com/v1"), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	if row := rows["remote"]; row == nil || !strings.Contains(strings.Join(row, " "), "http://") {
		t.Errorf("a plaintext remote server was not reported: %v", row)
	}
}

// Loopback is the documented shape of a server running beside you — `rta mcp
// serve --http 127.0.0.1:8443` — where there is no wire to cross. Failing it
// would train people to ignore the finding that matters.
func TestALoopbackRemoteServerIsNotAPlaintextFinding(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithPlaintextRemoteServer("http://127.0.0.1:8443/"), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	if row := rows["remote"]; row != nil && strings.Contains(strings.Join(row, " "), "plain http") {
		t.Errorf("a loopback server was reported as crossing a network: %v", row)
	}
}

// The hostname spelling of the same exception. plaintextEndpoint checks it
// separately from net.ParseIP — "localhost" does not parse as an IP — so it
// earns its own case rather than riding on the numeric one above.
func TestALocalhostRemoteServerIsNotAPlaintextFinding(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithPlaintextRemoteServer("http://localhost:8443/"), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	if row := rows["remote"]; row != nil && strings.Contains(strings.Join(row, " "), "plain http") {
		t.Errorf("a localhost server was reported as crossing a network: %v", row)
	}
}

// **The fallback that has to work without a recognised `type`.** remoteServer
// accepts a url on either of two signals — a transport type it knows, or a
// headers object — and every other test here only ever supplies the first.
// remoteTransports is a fixed list of today's spellings; the headers signal is
// what still calls this a server the day a client omits `type` or spells it
// something this file has never seen.
func TestARemoteServerWithHeadersAndNoTypeIsStillAServer(t *testing.T) {
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".cursor/mcp.json": {configWithHeadersOnlyRemoteServer("https://mcp.example.com/v1"), 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	row := rows["remote"]
	if row == nil {
		t.Fatalf("a remote server with headers but no `type` produced no finding; rows: %v", keysOf(rows))
	}
	if !strings.Contains(strings.Join(row, " "), "Authorization") {
		t.Errorf("the finding did not name the header, so it is not actionable: %v", row)
	}
}

// **The asymmetry that decides the predicate.** A missed server is the bug
// being fixed; an invented one puts a false finding in a security report,
// which is worse — it is the failure that teaches people to stop reading the
// output. `url` is an ordinary key in ordinary JSON, and .claude.json is a
// large file of it, so a url alone is not a server: it needs a transport type
// or headers beside it.
// The URLs here are http:// on purpose. An invented server only becomes a
// visible false finding when it also trips a grade, so a fixture of https
// links would pass whatever the predicate did — and an earlier version of this
// test did exactly that, staying green while the predicate was loosened to
// accept a bare url. A plain-http config value is both the realistic shape and
// the one that actually fails when the predicate is wrong.
func TestAnOrdinaryUrlIsNotMistakenForAServer(t *testing.T) {
	body := `{"projects":{"/some/dir":{"lastCost":0}},"tipsHistory":{"url":"http://example.com/docs"},` +
		`"telemetry":{"url":"http://collector.internal:4318"}}`
	fakeHome(t, map[string]struct {
		body string
		mode os.FileMode
	}{".claude.json": {body, 0o600}})

	rows := agentRows(t, plugin.SurfaceCLI)
	for _, name := range []string{"tipsHistory", "telemetry"} {
		if _, found := rows[name]; found {
			t.Errorf("%q is not an MCP server and was reported as one", name)
		}
	}
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
