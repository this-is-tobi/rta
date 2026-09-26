package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A name and --all say two different things about what to touch, and a bulk
// flag that quietly won an argument with an explicit argument would be the
// worst of the three possible behaviours. `profile repin` settled the grammar;
// these commands follow it rather than inventing a second one.
func TestBulkPluginCommandsRefuseAnAmbiguousScope(t *testing.T) {
	cases := []struct {
		name string
		args []string
		says string
	}{
		{"upgrade with neither", []string{"plugin", "upgrade"}, "name a plugin, or pass --all"},
		{"upgrade with both", []string{"plugin", "upgrade", "pg", "--all"}, "say two different things"},
		{"untrust with neither", []string{"plugin", "untrust"}, "name a plugin, or pass --all"},
		{"untrust with both", []string{"plugin", "untrust", "pg", "--all"}, "say two different things"},
		{"remove with neither", []string{"plugin", "remove"}, "name a plugin, or pass --all"},
		{"remove with both", []string{"plugin", "remove", "pg", "--all"}, "say two different things"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, registry.New(), tc.args...)
			verr := refusal(t, err)
			if !strings.Contains(verr.Message, tc.says) {
				t.Errorf("message = %q, want it to say %q", verr.Message, tc.says)
			}
			if !strings.HasSuffix(verr.Code, ".scope") {
				t.Errorf("code = %q, want a scope refusal", verr.Code)
			}
		})
	}
}

// --index narrows which supply chain a sweep pulls from, which is a statement
// about a set. Against one named plugin there is no set to narrow — the lock
// already records the index it came from — so accepting the flag there would
// be accepting a filter that either agrees with the record or contradicts it,
// and neither is worth a code path.
func TestTheIndexFilterIsRefusedWithoutASweep(t *testing.T) {
	_, _, err := run(t, registry.New(), "plugin", "upgrade", "pg", "--index", "official")
	verr := refusal(t, err)
	if !strings.Contains(verr.Message, "--index narrows a sweep") {
		t.Errorf("message = %q, want it to say what --index is for", verr.Message)
	}
}

// Sweeping the whole trust store or the whole managed store is a different
// size of act from the single-name form, and `plugin remove` already holds
// that line for one plugin. Neither may proceed on a bare command.
func TestBulkWithdrawalsNeedConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "untrust", "--all"},
		{"plugin", "remove", "--all"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := run(t, registry.New(), args...)
			verr := refusal(t, err)
			if verr.Code != CodeConfirmRequired {
				t.Errorf("code = %q, want %q", verr.Code, CodeConfirmRequired)
			}
			if !strings.Contains(verr.Hint, "--yes") {
				t.Errorf("hint = %q, want it to name --yes", verr.Hint)
			}
		})
	}
}

// With nothing installed there is nothing to sweep, and that is an answer
// rather than a failure — the shape `plugin outdated` already has.
func TestASweepWithNothingInstalledSaysSo(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "upgrade", "--all"},
		{"plugin", "untrust", "--all", "--yes"},
		{"plugin", "remove", "--all", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, errOut, err := run(t, registry.New(), args...)
			if err != nil {
				t.Fatalf("%v failed: %v (%s)", args, err, errOut)
			}
			if !strings.Contains(out, "no plugin") && !strings.Contains(out, "nothing") {
				t.Errorf("out = %q, want it to say there was nothing to do", out)
			}
		})
	}
}

// `outdated --index` answers the same question against one supply chain. It
// reads nothing and fetches nothing, so the only thing to hold it to is that
// the flag exists and narrows rather than being ignored.
func TestOutdatedAcceptsAnIndexFilter(t *testing.T) {
	// md, which draws an empty listing's sentence wherever it is written.
	out, errOut, err := run(t, registry.New(), "plugin", "outdated", "--index", "official", "-o", "md")
	if err != nil {
		t.Fatalf("outdated --index failed: %v (%s)", err, errOut)
	}
	if !strings.Contains(out, "no plugin is installed") {
		t.Errorf("out = %q", out)
	}
}

// An empty plugin listing is a sentence on a screen and a table to a parser.
// The sentence in place of the table was what every format got: `-o json |
// jq '.rows[]'` met a text view, and -o csv refused one and exited 2. Pretty
// output into a pipe is a parser's too, so it gets the headings.
//
// `plugin trust` with nothing waiting answered with key/value pairs, so its
// shape changed on the machine with nothing to approve. Discovery reads $PATH
// and the system root, so both are emptied: a developer's own installed
// plugins are not this test's to find.
func TestAnEmptyPluginListingIsATableToAParser(t *testing.T) {
	saved := isTTY
	t.Cleanup(func() { isTTY = saved })
	t.Setenv("PATH", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", t.TempDir())
	for _, c := range []struct {
		args        []string
		say, header string
	}{
		{[]string{"plugin", "outdated"}, "no plugin is installed", "Plugin,Installed,Available,Index"},
		{[]string{"plugin", "index", "list"}, "no index is attached", "Index,Origin,Pinned,Plugins,Problems"},
		{[]string{"plugin", "allow"}, "No installed plugin asks", "Plugin,Asks for,Status,To allow"},
		{[]string{"plugin", "trust"}, "Nothing is waiting", "Plugin,Digest,Artifact,To load it"},
	} {
		name := strings.Join(c.args, " ")
		isTTY = func() bool { return true }
		out, errOut, err := run(t, registry.New(), append(c.args, "-o", "pretty")...)
		if err != nil || !strings.Contains(out, c.say) {
			t.Errorf("%s: pretty on a terminal = %q (%v, %s), want the sentence", name, out, err, errOut)
		}
		isTTY = func() bool { return false }
		out, errOut, err = run(t, registry.New(), append(c.args, "-o", "pretty")...)
		if err != nil || strings.Contains(out, c.say) || !strings.Contains(out, strings.ToUpper(strings.Split(c.header, ",")[0])) {
			t.Errorf("%s: pretty into a pipe = %q (%v, %s), want the headings and no sentence", name, out, err, errOut)
		}
		out, errOut, err = run(t, registry.New(), append(c.args, "-o", "md")...)
		if err != nil || !strings.Contains(out, c.say) {
			t.Errorf("%s: md = %q (%v, %s), want the sentence", name, out, err, errOut)
		}
		out, errOut, err = run(t, registry.New(), append(c.args, "-o", "csv")...)
		if err != nil || strings.TrimSpace(out) != c.header {
			t.Errorf("%s: csv = %q (%v, %s), want the header row alone", name, out, err, errOut)
		}
	}
}

// The sweep's report is where an operator finds out what happened, and the two
// things it must never blur are "moved" and "held back". A skipped plugin that
// rendered like an upgraded one would be a silent failure of the guard; a
// skipped plugin that rendered like a crash would send somebody debugging an
// index that is fine.
func TestTheSweepReportSeparatesMovedHeldBackAndFailed(t *testing.T) {
	outcomes := []plugindist.UpgradeOutcome{
		{Upgraded: plugindist.Upgraded{
			Report:     plugindist.Report{Name: "cnpg", Version: "0.4.0", Digest: "bbbbbbbbbbbbbbbb"},
			FromDigest: "aaaaaaaaaaaaaaaa", FromVersion: "0.3.0",
			Diff: []string{"+ cnpg.backup.list  read"}}},
		{Upgraded: plugindist.Upgraded{
			Report:     plugindist.Report{Name: "kube", Version: "0.9.0", Digest: "dddddddddddddddd"},
			FromDigest: "cccccccccccccccc", FromVersion: "0.9.0",
			Widenings: []string{"+ kube.node.drain  destructive, needs a grant"}}},
		{Upgraded: plugindist.Upgraded{
			Report: plugindist.Report{Name: "pg", Version: "1.2.0", Digest: "eeeeeeeeeeeeeeee"},
			// An index that has not moved: the pin stands and nothing was fetched.
			FromDigest: "eeeeeeeeeeeeeeee", FromVersion: "1.2.0", UpToDate: true}},
		{Upgraded: plugindist.Upgraded{Report: plugindist.Report{Name: "ghost", Index: "gone"}},
			Problem: view.Errorf("plugin.install.index", "no index called gone is attached")},
	}

	v, verr := upgradeAllView(outcomes, false)
	if verr == nil {
		t.Fatal("a sweep that held one back and failed another returned no error; nothing would exit non-zero")
	}
	if !strings.Contains(verr.Message, "kube") {
		t.Errorf("message = %q, want it to name the plugin held back", verr.Message)
	}

	sections, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("view = %T, want Sections so the held-back detail travels with the table", v)
	}
	table, ok := sections.Items[0].View.(view.Table)
	if !ok {
		t.Fatalf("section = %T, want a Table", sections.Items[0].View)
	}
	if len(table.Rows) != 4 {
		t.Fatalf("rows = %v, want one per plugin", table.Rows)
	}

	result := map[string]string{}
	row := map[string][]string{}
	for _, r := range table.Rows {
		result[r[0]] = r[len(r)-1]
		row[r[0]] = r
	}

	// A held-back row has to name the pin that still stands and the version it
	// declined to move to, or it tells somebody their plugin was not upgraded
	// without telling them what they have.
	if got := row["kube"][1]; got != "0.9.0 → 0.9.0" {
		t.Errorf("held-back version cell = %q, want both versions", got)
	}
	if !strings.Contains(row["kube"][2], "kept") {
		t.Errorf("held-back digest cell = %q, want the digest still in force", row["kube"][2])
	}
	for name, want := range map[string]string{
		"cnpg":  "upgraded",
		"kube":  "held back",
		"pg":    "up to date",
		"ghost": "failed",
	} {
		if !strings.Contains(result[name], want) {
			t.Errorf("%s result = %q, want it to say %q", name, result[name], want)
		}
	}

	// The reasons are the whole point of holding it back, so they travel in
	// the report rather than behind another command the operator has to run.
	joined := ""
	for _, w := range sections.Warnings {
		joined += w.Message + " " + w.Hint + " "
	}
	if !strings.Contains(joined, "kube.node.drain") {
		t.Errorf("warnings = %q, want the widening that caused the skip", joined)
	}
	if !strings.Contains(joined, "no index called gone") {
		t.Errorf("warnings = %q, want the failure's own message", joined)
	}
}

// A sweep where everything moved is not a failure, and must not exit non-zero.
func TestACleanSweepReturnsNoError(t *testing.T) {
	_, verr := upgradeAllView([]plugindist.UpgradeOutcome{
		{Upgraded: plugindist.Upgraded{
			Report:     plugindist.Report{Name: "pg", Version: "1.2.0", Digest: "bbbbbbbbbbbbbbbb"},
			FromDigest: "aaaaaaaaaaaaaaaa", FromVersion: "1.1.0"}},
	}, false)
	if verr != nil {
		t.Fatalf("a clean sweep returned %v", verr)
	}
}

// refusal is the *view.Error a command returned, through whatever the exit-code
// contract wraps it in.
func refusal(t *testing.T, err error) *view.Error {
	t.Helper()
	if err == nil {
		t.Fatal("the command was accepted")
	}
	var verr *view.Error
	if !asViewError(err, &verr) {
		t.Fatalf("err = %v (%T), want a *view.Error", err, err)
	}
	return verr
}
