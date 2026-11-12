package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

var (
	installedDigest = strings.Repeat("a", 64)
	olderDigest     = strings.Repeat("b", 64)
)

// heldBack is what upgrade answers for hello at 1.4.0 when index lab claims
// 1.3.2, and movedBack the move once somebody agreed to it.
func heldBack() plugindist.Upgraded {
	return plugindist.Upgraded{Report: plugindist.Report{Name: "hello", Version: "1.3.2", Index: "lab"},
		FromDigest: installedDigest, FromVersion: "1.4.0", Older: true}
}

func movedBack() plugindist.Upgraded {
	up := heldBack()
	up.Digest = olderDigest
	return up
}

// stubMoves stands in for the index and the artifact for one test: the
// upgrade finds an older claim, and every move asked for is recorded.
func stubMoves(t *testing.T) *[]string {
	t.Helper()
	savedUp, savedPreviewUp, savedDown, savedPreviewDown :=
		upgradePlugin, previewUpgradePlugin, downgradePlugin, previewDowngradePlugin
	t.Cleanup(func() {
		upgradePlugin, previewUpgradePlugin, downgradePlugin, previewDowngradePlugin =
			savedUp, savedPreviewUp, savedDown, savedPreviewDown
	})
	var calls []string
	refusal := view.Errorf(plugindist.CodeDowngrade, "index lab now claims hello 1.3.2, older than the 1.4.0 installed")
	upgradePlugin = func(_ context.Context, name string) (plugindist.Upgraded, *view.Error) {
		calls = append(calls, "upgrade "+name)
		return heldBack(), refusal
	}
	previewUpgradePlugin = func(_ context.Context, name string) (plugindist.Upgraded, *view.Error) {
		calls = append(calls, "preview upgrade "+name)
		return heldBack(), refusal
	}
	downgradePlugin = func(_ context.Context, name, to string) (plugindist.Upgraded, *view.Error) {
		calls = append(calls, "downgrade "+name+" "+to)
		return movedBack(), nil
	}
	previewDowngradePlugin = func(_ context.Context, name, to string) (plugindist.Upgraded, *view.Error) {
		calls = append(calls, "preview downgrade "+name+" "+to)
		return movedBack(), nil
	}
	return &calls
}

// nobodyToAsk says nobody is there to ask, for the length of a test.
func nobodyToAsk(t *testing.T) {
	t.Helper()
	saved := confirmTerminal
	t.Cleanup(func() { confirmTerminal = saved })
	confirmTerminal = func() bool { return false }
}

// With nobody to ask, a move back is refused as any unconfirmed change is,
// saying both versions and the index, and nothing past the question is done.
func TestAnOlderClaimWithNobodyToAskNeedsConfirmation(t *testing.T) {
	calls := stubMoves(t)
	nobodyToAsk(t)
	_, _, err := run(t, registry.New(), "plugin", "upgrade", "hello")
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != CodeConfirmRequired || ExitCode(err) != 3 {
		t.Fatalf("err = %v (exit %d), want %s and exit 3", err, ExitCode(err), CodeConfirmRequired)
	}
	for _, want := range []string{"1.3.2", "1.4.0", "lab", "needs confirmation"} {
		if !strings.Contains(verr.Message, want) {
			t.Errorf("message %q does not say %q", verr.Message, want)
		}
	}
	if !strings.Contains(verr.Hint, "--yes") {
		t.Errorf("hint %q does not name --yes", verr.Hint)
	}
	if strings.Join(*calls, ",") != "upgrade hello" {
		t.Errorf("calls = %v, want the question only", *calls)
	}
}

// --yes answers the question, and the move is to the version the refusal
// named, said as the move back it is.
func TestYesTakesTheDowngradeItWasShown(t *testing.T) {
	calls := stubMoves(t)
	nobodyToAsk(t)
	out, errOut, err := run(t, registry.New(), "plugin", "upgrade", "hello", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Join(*calls, ",") != "upgrade hello,downgrade hello 1.3.2" {
		t.Errorf("calls = %v", *calls)
	}
	if got := answerPairs(t, out)["downgraded"]; got != "hello 1.4.0 → 1.3.2" {
		t.Errorf("downgraded = %q in %s", got, out)
	}
}

// A person at a terminal is told both versions and asked; anything but a yes
// changes nothing and exits as a script that did not say --yes does.
func TestAnOlderClaimAtATerminalIsAskedAndANoChangesNothing(t *testing.T) {
	for answer, want := range map[string]string{"n\n": "upgrade hello", "y\n": "upgrade hello,downgrade hello 1.3.2"} {
		calls := stubMoves(t)
		asked := askedOf(t, answer, nil)
		_, errOut, err := run(t, registry.New(), "plugin", "upgrade", "hello")
		if strings.Join(*calls, ",") != want {
			t.Errorf("answer %q: calls = %v, want %s", answer, *calls, want)
		}
		if len(*asked) != 1 || (*asked)[0] != "Move hello back to 1.3.2? [y/N] " {
			t.Errorf("answer %q: asked %q", answer, *asked)
		}
		if !strings.Contains(errOut, "hello 1.4.0 is installed, and index lab now claims 1.3.2 — an older version.") {
			t.Errorf("answer %q: stderr = %q, want both versions said before the question", answer, errOut)
		}
		if answer == "n\n" {
			if ExitCode(err) != 3 || !strings.Contains(errOut, "Not confirmed — nothing was changed.") {
				t.Errorf("a no = exit %d, stderr %q", ExitCode(err), errOut)
			}
		} else if err != nil {
			t.Errorf("a yes = %v", err)
		}
	}
}

// --dry-run previews the move back without asking: it changes nothing, and
// says how to take it.
func TestTheDryRunPreviewsADowngradeWithoutAsking(t *testing.T) {
	calls := stubMoves(t)
	asked := askedOf(t, "y\n", nil)
	out, errOut, err := run(t, registry.New(), "plugin", "upgrade", "hello", "--dry-run", "--no-color")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(*asked) != 0 {
		t.Errorf("a preview asked %q", *asked)
	}
	if strings.Join(*calls, ",") != "preview upgrade hello,preview downgrade hello 1.3.2" {
		t.Errorf("calls = %v", *calls)
	}
	if !strings.Contains(out, "would downgrade") || !strings.Contains(out, "to move it back") {
		t.Errorf("out = %q, want the preview and how to take it", out)
	}
}

// The receipt says a move back is one, and only a preview of one says how to
// take it; a move forward reads as it did.
func TestADowngradeIsSaidAsOne(t *testing.T) {
	isolate(t)
	first := func(kv view.KeyValue) view.Pair { return kv.Pairs[0] }
	last := func(kv view.KeyValue) view.Pair { return kv.Pairs[len(kv.Pairs)-1] }

	if p := first(upgradeAnswer(movedBack(), false)); p.Key != "downgraded" || p.Value != "hello 1.4.0 → 1.3.2" {
		t.Errorf("taken = %+v", p)
	}
	preview := upgradeAnswer(movedBack(), true)
	if first(preview).Key != "would downgrade" || last(preview).Key != "to move it back" ||
		!strings.Contains(last(preview).Value, "--yes") {
		t.Errorf("preview = %+v", preview.Pairs)
	}
	forward := movedBack()
	forward.Older = false
	for dryRun, want := range map[bool]string{false: "upgraded", true: "would upgrade"} {
		kv := upgradeAnswer(forward, dryRun)
		if first(kv).Key != want || last(kv).Key == "to move it back" {
			t.Errorf("forward, dry run %v = %+v", dryRun, kv.Pairs)
		}
	}
}

// A sweep holds a plugin its index moved back, says why with the command that
// moves it, refuses to exit 0, and leaves nothing behind for it; a dry run
// says all of it as what would happen.
func TestTheSweepSaysAPluginWasHeldBackForBeingOlder(t *testing.T) {
	isolate(t)
	outcomes := []plugindist.UpgradeOutcome{{Upgraded: heldBack()}}
	if left := sweepLeftBehind(outcomes, false); len(left) != 0 {
		t.Errorf("left behind = %v, want nothing for a plugin that did not move", left)
	}
	for dryRun, want := range map[bool]struct{ row, refusal, hint string }{
		false: {"held back — older than installed", "1 plugin held back (hello)", "was upgraded"},
		true:  {"would be held back — older than installed", "1 plugin would be held back (hello)", "would be upgraded"},
	} {
		v, verr := upgradeAllView(outcomes, nil, dryRun)
		s, ok := v.(view.Sections)
		if !ok {
			t.Fatalf("dry run %v: view is %T", dryRun, v)
		}
		row := s.Items[0].View.(view.Table).Rows[0]
		if row[0] != "hello" || row[1] != "1.4.0 → 1.3.2" || !strings.HasSuffix(row[2], "(kept)") || row[3] != want.row {
			t.Errorf("dry run %v: row = %q", dryRun, row)
		}
		if len(s.Warnings) != 1 || s.Warnings[0].Code != plugindist.CodeDowngrade ||
			!strings.Contains(s.Warnings[0].Message, "1.3.2") || !strings.Contains(s.Warnings[0].Hint, "rta plugin upgrade hello --dry-run") {
			t.Errorf("dry run %v: warnings = %+v", dryRun, s.Warnings)
		}
		if verr == nil || verr.Code != "plugin.upgrade.incomplete" || verr.Message != want.refusal ||
			!strings.Contains(verr.Hint, want.hint) {
			t.Errorf("dry run %v: refusal = %v (%s)", dryRun, verr, verr.Hint)
		}
	}
}

// A move onto a build that ran here before says what applies again: what it
// was given to read, the grants on it still inside their window (not one that
// ran out), and the profiles and the section that pin it — each with what
// takes it back. A build that never ran here brings nothing back.
func TestAMoveBackToABuildThatRanHereSaysWhatAppliesAgain(t *testing.T) {
	_, configDir := isolate(t)
	back, current := "6db7eaeebf84"+strings.Repeat("0", 52), "099d72c38cfc"+strings.Repeat("1", 52)
	for digest, at := range map[string]string{back: "/store/kube/back", current: "/store/kube/now"} {
		if verr := plugintrust.Add(digest, "kube", at); verr != nil {
			t.Fatal(verr)
		}
	}
	if verr := plugintrust.Allow(back, []string{string(plugin.NeedKubeconfig)}); verr != nil {
		t.Fatal(verr)
	}
	if verr := plugintrust.AllowPaths(back, []string{"/srv/lab certs"}); verr != nil {
		t.Fatal(verr)
	}
	now := time.Now()
	if verr := grant.Save([]grant.Grant{
		{Target: "kube.pod.list", Agent: "claude", Digest: back, Issued: now, Expires: now.Add(15 * time.Minute)},
		{Target: "kube.node.drain", Agent: "claude", Digest: back, Issued: now.Add(-2 * time.Hour), Expires: now.Add(-time.Hour)},
		{Target: "kube.pod.list", Agent: "codex", Digest: current, Issued: now, Expires: now.Add(15 * time.Minute)},
	}); verr != nil {
		t.Fatal(verr)
	}
	pin := "kube@" + back[:12]
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("plugins:\n  "+pin+":\n    namespace: lab\n"+
		"profiles:\n  staging:\n    plugins:\n      "+pin+":\n        kube: lab/default/svc/api:443\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	moved := plugindist.Upgraded{Report: plugindist.Report{Name: "kube", Digest: back},
		FromDigest: current, Older: true}
	got := map[string]string{}
	for _, p := range regainedPairs(moved, false) {
		got[p.Key] = p.Value
	}
	receipt := upgradeAnswer(moved, false)
	if !slices.ContainsFunc(receipt.Pairs, func(p view.Pair) bool { return strings.Contains(p.Value, "has it again") }) {
		t.Errorf("the receipt does not say what applies again: %+v", receipt.Pairs)
	}
	for key, want := range map[string][]string{
		"your access":   {"kube@6db7eaeebf84 was given kubeconfig", "has it again", "`rta plugin access rm kube kubeconfig`"},
		"your paths":    {"/srv/lab certs under confine: strict", "`rta plugin access rm kube --path '/srv/lab certs'`"},
		"your grants":   {"1 grant issued against kube@6db7eaeebf84 authorizes again: kube.pod.list", "`rta grant revoke kube`"},
		"your profiles": {"1 profile (staging) pins kube@6db7eaeebf84 and applies again"},
		"your pin":      {"plugins.kube@6db7eaeebf84 applies again"},
	} {
		for _, w := range want {
			if !strings.Contains(got[key], w) {
				t.Errorf("%s = %q, want it to say %q", key, got[key], w)
			}
		}
	}
	if strings.Contains(got["your grants"], "drain") {
		t.Errorf("a grant that ran out is said to authorize again: %q", got["your grants"])
	}

	elsewhere := "abcdef012345" + strings.Repeat("2", 52)
	if pairs := regainedPairs(plugindist.Upgraded{Report: plugindist.Report{Name: "kube", Digest: elsewhere},
		FromDigest: current}, false); len(pairs) != 0 {
		t.Errorf("a build that never ran here brought back %v", pairs)
	}
}

// outdated says when a claim is below the version installed, in the cell that
// names the claim; a problem keeps its own words there.
func TestOutdatedMarksAnOlderClaim(t *testing.T) {
	for _, tc := range []struct {
		row  plugindist.OutdatedRow
		want string
	}{
		{plugindist.OutdatedRow{Name: "hello", InstalledVersion: "1.4.0", AvailableVersion: "1.3.2", Older: true}, "1.3.2 (older)"},
		{plugindist.OutdatedRow{Name: "hello", InstalledVersion: "1.3.2", AvailableVersion: "1.4.0"}, "1.4.0"},
		{plugindist.OutdatedRow{Name: "hello", InstalledVersion: "1.4.0", Problem: "no index called lab is attached"},
			"no index called lab is attached"},
	} {
		if got := outdatedRow(tc.row)[2]; got != tc.want {
			t.Errorf("available = %q, want %q", got, tc.want)
		}
	}
}
