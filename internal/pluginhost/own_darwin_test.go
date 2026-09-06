package pluginhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The carve-out DenySet.Own describes, run through the same
// /usr/bin/sandbox-exec the spawn path uses: a confined process reads the
// artifact's own directory, still cannot read the rest of rta's state beside
// it, and still cannot write into the directory it may now read.
//
// The TLS fact itself — a process that cannot read its own directory cannot
// verify a certificate — needs a network and is recorded on the field rather
// than asserted here; what this pins is that the rule rta renders opens
// exactly the directory and nothing more.
func TestAnInstalledArtifactCanReadItsOwnDirectoryAndNothingMore(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))

	own := filepath.Join(ManagedStore(), "pg", "abc123")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(own, "rta-plugin-pg")
	if err := os.WriteFile(binary, []byte("the artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(data, "kv.identity")
	if err := os.WriteFile(secret, []byte("AGE-SECRET-KEY-1"), 0o600); err != nil {
		t.Fatal(err)
	}

	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	// Before the carve-out the artifact cannot read its own directory —
	// otherwise the success below proves nothing.
	name, argv := wrap(d, "/bin/cat", []string{binary})
	if out, err := exec.Command(name, argv...).CombinedOutput(); err == nil {
		t.Fatalf("the store was readable before any carve-out, so the test cannot prove anything: %s", out)
	}

	d, err = d.Launching(binary)
	if err != nil {
		t.Fatal(err)
	}
	name, argv = wrap(d, "/bin/cat", []string{binary})
	out, err := exec.Command(name, argv...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "the artifact") {
		t.Fatalf("the artifact cannot read its own directory: %v (%s)", err, out)
	}

	name, argv = wrap(d, "/bin/cat", []string{secret})
	if out, err := exec.Command(name, argv...).CombinedOutput(); err == nil {
		t.Fatalf("the carve-out opened rta's state beside the store: %s", out)
	}

	forged := filepath.Join(own, "forged")
	name, argv = wrap(d, "/bin/sh", []string{"-c", "echo forged > " + forged})
	if out, err := exec.Command(name, argv...).CombinedOutput(); err == nil {
		t.Fatalf("the carve-out opened writes into the artifact's directory: %s", out)
	}
	if _, statErr := os.Stat(forged); statErr == nil {
		t.Fatal("the write landed despite a non-zero exit")
	}
}
