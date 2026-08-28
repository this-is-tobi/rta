package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
)

// canary installs a "plugin" whose only behaviour is to prove it ran.
//
// A shell script rather than a real plugin, deliberately: it cannot complete
// the handshake, so if trust lets it through the load *fails* — and the file
// it leaves behind is the only evidence that matters. What is being tested is
// not whether an untrusted plugin registers, which any check would get right.
// It is whether a stranger's code executes at all, and the only honest way to
// ask that is to give it something to do and look for the trace.
func canary(t *testing.T) (dir, trace string, digest string) {
	t.Helper()
	dir = t.TempDir()
	trace = filepath.Join(dir, "it-ran")
	script := "#!/bin/sh\n: > " + trace + "\nexit 1\n"
	path := filepath.Join(dir, Prefix+"canary")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	return dir, trace, id.Digest
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

	// Same path, same name, different bytes.
	path := filepath.Join(dir, Prefix+"canary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n: > "+trace+"\nexit 1\n# changed\n"), 0o755); err != nil {
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
