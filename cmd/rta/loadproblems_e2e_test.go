//go:build !windows

package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What rta says at startup about the plugins it could not load, through the
// real binary: main is what prints it, before any command runs.

// installed puts an executable named rta-plugin-<name> running each script in
// a directory of its own, trusts it, and returns the environment a run that
// finds it needs. A script and not a plugin, because what these tests read is
// what is said about a launch that fails, so nothing has to answer a
// handshake — and nothing has to be compiled.
func installed(t *testing.T, scripts map[string]string) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	bin := t.TempDir()
	for name, script := range scripts {
		path := filepath.Join(bin, "rta-plugin-"+name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	data := t.TempDir()
	env := append(os.Environ(),
		"RTA_DATA_DIR="+data,
		"RTA_CONFIG="+filepath.Join(data, "config.yaml"),
		"NO_COLOR=1",
		"COLUMNS=",
		// Nothing but these on PATH, so a plugin installed on the machine
		// running the suite is not discovered beside them.
		"PATH="+bin+":/usr/bin:/bin",
	)
	for name := range scripts {
		trust := exec.Command(binary, "plugin", "trust", name, "--yes")
		trust.Env = env
		if out, err := trust.CombinedOutput(); err != nil {
			t.Fatalf("trusting %s: %v\n%s", name, err, out)
		}
	}
	return env
}

// runIn is run with the environment given.
func runIn(t *testing.T, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	var out, errBuf strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	return result{stdout: out.String(), stderr: errBuf.String(), code: code}
}

// What a plugin wrote reaches the terminal as text, never as something the
// terminal acts on. go-plugin quotes the first line a plugin writes that is
// not its handshake in the error it returns, and a load problem was printed
// as it came, so a trusted plugin's OSC reached the operator's terminal —
// the window title here, the clipboard with OSC 52 — before every command.
func TestALoadProblemReachesTheTerminalAsText(t *testing.T) {
	env := installed(t, map[string]string{
		"evil": `printf '\033]0;PWNED\007 not a handshake\n'`,
	})
	r := runIn(t, env, "--version")
	if r.code != 0 {
		t.Fatalf("exit %d, want 0: a plugin that fails to load costs only itself (stderr %q)",
			r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "plugin evil") || !strings.Contains(r.stderr, "not a handshake") {
		t.Fatalf("stderr does not report the plugin and what it wrote: %q", r.stderr)
	}
	for _, c := range []rune{0x1b, 0x07} {
		if strings.ContainsRune(r.stderr, c) {
			t.Errorf("stderr holds %U, which a terminal acts on: %q", c, r.stderr)
		}
	}
}

// A refusal every plugin meets alike is said once, and with its hint. The
// TMPDIR refusals are the machine's and not any plugin's, and on a cold
// declaration cache, which is the first run after an install, every plugin
// met them at load: each was printed as rta: and its message, without the
// hint that says what to change, as many times as there were plugins.
func TestARefusalEveryPluginMeetsIsSaidOnceWithItsHint(t *testing.T) {
	env := installed(t, map[string]string{"one": "exit 1", "two": "exit 2"})
	// Too long for any socket's path, and there, so nothing fails on
	// TMPDIR before a plugin's launch does.
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 110))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	r := runIn(t, append(env, "TMPDIR="+long), "--version")
	if r.code != 0 {
		t.Fatalf("exit %d, want 0 (stderr %q)", r.code, r.stderr)
	}
	if n := strings.Count(r.stderr, "TMPDIR is too long"); n != 1 {
		t.Errorf("the refusal was said %d times, want once: %q", n, r.stderr)
	}
	for _, want := range []string{"plugin.tmpdir.toolong", "plugins one, two:", "HINT", "shorter directory"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not say %q: %q", want, r.stderr)
		}
	}
}
