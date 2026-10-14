//go:build linux || darwin

package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// What `rta plugin dev <dir> -- <command>` does with the command it runs,
// through the real binary and a real plugin: the command runs in a root of
// its own, nested in plugin dev's, and what reaches it from outside — a
// signal, the format its refusal is drawn in — is decided in main.

// scaffolded is a plugin `rta plugin new` wrote, building against this
// checkout, and the environment to run plugin dev on it in.
func scaffolded(t *testing.T) (string, []string) {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	env := append(os.Environ(),
		"RTA_DATA_DIR="+data,
		"RTA_CONFIG="+filepath.Join(data, "config.yaml"),
		"NO_COLOR=1",
		// The scaffold is a new module whose go.sum starts empty; its tidy
		// resolves from the module cache rather than asking the checksum
		// database for every entry (internal/app's goFromCache says more).
		"GOSUMDB=off",
	)
	src := filepath.Join(t.TempDir(), "rta-plugin-probe")
	scaffold := exec.Command(binary, "plugin", "new", "probe", "--dir", src, "--rta-source", root)
	scaffold.Env = env
	if out, err := scaffold.CombinedOutput(); err != nil {
		t.Fatalf("plugin new: %v\n%s", err, out)
	}
	return src, env
}

// `rta plugin dev <dir> -- mcp serve` stops on SIGTERM as `rta mcp serve`
// does. The command after `--` ran in a nested root executed without the
// outer command's context, which is the one a signal cancels, and mcp serve
// owns its shutdown, so no grace cut it short either: it went on serving
// until its standard input closed, and only a second signal ended it.
func TestPluginDevStopsTheCommandItRunsOnSIGTERM(t *testing.T) {
	src, env := scaffolded(t)
	cmd := exec.Command(binary, "plugin", "dev", src, "--", "mcp", "serve", "--as", "probe")
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Compiling the plugin comes first, and with a cold build cache that is
	// the slow part of this test by far.
	deadline := time.Now().Add(5 * time.Minute)
	for !strings.Contains(stderr.String(), "listening") {
		select {
		case err := <-done:
			t.Fatalf("plugin dev ended (%v) before the server listened: %q", err, stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never said it was listening: %q", stderr.String())
		}
	}
	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		took := time.Since(sent)
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != 0 || took > 2*time.Second {
			t.Errorf("exit %d %s after SIGTERM, want 0 at once (stderr %q)", code, took, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("still serving 30s after SIGTERM (stderr %q)", stderr.String())
	}
}

// A refusal from the command after `--` is drawn in the format that command
// asked for. plugin dev handed it back to main unrendered, and main drew it
// with plugin dev's own options, which a flag after `--` never reaches, so
// a script running a plugin's command with -o json got a pretty ERROR line
// where `rta <command> -o json` gives json.
func TestPluginDevDrawsTheRefusalOfTheCommandItRunsInThatCommandsFormat(t *testing.T) {
	src, env := scaffolded(t)
	for _, args := range [][]string{
		// A flag pflag stops at, so the -o json after it is never parsed.
		{"probe", "greet", "--bogus", "-o", "json"},
		// And a refusal made once every flag was.
		{"probe", "greet", "-o", "json"},
	} {
		cmd := exec.Command(binary, append([]string{"plugin", "dev", src, "--"}, args...)...)
		cmd.Env = env
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
			t.Errorf("%v: %v, want exit 2 (stderr %q)", args, err, stderr.String())
			continue
		}
		var answer map[string]any
		if err := json.Unmarshal([]byte(stderr.String()), &answer); err != nil {
			t.Errorf("%v: stderr is not the json asked for (%v): %q", args, err, stderr.String())
			continue
		}
		if answer["code"] != "core.usage" {
			t.Errorf("%v: stderr = %v, want core.usage", args, answer)
		}
	}
}

// The second pass through the loop the docs teach: the plugin is built onto
// $PATH and trusted, and the next edit-and-run is `rta plugin dev` in the same
// directory. It was refused as "namespace already registered" with a hint to
// rename the plugin, which the author's own installed copy had nothing to do
// with. The directory is what runs, and the run says which installed copy it
// did not use.
func TestPluginDevRunsTheDirectoryWhenThePluginIsAlsoInstalled(t *testing.T) {
	src, env := scaffolded(t)
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	installedAt := filepath.Join(bin, "rta-plugin-probe")
	build := exec.Command(goTool, "build", "-o", installedAt, ".")
	build.Dir = src
	build.Env = env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the installed copy: %v\n%s", err, out)
	}
	// Nothing but the installed copy and the toolchain on PATH, so a plugin on
	// the machine running the suite is not discovered beside it.
	env = append(env, "PATH="+bin+":"+filepath.Dir(goTool)+":/usr/bin:/bin")
	if r := runIn(t, env, "plugin", "trust", "probe", "--yes"); r.code != 0 {
		t.Fatalf("trusting the installed copy: exit %d (%s)", r.code, r.stderr)
	}
	if r := runIn(t, env, "probe", "greet", "world"); r.code != 0 || !strings.Contains(r.stdout, "Hello, world!") {
		t.Fatalf("the installed copy does not run: exit %d, %q (%s)", r.code, r.stdout, r.stderr)
	}

	r := runIn(t, env, "plugin", "dev", src, "--", "probe", "greet", "world")
	if r.code != 0 || !strings.Contains(r.stdout, "Hello, world!") {
		t.Fatalf("plugin dev did not run the directory: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	for _, want := range []string{"probe is also installed", installedAt} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr does not say %q: %q", want, r.stderr)
		}
	}

	// And the bare report, which is what `rta plugin dev` shows when nothing
	// follows it, is the one for the directory's build.
	r = runIn(t, env, "plugin", "dev", src, "-o", "json")
	if r.code != 0 || !strings.Contains(r.stdout, "probe.greet") {
		t.Errorf("the report: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}
