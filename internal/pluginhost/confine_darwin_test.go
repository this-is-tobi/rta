package pluginhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The one test that proves the macOS claim. Everything else in this package
// asserts that rta *builds* a policy; this runs the policy rta built, through
// the same /usr/bin/sandbox-exec the spawn path uses, and checks that a read
// is actually refused.
//
// Without it the package would be a set of confident assertions about a
// string. ADR 0012 rests the whole platform story on this profile working,
// and "we generated some SBPL" is not evidence that it does.
func TestTheGeneratedProfileActuallyDeniesReads(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	data := t.TempDir()
	secret := filepath.Join(data, "kv.identity")
	if err := os.WriteFile(secret, []byte("AGE-SECRET-KEY-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))

	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}

	// Unconfined, the read works — otherwise the test below proves nothing
	// but that the file is unreadable.
	if out, err := exec.Command("/bin/cat", secret).CombinedOutput(); err != nil {
		t.Fatalf("reading the fixture unconfined failed, so the test cannot prove anything: %v (%s)", err, out)
	}

	name, argv := wrap(d, "/bin/cat", []string{secret})
	out, err := exec.Command(name, argv...).CombinedOutput()
	if err == nil {
		t.Fatalf("a confined process read %s: %s", secret, out)
	}
	if strings.Contains(string(out), "AGE-SECRET-KEY-1") {
		t.Fatalf("the key contents came back despite a non-zero exit: %s", out)
	}
}

// The gap that took an exploit to find. A first pass denied reads only, on
// the reasoning that these directories hold secrets — and left the one member
// whose dangerous operation is a *write*. A confined plugin could not read
// grants.json and did not need to: the Grant struct is public, so a blind
// overwrite handed it a standing grant over every kv capability.
func TestTheGeneratedProfileActuallyDeniesWrites(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))

	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(data, "grants.json")

	name, argv := wrap(d, "/bin/sh", []string{"-c", "echo forged > " + target})
	out, err := exec.Command(name, argv...).CombinedOutput()
	if err == nil {
		t.Fatalf("a confined process wrote to %s: %s", target, out)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatalf("%s exists, so the write landed despite a non-zero exit", target)
	}
}

// Tier 2 is read-denied and deliberately not write-denied: a plugin that runs
// ssh-keygen or aws configure on the user's behalf is doing what it was
// installed to do, and denying that breaks honest tools to protect nothing
// rta owns.
func TestCredentialDirectoriesAreReadDeniedOnly(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(home, ".ssh")
	if !containsPath(d.NoRead, ssh) {
		t.Fatalf("%s is not in the read-denied set: %v", ssh, d.NoRead)
	}
	if containsPath(d.NoAccess, ssh) {
		t.Errorf("%s is write-denied, which breaks ssh-keygen for no gain", ssh)
	}

	// And prove the read denial is real rather than declared, against a path
	// that exists on this machine.
	if _, statErr := os.Stat(ssh); statErr != nil {
		t.Skip("no ~/.ssh on this machine")
	}
	name, argv := wrap(d, "/bin/ls", []string{ssh})
	if out, err := exec.Command(name, argv...).CombinedOutput(); err == nil {
		t.Errorf("a confined process listed %s: %s", ssh, out)
	}
}

// Everything outside the deny set stays readable. The profile is (allow
// default) plus denials, and a plugin that cannot read its own files or the
// project it was pointed at is a plugin nobody installs.
func TestEverythingElseStaysReadable(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "project.txt")
	if err := os.WriteFile(work, []byte("ordinary"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, argv := wrap(d, "/bin/cat", []string{work})
	out, err := exec.Command(name, argv...).CombinedOutput()
	if err != nil {
		t.Fatalf("a confined process could not read an ordinary file: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "ordinary") {
		t.Errorf("contents = %q", out)
	}
}

// The profile is passed inline with -p and never as a file. A profile written
// to a temp file and unlinked after cmd.Start() loses the race every time,
// because Start returns after fork+exec and before sandbox-exec opens the
// file — which under a fail-closed rule is 100% spawn failure on the one
// platform this decision relies on.
func TestTheProfileIsPassedInline(t *testing.T) {
	d := DenySet{NoAccess: []string{"/nope"}}
	name, argv := wrap(d, "/bin/echo", []string{"hi"})
	if name != "/usr/bin/sandbox-exec" {
		t.Fatalf("wrapper = %q", name)
	}
	if argv[0] != "-p" {
		t.Errorf("argv[0] = %q, want -p (a -f profile file loses the unlink race)", argv[0])
	}
	if !strings.Contains(argv[1], "(deny file-read* file-write*") {
		t.Errorf("the policy is not inline in argv[1]: %q", argv[1])
	}
	if argv[2] != "/bin/echo" || argv[3] != "hi" {
		t.Errorf("the wrapped command is wrong: %v", argv[2:])
	}
}

// (allow default) must come before the denials: SBPL is last-match-wins, so
// the reverse order is a policy that denies nothing at all.
func TestTheBlanketAllowComesBeforeTheDenials(t *testing.T) {
	p := profile(DenySet{NoAccess: []string{"/a"}, NoRead: []string{"/b"}})
	allow := strings.Index(p, "(allow default)")
	deny := strings.Index(p, "(deny")
	if allow < 0 || deny < 0 {
		t.Fatalf("profile is missing a form:\n%s", p)
	}
	if allow > deny {
		t.Errorf("(allow default) comes after the denials, which overrides them:\n%s", p)
	}
}

// The grant seal (ADR 0015) is only worth anything if a confined plugin
// cannot read the key. Sealing was built for exactly one attacker — a writer
// that cannot read the directory it writes to, which is the shape a sandbox
// creates — so the two mechanisms have to line up, and "the key happens to be
// in a directory that happens to be denied" is the kind of alignment that
// survives until somebody moves a file.
func TestTheGrantSealKeyIsUnreadableToAConfinedPlugin(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))

	key := filepath.Join(data, "grants.key")
	if err := os.WriteFile(key, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}

	name, argv := wrap(d, "/bin/cat", []string{key})
	out, err := exec.Command(name, argv...).CombinedOutput()
	if err == nil {
		t.Fatalf("a confined plugin read the grant seal key: %s", out)
	}
	if strings.Contains(string(out), "0123456789abcdef") {
		t.Fatalf("the key leaked despite a non-zero exit: %s", out)
	}

	// And it cannot be replaced either, which is the other half: a plugin
	// that could write its own key would seal its own grants.
	name, argv = wrap(d, "/bin/sh", []string{"-c", "echo forged > " + key})
	if out, err := exec.Command(name, argv...).CombinedOutput(); err == nil {
		t.Errorf("a confined plugin overwrote the grant seal key: %s", out)
	}
}

// The Tier 1 write-deny should break nothing honest, and the failure mode if
// it did would be a plugin dropping a temp file and getting an opaque
// "Operation not permitted" far from the cause. TMPDIR is on the environment
// allowlist precisely so this stays true.
func TestOrdinaryTempWritesStillWork(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{os.TempDir(), t.TempDir()} {
		target := filepath.Join(dir, "rta-confined-write-probe")
		defer os.Remove(target)
		name, argv := wrap(d, "/bin/sh", []string{"-c", "echo ok > " + target + " && cat " + target})
		out, err := exec.Command(name, argv...).CombinedOutput()
		if err != nil {
			t.Errorf("a confined plugin could not write to %s: %v (%s)", dir, err, out)
			continue
		}
		if !strings.Contains(string(out), "ok") {
			t.Errorf("write to %s produced %q", dir, out)
		}
	}
}

// A refusal has to be attributable. A plugin author who hits the deny set
// should see the path in the error, not a bare errno from somewhere in their
// dependency tree.
func TestARefusedWriteNamesThePath(t *testing.T) {
	if err := available(); err != nil {
		t.Skipf("no sandbox-exec: %v", err)
	}
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))
	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(data, "scratch.tmp")
	name, argv := wrap(d, "/bin/sh", []string{"-c", "echo x > " + target})
	out, _ := exec.Command(name, argv...).CombinedOutput()
	if !strings.Contains(string(out), "scratch.tmp") {
		t.Errorf("the refusal does not name the path a plugin author would search for: %q", out)
	}
}
