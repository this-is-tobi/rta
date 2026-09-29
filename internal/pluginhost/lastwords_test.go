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

// What a plugin writes to its stderr is read by nothing that panics on it.
// go-plugin parsed every line as a JSON log entry and asserted its "@message",
// "@level" and "@timestamp" were strings: a plugin that wrote {"@message":1}
// took the goroutine reading it down, and rta with it — every plugin it ran,
// an MCP server's included.
func TestAPluginsStderrIsReadByNothingThatPanicsOnIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script is no plugin binary on Windows")
	}
	p := filepath.Join(t.TempDir(), "rta-plugin-shaped")
	script := "#!/bin/sh\n" +
		`echo '{"@message":1,"@level":"error"}' >&2` + "\n" +
		`echo '{"@message":"m","@level":1}' >&2` + "\n" +
		`echo '{"@message":"m","@level":"error","@timestamp":1}' >&2` + "\n" +
		`echo '{"@message":{"a":1},"@level":"error"}' >&2` + "\n" +
		"echo '[ERROR] still read after them' >&2\nexit 4\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New()
	_, err := h.Open(context.Background(), p)
	h.CloseAll()
	if err == nil {
		t.Fatal("a script was accepted as a plugin")
	}
	for _, want := range []string{"the plugin exited: exit status 4", "the plugin wrote: m\n  [ERROR] still read after them"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the launch's error does not say %q: %v", want, err)
		}
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
	for _, want := range []string{"the plugin exited: exit status 3", "the plugin wrote: [ERROR] no config at /etc/x"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the launch's error does not say %q: %s", want, stdout.String())
		}
	}
}
