package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/paths"
)

// ManagedRun is where a plugin found anywhere but in a store is run from: a
// private, content-addressed copy, one directory per digest.
func ManagedRun() string { return filepath.Join(paths.Data(), "plugins", "run") }

// staleRun is how long a copy nobody asked for is kept. A plugin developer
// rebuilds a binary on every run and each build is a digest of its own, so
// the directory would otherwise hold every build they ever made.
const staleRun = 24 * time.Hour

// inStore reports whether exe lies in a directory rta itself keeps: the
// managed store, the system root's, or the copies staged below.
func inStore(exe string) bool {
	for _, store := range []string{ManagedStore(), SystemStore(), ManagedRun()} {
		if store == "" {
			continue
		}
		for _, form := range withTarget(store) {
			if inside(exe, form) {
				return true
			}
		}
	}
	return false
}

// execPath is the file a launch of id runs: the artifact where it already
// sits in a directory rta keeps, and otherwise the private copy stage makes.
//
// A pure function of the identity, so the sandbox's own-directory rule and
// the process cache key — both computed before anything is launched — name
// the directory the process will actually run from.
func (id Identity) execPath() string {
	if inStore(id.Path) {
		return id.Path
	}
	return filepath.Join(ManagedRun(), id.Digest, filepath.Base(id.Path))
}

// stage makes the file execPath names, holding exactly the bytes the digest
// says, and returns it.
//
// **The digest is checked on the bytes that are then executed, not on a path
// that can change between the two.** Identify hashes a file and the launch
// ran it by name, and for a plugin found on $PATH — a directory the operator
// owns, and whatever else they run can write to — the name could be given
// other contents in between: the approval was for the bytes that were hashed
// and the process was whatever was there at exec. A plugin in rta's own store
// is out of that reach (the store is in the data directory, which a confined
// plugin may neither read nor write), so only the artifacts that are not are
// copied, and the copy is written from the descriptor the digest is computed
// over. What runs is a file in rta's private directory, mode 0700, that no
// process confined by rta can open for writing.
//
// An existing copy is reused only after being hashed again. What stays
// unclosed is a writer of rta's own data directory outside any confinement:
// the same user, who could equally edit the grants file.
func stage(id Identity) (string, error) {
	exe := id.execPath()
	if exe == id.Path {
		return exe, nil
	}
	defer sweepRun(id.Digest)
	// Before the look at the copy, and not after: the sweep judges a copy by
	// when its directory was last touched, and a copy that was reused without
	// touching it would be a day old to a sweep started by another plugin's
	// launch in the same moment, removed between this look and the exec.
	now := time.Now()
	_ = os.Chtimes(filepath.Dir(exe), now, now)
	if matches(exe, id.Digest) {
		return exe, nil
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		return "", fmt.Errorf("staging plugin %q: %w", id.Path, err)
	}
	src, err := atomicfile.Open(id.Path)
	if err != nil {
		return "", fmt.Errorf("reading plugin %q: %w", id.Path, err)
	}
	defer func() { _ = src.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".stage-*")
	if err != nil {
		return "", fmt.Errorf("staging plugin %q: %w", id.Path, err)
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o700)
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != id.Digest {
		err = fmt.Errorf("the plugin at %s changed on disk after it was trusted (%s → %s); restart rta so it is re-read",
			id.Path, id.Short(), hex.EncodeToString(h.Sum(nil))[:12])
	}
	if err == nil {
		err = os.Rename(tmp.Name(), exe)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		if matches(exe, id.Digest) {
			return exe, nil
		}
		return "", err
	}
	return exe, nil
}

// matches reports whether the file at path holds the digest.
func matches(path, digest string) bool {
	f, err := atomicfile.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == digest
}

// sweepRun removes the copies of other digests nobody has started in a day. A
// process running from one keeps its file on every platform that lets a
// running file be removed, and elsewhere the removal fails and is not said.
func sweepRun(keep string) {
	entries, err := os.ReadDir(ManagedRun())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() == keep || !e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleRun {
			_ = os.RemoveAll(filepath.Join(ManagedRun(), e.Name()))
		}
	}
}
