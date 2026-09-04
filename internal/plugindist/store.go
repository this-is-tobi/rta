package plugindist

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
func StoreDir() string { return filepath.Join(paths.Data(), "plugins", "store") }

// BinDir is the directory of current-version symlinks. It is what discovery
// scans (appended after $PATH) and what an operator adds to their own $PATH
// if they want managed plugins visible to other tools. The path itself is
// pluginhost's, because discovery lives there and the import runs this way.
func BinDir() string { return pluginhost.ManagedBin() }

// binaryName is the on-disk name for a namespace on this machine — with
// `.exe` on Windows, because that is what discovery looks for and what the OS
// will run.
func binaryName(name string) string { return pluginhost.BinaryName(name) }

// symlink is os.Symlink, overridable so a test can make it fail the way
// Windows does without Developer Mode. See place.
var symlink = os.Symlink

// artifactName is what an index calls the same binary, and it is deliberately
// not binaryName: a manifest describes six platforms from whichever one
// generated it, so the host's own suffix has no business in any of them.
func artifactName(name string) string { return pluginhost.Prefix + name }

// place moves a staged, verified binary into the store and points the bin/
// symlink at it. The rename is atomic on one filesystem — staging lives under
// the same data dir for exactly that — and the symlink swap goes through a
// temporary name so no reader ever sees a missing or half-written link.
func place(name, digest, staged string) (string, *view.Error) {
	dir := filepath.Join(StoreDir(), name, digest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	dest := filepath.Join(dir, binaryName(name))
	if err := os.Rename(staged, dest); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	if err := os.MkdirAll(BinDir(), 0o755); err != nil {
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	// Relative, so the whole data dir can move — a backup restored under
	// another home keeps working.
	target := filepath.Join("..", "store", name, digest, binaryName(name))
	tmp := filepath.Join(BinDir(), "."+binaryName(name)+".swap")
	_ = os.Remove(tmp)
	// A symlink where the OS will make one, a copy where it will not.
	//
	// Windows refuses an unprivileged symlink unless Developer Mode is on —
	// ERROR_PRIVILEGE_NOT_HELD — so on a stock machine this is the step that
	// ends the install, after the artifact has been fetched, hashed, launched
	// and approved. Falling back costs a second copy of the binary and keeps
	// every property the link had: bin/ still holds exactly one current
	// version, the store still holds the rest for rollback, and CurrentDigest
	// still reads which from the layout — by hashing it, which is a stronger
	// answer than a link target anyway, since a target is a claim and a hash
	// is the thing itself.
	// A var so a test can take the fallback on a machine that symlinks
	// happily; there is no Windows in reach to prove it the honest way.
	if err := symlink(target, tmp); err != nil {
		if verr := copyFile(dest, tmp); verr != nil {
			return "", verr
		}
	}
	if err := os.Rename(tmp, filepath.Join(BinDir(), binaryName(name))); err != nil {
		_ = os.Remove(tmp)
		return "", view.Errorf("plugin.install.place", "%v", err)
	}
	return dest, nil
}

// copyFile writes src to dst, executable, replacing whatever was there.
func copyFile(src, dst string) *view.Error {
	in, err := os.Open(src)
	if err != nil {
		return view.Errorf("plugin.install.place", "%v", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return view.Errorf("plugin.install.place", "%v", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return view.Errorf("plugin.install.place", "%v", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return view.Errorf("plugin.install.place", "%v", err)
	}
	return nil
}

// CurrentDigest reads which digest the bin/ symlink points at — the store's
// own statement of "current", read from the layout rather than from the
// lockfile, so the two can be compared instead of one asserting both.
func CurrentDigest(name string) (string, bool) {
	current := filepath.Join(BinDir(), binaryName(name))
	target, err := os.Readlink(current)
	if err != nil {
		// Not a link: place fell back to a copy, which is the ordinary case
		// on a Windows without Developer Mode. Hash it and see which stored
		// version it is. Slower — it reads the whole binary — and a better
		// answer than a link target, which only ever said what somebody
		// intended; this says what is there.
		return storedDigestOf(name, current)
	}
	parts := strings.Split(filepath.ToSlash(target), "/")
	// ../store/<name>/<digest>/<binary>
	if len(parts) != 5 || parts[1] != "store" || parts[2] != name {
		return "", false
	}
	return parts[3], true
}

// storedDigestOf hashes the current binary and returns the stored digest it
// matches. A digest that is not in the store is not "current" — it is a file
// somebody put in bin/ by hand, and answering with it would let anything
// dropped there claim to be the installed version.
func storedDigestOf(name, path string) (string, bool) {
	sum, verr := digestFile(path)
	if verr != nil {
		return "", false
	}
	for _, held := range StoredDigests(name) {
		if held == sum {
			return sum, true
		}
	}
	return "", false
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
	link := filepath.Join(BinDir(), binaryName(name))
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return view.Errorf("plugin.remove.store", "%v", err)
	}
	if err := os.RemoveAll(filepath.Join(StoreDir(), name)); err != nil {
		return view.Errorf("plugin.remove.store", "%v", err)
	}
	return nil
}
