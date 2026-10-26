package plugindist

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/view"
)

// rta.lock records what rta computed, per installed plugin — never what a
// manifest claimed. Every field is either observed by rta (the
// digest it hashed, the time, the signature check's outcome) or chosen by the
// operator (which index, which URL was fetched). The version is the one
// labelled exception: it is the index's claim, recorded *as* the claim so
// upgrade can print "0.1.0 → 0.2.0", and nothing resolves through it.
//
// Plain JSON behind the same file lock plugintrust uses, and deliberately not
// MAC-sealed, following trusted.json rather than the grant file: nothing
// *authorizes* through this record. A forger who can write the data dir could
// also swap store binaries — and gains nothing by either, because trust and
// authorization bind to digests recomputed from the bytes on every Open. What
// a forged lockfile can lie about is provenance rows in reports, and a lie
// there is caught by the digest mismatch doctor already checks.

// LockEntry is one installed plugin's record.
type LockEntry struct {
	// Name is the namespace, which is also the store directory and the
	// binary's suffix.
	Name string `json:"name"`
	// Digest is what rta hashed from the installed binary — the value pins
	// and trust bind to, and never the manifest's checksum.
	Digest string `json:"digest"`
	// Version is the index's claim at install time, recorded as a claim.
	Version string `json:"version"`
	// Index and URL are where the operator chose to fetch from.
	Index string `json:"index"`
	URL   string `json:"url"`
	// Signature is the outcome of the recorded-never-required check:
	// "none stated", "not checked (cosign not installed)", "verified", or
	// "FAILED verification" — the loud spelling, because this records rather
	// than gates and a failure must at least be unmissable.
	Signature string `json:"signature"`
	// InstalledAt is when rta placed the bytes.
	InstalledAt time.Time `json:"installedAt"`
}

type lockDoc struct {
	Plugins []LockEntry `json:"plugins"`
}

// LockPath is where the record lives, beside the store it describes.
func LockPath() string { return filepath.Join(paths.Data(), "plugins", "rta.lock") }

// SystemLockPath is the same record under the system root (paths.System):
// what an image or a package installed at build time, read by `doctor` and
// never written after. "" when there is no system root.
func SystemLockPath() string {
	if root := paths.System(); root != "" {
		return filepath.Join(root, "plugins", "rta.lock")
	}
	return ""
}

// ReadSystemLock is the system root's record, empty when there is none.
func ReadSystemLock() []LockEntry {
	p := SystemLockPath()
	if p == "" {
		return nil
	}
	return readLockAt(p)
}

// ReadLock lists what is recorded, sorted by name. Every failure answers the
// empty list: the lock is provenance, and a plugin whose record cannot be
// read shows up as "found on $PATH and not ours" rather than blocking
// anything — the informational direction of plugintrust.Load's fail-closed.
func ReadLock() []LockEntry { return readLockAt(LockPath()) }

// ReadLockForChange is the record as a command that acts on it reads it:
// install, upgrade, remove and prune, and detaching an index, each of which
// decides something from what the record holds before it changes anything.
// A file that is not there is the empty record, a store nothing was installed
// into yet; one that is there and cannot be read or parsed refuses with
// plugin.lock.unreadable. ReadLock's empty answer for that file is right for
// a listing and wrong for every one of these: an install got past the check
// that the plugin was not installed already, a removal or an upgrade called
// a plugin the record lists not managed by rta, a prune chose which copy was
// current without the record's half of the answer, and a detach took an index
// from under the plugins that came from it. It is also how `rta doctor` says
// the record is not the empty one ReadLock shows.
func ReadLockForChange() ([]LockEntry, *view.Error) { return readLockForRewrite(LockPath()) }

// lockedForChange is LockedFor over ReadLockForChange.
func lockedForChange(name string) (LockEntry, bool, *view.Error) {
	entries, verr := ReadLockForChange()
	if verr != nil {
		return LockEntry{}, false, verr
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true, nil
		}
	}
	return LockEntry{}, false, nil
}

func readLockAt(path string) []LockEntry {
	raw, err := atomicfile.ReadFile(path)
	if err != nil {
		return nil
	}
	entries, err := parseLock(raw)
	if err != nil {
		return nil
	}
	return entries
}

// readLockForRewrite is mutateLock's reading of the record, and
// ReadLockForChange's, and the one that fails, for plugintrust.read's
// reason: what it returns is written back. readLockAt's empty answer for a
// file that does not parse would be rewritten holding only the change being
// made, every other plugin's entry dropped and success reported, after which
// `rta plugin upgrade` calls them not managed by rta and the sweep and prune
// pass them over. The record is plain JSON an operator may edit, so a
// trailing comma is all that takes. Only a file that is not there is the
// empty record: a store nothing was installed into yet.
func readLockForRewrite(path string) ([]LockEntry, *view.Error) {
	raw, err := atomicfile.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var entries []LockEntry
	if err == nil {
		entries, err = parseLock(raw)
	}
	if err != nil {
		return nil, view.Errorf("plugin.lock.unreadable", "reading %s: %v", path, err).
			WithHint("nothing was changed and the file is as it was, since what rta decides from " +
				"the record cannot be decided from one it could not read: fix it, or remove it and " +
				"install each plugin again")
	}
	return entries, nil
}

func parseLock(raw []byte) ([]LockEntry, error) {
	var doc lockDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make([]LockEntry, 0, len(doc.Plugins))
	for _, e := range doc.Plugins {
		if e.Name != "" && e.Digest != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LockedFor finds one plugin's record.
func LockedFor(name string) (LockEntry, bool) {
	for _, e := range ReadLock() {
		if e.Name == name {
			return e, true
		}
	}
	return LockEntry{}, false
}

// mutateLock applies fn to the current entries under the file lock and
// writes the result. fn returns the new list; the read happens inside the
// lock for plugintrust's reason — the dangerous direction is a lost removal.
//
// land, when not nil, is the rest of the change the record will describe —
// an install's artifact placed and trusted, a removal's trust withdrawn and
// copies deleted — and it runs between the read and the write, under the
// same lock. Run before mutateLock, as it was, those writes were done by the
// time the record was found unreadable: a refused install had placed, linked
// and trusted the artifact, and a refused removal had untrusted and deleted
// it, everything but the line saying so. The callers read the record first
// as well (ReadLockForChange), for an answer before anything is fetched;
// this read, held until the write, is the one no other process can change
// between the check and the rewrite.
func mutateLock(land func() *view.Error, fn func([]LockEntry) []LockEntry) *view.Error {
	dir := filepath.Dir(LockPath())
	if _, err := paths.EnsureData(); err != nil {
		if verr := filelock.Unwritable(err); verr != nil {
			return verr
		}
		return view.Errorf("plugin.lock.write", "%v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return view.Errorf("plugin.lock.write", "%v", err)
	}
	release, err := filelock.Acquire(filepath.Join(dir, "rta.lock.lock"),
		5*time.Second, 10*time.Millisecond, 2*time.Second)
	if err != nil {
		return filelock.Failed(err, "plugin.lock.lock", "acquiring the lockfile's lock")
	}
	defer release()

	current, verr := readLockForRewrite(LockPath())
	if verr != nil {
		return verr
	}
	if land != nil {
		if verr := land(); verr != nil {
			return verr
		}
	}
	entries := fn(current)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	raw, err := json.MarshalIndent(lockDoc{Plugins: entries}, "", "  ")
	if err != nil {
		return view.Errorf("plugin.lock.write", "%v", err)
	}
	if err := atomicfile.Write(LockPath(), append(raw, '\n'), 0o644); err != nil {
		return view.Errorf("plugin.lock.write", "%v", err)
	}
	return nil
}

// recordInstall runs land and upserts one plugin's entry (see mutateLock).
func recordInstall(e LockEntry, land func() *view.Error) *view.Error {
	return mutateLock(land, func(entries []LockEntry) []LockEntry {
		kept := entries[:0]
		for _, cur := range entries {
			if cur.Name != e.Name {
				kept = append(kept, cur)
			}
		}
		return append(kept, e)
	})
}

// recordRemoval runs land and drops one plugin's entry (see mutateLock).
func recordRemoval(name string, land func() *view.Error) *view.Error {
	return mutateLock(land, func(entries []LockEntry) []LockEntry {
		kept := entries[:0]
		for _, cur := range entries {
			if cur.Name != name {
				kept = append(kept, cur)
			}
		}
		return kept
	})
}
