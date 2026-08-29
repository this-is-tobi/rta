package plugindist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/filelock"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
	// "FAILED verification" — the loud spelling, because §5 records rather
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

// ReadLock lists what is recorded, sorted by name. Every failure answers the
// empty list: the lock is provenance, and a plugin whose record cannot be
// read shows up as "found on $PATH and not ours" rather than blocking
// anything — the informational direction of plugintrust.Load's fail-closed.
func ReadLock() []LockEntry {
	raw, err := os.ReadFile(LockPath())
	if err != nil {
		return nil
	}
	var doc lockDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	out := make([]LockEntry, 0, len(doc.Plugins))
	for _, e := range doc.Plugins {
		if e.Name != "" && e.Digest != "" {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
func mutateLock(fn func([]LockEntry) []LockEntry) *view.Error {
	dir := filepath.Dir(LockPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("plugin.lock.write", "%v", err)
	}
	release, err := filelock.Acquire(filepath.Join(dir, "rta.lock.lock"),
		5*time.Second, 10*time.Millisecond, 2*time.Second)
	if err != nil {
		return view.Errorf("plugin.lock.lock", "acquiring the lockfile's lock: %v", err)
	}
	defer release()

	entries := fn(ReadLock())
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

// recordInstall upserts one plugin's entry.
func recordInstall(e LockEntry) *view.Error {
	return mutateLock(func(entries []LockEntry) []LockEntry {
		kept := entries[:0]
		for _, cur := range entries {
			if cur.Name != e.Name {
				kept = append(kept, cur)
			}
		}
		return append(kept, e)
	})
}

// recordRemoval drops one plugin's entry.
func recordRemoval(name string) *view.Error {
	return mutateLock(func(entries []LockEntry) []LockEntry {
		kept := entries[:0]
		for _, cur := range entries {
			if cur.Name != name {
				kept = append(kept, cur)
			}
		}
		return kept
	})
}
