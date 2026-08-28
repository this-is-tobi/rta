package main_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// End-to-end, through the real binary.
//
// Everything else in this repo tests the CLI in-process, which cannot see the
// three things a caller actually depends on: the exit code the shell reads,
// which stream each kind of output goes to, and whether `-o json` emits
// something a pipe can parse with nothing else mixed in. `main` is where
// those are decided and it had no test at all.
//
// PROJECT.md §10: "E2E CLI — binary invocation, --output json, assert on
// parsed structures + exit codes."
//
// Skipped under -short; it builds the binary once.

var binary string

func TestMain(m *testing.M) {
	// Flags are not parsed yet at this point, so -short cannot be consulted
	// here; the individual tests skip on it after Parse has run.
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "rta-e2e")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "rta")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		panic("building rta: " + err.Error() + "\n" + string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// lockedBuffer collects a child process's output safely. exec writes it from
// a goroutine of its own, so anything that reads before Wait — this test does,
// to report what the server said while it is still running — needs the lock.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type result struct {
	stdout, stderr string
	code           int
}

// run invokes the binary against a private data and config directory, so a
// test can never read — or write — the state of the machine it runs on.
func run(t *testing.T, args ...string) result {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(),
		"RTA_DATA_DIR="+dataDir(t),
		"RTA_CONFIG="+filepath.Join(dataDir(t), "config.yaml"),
		"RTA_KV_PASSPHRASE=",
		"RTA_KV_IDENTITY=",
		"NO_COLOR=1",
	)
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

// dataDir gives each test one directory for the whole of its life.
func dataDir(t *testing.T) string {
	t.Helper()
	if d, ok := dirs[t.Name()]; ok {
		return d
	}
	d := t.TempDir()
	dirs[t.Name()] = d
	t.Cleanup(func() { delete(dirs, t.Name()) })
	return d
}

var dirs = map[string]string{}

// envelope parses `-o json` output, which is the contract a script depends on.
func envelope(t *testing.T, r result) map[string]any {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d, stderr: %s", r.code, r.stderr)
	}
	if r.stderr != "" {
		t.Errorf("json output wrote to stderr as well: %q", r.stderr)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		t.Fatalf("stdout is not JSON (%v):\n%s", err, r.stdout)
	}
	if env["type"] == nil {
		t.Errorf("envelope has no type discriminator: %v", env)
	}
	return env
}

// 0: it worked, and stdout is a clean parse.
func TestSuccessIsZeroAndParses(t *testing.T) {
	env := envelope(t, run(t, "sys", "host", "-o", "json"))
	if env["type"] != "keyvalue" {
		t.Errorf("type = %v, want keyvalue", env["type"])
	}
}

// 2: usage. A misspelled command is not a capability failure, and a script
// that retries on 1 must not retry on this.
func TestUnknownCommandIsAUsageError(t *testing.T) {
	r := run(t, "sys", "cpuu")
	if r.code != 2 {
		t.Errorf("exit = %d, want 2 (stderr: %s)", r.code, r.stderr)
	}
}

// 1: the capability ran and refused. The code and hint go to stderr, and
// stdout stays empty — `rta kv get x > secret` must not write an error there.
func TestCapabilityErrorIsOneAndKeepsStdoutClean(t *testing.T) {
	r := run(t, "todo", "show", "999")
	if r.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr: %s)", r.code, r.stderr)
	}
	if strings.TrimSpace(r.stdout) != "" {
		t.Errorf("stdout got %q, want nothing", r.stdout)
	}
	if !strings.Contains(r.stderr, "todo.notfound") {
		t.Errorf("stderr = %q, want the error code", r.stderr)
	}
}

// 3: a destructive capability refused for want of confirmation — its own
// exit code, so a script can tell "you must confirm" from "it failed".
func TestConfirmationDeclinedIsThree(t *testing.T) {
	r := run(t, "todo", "rm", "1")
	if r.code != 3 {
		t.Errorf("exit = %d, want 3 (stderr: %s)", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "confirm") {
		t.Errorf("stderr = %q, want it to say what is needed", r.stderr)
	}
}

// The round trip a person actually performs, through the binary, with the
// store on disk between the two calls.
func TestWriteThenReadThroughTheBinary(t *testing.T) {
	if r := run(t, "todo", "add", "ship the release", "--tag", "work"); r.code != 0 {
		t.Fatalf("add: exit %d, %s", r.code, r.stderr)
	}
	env := envelope(t, run(t, "todo", "list", "-o", "json"))
	rows, _ := env["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", env["rows"])
	}
	row, _ := rows[0].([]any)
	joined := strings.Join(func() []string {
		out := make([]string, 0, len(row))
		for _, c := range row {
			out = append(out, c.(string))
		}
		return out
	}(), " ")
	if !strings.Contains(joined, "ship the release") {
		t.Errorf("row = %v", row)
	}
}

// --dry-run is a promise: it must change nothing on disk.
func TestDryRunChangesNothing(t *testing.T) {
	if r := run(t, "todo", "add", "real", "--dry-run"); r.code != 0 {
		t.Fatalf("dry-run add: exit %d, %s", r.code, r.stderr)
	}
	env := envelope(t, run(t, "todo", "list", "-o", "json"))
	if rows, _ := env["rows"].([]any); len(rows) != 0 {
		t.Errorf("dry-run wrote a task: %v", rows)
	}
}

// Machine-readable formats have to stay machine-readable: csv for a table,
// yaml for anything.
func TestAlternateFormatsAreClean(t *testing.T) {
	if r := run(t, "todo", "add", "one"); r.code != 0 {
		t.Fatal(r.stderr)
	}
	csv := run(t, "todo", "list", "-o", "csv")
	if csv.code != 0 || !strings.HasPrefix(csv.stdout, "ID,Status") {
		t.Errorf("csv = %q (exit %d)", csv.stdout, csv.code)
	}
	yaml := run(t, "sys", "host", "-o", "yaml")
	if yaml.code != 0 || !strings.Contains(yaml.stdout, "type: keyvalue") {
		t.Errorf("yaml = %q (exit %d)", yaml.stdout, yaml.code)
	}
}

// The MCP surface has to be reachable the way a client starts it, and stdout
// has to stay the transport: one stray banner line there and every client
// fails to parse the handshake.
func TestMCPServeKeepsStdoutForTheProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds the binary")
	}
	cmd := exec.Command(binary, "mcp", "serve")
	cmd.Env = append(os.Environ(), "RTA_DATA_DIR="+t.TempDir(), "NO_COLOR=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errBuf lockedBuffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// stdin stays open: closing it is how a client says goodbye, and the
	// server is entitled to exit before answering anything.
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	if _, err := stdin.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}` + "\n")); err != nil {
		t.Fatal(err)
	}

	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				lines <- line
				return
			}
		}
		close(lines)
	}()

	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatalf("server closed stdout without answering (stderr %q)", errBuf.String())
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout is not JSON-RPC: %q", line)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("first line on stdout is not a JSON-RPC message: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no answer in 10s (stderr %q)", errBuf.String())
	}

	// …and the human-facing banner belongs on stderr, where it cannot corrupt
	// the stream a client is parsing.
	//
	// Waited for rather than sampled. stdout and stderr are independent pipes,
	// and os/exec copies stderr into errBuf from a goroutine of its own — so
	// the instant the first JSON-RPC line arrives on stdout guarantees nothing
	// about what has reached this buffer yet. Asserting there passed almost
	// always and failed inside the full -race -shuffle suite, reporting an
	// empty stderr for a server that had printed the banner before it served.
	// The claim is "the banner goes to stderr", not "it is there by the time
	// stdout answers", so the wait is the honest way to ask.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(errBuf.String(), "listening") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(errBuf.String(), "listening") {
		t.Errorf("banner should be on stderr, got %q", errBuf.String())
	}
}
