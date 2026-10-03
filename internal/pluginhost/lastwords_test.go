package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// saying is a launch's stderr as go-plugin writes it to lastWords: each line
// as it came, and its end in a call of its own.
func saying(t *testing.T) (*lastWords, func(lines ...string)) {
	t.Helper()
	words := &lastWords{}
	return words, func(lines ...string) {
		for _, line := range lines {
			_, _ = words.Write([]byte(line))
			_, _ = words.Write([]byte("\n"))
		}
	}
}

// What a plugin says at error is kept for rta's own word on its failure: its
// last lines, after the status the process exited with, and nothing of what
// it says below error.
func TestAPluginsLastWordsAreKeptForRtasOwnReport(t *testing.T) {
	words, say := saying(t)
	if got := words.told(""); got != "" {
		t.Errorf("a plugin that said nothing told %q", got)
	}
	say("[ERROR] one", `{"@level":"error","@message":"two","@timestamp":"2026-09-29T10:00:00.000000Z"}`,
		"[ERROR] three", "[DEBUG] chatter", `{"@level":"info","@message":"chatter"}`, "chatter",
		"[ERROR] four", `{"@level":"ERROR","@message":"five"}`)
	want := "\nthe plugin exited: exit status 1" +
		"\nthe plugin wrote: two\n  [ERROR] three\n  [ERROR] four\n  five"
	if got := words.told("exit status 1"); got != want {
		t.Errorf("told %q, want %q", got, want)
	}
	var none *lastWords
	if got := none.told("signal: killed"); got != "\nthe plugin exited: signal: killed" {
		t.Errorf("no words told %q", got)
	}
}

// A panic is kept from its first line, which says what went wrong, and not
// from the end of its trace; a line that writes without end costs a bounded
// piece of itself, in memory as in what is told.
func TestAPanicIsKeptFromItsFirstLine(t *testing.T) {
	words, say := saying(t)
	say("[ERROR] earlier", "panic: boom", "", "goroutine 1 [running]:", "main.main()",
		"\t/src/main.go:9 +0x1d", "exit status 2")
	want := "\nthe plugin wrote: panic: boom\n  goroutine 1 [running]:\n  main.main()\n  \t/src/main.go:9 +0x1d"
	if got := words.told(""); got != want {
		t.Errorf("told %q, want %q", got, want)
	}

	long, _ := saying(t)
	for range 3 * lineLimit / 1024 {
		_, _ = long.Write([]byte("[ERROR] " + strings.Repeat("x", 1016)))
	}
	if len(long.line) > lineLimit {
		t.Errorf("a line without end is held at %d bytes", len(long.line))
	}
	_, _ = long.Write([]byte("\n[ERROR] next\n"))
	if got := long.told(""); len(got) > 2*wordLength+len("\nthe plugin wrote: \n  ") ||
		!strings.HasSuffix(got, "\n  [ERROR] next") {
		t.Errorf("a long line was kept as %d bytes: %.80q", len(got), got)
	}
}

// A line kept cut says it was cut. The refusal a plugin gives for a declaration
// the host will not load is one long sentence, and kept to wordLength bytes it
// ended in the middle of itself — "…at a machine it" — as though that were all
// it had said.
func TestALineKeptCutSaysSo(t *testing.T) {
	words, say := saying(t)
	say("[ERROR] "+strings.Repeat("word ", 100), "[ERROR] short")
	got := words.told("")
	lines := strings.Split(got, "\n  ")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "…") || strings.HasSuffix(lines[1], "…") {
		t.Errorf("told %q: want the long line marked cut and the short one left alone", got)
	}
}

// What a plugin writes to its stderr is read by nothing that panics on it.
// go-plugin parsed every line as a JSON log entry and asserted its "@message",
// "@level" and "@timestamp" were strings: a plugin that wrote {"@message":1}
// took the goroutine reading it down, and rta with it — every plugin it ran,
// an MCP server's included.
func TestAPluginsStderrIsReadByNothingThatPanicsOnIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is no plugin binary on Windows")
	}
	lines := []string{
		`{"@message":1,"@level":"error"}`,
		`{"@message":"m","@level":1}`,
		`{"@message":"m","@level":"error","@timestamp":1}`,
		`{"@message":{"a":1},"@level":"error"}`,
		"[ERROR] still read after them",
	}
	p := filepath.Join(t.TempDir(), "rta-plugin-shaped")
	script := "#!/bin/sh\n"
	for _, line := range lines {
		script += "echo '" + line + "' >&2\n"
	}
	if err := os.WriteFile(p, []byte(script+"exit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New()
	_, err := h.Open(context.Background(), p)
	h.CloseAll()
	if err == nil {
		t.Fatal("a script was accepted as a plugin")
	}
	// Before its handshake, so what is said is its last lines as written;
	// what it said at error, read from the same lines, is what a process
	// that ran is judged by.
	for _, want := range []string{"exited before its handshake: exit status 4", "\n  [ERROR] still read after them"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the launch's error does not say %q: %v", want, err)
		}
	}
	words, say := saying(t)
	say(lines...)
	if got, want := words.told(""), "\nthe plugin wrote: m\n  [ERROR] still read after them"; got != want {
		t.Errorf("what it said at error was read as %q, want %q", got, want)
	}
}

// A call whose process stopped under it ends in plugin.gone, with how the
// process exited and what it said on its way out: its stderr reaches no
// terminal, so this is the one place its author reads why.
func TestAPluginThatStopsUnderACallSaysHowAndWhy(t *testing.T) {
	_, c := open(t)
	// Started: a declaration the cache held opens with no process yet.
	if _, err := c.live(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = c.words.Write([]byte("[ERROR] dying words\n"))
	_ = c.cmd.Process.Kill()
	for deadline := time.Now().Add(5 * time.Second); !c.client.Exited() && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	verr := c.transportError(context.Background(), "hello.greet", errors.New("connection reset"))
	if verr.Code != "plugin.gone" {
		t.Fatalf("the call ended in %s: %s", verr.Code, verr.Message)
	}
	for _, want := range []string{"\nthe plugin exited: ", "\nthe plugin wrote: [ERROR] dying words"} {
		if !strings.Contains(verr.Message, want) {
			t.Errorf("plugin.gone does not say %q: %s", want, verr.Message)
		}
	}
}

// A call that races its plugin's death is reported as that death, whichever
// comes first: the connection closing under the call, or go-plugin hearing
// that the process has exited. The call that read the closed connection first
// was said to have failed in talking to a plugin, which names no exit, none of
// what the plugin said and no restart, about one in eight of the calls made
// just after a kill. Each is either served by the restart a process
// go-plugin had already heard of gets, or ends in plugin.gone.
func TestACallThatRacesAPluginsDeathIsReportedAsItsDeath(t *testing.T) {
	_, c := open(t)
	for i := range 30 {
		var up error
		for range 50 {
			if _, up = greetWith(t, c, "x"); up == nil {
				break
			}
		}
		if up != nil {
			t.Fatalf("run %d: the plugin did not come back up: %v", i, up)
		}
		c.mu.Lock()
		_ = c.cmd.Process.Kill()
		c.mu.Unlock()

		_, err := greetWith(t, c, "y")
		if err == nil {
			continue
		}
		var verr *view.Error
		if !errors.As(err, &verr) || verr.Code != "plugin.gone" {
			t.Fatalf("run %d: a call just after a kill ended in %v, want plugin.gone", i, err)
		}
		if !strings.Contains(verr.Message, "\nthe plugin exited: ") {
			t.Errorf("run %d: plugin.gone does not say how the plugin exited: %s", i, verr.Message)
		}
	}
}

// launchChild names the plugin the child process of
// TestAPluginThatFailsToStartWritesNothingToTheTerminal launches.
const launchChild = "RTA_PLUGINHOST_TEST_LAUNCH"

// A plugin that fails to start writes nothing to rta's standard error: what
// it wrote and how it exited are in the error the launch returns, which every
// caller reports in rta's own words. go-plugin's logger wrote each as a raw
// line of JSON on the operator's terminal, beside rta's line about the same
// failure, before every command.
//
// The launch runs in a child process, and what is read is that process's own
// standard error, the file descriptor and not the variable: handed no logger,
// go-plugin writes to hclog's default output, which is the os.Stderr the
// process started with, so a test that swapped os.Stderr for a pipe passed
// with the logger taken out and every line on the terminal.
func TestAPluginThatFailsToStartWritesNothingToTheTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is no plugin binary on Windows")
	}
	if p := os.Getenv(launchChild); p != "" {
		h := New()
		_, err := h.Open(context.Background(), p)
		h.CloseAll()
		fmt.Println(err)
		return
	}
	p := filepath.Join(t.TempDir(), "rta-plugin-broken")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '[ERROR] no config at /etc/x' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPluginThatFailsToStartWritesNothingToTheTerminal$",
		"-test.count=1")
	child.Env = append(os.Environ(), launchChild+"="+p)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		t.Fatalf("the launching process: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("the launch wrote to standard error: %q", stderr.String())
	}
	for _, want := range []string{"exited before its handshake: exit status 3", "the plugin wrote: [ERROR] no config at /etc/x"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the launch's error does not say %q: %s", want, stdout.String())
		}
	}
}

// A plugin's last lines are kept whatever they say, as it wrote them, beside
// what it said at error: the last few, a panic's first few.
func TestAPluginsLastLinesAreKeptWhateverTheySay(t *testing.T) {
	words, say := saying(t)
	if got := words.wrote(true); got != "" {
		t.Errorf("a plugin that said nothing wrote %q", got)
	}
	say("one", "[DEBUG] two", "", `{"@level":"info","@message":"three"}`, "[ERROR] four", "five  ")
	want := "\nthe plugin wrote: [DEBUG] two\n  {\"@level\":\"info\",\"@message\":\"three\"}\n  [ERROR] four\n  five"
	if got := words.wrote(true); got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
	if got := words.wrote(false); got != "\nthe plugin wrote: [ERROR] four" {
		t.Errorf("at error it wrote %q", got)
	}

	panicked, say := saying(t)
	say("starting", "panic: boom", "", "goroutine 1 [running]:", "main.main()", "\t/src/main.go:9", "more")
	if got, want := panicked.wrote(true), "\nthe plugin wrote: panic: boom\n  goroutine 1 [running]:\n  main.main()\n  \t/src/main.go:9"; got != want {
		t.Errorf("a panic wrote %q, want %q", got, want)
	}
}

// A plugin that exits before its handshake is said to have, naming it, with
// how it exited and the last lines it wrote at any level. go-plugin said it
// as "Failed to read any lines from plugin's stdout", its guesses at a cause
// and notes on the file it ran, which on macOS is /usr/bin/sandbox-exec, and
// what the plugin wrote below error was dropped. One that printed a line of
// its own where the handshake goes had that line said before the same
// guesses, and is said to have exited too, with the line.
//
// Or, for that one, to have printed it: go-plugin kills the process as it
// reads the line, and on a loaded runner that kill can land before the
// script's own exit — which of the two accounts a run gives is that race,
// and both are rta's, never go-plugin's guesses. Its stderr line is written
// before its stdout line, so the line is there to be said whichever wins.
//
// Each case is launched as rta launches it, under the sandbox on macOS, and
// unwrapped, as it runs everywhere else.
func TestAPluginThatExitsBeforeItsHandshakeIsSaidToHave(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is no plugin binary on Windows")
	}
	for _, tc := range []struct{ name, script, want, or string }{
		{"exits at once", "exit 1", " exited before its handshake: exit status 1", ""},
		{"prints, then exits", "echo 'no config at /etc/x' >&2\necho '[DEBUG] looked in /etc' >&2\nexit 3",
			" exited before its handshake: exit status 3" +
				"\nthe plugin wrote: no config at /etc/x\n  [DEBUG] looked in /etc", ""},
		{"prints on stdout, then exits", "echo 'unknown flag' >&2\necho 'usage: x [flags]'\nexit 2",
			" exited before its handshake: exit status 2" +
				"\nit printed in place of its handshake: usage: x [flags]\nthe plugin wrote: unknown flag",
			" printed in place of its handshake: usage: x [flags]\nthe plugin wrote: unknown flag"},
	} {
		p := filepath.Join(t.TempDir(), "rta-plugin-exits")
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		id, err := Identify(p)
		if err != nil {
			t.Fatal(err)
		}
		saidAbout := func(t *testing.T, err error) {
			t.Helper()
			if err == nil {
				t.Fatal("a script was accepted as a plugin")
			}
			got := err.Error()
			if got != id.Path+tc.want && (tc.or == "" || got != id.Path+tc.or) {
				t.Errorf("the launch said\n%s\nwant\n%s", got, id.Path+tc.want)
			}
		}
		t.Run(tc.name+", launched", func(t *testing.T) {
			h := New()
			defer h.CloseAll()
			_, err := h.Open(context.Background(), p)
			saidAbout(t, err)
		})
		t.Run(tc.name+", unwrapped", func(t *testing.T) {
			h := New()
			defer h.CloseAll()
			cmd := exec.Command(id.Path)
			cmd.Env = Environ()
			harden(cmd)
			_, err := h.start(context.Background(), id, DenySet{}, nil, cmd)
			saidAbout(t, err)
		})
	}
}

// A plugin that answers its handshake with something else and goes on
// running is said to have printed it, in rta's words, launched or not.
// go-plugin's notes on the file it ran named /usr/bin/sandbox-exec,
// root-owned, as if it were the plugin, and its guesses — the architecture,
// a library, the file's mode — were never why: a process that printed a line
// ran.
func TestAPluginThatSaysSomethingElseIsNotDescribedAsTheWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is no plugin binary on Windows")
	}
	p := filepath.Join(t.TempDir(), "rta-plugin-talks")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho not a handshake\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	saidAbout := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), id.Path+" printed in place of its handshake: not a handshake") {
			t.Fatalf("the launch said %v", err)
		}
		for _, guess := range []string{"sandbox-exec", "This usually means", "Additional notes"} {
			if strings.Contains(err.Error(), guess) {
				t.Errorf("the launch said %q, as go-plugin guessed: %v", guess, err)
			}
		}
	}
	t.Run("launched", func(t *testing.T) {
		h := New()
		defer h.CloseAll()
		_, err := h.Open(context.Background(), p)
		saidAbout(t, err)
	})
	t.Run("unwrapped", func(t *testing.T) {
		h := New()
		defer h.CloseAll()
		cmd := exec.Command(id.Path)
		cmd.Env = Environ()
		harden(cmd)
		_, err := h.start(context.Background(), id, DenySet{}, nil, cmd)
		saidAbout(t, err)
	})
}

// sandbox-exec's own line, when it cannot run the plugin, is what says why,
// and it has no level: a plugin whose interpreter is not there.
func TestTheSandboxsOwnWordOnAPluginItCannotRunIsKept(t *testing.T) {
	if err := available(); err != nil || !Confined() {
		t.Skip("no sandbox wrapper here")
	}
	p := filepath.Join(t.TempDir(), "rta-plugin-gone")
	if err := os.WriteFile(p, []byte("#!/nonexistent/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	h := New()
	defer h.CloseAll()
	_, err = h.launch(context.Background(), id, DenySet{}, nil)
	if err == nil {
		t.Fatal("a plugin that cannot run was launched")
	}
	for _, want := range []string{id.Path + " exited before its handshake: exit status 71",
		"\nthe plugin wrote: sandbox-exec: execvp() of '" + id.execPath() + "' failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the launch does not say %q: %v", want, err)
		}
	}
}

// A plugin that printed a line in place of its handshake and went on running
// did not exit before it: go-plugin kills it as it reports the line, and its
// SIGKILL is go-plugin's, not the plugin's way out. Said to have exited, it
// read as a plugin something outside had killed; it is said to have printed
// the line, which is all that is known, in rta's words and not go-plugin's
// guesses.
func TestAPluginKilledOverTheLineItPrintedIsNotSaidToHaveExited(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is no plugin binary on Windows")
	}
	p := filepath.Join(t.TempDir(), "rta-plugin-early")
	// exec, so the one process that printed is the one go-plugin kills and
	// no child holds its output open past it.
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'debug: starting'\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	saidAbout := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), id.Path+" printed in place of its handshake: debug: starting") {
			t.Fatalf("the launch said %v", err)
		}
		if strings.Contains(err.Error(), "exited before its handshake") {
			t.Errorf("a plugin go-plugin killed was said to have exited: %v", err)
		}
		if strings.Contains(err.Error(), "This usually means") {
			t.Errorf("go-plugin's guesses are back: %v", err)
		}
	}
	t.Run("launched", func(t *testing.T) {
		h := New()
		defer h.CloseAll()
		_, err := h.Open(context.Background(), p)
		saidAbout(t, err)
	})
	t.Run("unwrapped", func(t *testing.T) {
		h := New()
		defer h.CloseAll()
		cmd := exec.Command(id.Path)
		cmd.Env = Environ()
		harden(cmd)
		_, err := h.start(context.Background(), id, DenySet{}, nil, cmd)
		saidAbout(t, err)
	})
}
