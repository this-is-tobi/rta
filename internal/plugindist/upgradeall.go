package plugindist

import (
	"context"
	"io"

	"github.com/this-is-tobi/rta/pkg/view"
)

// UpgradeOutcome is what a sweep did about one plugin. Exactly one of the
// three shapes is true of it: Problem is set and nothing was attempted past
// the failure, Widenings is set and it was refused before anything landed, or
// the embedded Upgraded says what happened — UpToDate, or moved with a Diff.
type UpgradeOutcome struct {
	Upgraded
	Problem *view.Error
}

// Skipped reports whether the plugin was held back because upgrading it would
// widen what it may do.
func (o UpgradeOutcome) Skipped() bool { return len(o.Widenings) > 0 }

// UpgradeAll upgrades every plugin rta has a record of, or every one that came
// from index when index is not empty, and reports one outcome per plugin in
// name order.
//
// **The candidate set is the lock, never Outdated().** Outdated compares
// recorded versions against manifest claims and says of itself that a respin
// under an unchanged version number is invisible to it, for the same reason it
// is invisible to a signature. Picking candidates that way would make a sweep
// skip precisely the event upgrade exists to catch — the same publisher, the
// same version string, different bytes and a wider declaration.
//
// Every upgrade is guarded: one that would hand a plugin more than the
// operator last agreed to is refused before its bytes land, and reported. A
// sweep is the one place nobody is reading the declaration diff as it goes,
// which is exactly why the diff has to be able to stop it.
//
// One plugin's failure never ends the run — the rule Manifests and LoadInto
// already follow, applied to an action rather than a listing. An index
// detached months ago must not be why the other nine went un-upgraded.
func UpgradeAll(ctx context.Context, index string, stderr io.Writer) []UpgradeOutcome {
	return upgradeAll(ctx, index, stderr, false)
}

// PreviewUpgradeAll is UpgradeAll without the durable writes: every fetch,
// every launch, every verdict, and nothing recorded.
func PreviewUpgradeAll(ctx context.Context, index string, stderr io.Writer) []UpgradeOutcome {
	return upgradeAll(ctx, index, stderr, true)
}

func upgradeAll(ctx context.Context, index string, stderr io.Writer, dryRun bool) []UpgradeOutcome {
	var out []UpgradeOutcome
	// ReadLock sorts by name, so the report's order is the operator's own
	// alphabetical one rather than whatever order the file happens to hold.
	for _, e := range ReadLock() {
		if index != "" && e.Index != index {
			continue
		}
		up, verr := upgrade(ctx, e.Name, stderr, dryRun, true)
		if verr != nil && len(up.Widenings) > 0 {
			// A refusal by the guard, not a failure: the run did what it was
			// asked to and the answer was no. Carrying the *view.Error along
			// too would put it in the failure column of every report that
			// tells the two apart by looking.
			out = append(out, UpgradeOutcome{Upgraded: up})
			continue
		}
		if verr != nil {
			out = append(out, UpgradeOutcome{
				Upgraded: Upgraded{Report: Report{Name: e.Name, Version: e.Version, Index: e.Index}},
				Problem:  verr})
			continue
		}
		out = append(out, UpgradeOutcome{Upgraded: up})
	}
	return out
}
