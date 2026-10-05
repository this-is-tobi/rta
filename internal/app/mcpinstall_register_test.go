package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// loggingClient puts an executable named bin on PATH that appends its argv, one
// call per line, to a log, and then runs body. calls reads the log back.
func loggingClient(t *testing.T, bin, body string) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	return func() []string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

// claudeHas writes ~/.claude.json as claude leaves it after `mcp add`: an entry
// under the scope's own key.
func claudeHas(t *testing.T, scope claudeScope, args ...string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	self, err := registeredBinary()
	if err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"type": "stdio", "command": self, "args": args}
	servers := map[string]any{"rta": entry}
	doc := map[string]any{}
	if body, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".claude.json")); err == nil {
		_ = json.Unmarshal(body, &doc)
	}
	switch scope {
	case scopeUser:
		doc["mcpServers"] = servers
	case scopeLocal:
		doc["projects"] = map[string]any{wd: map[string]any{"mcpServers": servers}}
	}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The default of `claude mcp add` is this directory and nothing else, and the
// receipt said none of it: somebody who ran this from their downloads folder
// had an agent that saw no rta in every project they opened it in, and
// found out from doctor. The receipt says where it registered and what to
// do next, in every format.
func TestTheReceiptSaysWhereItRegisteredAndWhatToDoNext(t *testing.T) {
	loggingClient(t, "claude", "exit 0")
	wd, _ := os.Getwd()

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if !strings.Contains(pairs["scope"], "this directory only ("+wd+")") || !strings.Contains(pairs["scope"], "--global") {
		t.Errorf("scope = %q, want this directory named and the --global way out", pairs["scope"])
	}
	for _, want := range []string{"restart Claude Code in this directory", "sys_overview", "rta agent overview"} {
		if !strings.Contains(pairs["next"], want) {
			t.Errorf("next = %q, missing %q", pairs["next"], want)
		}
	}

	out, errOut, err = run(t, testRegistry(t), "mcp", "install", "claude", "--global", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs = answerPairs(t, out)
	if !strings.Contains(pairs["scope"], "every project") || strings.Contains(pairs["next"], "in this directory") {
		t.Errorf("--global answered scope=%q next=%q", pairs["scope"], pairs["next"])
	}
}

// The scope is stated where rta knows it and not guessed where it does not.
func TestTheReceiptDoesNotInventAScopeForAClientItHasNotVerified(t *testing.T) {
	loggingClient(t, "codex", "exit 0")
	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "codex", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	scope := answerPairs(t, out)["scope"]
	if !strings.Contains(scope, "codex mcp list") || strings.Contains(scope, "every project") {
		t.Errorf("scope = %q, want it to say it does not know and how to look", scope)
	}

	loggingClient(t, "code", "exit 0")
	out, _, err = run(t, testRegistry(t), "mcp", "install", "vscode", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if scope := answerPairs(t, out)["scope"]; !strings.Contains(scope, "every project") {
		t.Errorf("vscode scope = %q, want its single user-level file named", scope)
	}
}

// A client whose own command is missing or fails used to be answered with a
// block and no reason, exit 0: the operator was left to work out that claude
// was not on PATH. The reason comes before the block, as an answer of its own.
func TestTheBlockInsteadOfARegistrationSaysWhy(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	out, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if why := answerPairs(t, out)["why"]; !strings.Contains(why, "claude is not on PATH") {
		t.Errorf("why = %q, want it to say claude is not on PATH and nothing was run", why)
	}

	loggingClient(t, "claude", "echo 'boom' >&2; exit 3")
	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if why := answerPairs(t, out)["why"]; !strings.Contains(why, "exit status 3") || !strings.Contains(why, "above") {
		t.Errorf("why = %q, want the failure and where the client's own words are", why)
	}
	if !strings.Contains(errOut, "boom") {
		t.Errorf("the client's own words were lost: %q", errOut)
	}

	// Asked for with --show it is the answer, not a failure, and says nothing of one.
	out, _, err = run(t, testRegistry(t), "mcp", "install", "claude", "--show", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if why, ok := answerPairs(t, out)["why"]; ok {
		t.Errorf("--show gave a reason, %q, for what was asked for", why)
	}
}

// The server's own options go into the registered line, because the client is
// what launches the server and a flag that is not in that line is not set. They
// are shown wherever the line is: under ran, --dry-run and --show.
func TestTheServersOptionsAreRegisteredAndShown(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	root := t.TempDir()
	args := []string{"mcp", "install", "claude", "--consent", "--consent-notify", "--consent-wait", "2m",
		"--max-result", "32", "--root", root, "-o", "json"}

	out, errOut, err := run(t, testRegistry(t), args...)
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	want := "mcp serve --as claude --consent --consent-notify --consent-wait 2m --max-result 32 --root " + root
	if got := calls(); len(got) != 1 || !strings.HasSuffix(got[0], want) {
		t.Fatalf("claude was run as %v, want a line ending %q", got, want)
	}
	pairs := answerPairs(t, out)
	if !strings.HasSuffix(pairs["ran"], want) {
		t.Errorf("ran = %q, want the options in it", pairs["ran"])
	}
	for _, key := range []string{"consent", "roots", "max result"} {
		if pairs[key] == "" {
			t.Errorf("no %q pair in %v", key, pairs)
		}
	}
	if !strings.Contains(pairs["reach"], "waits for your answer") {
		t.Errorf("reach = %q, want it to say a call that needs a grant waits", pairs["reach"])
	}

	// A dry run runs nothing and shows the same line.
	before := len(calls())
	out, _, err = run(t, testRegistry(t), append(args, "--dry-run")...)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls()) != before {
		t.Error("--dry-run ran the client")
	}
	if got := answerPairs(t, out)["would run"]; !strings.HasSuffix(got, want) {
		t.Errorf("would run = %q, want the options in it", got)
	}

	// And --show carries them in the block to paste.
	out, _, err = run(t, testRegistry(t), append(args, "--show")...)
	if err != nil {
		t.Fatal(err)
	}
	var block struct {
		MCPServers map[string]stdioServer `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(answerPairs(t, out)["block"]), &block); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(block.MCPServers["rta"].Args, " "); got != want {
		t.Errorf("the block launches %q, want %q", got, want)
	}
}

// Every option is off unless given: the registration anyone who passes none of
// them makes is the one it always was.
func TestNothingIsRegisteredThatWasNotAskedFor(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "claude"); err != nil {
		t.Fatal(err)
	}
	line := calls()[0]
	for _, flag := range []string{"--consent", "--max-result", "--root", "--consent-wait"} {
		if strings.Contains(line, flag) {
			t.Errorf("%q registered %s without being asked", line, flag)
		}
	}
}

// What `rta mcp serve` would only warn about or fail on at the first call is
// refused where it is typed, once, with a terminal to say so: a registration
// is read months later by a server nobody is watching.
func TestOptionsThatCannotWorkAreRefusedBeforeAnythingIsRegistered(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	for name, args := range map[string][]string{
		"notify without consent":   {"--consent-notify"},
		"wait without consent":     {"--consent-wait", "1m"},
		"wait past the maximum":    {"--consent", "--consent-wait", "20m"},
		"max-result below one":     {"--max-result", "0"},
		"max-result above ceiling": {"--max-result", "999"},
		"a root that is no dir":    {"--root", filepath.Join(t.TempDir(), "nope")},
	} {
		_, _, err := run(t, testRegistry(t), append([]string{"mcp", "install", "claude"}, args...)...)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Hint == "" {
			t.Errorf("%s: err = %#v, want a coded refusal with a hint", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Errorf("claude was run despite a refused option: %v", calls())
	}
}

// A relative root is a different directory in every place the client starts the
// server from, so it is registered as the absolute path it names today.
func TestARootIsRegisteredAbsolute(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--root", "."); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if got := calls()[0]; !strings.HasSuffix(got, "--root "+wd) {
		t.Errorf("registered %q, want --root %s", got, wd)
	}
}

// Run twice with the same arguments, the second says there is nothing to do and
// runs nothing: a provisioning script that registers on every boot is not
// answered with a failure, and not with a second registration.
func TestAnIdenticalRegistrationIsLeftAlone(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	claudeHas(t, scopeLocal, "mcp", "serve", "--as", "claude")

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if len(calls()) != 0 {
		t.Errorf("an identical registration ran the client: %v", calls())
	}
	pairs := answerPairs(t, out)
	if pairs["already registered"] != "Claude Code" || !strings.Contains(pairs["next"], "nothing to do") {
		t.Errorf("answered %v, want it to say there is nothing to do", pairs)
	}
}

// A different name, path or option is replaced through claude's own commands,
// and the answer says what changed. It used to answer "already registered" at
// exit 0 and keep what was there.
func TestADifferentRegistrationReplacesTheOldOne(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	claudeHas(t, scopeLocal, "mcp", "serve", "--as", "claude")

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "--as", "claude-work", "--consent", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	got := calls()
	if len(got) != 2 || got[0] != "mcp remove rta --scope local" || !strings.Contains(got[1], "mcp add rta -- ") ||
		!strings.HasSuffix(got[1], "mcp serve --as claude-work --consent") {
		t.Fatalf("claude was run as %v, want the old one taken out and the new one added", got)
	}
	pairs := answerPairs(t, out)
	if pairs["updated"] != "Claude Code" {
		t.Errorf("answered %v, want it to say it updated the registration", pairs)
	}
	if !strings.Contains(pairs["changed"], "name claude → claude-work") ||
		!strings.Contains(pairs["changed"], "options none → --consent") {
		t.Errorf("changed = %q, want the name and the options", pairs["changed"])
	}
}

// A replacement that fails halfway leaves nothing registered, which is worse
// than either registration: the old one is put back.
func TestAReplacementThatFailsPutsTheOldRegistrationBack(t *testing.T) {
	calls := loggingClient(t, "claude", `case "$*" in *claude-work*) echo refused >&2; exit 1;; esac`)
	claudeHas(t, scopeLocal, "mcp", "serve", "--as", "claude")

	_, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--as", "claude-work")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.mcp.install.add" || !strings.Contains(ve.Hint, "back as it was") {
		t.Fatalf("err = %#v, want core.mcp.install.add saying the old registration is back", err)
	}
	got := calls()
	if len(got) != 3 || got[0] != "mcp remove rta --scope local" || !strings.HasSuffix(got[2], "mcp serve --as claude") {
		t.Errorf("claude was run as %v, want remove, the refused add, and the old one added back", got)
	}
}

// --global registers for every project, and a directory-only registration left
// behind overrides it in that directory, with the old name and options: it is
// taken out after the new one is in.
func TestGlobalTakesOutTheDirectoryOnlyRegistrationItWouldBeShadowedBy(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	claudeHas(t, scopeLocal, "mcp", "serve", "--as", "claude", "--consent")

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "--global", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	got := calls()
	if len(got) != 2 || !strings.HasPrefix(got[0], "mcp add rta --scope user -- ") || got[1] != "mcp remove rta --scope local" {
		t.Fatalf("claude was run as %v, want the user-level add and then the local removal", got)
	}
	removed := answerPairs(t, out)["removed"]
	if !strings.Contains(removed, "directory-only registration") || !strings.Contains(removed, "--consent") {
		t.Errorf("removed = %q, want it to say what went and that it carried --consent", removed)
	}
}

// A registration under another name is the operator's own and is left alone.
func TestARegistrationUnderAnotherNameIsNotTouched(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	wd, _ := os.Getwd()
	self, _ := registeredBinary()
	doc := map[string]any{"projects": map[string]any{wd: map[string]any{"mcpServers": map[string]any{
		"mine": map[string]any{"command": self, "args": []string{"mcp", "serve", "--as", "mine"}}}}}}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--global"); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls() {
		if strings.Contains(c, "remove") {
			t.Errorf("a registration the operator named %q was removed: %v", "mine", calls())
		}
	}
}

// The name rta registers under is the operator's to have used for something
// else. A server of theirs that starts nothing of rta's, under that name, is not
// one `install` made, and taking it out to put rta in its place would delete a
// thing rta did not write: the client refuses, as it always did, and the answer
// carries the line that takes it out by hand.
func TestAServerThatIsNotRtaUnderRtasNameIsNotReplaced(t *testing.T) {
	calls := loggingClient(t, "claude", `echo "MCP server rta already exists in local config" >&2; exit 1`)
	wd, _ := os.Getwd()
	doc := map[string]any{"projects": map[string]any{wd: map[string]any{"mcpServers": map[string]any{
		"rta": map[string]any{"command": "/usr/bin/true", "args": []string{"--stdio"}}}}}}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, testRegistry(t), "mcp", "install", "claude")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.mcp.install.exists" || !strings.Contains(ve.Hint, "claude mcp remove rta") {
		t.Fatalf("err = %#v, want core.mcp.install.exists carrying the remove line", err)
	}
	for _, c := range calls() {
		if strings.Contains(c, "remove") {
			t.Errorf("a server that is not rta's was removed: %v", calls())
		}
	}
}

// claudeHasWithEnv is claudeHas for an entry the operator added variables to:
// `claude mcp add -e`, or an edit of ~/.claude.json, either of which rta
// registers none of.
func claudeHasWithEnv(t *testing.T, scope claudeScope, env map[string]any, args ...string) {
	t.Helper()
	wd, _ := os.Getwd()
	self, err := registeredBinary()
	if err != nil {
		t.Fatal(err)
	}
	entry := map[string]any{"type": "stdio", "command": self, "args": args, "env": env}
	servers := map[string]any{"rta": entry}
	doc := map[string]any{}
	if scope == scopeUser {
		doc["mcpServers"] = servers
	} else {
		doc["projects"] = map[string]any{wd: map[string]any{"mcpServers": servers}}
	}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".claude.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Replacing a registration is the client's remove and then an add of what rta
// passes it, so what the operator put on the old entry beyond that does not come
// back. One of those variables is usually where the server keeps its data, and
// a server that quietly starts looking somewhere else is the "registered and no
// traffic" report again. It is refused, naming the variables and never their
// values, with the line that takes the entry out; the same registration is
// still "nothing to do", variables or not.
func TestAReplacementThatWouldDropTheOperatorsVariablesIsRefused(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	claudeHasWithEnv(t, scopeLocal, map[string]any{"XDG_DATA_HOME": "/custom/data", "TOKEN": "hunter2"},
		"mcp", "serve", "--as", "claude")

	_, _, err := run(t, testRegistry(t), "mcp", "install", "claude", "--consent")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.mcp.install.env" {
		t.Fatalf("err = %#v, want core.mcp.install.env", err)
	}
	if !strings.Contains(ve.Message, "TOKEN, XDG_DATA_HOME") || !strings.Contains(ve.Hint, "claude mcp remove rta --scope local") {
		t.Errorf("the refusal names %q and hints %q, want the variables and the remove line", ve.Message, ve.Hint)
	}
	if strings.Contains(ve.Message+ve.Hint, "hunter2") || strings.Contains(ve.Message+ve.Hint, "/custom/data") {
		t.Errorf("the refusal printed a value: %q %q", ve.Message, ve.Hint)
	}
	if len(calls()) != 0 {
		t.Errorf("claude was run despite the refusal: %v", calls())
	}

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if answerPairs(t, out)["already registered"] != "Claude Code" || len(calls()) != 0 {
		t.Errorf("the same registration with variables on it was not left alone: %s %v", out, calls())
	}
}

// --global takes out the directory-only entry that would override it, and for
// the same reason a replacement is refused it leaves one that sets variables
// where it is: the answer says which, by name, and what takes it out.
func TestGlobalLeavesADirectoryOnlyEntryThatSetsVariablesInPlace(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	claudeHasWithEnv(t, scopeLocal, map[string]any{"XDG_DATA_HOME": "/custom/data"}, "mcp", "serve", "--as", "claude")

	out, errOut, err := run(t, testRegistry(t), "mcp", "install", "claude", "--global", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	got := calls()
	if len(got) != 1 || !strings.HasPrefix(got[0], "mcp add rta --scope user -- ") {
		t.Errorf("claude was run as %v, want the user-level add and no removal", got)
	}
	pairs := answerPairs(t, out)
	kept := pairs["kept"]
	if !strings.Contains(kept, "XDG_DATA_HOME") || strings.Contains(kept, "/custom/data") ||
		!strings.Contains(kept, "claude mcp remove rta --scope local") {
		t.Errorf("kept = %q, want the variable's name, not its value, and the line that takes it out", kept)
	}
	if pairs["removed"] != "" {
		t.Errorf("removed = %q, want nothing taken out", pairs["removed"])
	}
}

// Every other client that keeps a file rta can read says "nothing to do" for
// the registration that is already in it, the way Claude Code does.
func TestAnIdenticalRegistrationInAClientsFileIsLeftAlone(t *testing.T) {
	calls := loggingClient(t, "code", "exit 0")
	self, _ := registeredBinary()
	file := filepath.Join(os.Getenv("HOME"), ".config", "Code", "User", "mcp.json")
	if runtime.GOOS == "darwin" {
		file = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Code", "User", "mcp.json")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	// JSONC, as VS Code's own file is when the operator has annotated it.
	body := "{\n  // added by hand\n  \"servers\": {\n    \"rta\": {\"command\": " + jsonString(self) +
		", \"args\": [\"mcp\", \"serve\", \"--as\", \"vscode\"],},\n  },\n}\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, testRegistry(t), "mcp", "install", "vscode", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls()) != 0 {
		t.Errorf("an identical registration ran code: %v", calls())
	}
	if answerPairs(t, out)["already registered"] != "VS Code" {
		t.Errorf("answered %q", out)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The path a client is told to launch survives an upgrade: the link the
// operator ran, when it is on PATH and is this very file, and the file itself
// otherwise.
func TestTheRegisteredPathIsTheLinkOnPathWhenItIsTheSameFile(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "cellar", "0.34.0", "rta")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "rta")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin)
	if got := stableBinary("rta", real); got != link {
		t.Errorf("stableBinary = %q, want the link on PATH, %q", got, link)
	}
	if got := stableBinary(link, real); got != link {
		t.Errorf("run by its full path, stableBinary = %q, want %q", got, link)
	}

	// Not on PATH: nothing vouches for the link, so the file it resolves to.
	t.Setenv("PATH", t.TempDir())
	if got := stableBinary("rta", real); got != real {
		t.Errorf("off PATH, stableBinary = %q, want the resolved file %q", got, real)
	}

	// A different rta first on PATH is not this one.
	other := filepath.Join(t.TempDir(), "rta")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(other))
	if got := stableBinary("rta", real); got != real {
		t.Errorf("another rta on PATH was registered: %q", got)
	}
}
