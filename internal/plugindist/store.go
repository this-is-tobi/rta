package plugindist

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The managed store: Krew's store/ + bin/ split, for Krew's
// reasons.
//
//	<data>/plugins/store/pg/<digest>/rta-plugin-pg   # by name, then by digest
//	<data>/plugins/bin/rta-plugin-pg -> ../store/pg/<digest>/rta-plugin-pg
//
// Keeping a previous digest's directory makes rollback a rename rather than
// a re-download; the bin/ symlink is the one place "current" is stated, and
// swapping it is the upgrade. Bare $PATH discovery keeps working unchanged —
// the store is discovered *after* $PATH, so an operator's own copy earlier on
// $PATH shadows the managed one the ordinary way and `rta doctor` reports it,
// rather than rta's store silently outranking a deliberate local
// build.

// StoreDir is where installed artifacts live, by name then digest.
func StoreDir() string { return pluginhost.ManagedStore() }

// InStore reports whether an artifact sits inside the directory rta installs
// into for this operator.
//
// The path, and not a name in rta.lock, because pluginhost.Identify resolves
// symlinks before hashing — the store serves through a bin/ directory of
// links, so a managed plugin's origin is already its real path under the
// store. It is also the question `rta doctor` asks to spot a $PATH copy
// shadowing a managed one, asked the same way by every screen that says where
// a plugin came from, so they cannot disagree about which artifact is which.
//
// Only the operator's own store, and never the system root's: the doctor's
// shadow check is a question about the copy `rta plugin install` placed, and a
// system-root copy outranks it in discovery, so it is a shadow like any other.
// InSystemRoot is that other question.
func InStore(path string) bool { return pluginhost.Beneath(path, StoreDir()) }

// InSystemRoot reports whether an artifact sits inside the system root's
// plugins: its store, where an image or a package that ran `rta plugin install`
// at build time put it, or the bin/ directory discovery reads beside it.
//
// The root is the whole of what the question is about, and not its store alone:
// a package that dropped a binary straight into bin/ filled the root as much
// as one that laid out a store, and neither is on a $PATH. False when there is
// no system root.
func InSystemRoot(path string) bool {
	return pluginhost.Beneath(path, pluginhost.SystemStore()) ||
		pluginhost.Beneath(path, pluginhost.SystemBin())
}

// The four places a plugin's bytes can have come from, in the words every
// surface says them in: the TUI's inventory bands and `rta plugin list`'s
// Source column. They say how the bytes arrived, and not who vouches for
// them — the first-party index is only one place an installed plugin can come
// from, so nothing is named for it.
//
// **The system root is a word of its own, and "installed by rta" would not be
// true of it.** A full image's plugins were put there by `rta plugin install`
// at build time, but they are not the reader's installs: `rta plugin remove`
// and `upgrade` answer that such a plugin "is not managed by rta", `untrust`
// refuses to withdraw what the root trusts, and where the operator's own store
// holds a copy of the same plugin it is the root's that runs. It is not "found
// on $PATH" either — nothing in an image is on a $PATH, and the root keeps its
// own rta.lock saying where each plugin came from (ReadSystemLock), which is
// the opposite of "holds no record of".
const (
	SourceBuiltIn   = "built in"
	SourceInstalled = "installed by rta"
	SourceSystem    = "in the system root"
	SourcePath      = "found on $PATH"
)

// SourceOf says which of them the plugin served from path came from: the empty
// path of one compiled into the rta binary is built in, an artifact rta placed
// from an index into the operator's own store is installed, one in the system
// root an image or a package filled is the root's, and the rest — whatever is
// on $PATH — is bytes rta did not place and holds no record of.
//
// In that order, because the two can be one directory: an image build runs
// `rta plugin install` with the root as its data directory, and what it has just
// installed there is, for that run, the operator's own.
func SourceOf(path string) string {
	switch {
	case path == "":
		return SourceBuiltIn
	case InStore(path):
		return SourceInstalled
	case InSystemRoot(path):
		return SourceSystem
	}
	return SourcePath
}

// BinDir is the directory of current-version symlinks. It is what discovery
// scans (appended after $PATH) and what an operator adds to their own $PATH
// if they want managed plugins visible to other tools. The path itself is
// pluginhost's, because discovery lives there and the import runs this way.
func BinDir() string { return pluginhost.ManagedBin() }

// symlink is os.Symlink, overridable so a test can stand between the artifact
// being placed and the install being recorded, and see whether an exit can land
// there.
var symlink = os.Symlink

// removeAll is os.RemoveAll, overridable so a test can stand between a
// remove's or a prune's writes and see whether an exit can land there.
var removeAll = os.RemoveAll

// place moves a staged, verified binary into the store and points the bin/
// symlink at it. The rename is atomic on one filesystem — staging lives under
// the same data dir for exactly that — and the symlink swap goes through a
// temporary name so no reader ever sees a missing or half-written link.
func place(name, digest, staged string) (string, *view.Error) {
	dir := filepath.Join(StoreDir(), name, digest)
	if _, err := paths.EnsureData(); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	binary := pluginhost.BinaryName(name)
	dest := filepath.Join(dir, binary)
	if err := os.Rename(staged, dest); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	if err := os.MkdirAll(BinDir(), 0o700); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	// Relative, so the whole data dir can move — a backup restored under
	// another home keeps working.
	target := filepath.Join("..", "store", name, digest, binary)
	tmp := filepath.Join(BinDir(), "."+binary+".swap")
	_ = os.Remove(tmp)
	if err := symlink(target, tmp); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	if err := os.Rename(tmp, filepath.Join(BinDir(), binary)); err != nil {
		_ = os.Remove(tmp)
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	return dest, nil
}

// CurrentDigest reads which digest the bin/ symlink points at — the store's
// own statement of "current", read from the layout rather than from the
// lockfile, so the two can be compared instead of one asserting both.
func CurrentDigest(name string) (string, bool) {
	target, err := os.Readlink(filepath.Join(BinDir(), pluginhost.BinaryName(name)))
	if err != nil {
		return "", false
	}
	parts := strings.Split(target, "/")
	// ../store/<name>/<digest>/<binary>
	if len(parts) != 5 || parts[1] != "store" || parts[2] != name {
		return "", false
	}
	return parts[3], true
}

// StoredDigests lists the digests the store holds for one plugin, sorted —
// the current one and any kept for rollback.
func StoredDigests(name string) []string {
	entries, err := os.ReadDir(filepath.Join(StoreDir(), name))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// removeStored deletes one plugin's bin link and every stored digest.
func removeStored(name string) *view.Error {
	link := filepath.Join(BinDir(), pluginhost.BinaryName(name))
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return view.Errorf("plugin.remove.store", "%v", err)
	}
	if err := removeAll(filepath.Join(StoreDir(), name)); err != nil {
		return view.Errorf("plugin.remove.store", "%v", err)
	}
	return nil
}
