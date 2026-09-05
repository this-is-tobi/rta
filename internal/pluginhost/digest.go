package pluginhost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/this-is-tobi/rta/internal/registry"
)

// Identity is what rta believes it is about to run: the resolved binary and
// its content digest.
//
// Both halves are kept. The digest is what any authorisation should attach to,
// because a name is not an artifact — it is the difference between "this
// operator allowed this program to destroy things" and "this operator allowed
// whatever is called `pg` today". The path is recorded beside it so that a
// binary swapped under the same name is visibly a *new* identity rather than
// an inherited one.
type Identity struct {
	Path   string
	Digest string // hex sha256 of the file's contents
}

// Short is the digest as a person would quote it in a config or a log line.
func (i Identity) Short() string {
	if len(i.Digest) < 12 {
		return i.Digest
	}
	return i.Digest[:12]
}

// Identify resolves a plugin command to a path and hashes it.
//
// rta computes this itself rather than using go-plugin's SecureConfig, and the
// reason is specific rather than a preference. SecureConfig hashes cmd.Path —
// which, under any wrapper, is /usr/bin/sandbox-exec. So either every spawn
// fails the check, or the pin verifies Apple's binary while the plugin it was
// meant to protect is never hashed at all. The second is the worse outcome by
// a distance: a green checkmark over an unverified artifact is worse than no
// checkmark, because somebody will build a policy on it.
//
// This is TOFU at best and rta does not claim more: a
// $PATH binary can be replaced between this hash and the exec that follows it,
// and go-plugin's own SecureConfig has exactly the same TOCTOU shape. The
// digest's value is that it names *an artifact* for the cache key and for any
// later authorisation — not that it proves origin, which nothing here can do.
func Identify(name string) (Identity, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return Identity{}, fmt.Errorf("cannot find plugin %q: %w", name, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return Identity{}, fmt.Errorf("resolving %q: %w", resolved, err)
	}
	// Through symlinks, so Path names the file the digest below actually
	// reads — os.Open always followed the link, and an Identity whose Path is
	// a link records an artifact beside a name whose target can move. It is
	// also load-bearing on macOS: the managed store's bin/ links live inside
	// rta's own data dir, which the sandbox denies file-read* on, and SBPL
	// blocks *resolving a symlink* there even though executing the real file
	// is a separate operation it allows — so a spawn through the link dies
	// with EPERM while the same binary runs by its real path. Found by
	// running an installed plugin, not by reading the profile.
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return Identity{}, fmt.Errorf("resolving %q: %w", resolved, err)
	}
	f, err := os.Open(abs)
	if err != nil {
		return Identity{}, fmt.Errorf("reading plugin %q: %w", abs, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Identity{}, fmt.Errorf("hashing plugin %q: %w", abs, err)
	}
	return Identity{Path: abs, Digest: hex.EncodeToString(h.Sum(nil))}, nil
}

// specHash identifies the confinement a process was launched under.
//
// A running plugin may serve only calls whose spec hash matches the one it
// started with; anything else spawns a new process. Under the current deny set
// the spec is argument-independent, so this will fire zero times at M2 — every
// call to a given plugin hashes identically. It is encoded anyway because it
// is a *cache-key shape*, and a cache key is precisely the thing that cannot
// be widened later without auditing every call site that ever read it.
func specHash(d DenySet) string {
	h := sha256.New()
	// Length-prefixed rather than joined, so that two different sets cannot
	// render to the same bytes by moving a separator into a path.
	write := func(section string, entries []string) {
		fmt.Fprintf(h, "%s:%d\n", section, len(entries))
		for _, e := range entries {
			fmt.Fprintf(h, "%d:%s\n", len(e), e)
		}
	}
	fmt.Fprintf(h, "confined:%t\n", confined)
	write("noaccess", d.NoAccess)
	write("noread", d.NoRead)
	write("nomove", d.NoMove)
	return hex.EncodeToString(h.Sum(nil))
}

// Origin describes this client's artifact in the shape the registry records.
//
// The conversion exists because registry cannot import this package —
// pluginhost imports registry, for LoadInto — and a shared type would be a
// third package holding two fields. It is one function, and it is the seam
// where "what the host launched" becomes "what the catalogue says is
// registered", which is exactly where the two used to be able to disagree.
func (c *Client) Origin() registry.Origin {
	if c == nil {
		return registry.Origin{}
	}
	return registry.Origin{Path: c.Identity.Path, Digest: c.Identity.Digest}
}
