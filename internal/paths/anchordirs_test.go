package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anchorIn writes a token file at 0600 in a directory of the given mode and
// checks it the way a server's start does: the file's own mode and owner are
// right, so whatever is refused is refused for where the file is.
func anchorIn(t *testing.T, mode os.FileMode) (string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Mkdir is masked by the umask and drops the sticky bit; Chmod says it.
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	file := filepath.Join(dir, "tokens.txt")
	if err := os.WriteFile(file, []byte("work 0123456789abcdef0123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CheckTrustAnchor(file, info, "it is the entire trust anchor for the static-token path")
	return dir, err
}

// A file nobody else may write, in a directory anybody may, is a file anybody
// replaces: unlinking it and writing another in its place needs no bit of the
// file. Refused the way a wide mode on the file is, since a chmod of the
// directory answers it, and naming the directory.
func TestATrustAnchorInADirectoryOthersWriteIsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o770, 0o722} {
		dir, err := anchorIn(t, mode)
		if err == nil {
			t.Errorf("a token file in a directory of mode %s was accepted", mode)
			continue
		}
		if !errors.Is(err, ErrWeakMode) {
			t.Errorf("mode %s: refused as %v, want ErrWeakMode, which a chmod answers", mode, err)
		}
		if msg := err.Error(); !strings.Contains(msg, dir) || !strings.Contains(msg, "static-token path") {
			t.Errorf("mode %s: the refusal names neither the directory nor what the file decides: %s", mode, msg)
		}
	}
}

// Sticky is how /tmp is shared, and how Kubernetes mounts a Secret, where the
// chart keeps the token file and the roster: only an entry's owner may unlink
// or rename it there, so a sticky directory others write is not one they can
// replace the file in. Nor is one only this account writes.
func TestATrustAnchorInAStickyOrPrivateDirectoryLoads(t *testing.T) {
	for _, mode := range []os.FileMode{0o700, 0o755, os.ModeSticky | 0o777} {
		if _, err := anchorIn(t, mode); err != nil {
			t.Errorf("a token file in a directory of mode %s was refused: %v", mode, err)
		}
	}
}

// A link kept in a directory others write is a link they can point at another
// file, so it is refused for that directory even though the file it leads to
// sits somewhere private.
func TestATrustAnchorReachedThroughALinkInADirectoryOthersWriteIsRefused(t *testing.T) {
	private := t.TempDir()
	file := filepath.Join(private, "operators")
	if err := os.WriteFile(file, []byte("# roster\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(shared, 0o700) })
	link := filepath.Join(shared, "operators")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckTrustAnchor(link, info, "it decides who may operate this server"); err == nil ||
		!strings.Contains(err.Error(), shared) {
		t.Errorf("a roster reached through a link in a directory anybody writes: %v", err)
	}
}
