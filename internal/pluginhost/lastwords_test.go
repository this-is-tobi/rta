package pluginhost

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// saying is a launch's logger as go-plugin uses it: its own entries under the
// logger's name, and a plugin's stderr lines under a name below it.
func saying(t *testing.T) (*lastWords, func(level, msg string, args ...any)) {
	t.Helper()
	words := &lastWords{module: "plugin.abc"}
	logger := pluginLogger(words.module, words)
	stderr := logger.Named("rta-plugin-x")
	return words, func(level, msg string, args ...any) {
		switch level {
		case "own":
			logger.Error(msg, args...)
		case "debug":
			stderr.Debug(msg, args...)
		default:
			stderr.Error(msg, args...)
		}
	}
}

// What a plugin says at error is kept for rta's own word on its failure: its
// last lines, the status go-plugin says it exited with before them, and
// nothing of go-plugin's own plumbing or of what it logs below error.
func TestAPluginsLastWordsAreKeptForRtasOwnReport(t *testing.T) {
	words, say := saying(t)
	if got := words.told(); got != "" {
		t.Errorf("a plugin that said nothing told %q", got)
	}
	for _, line := range []string{"[ERROR] one", "[ERROR] two", "[ERROR] three", "[ERROR] four", "[ERROR] five"} {
		say("stderr", line)
	}
	say("debug", "chatter")
	say("own", "reading plugin stderr", "error", "file already closed")
	say("own", "plugin process exited", "plugin", "/usr/bin/sandbox-exec", "id", 42, "error", "exit status 1")
	want := "\nthe plugin exited: exit status 1" +
		"\nthe plugin wrote: [ERROR] two\n  [ERROR] three\n  [ERROR] four\n  [ERROR] five"
	if got := words.told(); got != want {
		t.Errorf("told %q, want %q", got, want)
	}
	var none *lastWords
	if got := none.told(); got != "" {
		t.Errorf("no words told %q", got)
	}
}

// A panic is kept from its first line, which says what went wrong, and not
// from the end of its trace; a line that writes without end costs a bounded
// piece of itself.
func TestAPanicIsKeptFromItsFirstLine(t *testing.T) {
	words, say := saying(t)
	say("stderr", "[ERROR] earlier")
	for _, line := range []string{"panic: boom", "", "goroutine 1 [running]:", "main.main()",
		"\t/src/main.go:9 +0x1d", "exit status 2"} {
		say("stderr", line)
	}
	want := "\nthe plugin wrote: panic: boom\n  goroutine 1 [running]:\n  main.main()\n  \t/src/main.go:9 +0x1d"
	if got := words.told(); got != want {
		t.Errorf("told %q, want %q", got, want)
	}

	long, say := saying(t)
	say("stderr", "[ERROR] "+strings.Repeat("x", 10*wordLength))
	if got := long.told(); len(got) > wordLength+len("\nthe plugin wrote: ") {
		t.Errorf("a long line was kept at %d bytes", len(got))
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
