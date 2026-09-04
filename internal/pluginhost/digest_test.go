package pluginhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Identity resolves symlinks: Path must name the file the digest was read
// from, not a link whose target can move under the pin — and on macOS the
// difference decides whether the spawn works at all, because the sandbox
// denies resolving a link inside rta's data dir while allowing the exec of
// the real file (see Identify). The spawn half is pinned by
// TestAManagedStoreSymlinkSpawns.
func TestIdentifyResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, BinaryName("real"))
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, BinaryName("link"))
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(link)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := Identify(real)
	if err != nil {
		t.Fatal(err)
	}
	if id.Path != direct.Path {
		t.Fatalf("through the link Path = %q, direct = %q — the pin records a name, not the artifact",
			id.Path, direct.Path)
	}
	if id.Digest != direct.Digest {
		t.Fatalf("digests differ across the link: %s vs %s", id.Digest, direct.Digest)
	}
}
