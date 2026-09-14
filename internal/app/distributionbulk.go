package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The sweeping half of the plugin commands: `upgrade`, `untrust` and `remove`
// applied to everything rather than to one name.
//
// What they have in common is the reason they are allowed to exist at all.
// Each either re-verifies a decision the operator already made (upgrade: these
// bytes replace bytes you approved, and the declaration diff is checked) or
// withdraws one (untrust, remove). None of them manufactures consent, which is
// why `rta plugin trust --all` and `rta plugin allow --all` are absent and
// should stay absent: "this binary may run" and "this binary may read my
// kubeconfig" are the two questions the whole boundary is built to make
// somebody answer one artifact at a time.

// bulkScope resolves the one-or-all grammar these commands share, in the shape
// `profile repin` already settled: a name, or --all, and never both. A bulk
// flag that silently won an argument with an explicit name would be the worst
// of the three available behaviours.
func bulkScope(args []string, all bool, code, verb string) (string, *view.Error) {
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	if name == "" && !all {
		return "", view.Errorf(code, "name a plugin, or pass --all to %s every one of them", verb).
			WithHint("`rta plugin " + verb + " pg`, or `rta plugin " + verb + " --all`")
	}
	if name != "" && all {
		return "", view.Errorf(code,
			"a plugin name and --all say two different things about which plugins to touch").
			WithHint("drop one or the other")
	}
	return name, nil
}

// upgradeAllView turns a sweep into the page an operator reads afterwards.
//
// The table's last column is the only place the three outcomes are told apart,
// so it says which of them happened in words rather than by the absence of
// something: a plugin held back by the guard that rendered like an upgraded
// one would be the guard failing silently, and one that rendered like a
// failure would send somebody debugging an index that is fine.
//
// Held-back plugins carry their reasons into Warnings rather than being
// summarised into a count, because the reasons are the entire point of holding
// them back. Pointing at another command to see them would be handing over the
// answer in instalments — what withOthers exists to avoid one layer down.
func upgradeAllView(outcomes []plugindist.UpgradeOutcome, dryRun bool) (view.View, *view.Error) {
	if len(outcomes) == 0 {
		return view.Text{Body: "no plugin is installed"}, nil
	}

	moved, held := "upgraded", "held back — would gain authority"
	if dryRun {
		moved, held = "would upgrade", "would be held back — would gain authority"
	}

	t := view.Table{Columns: []view.Column{{Name: "Plugin"}, {Name: "Version"},
		{Name: "Digest"}, {Name: "Signature"}, {Name: "Result"}}}
	var warnings []view.Error
	var heldNames, failedNames []string

	for _, o := range outcomes {
		switch {
		case o.Problem != nil:
			failedNames = append(failedNames, o.Name)
			t.Rows = append(t.Rows, []string{o.Name, "—", "—", "—", "failed"})
			warnings = append(warnings, *o.Problem)
		case o.Skipped():
			heldNames = append(heldNames, o.Name)
			// No signature: nothing was verified into the record, because
			// nothing was recorded. An empty cell here would read as "not
			// signed", which is a claim about the artifact rather than about
			// what rta did with it.
			t.Rows = append(t.Rows, []string{o.Name,
				o.FromVersion + " → " + o.Version,
				shortDigest(o.FromDigest) + " (kept)", "—", held})
			warnings = append(warnings, *view.Errorf("plugin.upgrade.authority",
				"%s was not upgraded: %s", o.Name, strings.Join(o.Widenings, "; ")).
				WithHint("`rta plugin upgrade " + o.Name + " --dry-run` shows the whole declaration " +
					"diff; the same command without it upgrades once you have read the change"))
		case o.UpToDate:
			t.Rows = append(t.Rows, []string{o.Name, o.Version,
				shortDigest(o.Digest), o.Signature, "up to date"})
		default:
			t.Rows = append(t.Rows, []string{o.Name,
				o.FromVersion + " → " + o.Version,
				shortDigest(o.FromDigest) + " → " + shortDigest(o.Digest),
				o.Signature, moved})
		}
	}

	v := view.View(t)
	if len(warnings) > 0 {
		v = view.Sections{
			Items:    []view.Section{{ID: "upgrades", Title: "Upgrades", View: t}},
			Warnings: warnings,
		}
	}
	if len(heldNames) == 0 && len(failedNames) == 0 {
		return v, nil
	}

	// Rendered first, then refused: the page is the answer, and the error
	// exists so a sweep that did not finish does not exit 0 into a pipeline
	// that takes silence for success.
	var parts []string
	if len(heldNames) > 0 {
		parts = append(parts, fmt.Sprintf("%s held back (%s)",
			plural(len(heldNames), "plugin", "plugins"), strings.Join(heldNames, ", ")))
	}
	if len(failedNames) > 0 {
		parts = append(parts, fmt.Sprintf("%s failed (%s)",
			plural(len(failedNames), "plugin", "plugins"), strings.Join(failedNames, ", ")))
	}
	return v, view.Errorf("plugin.upgrade.incomplete", "%s", strings.Join(parts, ", ")).
		WithHint("every other plugin was upgraded; the warnings above say what stopped these")
}

func runPluginUpgradeAll(cmd *cobra.Command, opts *globalOpts, index string) error {
	sweep := plugindist.UpgradeAll
	if opts.dryRun {
		sweep = plugindist.PreviewUpgradeAll
	}
	v, verr := upgradeAllView(sweep(cmd.Context(), index, cmd.ErrOrStderr()), opts.dryRun)
	if renderErr := renderView(cmd, opts, v); renderErr != nil {
		return renderErr
	}
	if verr != nil {
		return verr
	}
	return nil
}

// runPluginUntrustAll withdraws every approval in the operator's own record.
//
// The system root's entries are left alone and not counted: rta reads that
// file and never writes it, so sweeping into it would produce a per-entry
// refusal for every one of them and a report that claims less than it did.
func runPluginUntrustAll(cmd *cobra.Command, opts *globalOpts) error {
	if !opts.dryRun && !opts.yes {
		return &view.Error{
			Code:    CodeConfirmRequired,
			Message: "withdrawing trust from every plugin artifact needs confirmation",
			Hint:    "re-run with --yes to confirm, or --dry-run to preview",
		}
	}
	remove := plugintrust.Remove
	if opts.dryRun {
		remove = plugintrust.PreviewRemove
	}
	total := 0
	for _, e := range plugintrust.Load().Entries() {
		if e.System {
			continue
		}
		n, verr := remove(e.Digest)
		if verr != nil {
			return verr
		}
		total += n
	}
	if total == 0 {
		return renderView(cmd, opts, view.Text{Body: "no plugin artifact is trusted"})
	}
	verb, tail := "withdrew", "none of them will load again; a session already running keeps what it loaded"
	if opts.dryRun {
		verb, tail = "would withdraw", "none of them would load again"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s — %s\n", verb, plural(total, "approval", "approvals"), tail)
	return nil
}

// runPluginRemoveAll uninstalls every managed plugin, one at a time through
// the same path `remove <name>` takes, and reports what each one left behind.
//
// A failure part-way stops the sweep rather than carrying on: unlike an
// upgrade, where one unreachable index says nothing about the next plugin, a
// remove that fails means the store or the lockfile did not take a write, and
// the honest thing is to stop and say which plugin was being removed when it
// happened.
func runPluginRemoveAll(cmd *cobra.Command, opts *globalOpts) error {
	if !opts.dryRun && !opts.yes {
		return &view.Error{
			Code:    CodeConfirmRequired,
			Message: "uninstalling every managed plugin withdraws trust from every stored artifact and needs confirmation",
			Hint:    "re-run with --yes to confirm, or --dry-run to preview",
		}
	}
	locked := plugindist.ReadLock()
	if len(locked) == 0 {
		return renderView(cmd, opts, view.Text{Body: "no plugin is installed"})
	}
	remove := plugindist.Remove
	removedLabel, artifactsNote := "removed", "trust withdrawn from each"
	if opts.dryRun {
		remove = plugindist.PreviewRemove
		removedLabel, artifactsNote = "would remove", "trust would be withdrawn from each"
	}

	t := view.Table{Columns: []view.Column{{Name: "Plugin"}, {Name: "Artifacts"},
		{Name: "Still stated by"}}}
	orphaned := false
	for _, e := range locked {
		removed, verr := remove(e.Name)
		if verr != nil {
			return verr
		}
		orphans := "—"
		if len(removed.Orphans) > 0 {
			orphans = strings.Join(removed.Orphans, ", ")
			orphaned = true
		}
		t.Rows = append(t.Rows, []string{removed.Name, fmt.Sprint(len(removed.Digests)), orphans})
	}

	body := fmt.Sprintf("%s %s — %s", removedLabel,
		plural(len(t.Rows), "plugin", "plugins"), artifactsNote)
	if orphaned {
		// Named rather than cleaned, the same as the single-name form: the
		// config file is the operator's, and `rta doctor` keeps reporting the
		// orphans until they decide.
		body += ". The config statements listed are yours to keep or delete"
	}
	return renderView(cmd, opts, view.Sections{Items: []view.Section{
		{ID: "removed", Title: "Removed", View: t},
		{ID: "summary", Title: "Summary", View: view.Text{Body: body}},
	}})
}

// completeAttachedIndexes offers the index names --index will actually match
// against: the ones attached right now, not the ones a manifest mentions.
func completeAttachedIndexes(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	indexes := plugindist.Indexes()
	out := make([]cobra.Completion, 0, len(indexes))
	for _, ix := range indexes {
		out = append(out, ix.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
