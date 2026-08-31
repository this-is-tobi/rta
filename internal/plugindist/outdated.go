package plugindist

import "sort"

// OutdatedRow is one installed plugin whose index no longer agrees with what
// rta.lock recorded at install or upgrade time.
type OutdatedRow struct {
	Name             string
	Index            string
	InstalledVersion string
	// AvailableVersion is what the index currently claims, set only when
	// Problem is not — a version comparison needs a version to compare against.
	AvailableVersion string
	// Problem is set instead of AvailableVersion when the index no longer has
	// an answer at all: detached, or the manifest is gone from it. Either way
	// there is nothing to compare, which is itself worth a row rather than a
	// silent skip — Manifests' own "one malformed file must not cost the
	// operator the rest" rule, applied per plugin instead of per file.
	Problem string
}

// Outdated compares every installed plugin's recorded version against what
// its index currently claims. Cheap like Search — nothing is fetched or
// executed, so an empty Problem and a version match are only ever a hint that
// nothing has changed, never proof: the version string is the index's claim,
// and `rta plugin upgrade <name>` is what actually re-verifies against the
// bytes. A publisher respinning the same version number under different bytes
// is invisible here for exactly the reason Upgrade's own doc comment names —
// "the same publisher signing a worse plugin verifies perfectly" — a cheap
// check one layer up from a signature has the same blind spot.
func Outdated() []OutdatedRow {
	var rows []OutdatedRow
	for _, e := range ReadLock() {
		listed, verr := Resolve(e.Index + "/" + e.Name)
		if verr != nil {
			rows = append(rows, OutdatedRow{Name: e.Name, Index: e.Index,
				InstalledVersion: e.Version, Problem: verr.Message})
			continue
		}
		if listed.Manifest.Version != e.Version {
			rows = append(rows, OutdatedRow{Name: e.Name, Index: e.Index,
				InstalledVersion: e.Version, AvailableVersion: listed.Manifest.Version})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}
