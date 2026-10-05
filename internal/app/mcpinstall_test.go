package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
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
	// A registration reads the client's own configuration in the home
	// directory to compare it with what it is about to make, so a test that
	// registers gets a home of its own rather than the developer's.
	t.Setenv("HOME", t.TempDir())
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
//
// A name close to a client rta knows is a typo, and is told so with the one it
// was near. cobra's OnlyValidArgs once refused it in cobra's own words —
// `invalid argument "nope" for "rta mcp install"` — one step ahead of the check
// in RunE that names a client rta does know.
func TestATypoOfAKnownClientIsAnsweredWithTheOneItWasNear(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "mcp", "install", "cluade")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, `"claude"`) {
		t.Fatalf("err = %#v, want a usage refusal naming claude", err)
	}
	for _, name := range []string{"claude", "codex", "copilot", "cursor", "gemini", "vscode"} {
		if !strings.Contains(ve.Hint, name) {
			t.Errorf("%q is not offered in %q", name, ve.Hint)
		}
	}
}

// Any other client that speaks MCP over stdio takes the standard block, and
// rta says what it does not know instead of refusing the name: the docs say
// anything that speaks MCP works, and the command line was the one place that
// did not.
func TestAClientRtaDoesNotKnowGetsTheStandardBlock(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "windsurf", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	var block map[string]json.RawMessage
	if jerr := json.Unmarshal([]byte(pairs["block"]), &block); jerr != nil || block["mcpServers"] == nil {
		t.Errorf("block = %q, want the standard mcpServers json: %v", pairs["block"], jerr)
	}
	if !strings.Contains(pairs["block"], `"windsurf"`) || pairs["as"] != "windsurf" {
		t.Errorf("the block does not name the agent windsurf: %v", pairs)
	}
	if !strings.Contains(pairs["add to"], "rta does not know where") {
		t.Errorf("add to = %q, want it to say rta does not know where windsurf keeps its config", pairs["add to"])
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".windsurf")); err == nil {
		t.Error("something was written for a client rta cannot register with")
	}
}

func TestEveryClientIsRegisteredUnderAName(t *testing.T) {
	for _, c := range mcpClients() {
		t.Run(c.name, func(t *testing.T) {
			self := "/opt/rta"
			if c.bin != "" {
				got := strings.Join(c.args(self, serveArgs(c.name, serveOptions{})), " ")
				if !strings.Contains(got, "--as "+c.name) &&
					!strings.Contains(got, `"--as","`+c.name+`"`) {
					t.Errorf("the command rta would run does not name the agent: %s", got)
				}
			}
			if block := c.block(self, serveArgs(c.name, serveOptions{})); !strings.Contains(block, "--as") ||
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

// mcp install answers with pairs, in the format asked for, whichever of its
// three answers it gives: registered by the client's own command, would be
// under --dry-run, or the block to add by hand. It printed prose on stdout
// whatever -o said, and let the client's own command print into the same
// stream, so a script provisioning a machine parsed two tools' sentences.
func TestMCPInstallAnswersWithAViewInTheFormatAskedFor(t *testing.T) {
	argv := fakeClient(t, "claude", 0)
	// The client says something of its own, as `claude mcp add` does.
	chatty := filepath.Join(filepath.Dir(argv), "claude")
	script, err := os.ReadFile(chatty)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.Replace(string(script), "exit 0", "echo 'Added stdio MCP server rta'\nexit 0", 1))
	if err := os.WriteFile(chatty, script, 0o755); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if pairs["would register"] != "Claude Code" || !strings.Contains(pairs["would run"], "mcp add rta -- ") {
		t.Errorf("a dry run answered %v", pairs)
	}

	out, errOut, err = run(t, testRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs = answerPairs(t, out)
	if pairs["registered"] != "Claude Code" || pairs["as"] != "claude" ||
		!strings.HasSuffix(pairs["ran"], "mcp serve --as claude") ||
		!strings.Contains(pairs["reach"], "--agent claude") || !strings.Contains(pairs["next"], "restart Claude Code") {
		t.Errorf("answered %v, want the client, the name, the command it ran, what comes next and what it reaches", pairs)
	}
	if !strings.Contains(errOut, "Added stdio MCP server rta") {
		t.Errorf("the client's own words were lost rather than moved to stderr: %q", errOut)
	}

	// A default that names no format stops it before the client's command
	// runs, as it stops every command that renders.
	if err := os.Remove(argv); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_OUTPUT", "bogus")
	_, _, err = run(t, testRegistry(t), "mcp", "install", "claude")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeOutputInvalid {
		t.Errorf("err = %#v, want %s", err, CodeOutputInvalid)
	}
	if _, statErr := os.Stat(argv); statErr == nil {
		t.Error("a broken default ran the client's command before the refusal")
	}
	t.Setenv("RTA_OUTPUT", "")

	t.Setenv("PATH", t.TempDir())
	out, errOut, err = run(t, testRegistry(t), "mcp", "install", "cursor", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs = answerPairs(t, out)
	var block map[string]json.RawMessage
	if jerr := json.Unmarshal([]byte(pairs["block"]), &block); jerr != nil || block["mcpServers"] == nil {
		t.Errorf("block = %q, want the json to paste, whole: %v", pairs["block"], jerr)
	}
	if !strings.Contains(pairs["add to"], "~/.cursor/mcp.json") || pairs["as"] != "cursor" {
		t.Errorf("answered %v, want the file to add it to and the agent name", pairs)
	}

	onATerminal(t)
	out, errOut, err = run(t, testRegistry(t), "mcp", "install", "codex", "--show", "--no-color")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairsDrawn, _, _ := strings.Cut(out, "\n\n")
	readsOnATerminal(t, pairsDrawn, "client", "add to", "as", "next")
	if !strings.Contains(out, "[mcp_servers.rta]") {
		t.Errorf("the block to paste is not on the screen:\n%s", out)
	}
}

// The block is copied into a file, so a terminal draws it as it is, however
// narrow: drawn as a pair's value it was wrapped as prose, which split TOML's
// `command =` from its value and a JSON string across two lines. Every line
// of it is on the screen whole, at the start of a line, and the pairs around
// it still fit.
func TestMCPInstallDrawsTheBlockAsItIsOnANarrowTerminal(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	onATerminal(t)
	t.Setenv("COLUMNS", "30")
	t.Setenv("PATH", t.TempDir())
	for _, name := range []string{"codex", "cursor"} {
		client, _ := findClient(name)
		out, errOut, err := run(t, testRegistry(t), "mcp", "install", name, "--show", "--no-color")
		if err != nil {
			t.Fatalf("%s: %v %q", name, err, errOut)
		}
		pairsDrawn, drawnBlock, ok := strings.Cut(out, "\n\n")
		if !ok {
			t.Fatalf("%s: no block after the pairs:\n%s", name, out)
		}
		if want := strings.TrimRight(client.block(self, serveArgs(name, serveOptions{})), "\n"); strings.TrimRight(drawnBlock, "\n") != want {
			t.Errorf("%s: the block was reshaped on a 30-column terminal:\n%s\nwant:\n%s", name, drawnBlock, want)
		}
		if strings.Contains(pairsDrawn, "mcpServers") || strings.Contains(pairsDrawn, "mcp_servers") {
			t.Errorf("%s: the block was drawn among the pairs as well:\n%s", name, pairsDrawn)
		}
		if !strings.Contains(pairsDrawn, "next") || !strings.Contains(pairsDrawn, "add to") {
			t.Errorf("%s: the pairs around the block are gone:\n%s", name, pairsDrawn)
		}
	}
}

// The note under a block said that without the name "every MCP client on this
// machine shares one set of permissions". `rta mcp serve` has refused to start
// without a name for a long time, so there is no nameless client to share
// anything: the sentence described a state the block it sat under cannot be
// used to reach, and the reason the name matters is the one the server's own
// refusal gives — grants are issued to it, and a lock freezes it.
func TestTheNoteUnderABlockSaysWhatTheNameIsFor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "cursor", "--show", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	next := answerPairs(t, out)["next"]
	for _, want := range []string{"`--as cursor`", "grants are issued to that name", "`rta lock add cursor`"} {
		if !strings.Contains(next, want) {
			t.Errorf("the note %q lacks %q", next, want)
		}
	}
	if strings.Contains(next, "shares one set") {
		t.Errorf("the note describes clients with no name, which cannot start: %q", next)
	}
}

// D3: --dry-run used to be silently ignored here too — `rta mcp install
// claude --dry-run` ran claude's own registration command for real, the
// one command in this file that touches a config file rta does not own.
func TestInstallDryRunDoesNotRunTheClientsOwnCommand(t *testing.T) {
	argv := fakeClient(t, "claude", 0)
	out, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(argv); statErr == nil {
		t.Fatal("--dry-run ran the client's own command anyway")
	}
	if !strings.Contains(out, "would run") || !strings.Contains(out, "claude") {
		t.Errorf("dry-run output does not preview the command: %q", out)
	}

	// The real run still works afterwards.
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(argv); statErr != nil {
		t.Error("the real run did not run the client's own command")
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

// --global passes claude's own --scope user, the one client this file has
// verified the flag against — see docs/30-boundary/60-ai-clients.md.
func TestInstallGlobalPassesClaudesScopeFlag(t *testing.T) {
	argv := fakeClient(t, "claude", 0)
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--global"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argvOf(t, argv), " ")
	if !strings.Contains(got, "--scope user") {
		t.Errorf("argv = %q, want --scope user", got)
	}
	// Without --global, the ordinary project-scoped command runs — the flag
	// must not leak into every install.
	argv2 := fakeClient(t, "claude", 0)
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "claude"); err != nil {
		t.Fatal(err)
	}
	if got2 := strings.Join(argvOf(t, argv2), " "); strings.Contains(got2, "--scope") {
		t.Errorf("argv = %q, --scope leaked into a run that never asked for it", got2)
	}
}

// A second install is refused by the client because rta is already there, and
// was answered "could not register it — here is what to add instead" with a
// block to paste: a duplicate of the server the client had just said it holds.
// Then it was answered "already registered" at exit 0 while doing nothing of
// what was asked. When rta cannot read what is registered to compare it, the
// registration asked for is not the one that is there, and that is an error
// with the exact line that takes the old one out.
func TestARegistrationRtaCannotCompareIsRefusedWithTheLineThatTakesItOut(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'MCP server rta already exists in local config' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	for global, remove := range map[bool]string{false: "claude mcp remove rta --scope local", true: "claude mcp remove rta --scope user"} {
		args := []string{"mcp", "install", "claude"}
		if global {
			args = append(args, "--global")
		}
		out, errOut, err := run(t, testRegistry(t), args...)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != "core.mcp.install.exists" || !strings.Contains(ve.Hint, remove) {
			t.Errorf("global=%v: err = %#v, want core.mcp.install.exists naming %q", global, err, remove)
		}
		if strings.Contains(out+errOut, "here is what to add instead") || strings.Contains(out, `"mcpServers"`) {
			t.Errorf("a server that is already there was answered with a block to add:\n%s\n%s", out, errOut)
		}
	}
}

func TestInstallGlobalDryRunPreviewsTheScopeFlag(t *testing.T) {
	fakeClient(t, "claude", 0)
	out, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--global", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--scope user") {
		t.Errorf("dry-run output does not preview the scope flag: %q", out)
	}
}

// codex and gemini are declared but not verified against their real CLIs —
// see the "verified" column in docs/30-boundary/60-ai-clients.md — so
// --global refuses rather than guessing a scope flag on top of an already
// unverified command.
func TestInstallGlobalRefusesForAnUnverifiedClient(t *testing.T) {
	argv := fakeClient(t, "codex", 0)
	_, _, err := run(t, testRegistry(t), "mcp", "install", "codex", "--global", "-o", "json")
	if err == nil {
		t.Fatal("want a refusal — rta does not know codex's global-scope flag")
	}
	// Coded, so it reaches -o json as json: it was the plain error fang
	// styled as a box. The command line cannot work as typed, so core.usage.
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Hint, "--show") {
		t.Errorf("err = %#v, want %s with --show in the hint", err, CodeUsage)
	}
	if _, statErr := os.Stat(argv); statErr == nil {
		t.Error("codex was run despite rta not knowing the right flag to pass it")
	}
}

// --show never runs anything, so there is no command for --global to be
// refused about: the refusal used to fire anyway, telling the operator to pass
// the flag they had just passed.
func TestInstallGlobalShowStillPrintsTheBlock(t *testing.T) {
	fakeClient(t, "codex", 0)
	out, _, err := run(t, testRegistry(t), "mcp", "install", "codex", "--global", "--show")
	if err != nil {
		t.Fatalf("--show with --global was refused: %v", err)
	}
	if !strings.Contains(out, "mcp_servers.rta") {
		t.Errorf("output does not carry the block to paste: %q", out)
	}
}

// Nothing runs for a client that is not installed either — the operator gets
// the block, the same as without --global.
func TestInstallGlobalFallsBackWhenTheClientIsNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	out, _, err := run(t, testRegistry(t), "mcp", "install", "codex", "--global")
	if err != nil {
		t.Fatalf("--global was refused for a client that cannot run at all: %v", err)
	}
	if !strings.Contains(out, "~/.codex/config.toml") {
		t.Errorf("output does not name the file to edit: %q", out)
	}
}

// VS Code's own command already writes to the one user-level file it has —
// --global changes nothing about what runs, and must not be refused as
// though it asked for something rta cannot do.
func TestInstallGlobalIsANoOpForVSCode(t *testing.T) {
	argv := fakeClient(t, "code", 0)
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "vscode", "--global"); err != nil {
		t.Fatal(err)
	}
	withGlobal := argvOf(t, argv)

	argv2 := fakeClient(t, "code", 0)
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "vscode"); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(argvOf(t, argv2), " "), strings.Join(withGlobal, " "); got != want {
		t.Errorf("--global changed vscode's command: %q vs %q", got, want)
	}
}

// Cursor has no command, so --global steers which file rta recommends
// instead of which command it runs.
func TestInstallGlobalPicksTheUserPathForCursor(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	out, _, err := run(t, testRegistry(t), "mcp", "install", "cursor", "--global")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "~/.cursor/mcp.json") {
		t.Errorf("output does not name the user path: %q", out)
	}
	if strings.Contains(out, "for one project") {
		t.Errorf("output still mentions the project path under --global: %q", out)
	}

	// Without --global, the existing combined recommendation is unchanged.
	out2, _, err := run(t, testRegistry(t), "mcp", "install", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "for one project") {
		t.Errorf("the default recommendation regressed: %q", out2)
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
			if err := json.Unmarshal([]byte(c.block("/opt/rta", serveArgs("x", serveOptions{}))), &parsed); err != nil {
				t.Fatalf("the block is not valid JSON: %v", err)
			}
			if _, ok := parsed[tc.wantKey]; !ok {
				t.Errorf("top-level key is %v, want %q", keysOf(parsed), tc.wantKey)
			}
		})
	}
	// Codex is TOML, alone among them, so it is checked for what it is.
	c, _ := findClient("codex")
	block := c.block("/opt/rta", serveArgs("x", serveOptions{}))
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

// `rta mcp install` with no client said "missing <client>" and left a person
// to find the six in --help. It names them, the ones found on this machine
// first.
func TestInstallWithNoClientListsTheClientsAndMarksTheOnesFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, testRegistry(t), "mcp", "install")
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != CodeUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if !strings.HasPrefix(verr.Message, "missing <client>") {
		t.Errorf("message = %q", verr.Message)
	}
	if !strings.Contains(verr.Hint, "cursor (found here), ") {
		t.Errorf("hint = %q, want cursor first and marked", verr.Hint)
	}
	for _, c := range mcpClients() {
		if !strings.Contains(verr.Hint, c.name) {
			t.Errorf("hint = %q, does not name %s", verr.Hint, c.name)
		}
		if c.name != "cursor" && strings.Contains(verr.Hint, c.name+" (found here)") {
			t.Errorf("hint = %q, marks %s found when it is not here", verr.Hint, c.name)
		}
	}
}
