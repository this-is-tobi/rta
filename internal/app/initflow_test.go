package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/view"
)

// initMachine is a machine for `rta init` to look at: a home with nothing in it,
// a PATH that holds only the clients named — each a script that logs its argv
// and then runs the body given — no shell, and no way to reach anything real.
// The PATH is replaced and not extended: a real `code` or `claude` on the
// machine running the suite would otherwise be offered, and run, under --yes.
func initMachine(t *testing.T, clients map[string]string) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	for bin, body := range clients {
		script := "#!/bin/sh\necho \"" + bin + " $*\" >> " + log + "\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "")
	return func() []string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

// attachedIndex says a plugin index is attached, so that init has no plugin
// command to tell anybody about.
func attachedIndex(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(paths.Indexes(), "official"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// answers stands in for the person at the terminal: asked about the offers, it
// says what each was answered, and records what it was asked.
func answers(t *testing.T, yes ...bool) (asked *[]string) {
	t.Helper()
	savedAsk, savedTerminal := askInit, initTerminal
	t.Cleanup(func() { askInit, initTerminal = savedAsk, savedTerminal })
	initTerminal = func() (io.Reader, io.Writer, func(), error) {
		return strings.NewReader(""), io.Discard, func() {}, nil
	}
	asked = new([]string)
	askInit = func(_ context.Context, offers []initOffer, _ io.Reader, _ io.Writer) ([]bool, error) {
		out := make([]bool, len(offers))
		for i, o := range offers {
			*asked = append(*asked, o.question()+" "+o.explain())
			out[i] = i < len(yes) && yes[i]
		}
		return out, nil
	}
	return asked
}

func neverAsked(t *testing.T) {
	t.Helper()
	saved := askInit
	t.Cleanup(func() { askInit = saved })
	askInit = func(context.Context, []initOffer, io.Reader, io.Writer) ([]bool, error) {
		t.Error("init asked a question it should not have")
		return nil, nil
	}
}

func writesNoConfig(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(config.Path()); !os.IsNotExist(err) {
		t.Errorf("init wrote %s: %v", config.Path(), err)
	}
}

// On a machine with nothing to set up, init says that is the good outcome and
// writes nothing. It used to write a file holding `{}`.
func TestInitSaysWhenThereIsNothingToDoAndWritesNothing(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, nil)
	attachedIndex(t)
	neverAsked(t)

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if !strings.Contains(pairs["unchanged"], "configured as well as it can be without your choices") {
		t.Errorf("answered %v", pairs)
	}
	writesNoConfig(t)
}

// Nothing to ask means nothing needs a terminal: a dotfiles script or a
// devcontainer runs it, and gets the notes.
func TestInitNeedsNoTerminalWhenThereIsNothingToAsk(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, nil)
	noTerminal(t)
	if _, errOut, err := run("init"); err != nil {
		t.Errorf("init with nothing to ask and no terminal: %v %q", err, errOut)
	}
}

// Each client found is offered with the command that would register it, and
// --dry-run runs none of them.
func TestInitOffersTheClientsItFindsAsTheCommandsItWouldRun(t *testing.T) {
	run := session(t, testRegistry(t))
	calls := initMachine(t, map[string]string{"claude": "exit 0", "code": "exit 0"})
	attachedIndex(t)

	out, errOut, err := run("init", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if len(calls()) != 0 {
		t.Errorf("--dry-run ran %q", calls())
	}
	var offered []string
	for _, p := range answerPairsList(t, out) {
		if p.Key == "would register" {
			offered = append(offered, p.Value)
		}
	}
	if len(offered) != 2 {
		t.Fatalf("offered %q, want Claude Code and VS Code", offered)
	}
	if !strings.Contains(offered[0], "Claude Code, every project") ||
		!strings.Contains(offered[0], "claude mcp add rta --scope user -- ") ||
		!strings.Contains(offered[0], "mcp serve --as claude") {
		t.Errorf("Claude Code is offered as %q, want every project and the command shown", offered[0])
	}
	if !strings.Contains(offered[1], "code --add-mcp") {
		t.Errorf("VS Code is offered as %q", offered[1])
	}
	writesNoConfig(t)
}

// Registering waits for every answer, and runs only what was said yes to.
func TestInitRunsOnlyWhatWasAnswered(t *testing.T) {
	run := session(t, testRegistry(t))
	calls := initMachine(t, map[string]string{"claude": "exit 0", "code": "exit 0"})
	attachedIndex(t)
	asked := answers(t, true, false)

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if len(*asked) != 2 || !strings.Contains((*asked)[0], "Register rta with Claude Code?") ||
		!strings.Contains((*asked)[0], "$ ") || !strings.Contains((*asked)[0], "--scope user") {
		t.Errorf("asked %q, want a question per client showing where and the command", *asked)
	}
	got := calls()
	if len(got) != 1 || !strings.HasPrefix(got[0], "claude mcp add rta --scope user -- ") {
		t.Errorf("ran %q, want only the registration answered yes", got)
	}
	var registered, skipped, next string
	for _, p := range answerPairsList(t, out) {
		switch p.Key {
		case "registered":
			registered = p.Value
		case "skipped":
			skipped = p.Value
		case "next":
			next = p.Value
		}
	}
	if !strings.Contains(registered, "Claude Code") || !strings.Contains(skipped, "rta mcp install vscode") ||
		!strings.Contains(next, "restart Claude Code, ask it to call sys_overview") {
		t.Errorf("answered registered=%q skipped=%q next=%q", registered, skipped, next)
	}
	writesNoConfig(t)
}

// --yes answers every question yes and needs no terminal, which is what a
// dotfiles script and a devcontainer have.
func TestInitYesRegistersWhatItListsWithoutAsking(t *testing.T) {
	run := session(t, testRegistry(t))
	calls := initMachine(t, map[string]string{"claude": "exit 0", "code": "exit 0"})
	attachedIndex(t)
	noTerminal(t)
	neverAsked(t)

	out, errOut, err := run("init", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	got := calls()
	if len(got) != 2 || !strings.HasPrefix(got[0], "claude mcp add rta --scope user -- ") ||
		!strings.HasPrefix(got[1], "code --add-mcp ") {
		t.Errorf("ran %q", got)
	}
	if next := answerPairs(t, out)["next"]; !strings.Contains(next, "Claude Code and VS Code, ask one of them") {
		t.Errorf("next = %q, want both clients and one question about which to ask", next)
	}
}

// Without a terminal and without --yes there is nobody to ask, and the answer
// is the one every confirmation gives: exit 3, and the flag that answers it.
func TestInitWithOffersAndNoTerminalWantsConfirmation(t *testing.T) {
	run := session(t, testRegistry(t))
	calls := initMachine(t, map[string]string{"claude": "exit 0"})
	attachedIndex(t)
	noTerminal(t)

	_, _, err := run("init", "-o", "json")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired || !strings.Contains(ve.Hint, "--yes") {
		t.Fatalf("err = %#v, want %s naming --yes", err, CodeConfirmRequired)
	}
	if ExitCode(err) != 3 {
		t.Errorf("exit code = %d, want 3", ExitCode(err))
	}
	if len(calls()) != 0 {
		t.Errorf("ran %q with nobody to ask", calls())
	}
}

// Closing the form is leaving, with nothing done; ctrl-c halfway never leaves
// a client registered that the person had not got to.
func TestInitClosedHalfwayChangesNothing(t *testing.T) {
	run := session(t, testRegistry(t))
	calls := initMachine(t, map[string]string{"claude": "exit 0"})
	attachedIndex(t)
	answers(t)
	askInit = func(context.Context, []initOffer, io.Reader, io.Writer) ([]bool, error) {
		return nil, huh.ErrUserAborted
	}

	_, _, err := run("init")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.init.aborted" {
		t.Fatalf("err = %v, want core.init.aborted", err)
	}
	if len(calls()) != 0 {
		t.Errorf("ran %q after the form was closed", calls())
	}
}

// A registration that is already there is left as it is, in any scope and with
// any options: init puts nothing over a choice made on purpose.
func TestInitLeavesAnExistingRegistrationAlone(t *testing.T) {
	run := session(t, testRegistry(t))
	calls := initMachine(t, map[string]string{"claude": "exit 0"})
	attachedIndex(t)
	claudeHas(t, scopeUser, "mcp", "serve", "--as", "claude", "--consent")
	neverAsked(t)

	out, errOut, err := run("init", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if len(calls()) != 0 {
		t.Errorf("ran %q over a registration that was there", calls())
	}
	pairs := answerPairs(t, out)
	if pairs["already registered"] != "Claude Code" || pairs["unchanged"] == "" {
		t.Errorf("answered %v", pairs)
	}
}

// A refusal from the client's own command is reported, with the others still
// done, and is the exit status.
func TestInitReportsARegistrationTheClientRefusedAndExitsOne(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, map[string]string{"claude": "echo no >&2; exit 1"})
	attachedIndex(t)

	out, _, err := run("init", "--yes", "-o", "json")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.init.failed" || !strings.Contains(ve.Message, "claude") {
		t.Fatalf("err = %#v, want core.init.failed naming claude", err)
	}
	if got := answerPairs(t, out)["failed"]; !strings.Contains(got, "Claude Code") ||
		!strings.Contains(got, "rta mcp install claude --show") {
		t.Errorf("failed = %q, want the client and the way to add it by hand", got)
	}
}

// A client rta can run nothing for is a line saying so, not a question.
func TestInitNamesAClientItHasNoCommandFor(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, nil)
	attachedIndex(t)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	neverAsked(t)

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := answerPairs(t, out)["cursor"]; !strings.Contains(got, "rta mcp install cursor") ||
		!strings.Contains(got, "~/.cursor/mcp.json") {
		t.Errorf("cursor = %q", got)
	}
}

// A machine with none of the clients rta knows is told so, with the one command
// that serves any other client: a person on Windsurf or Zed was otherwise told
// rta was configured as well as it could be and never that their client had
// not been looked at.
func TestInitSaysWhenNoClientIsOnTheMachine(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, nil)
	attachedIndex(t)
	neverAsked(t)

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := answerPairs(t, out)["clients"]; !strings.Contains(got, "rta mcp install <name>") {
		t.Errorf("clients = %q, want the command that serves a client rta does not know", got)
	}
}

func TestInitSaysNothingAboutMissingClientsWhenOneIsHere(t *testing.T) {
	run := session(t, testRegistry(t))
	initMachine(t, nil)
	attachedIndex(t)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	neverAsked(t)

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got, ok := answerPairs(t, out)["clients"]; ok {
		t.Errorf("clients = %q on a machine with Cursor", got)
	}
}
