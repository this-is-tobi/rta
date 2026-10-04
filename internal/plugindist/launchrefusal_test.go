package plugindist

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
)

// longTMPDIR points TMPDIR at a directory too long for any socket's path,
// made first, so that what makes its own directory in TMPDIR gets as far as
// the launch, which the host refuses as plugin.tmpdir.toolong.
func longTMPDIR(t *testing.T) {
	t.Helper()
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 110))
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
}

// A refusal the launch already coded reaches the operator as itself, from an
// install's verification, a manifest's generation and an upgrade's reading of
// the installed declaration alike. Describe re-coded every failed launch as
// plugin.declaration.unreadable, and the upgrade's as plugin.upgrade.old, each
// with a hint of its own, so a TMPDIR too long for the plugin's socket was
// reported as a binary that cannot answer, and the operator was told to
// remove and reinstall a plugin that was fine, the TMPDIR fix unsaid.
func TestALaunchRefusalIsPassedOnAsItself(t *testing.T) {
	testData(t)
	script := filepath.Join(t.TempDir(), "rta-plugin-any")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	longTMPDIR(t)
	_, verr := Describe(context.Background(), script)
	if verr == nil || verr.Code != "plugin.tmpdir.toolong" || !strings.Contains(verr.Hint, "TMPDIR") {
		t.Fatalf("Describe = %v, want the launch's own plugin.tmpdir.toolong and its hint", verr)
	}
}

func TestAnUpgradePassesALaunchRefusalOnAsItself(t *testing.T) {
	installHello(t)
	// The install's launch left the declaration in the host's cache, which
	// the upgrade would read without launching anything.
	if err := os.RemoveAll(filepath.Join(paths.Data(), "plugin-cache")); err != nil {
		t.Fatal(err)
	}
	longTMPDIR(t)
	_, verr := Upgrade(context.Background(), "hello", io.Discard)
	if verr == nil || verr.Code != "plugin.tmpdir.toolong" || !strings.Contains(verr.Hint, "TMPDIR") {
		t.Fatalf("Upgrade = %v, want the launch's own plugin.tmpdir.toolong and its hint", verr)
	}
}

// A name where a program was wanted is told apart from a program that ran and
// could not answer. `rta plugin doc pg` is the mistake of anyone who has
// installed pg, and its refusal explained how rta learns a declaration — true,
// and about a binary that ran — instead of saying what the argument is.
func TestANameWhereAProgramIsWantedSaysWhatIsWanted(t *testing.T) {
	testData(t)
	for _, path := range []string{"hello", filepath.Join(t.TempDir(), "rta-plugin-absent")} {
		_, verr := Describe(context.Background(), path)
		if verr == nil || verr.Code != "plugin.declaration.unreadable" {
			t.Fatalf("Describe(%q) = %v, want plugin.declaration.unreadable", path, verr)
		}
		if !strings.Contains(verr.Hint, "executable itself") || !strings.Contains(verr.Hint, "rta-plugin-<name>") {
			t.Errorf("Describe(%q) hint %q does not say what the argument is", path, verr.Hint)
		}
	}
}

// Anything the launch did not code is still the caller's to name.
func TestALaunchFailureWithNoCodeIsNamedByItsCaller(t *testing.T) {
	testData(t)
	_, verr := Describe(context.Background(), filepath.Join(t.TempDir(), "rta-plugin-absent"))
	if verr == nil || verr.Code != "plugin.declaration.unreadable" {
		t.Fatalf("Describe = %v, want plugin.declaration.unreadable", verr)
	}
}
