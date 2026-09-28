package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `--platform` is where an author says the one thing a binary cannot say
// about itself, so its grammar is the one place a typo turns into a manifest
// nobody can install from. Every refusal here is one caught before the file
// exists.
func TestAPlatformSpecIsReadOrRefused(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "rta-plugin-lab.tar.gz")
	if err := os.WriteFile(artifact, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	src, verr := parsePlatformSpec("linux/amd64=https://example.com/lab.tar.gz", "")
	if verr != nil {
		t.Fatalf("a published artifact was refused: %v", verr)
	}
	if src.OS != "linux" || src.Arch != "amd64" || src.URL != "https://example.com/lab.tar.gz" {
		t.Fatalf("src = %+v", src)
	}

	// A value with no scheme is a file here, and it becomes the absolute
	// file:// URL a manifest requires — a relative one is refused by the
	// manifest grammar, so leaving it alone would push the failure one step
	// further from the person who can fix it.
	src, verr = parsePlatformSpec("darwin/arm64="+artifact, "inner/rta-plugin-lab")
	if verr != nil {
		t.Fatalf("a local artifact was refused: %v", verr)
	}
	if !strings.HasPrefix(src.URL, "file:///") || !strings.HasSuffix(src.URL, "rta-plugin-lab.tar.gz") {
		t.Fatalf("URL = %q, want an absolute file URL", src.URL)
	}
	if src.Bin != "inner/rta-plugin-lab" {
		t.Fatalf("bin = %q", src.Bin)
	}

	for _, bad := range []struct{ spec, says string }{
		{"linux/amd64", "states no artifact"},
		{"linux/amd64=", "states no artifact"},
		{"=https://example.com/lab", "does not start with <os>/<arch>"},
		{"amd64=https://example.com/lab", "does not start with <os>/<arch>"},
		{"/amd64=https://example.com/lab", "does not start with <os>/<arch>"},
		{"linux/=https://example.com/lab", "does not start with <os>/<arch>"},
	} {
		if _, verr := parsePlatformSpec(bad.spec, ""); verr == nil {
			t.Errorf("%q was accepted", bad.spec)
		} else if !strings.Contains(verr.Message, bad.says) {
			t.Errorf("%q: %q, want it to say %q", bad.spec, verr.Message, bad.says)
		}
	}

	// A path that is not there is the likeliest mistake of all — a typo, or a
	// build that did not run — and it must not become a manifest claiming an
	// artifact nobody can fetch.
	_, verr = parsePlatformSpec("linux/amd64="+filepath.Join(dir, "never-built"), "")
	if verr == nil {
		t.Fatal("a platform pointing at nothing was accepted")
	}
	if !strings.Contains(verr.Hint, "https:// URL") {
		t.Fatalf("hint = %q, want it to name the published case", verr.Hint)
	}
}

// --checksums is read no further than a checksums file may be. It was read
// whole before its size was looked at, so a path naming something else, a
// release archive given in the wrong flag or a device, cost its whole size in
// memory to be refused, and one that never ends was read until memory ran
// out: /dev/zero took seven gigabytes in eight seconds and never finished.
func TestAChecksumsFileIsRefusedPastTheCapWithoutBeingRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checksums.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(32 << 20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, verr := readChecksumsFile(path)
	runtime.ReadMemStats(&after)
	if verr == nil || verr.Code != "plugin.manifest.checksums" {
		t.Fatalf("a checksums file of 32 MiB = %v, want plugin.manifest.checksums", verr)
	}
	if taken := after.TotalAlloc - before.TotalAlloc; taken > 8<<20 {
		t.Errorf("refusing a 32 MiB checksums file took %d bytes", taken)
	}
	if _, verr := readChecksumsFile(filepath.Join(t.TempDir(), "missing")); verr == nil ||
		verr.Code != "plugin.manifest.checksums" {
		t.Errorf("a missing checksums file = %v, want plugin.manifest.checksums", verr)
	}
}
