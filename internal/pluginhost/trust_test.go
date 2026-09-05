package pluginhost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
)

// canarySource is a "plugin" whose only behaviour is to prove it ran.
//
// It cannot complete the handshake, so if trust lets it through the load
// *fails* — and the file it leaves behind is the only evidence that matters.
// What is being tested is not whether an untrusted plugin registers, which any
// check would get right. It is whether a stranger's code executes at all, and
// the only honest way to ask that is to give it something to do and look for
// the trace.
//
// **A compiled binary rather than a shell script**, which is what this was.
// Windows executes neither a shebang nor a .bat through CreateProcess, so on
// the one platform where rta's plugin loading had just been found broken in
// three separate ways, the test that asks "does a stranger's code run"
// answered no because *nothing* could run. That is a pass for the wrong
// reason, on a security property, which is worse than a skip — a skip at
// least says so.
//
// It writes beside its own executable rather than to a path baked in at build
// time, so one build serves every test: each copies it into a directory of its
// own and reads back its own trace.
const canarySource = `package main

import "os"

func main() {
	if exe, err := os.Executable(); err == nil {
		_ = os.WriteFile(exe+".ran", nil, 0o644)
	}
	os.Exit(1)
}
`

var (
	canaryOnce sync.Once
	canaryBin  string
	canaryErr  error
)

// canaryBinary builds the canary once for the package, the way hello does, and
// for the same reason: the spawn path is the thing under test and a fixture
// that cannot be spawned tests nothing.
//
// A single file with no go.mod, which `go build` accepts for a program that
// imports only the standard library — so this needs no committed source and
// no module of its own.
func canaryBinary(t *testing.T) string {
	t.Helper()
	canaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rta-canary-*")
		if err != nil {
			canaryErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(canarySource), 0o644); err != nil {
			canaryErr = err
			return
		}
		canaryBin = filepath.Join(dir, BinaryName("canary"))
		build := exec.Command("go", "build", "-o", canaryBin, src)
		if out, err := build.CombinedOutput(); err != nil {
			canaryErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if canaryErr != nil {
		t.Fatalf("building the canary: %v", canaryErr)
	}
	return canaryBin
}

func canary(t *testing.T) (dir, trace string, digest string) {
	t.Helper()
	dir = t.TempDir()
	body, err := os.ReadFile(canaryBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, BinaryName("canary"))
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	return dir, path + ".ran", id.Digest
}

func ran(t *testing.T, trace string) bool {
	t.Helper()
	_, err := os.Stat(trace)
	return err == nil
}

// The whole point. A binary on $PATH is not consent, and rta loads a plugin by
// running it — so before this, dropping a file called rta-plugin-anything into
// any $PATH directory bought code execution on the next rta invocation,
// including the `rta __complete` a tab press runs.
func TestAnUntrustedPluginIsNeverExecuted(t *testing.T) {
	dir, trace, _ := canary(t)
	t.Setenv("PATH", dir)
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	h := New(nil)
	t.Cleanup(h.CloseAll)
	problems := h.LoadInto(context.Background(), registry.New())

	if ran(t, trace) {
		t.Fatal("an unapproved binary was executed")
	}
	// And not as a failure, either: a decision nobody has made is not an
	// error to print before every command for the rest of the installation's
	// life. It travels out through Untrusted().
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none — an untrusted plugin is not a failure", problems)
	}
	found := h.Untrusted()
	if len(found) != 1 || found[0].Name != "canary" {
		t.Fatalf("untrusted = %+v, want exactly the canary", found)
	}
	if found[0].Digest == "" {
		t.Error("an untrusted plugin was recorded with no digest, so nothing could approve it")
	}
}

// And once approved, it runs — which is what makes the test above about trust
// rather than about the script being unrunnable.
func TestATrustedPluginIsExecuted(t *testing.T) {
	dir, trace, digest := canary(t)
	t.Setenv("PATH", dir)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if verr := plugintrust.Add(digest, "canary", ""); verr != nil {
		t.Fatal(verr)
	}

	h := New(nil)
	t.Cleanup(h.CloseAll)
	h.LoadInto(context.Background(), registry.New())

	if !ran(t, trace) {
		t.Fatal("an approved binary was not executed")
	}
	if u := h.Untrusted(); len(u) != 0 {
		t.Errorf("an approved plugin was reported as untrusted: %+v", u)
	}
}

// Trust attaches to the bytes, not to the name. A plugin replaced under a name
// already approved is the substitution the check exists to notice — and it is
// also the ordinary case of a rebuild, which is why the friction is the
// feature rather than the cost.
func TestTrustDoesNotSurviveTheArtifactChanging(t *testing.T) {
	dir, trace, digest := canary(t)
	t.Setenv("PATH", dir)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if verr := plugintrust.Add(digest, "canary", ""); verr != nil {
		t.Fatal(verr)
	}

	// Same path, same name, different bytes — the real canary with a byte
	// appended, so the replacement is still something that would leave a
	// trace if it were allowed to run. A replacement that could not execute
	// would pass this test without the trust check doing anything.
	path := filepath.Join(dir, BinaryName("canary"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}

	h := New(nil)
	t.Cleanup(h.CloseAll)
	h.LoadInto(context.Background(), registry.New())

	if ran(t, trace) {
		t.Fatal("a replaced artifact ran on the approval given to the one it replaced")
	}
	if u := h.Untrusted(); len(u) != 1 {
		t.Fatalf("untrusted = %+v, want the replacement", u)
	}
}

// A gate whose failure mode is "allow" is not a gate. Every way of failing to
// read the record has to answer "nothing is trusted", because the alternative
// — unreadable, so allow — is how a security control becomes a formality the
// first time a disk fills up.
func TestAnUnreadableTrustRecordTrustsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"absent", func(*testing.T, string) {}},
		{"corrupt", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"truncated", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"a directory", func(t *testing.T, path string) {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, trace, digest := canary(t)
			data := t.TempDir()
			t.Setenv("PATH", dir)
			t.Setenv("RTA_DATA_DIR", data)
			tc.write(t, plugintrust.Path())

			if plugintrust.Load().Trusts(digest) {
				t.Fatal("an unreadable record trusted something")
			}
			h := New(nil)
			t.Cleanup(h.CloseAll)
			h.LoadInto(context.Background(), registry.New())
			if ran(t, trace) {
				t.Fatal("an unreadable record let a binary run")
			}
		})
	}
}
