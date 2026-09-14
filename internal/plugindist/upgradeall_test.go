package plugindist

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What `--all` upgrades is every plugin rta has a record of, and the reason it
// is the lock rather than Outdated() is the case Outdated documents about
// itself: a respin under an unchanged version number is invisible to a
// manifest comparison, exactly as it is invisible to a signature. Picking
// candidates from Outdated would skip the one event upgrade exists to catch.
func TestUpgradeAllVisitsEveryLockedPluginNotOnlyTheOutdatedOnes(t *testing.T) {
	installHello(t)

	if rows := Outdated(); len(rows) != 0 {
		t.Fatalf("outdated = %v, want none — the fixture index has not moved", rows)
	}

	outcomes := UpgradeAll(context.Background(), "", io.Discard)
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v, want one for the installed plugin", outcomes)
	}
	if outcomes[0].Name != "hello" || !outcomes[0].UpToDate {
		t.Fatalf("outcome = %+v, want hello reported up to date", outcomes[0])
	}
	if outcomes[0].Problem != nil {
		t.Fatalf("an unchanged index reported a problem: %v", outcomes[0].Problem)
	}
}

// One plugin whose index went away must not cost the operator the rest of the
// run — the rule Manifests and LoadInto already follow, applied to an action.
// And --index narrows what a bulk upgrade will reach for at all, which is the
// only control an operator has over which supply chain a sweep pulls from.
func TestUpgradeAllContinuesPastFailuresAndHonoursTheIndexFilter(t *testing.T) {
	installHello(t)

	// A record for a plugin whose index is not attached. Written the way
	// somebody with the data dir has it, which is also the ordinary way it
	// happens: `rta plugin index remove` outlives the installs it fed.
	ghost := LockEntry{
		Name:   "ghost",
		Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		//nolint:misspell // an index name, not a word
		Version: "1.0.0", Index: "elsewhere",
	}
	if verr := recordInstall(ghost); verr != nil {
		t.Fatal(verr)
	}

	outcomes := UpgradeAll(context.Background(), "", io.Discard)
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %+v, want one per locked plugin", outcomes)
	}
	byName := map[string]UpgradeOutcome{}
	for _, o := range outcomes {
		byName[o.Name] = o
	}
	if o, ok := byName["ghost"]; !ok || o.Problem == nil {
		t.Fatalf("ghost = %+v, want a reported problem", o)
	}
	if o, ok := byName["hello"]; !ok || o.Problem != nil {
		t.Fatalf("hello = %+v, want it upgraded despite ghost failing", o)
	}

	only := UpgradeAll(context.Background(), "lab", io.Discard)
	if len(only) != 1 || only[0].Name != "hello" {
		t.Fatalf("--index lab = %+v, want only the plugin that came from lab", only)
	}

	// A name no attached index answers to is a typo, and a sweep that silently
	// did nothing would read as "everything is up to date".
	if got := UpgradeAll(context.Background(), "nosuch", io.Discard); len(got) != 0 {
		t.Fatalf("--index nosuch = %+v, want nothing", got)
	}
}

// Preview is the rehearsal an operator runs before a sweep, so it has to reach
// the same verdicts by the same route and write none of them.
func TestPreviewUpgradeAllDecidesWithoutRecording(t *testing.T) {
	first := installHello(t)

	outcomes := PreviewUpgradeAll(context.Background(), "", io.Discard)
	if len(outcomes) != 1 || outcomes[0].Problem != nil {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if e, held := LockedFor("hello"); !held || e.Digest != first.Digest {
		t.Fatalf("a previewed sweep moved the pin to %v", e)
	}
}

// widened builds the testdata fixture: the same plugin with hello.greet raised
// from read to destructive. Built the same way hello is, because install's
// verification launch is the real spawn path and a fixture that skipped the
// fork would skip the thing being verified.
func widened(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, binaryName("hello"))
	cmd := exec.Command("go", "build", "-o", out, "./testdata/widenedplugin")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the widened fixture: %v: %s", err, combined)
	}
	return out
}

// widenedManifest claims what the widened fixture actually declares — an index
// that misstated it would be refused by verifyClaims long before the guard had
// an opinion, which would pass this test for the wrong reason.
func widenedManifest(t *testing.T, artifact string) string {
	t.Helper()
	return fmt.Sprintf(`name: hello
version: 0.2.0
summary: the example plugin
platforms:
  - os: %s
    arch: %s
    url: %s
    sha256: %s
capabilities:
  - id: hello.greet
    summary: greet someone
    safety: destructive
  - id: hello.languages
    summary: list languages
    safety: read
`, runtimeGOOS(), runtimeGOARCH(), fileURL(artifact), sha256Of(t, artifact))
}

// The sweep's whole reason for existing: an upstream that grew a destructive
// capability does not get to land during a bulk upgrade, and the operator is
// told which change stopped it.
//
// What must hold afterwards is everything an operator would check: the pin did
// not move, the old bytes are still what loads, the new bytes were never
// trusted, and the report can say what version and digest are still in force —
// a held-back row that cannot name the pin it kept is telling somebody their
// plugin was not upgraded without telling them what they still have.
func TestASweepHoldsBackAnUpgradeThatWouldWidenWhatThePluginMayDo(t *testing.T) {
	first := installHello(t)

	writeManifests(t, labRepo(t), map[string]string{"hello": widenedManifest(t, widened(t))})
	commitAll(t, labRepo(t), "0.2.0")
	if verr := UpdateIndex(context.Background(), "lab"); verr != nil {
		t.Fatal(verr)
	}

	outcomes := UpgradeAll(context.Background(), "", io.Discard)
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	got := outcomes[0]
	if !got.Skipped() {
		t.Fatalf("outcome = %+v, want it held back", got)
	}
	if got.Problem != nil {
		t.Errorf("a held-back plugin was also reported as failed: %v", got.Problem)
	}
	if len(got.Widenings) != 1 || !strings.Contains(got.Widenings[0], "read → destructive") {
		t.Errorf("widenings = %q, want the safety rise that caused it", got.Widenings)
	}

	// The row has to be renderable: name, the pin it kept, and the version it
	// refused to move to.
	if got.Name != "hello" {
		t.Errorf("name = %q", got.Name)
	}
	if got.FromDigest != first.Digest {
		t.Errorf("from digest = %q, want the digest still installed (%s)", got.FromDigest, first.Digest)
	}
	if got.FromVersion != "0.1.0" || got.Version != "0.2.0" {
		t.Errorf("versions = %q → %q, want 0.1.0 → 0.2.0", got.FromVersion, got.Version)
	}

	// And nothing moved.
	if e, held := LockedFor("hello"); !held || e.Digest != first.Digest {
		t.Fatalf("a held-back upgrade moved the pin to %+v", e)
	}
	if got, _ := CurrentDigest("hello"); got != first.Digest {
		t.Errorf("current = %s, want the artifact that was already there", got)
	}
}

// labRepo finds the fixture index installHello attached, so a test can move
// what it claims.
func labRepo(t *testing.T) string {
	t.Helper()
	ix, ok := IndexByName("lab")
	if !ok {
		t.Fatal("the lab index is not attached")
	}
	return ix.Dir
}
