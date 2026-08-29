package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeClient puts an executable named bin on PATH that records its argv and
// exits with code. Every client rta claims to register with is reached through
// exec, so this is the seam where "what does rta actually run" is answerable.
func fakeClient(t *testing.T, bin string, code int) (argvFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	script := "#!/bin/sh\n: > " + argvFile + "\nfor a in \"$@\"; do echo \"$a\" >> " +
		argvFile + "; done\nexit " + strconv.Itoa(code) + "\n"
	path := filepath.Join(dir, bin)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvFile
}

func argvOf(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the client was never run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// **Every registration names the agent.** Without it every MCP client on the
// machine shares one set of grants, which is the whole point of naming one
// — and a feature that has to be turned on by hand is one that stays off
// (a control that requires homework is a control that stays off).
func TestEveryClientIsRegisteredUnderAName(t *testing.T) {
	for _, c := range mcpClients() {
		t.Run(c.name, func(t *testing.T) {
			self := "/opt/rta"
			if c.bin != "" {
				got := strings.Join(c.args(self, c.name), " ")
				if !strings.Contains(got, "--as "+c.name) &&
					!strings.Contains(got, `"--as","`+c.name+`"`) {
					t.Errorf("the command rta would run does not name the agent: %s", got)
				}
			}
			if block := c.block(self, c.name); !strings.Contains(block, "--as") ||
				!strings.Contains(block, c.name) {
				t.Errorf("the block rta would print does not name the agent:\n%s", block)
			}
			if c.file == "" {
				t.Error("no config file named, so the printed block says nothing about where it goes")
			}
		})
	}
}

func TestInstallRunsTheClientsOwnCommand(t *testing.T) {
	argv := fakeClient(t, "claude", 0)
	out, _, err := run(t, testRegistry(t), "mcp", "install", "claude")
	if err != nil {
		t.Fatal(err)
	}
	got := argvOf(t, argv)
	// The `--` matters: without it claude reads `--as` as its own flag and
	// the agent is registered nameless. Verified against the real claude.
	want := []string{"mcp", "add", "rta", "--"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("argv[%d] = %q, want %q (full: %v)", i, got[i], w, got)
		}
	}
	tail := strings.Join(got[len(got)-4:], " ")
	if tail != "mcp serve --as claude" {
		t.Errorf("the server is launched as %q, want it named", tail)
	}
	if !strings.Contains(out, "registered") {
		t.Errorf("nothing said it worked: %s", out)
	}
}

func TestTheAgentNameDefaultsToTheClientAndIsOverridable(t *testing.T) {
	argv := fakeClient(t, "claude", 0)
	if _, _, err := run(t, testRegistry(t),
		"mcp", "install", "claude", "--as", "work-laptop"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(argvOf(t, argv), " "); !strings.HasSuffix(got, "--as work-laptop") {
		t.Errorf("--as was ignored: %s", got)
	}
}

// The same charset rule `grant allow --agent` and `mcp serve --as` use, and
// deliberately the same function: a name registered here that the grant
// command would refuse is a server nobody can ever grant anything to.
func TestAnUnusableAgentNameIsRefusedBeforeAnythingIsRegistered(t *testing.T) {
	argv := fakeClient(t, "claude", 0)
	_, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--as", "not a name")
	if err == nil {
		t.Fatal("a name the grant command refuses was registered anyway")
	}
	if _, statErr := os.Stat(argv); statErr == nil {
		t.Error("the client was run before the name was checked")
	}
}

// A client that is not installed is the ordinary case, not an error: the
// operator gets the block to paste rather than a failure.
func TestAMissingClientFallsBackToShowingTheConfig(t *testing.T) {
	// No fake on PATH, and PATH emptied so a real `cursor`/`claude` on the
	// machine running this cannot change the answer.
	t.Setenv("PATH", t.TempDir())
	out, _, err := run(t, testRegistry(t), "mcp", "install", "claude")
	if err != nil {
		t.Fatalf("a missing client was an error: %v", err)
	}
	if !strings.Contains(out, "mcpServers") || !strings.Contains(out, "--as") {
		t.Errorf("no usable config was printed:\n%s", out)
	}
}

// And a client whose command has moved on is the case that most needs the
// fallback: failing there would leave somebody with nothing at all.
func TestAFailingClientCommandStillPrintsWhatToAdd(t *testing.T) {
	fakeClient(t, "claude", 1)
	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude")
	if err != nil {
		t.Fatalf("a failing client command was fatal: %v", err)
	}
	if !strings.Contains(out, "mcpServers") {
		t.Errorf("no fallback config was printed:\n%s", out)
	}
	if !strings.Contains(errOut, "could not register") {
		t.Errorf("the failure was not mentioned: %s", errOut)
	}
}

// **rta does not write another tool's config file.** That is the claim the
// package comment makes, and it is the one worth a test: these files hold
// comments and credentials rta has no business round-tripping, and this is
// the file that gives an agent access to the operator's secrets.
func TestInstallWritesNoFileOfItsOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("PATH", t.TempDir())

	for _, c := range mcpClients() {
		if _, _, err := run(t, testRegistry(t), "mcp", "install", c.name); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}

	var found []string
	_ = filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("install created %v — it must only ever print", found)
	}
}

// The shape each client actually wants. VS Code is the one that differs, and
// it is the reason this is a table rather than one renderer: its key is
// `servers`, everything else inherited Claude Desktop's `mcpServers`, and
// getting that wrong writes a config that silently registers nothing.
func TestEachClientGetsItsOwnShape(t *testing.T) {
	for _, tc := range []struct{ client, wantKey string }{
		{"claude", "mcpServers"},
		{"vscode", "servers"},
		{"cursor", "mcpServers"},
		{"gemini", "mcpServers"},
		{"copilot", "mcpServers"},
	} {
		t.Run(tc.client, func(t *testing.T) {
			c, ok := findClient(tc.client)
			if !ok {
				t.Fatalf("no such client")
			}
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(c.block("/opt/rta", "x")), &parsed); err != nil {
				t.Fatalf("the block is not valid JSON: %v", err)
			}
			if _, ok := parsed[tc.wantKey]; !ok {
				t.Errorf("top-level key is %v, want %q", keysOf(parsed), tc.wantKey)
			}
		})
	}
	// Codex is TOML, alone among them, so it is checked for what it is.
	c, _ := findClient("codex")
	block := c.block("/opt/rta", "x")
	if !strings.Contains(block, "[mcp_servers.rta]") || !strings.Contains(block, `"--as", "x"`) {
		t.Errorf("codex's block is not the TOML it needs:\n%s", block)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
