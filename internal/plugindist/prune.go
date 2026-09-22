package plugindist

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Pruned is what one plugin gave up: the stored versions nothing runs.
type Pruned struct {
	Name string
	// Kept are the digests that stay — the one bin/ points at and the one
	// rta.lock records, which agree unless an install is mid-way.
	Kept []string
	// Removed are the digests taken out, trust withdrawn from each.
	Removed []string
	// Bytes is what they held, or would have.
	Bytes int64
	// Unclear marks a plugin whose store names no current version at all —
	// no bin/ link and no lock record. It is left exactly as it is, and said
	// so: nothing here may guess which copy somebody runs.
	Unclear bool
}

// Prune removes every stored version of every managed plugin that nothing
// runs, and reports what went.
//
// The store keeps a previous digest's directory so a rollback is a rename
// rather than a re-download (store.go), and nothing ever took the older ones
// out: an upgrade added a directory and removed none, so a machine that
// followed a plugin through ten releases held ten copies of it — a gigabyte
// and a half, measured, for twelve plugins. What stays is what runs: the
// digest bin/ points at and the one rta.lock records. A version wanted back
// is a re-install away, which is the distance upgrade's own help already
// promises. Trust is withdrawn from each pruned digest the way remove
// withdraws it: the approval was for those bytes, and the bytes are going.
func Prune() ([]Pruned, *view.Error) { return prune(false) }

// PreviewPrune answers what Prune would do without touching the store or
// the trust record, for `rta plugin prune --dry-run`.
func PreviewPrune() ([]Pruned, *view.Error) { return prune(true) }

func prune(dryRun bool) ([]Pruned, *view.Error) {
	entries, err := os.ReadDir(StoreDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, view.Errorf("plugin.prune.store", "%v", err)
	}
	var out []Pruned
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		keep := map[string]bool{}
		if cur, ok := CurrentDigest(name); ok {
			keep[cur] = true
		}
		if l, ok := LockedFor(name); ok && l.Digest != "" {
			keep[l.Digest] = true
		}
		p := Pruned{Name: name}
		stored := StoredDigests(name)
		if len(keep) == 0 {
			p.Unclear, p.Kept = true, stored
			out = append(out, p)
			continue
		}
		for _, d := range stored {
			if keep[d] {
				p.Kept = append(p.Kept, d)
				continue
			}
			p.Removed = append(p.Removed, d)
			p.Bytes += dirSize(filepath.Join(StoreDir(), name, d))
		}
		if len(p.Removed) == 0 {
			continue
		}
		if !dryRun {
			for _, d := range p.Removed {
				// By digest, as remove does: untrusting by name would also
				// revoke a same-named binary the operator trusted on $PATH.
				if _, verr := plugintrust.Remove(d); verr != nil {
					return nil, verr
				}
				if err := os.RemoveAll(filepath.Join(StoreDir(), name, d)); err != nil {
					return nil, view.Errorf("plugin.prune.store", "%v", err)
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// dirSize is the bytes under dir, for the receipt. An entry that cannot be
// read counts as nothing rather than failing the prune over a number.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an entry that cannot be read is worth nothing on the receipt, and no reason to stop counting the rest
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
