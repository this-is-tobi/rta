package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gap these cover, stated once.
//
// Every other axis of this package defends against a hostile *edit*, which can
// only ever subtract authority — that is the property that lets the file be
// committed with no seal. None of them defends against the file not being
// there, and the failure modes were inverted: corrupting .rta-policy.yaml
// failed loudly and closed, while deleting it or replacing its contents with
// `{}` left rta running with no ceiling and saying nothing.
//
// A repository file cannot close that on its own, because it is removed along
// with its own demand. So the requirement lives in the operator's own policy
// file, outside every repository.

// operatorPolicy writes the operator's own policy file for a test, at whatever
// path OperatorPath currently resolves to.
func operatorPolicy(t *testing.T, body string) {
	t.Helper()
	path := OperatorPath()
	if path == "" {
		t.Fatal("OperatorPath is empty — the isolation in this test is not working")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// **The attack the subtract-only property does not cover.** A ceiling that is
// merely absent is a ceiling that is gone, and nothing said so.
func TestARequiredRepoPolicyThatIsMissingIsRefused(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	chdir(t, dir)
	operatorPolicy(t, "requireRepoPolicy: true\n")

	_, verr := Load()
	if verr == nil {
		t.Fatal("no repository policy, and the machine requiring one did not refuse")
	}
	if verr.Code != "policy.repo.missing" {
		t.Errorf("code = %q, want policy.repo.missing", verr.Code)
	}
	// The directory rta searched has to be in the message. For an MCP server
	// this is a directory the client chose, and it is the fact most likely to
	// explain the refusal.
	if !strings.Contains(verr.Message, dir) {
		t.Errorf("the refusal does not say where rta looked: %q", verr.Message)
	}
}

// The subtler edit: leave a file, remove its contents. It parses, it is found,
// and it constrains nothing — so a check that only asked "is a file there"
// would make the quietest change the most effective one.
func TestARequiredRepoPolicyThatConstrainsNothingIsRefused(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	chdir(t, dir)
	operatorPolicy(t, "requireRepoPolicy: true\n")

	for _, body := range []string{"{}\n", "", "# nothing but a comment\n"} {
		write(t, dir, body)
		_, verr := Load()
		if verr == nil {
			t.Fatalf("a policy file holding %q satisfied the requirement", body)
		}
		if verr.Code != "policy.repo.empty" {
			t.Errorf("body %q: code = %q, want policy.repo.empty", body, verr.Code)
		}
	}
}

// The requirement is satisfied by a policy that actually says something, and
// only then.
func TestARequiredRepoPolicyThatConstrainsIsAccepted(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	chdir(t, dir)
	operatorPolicy(t, "requireRepoPolicy: true\n")
	write(t, dir, "maxTTL: 15m\n")

	c, verr := Load()
	if verr != nil {
		t.Fatalf("a real policy was refused: %v", verr)
	}
	if !c.RepoFound {
		t.Error("RepoFound is false with a repository policy in the working directory")
	}
	if !c.RequireRepo {
		t.Error("RequireRepo did not survive the intersect")
	}
}

// **A repository file must not be able to satisfy its own demand.**
//
// This is the whole reason the requirement lives elsewhere. If a
// .rta-policy.yaml could set requireRepoPolicy, deleting it would delete the
// demand too, and the check would pass in exactly the case it exists to catch.
func TestARepositoryFileCannotRequireItself(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	chdir(t, dir)
	write(t, dir, "requireRepoPolicy: true\nmaxTTL: 15m\n")

	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if c.RequireRepo {
		t.Error("a repository file set the requirement — deleting it would delete the demand, " +
			"so the check would pass exactly when it is needed")
	}
	// Its actual constraint is still honoured. Ignoring one key is not a
	// reason to ignore the file.
	if c.MaxTTL == 0 {
		t.Error("the rest of the repository policy was dropped along with the ignored key")
	}
}

// RTA_POLICY is outside the repository too, so it may carry the requirement.
// A managed or CI setup that points at one file is exactly the case where
// "there must be a ceiling" needs saying.
func TestAnExplicitPolicyPathMayCarryTheRequirement(t *testing.T) {
	isolate(t)
	chdir(t, t.TempDir())
	explicit := filepath.Join(t.TempDir(), "managed.yaml")
	if err := os.WriteFile(explicit, []byte("requireRepoPolicy: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_POLICY", explicit)

	_, verr := Load()
	if verr == nil || verr.Code != "policy.repo.missing" {
		t.Fatalf("RTA_POLICY could not require a repository policy: %v", verr)
	}
}

// The default has to stay exactly what it was. A machine that never asked for
// this must behave as it did before the mechanism existed.
func TestNothingChangesForAMachineThatDidNotAskForIt(t *testing.T) {
	isolate(t)
	chdir(t, t.TempDir())

	c, verr := Load()
	if verr != nil {
		t.Fatalf("a machine with no policy at all now errors: %v", verr)
	}
	if !c.Empty() {
		t.Errorf("a machine with no policy got a ceiling: %+v", c)
	}
	if c.RequireRepo {
		t.Error("the requirement defaulted to on")
	}
}

// A ceiling that demands a repository policy is not an empty one, whatever
// else it says. Reporting it as empty would put it back in the silent case
// this mechanism exists to leave.
func TestARequirementAloneIsNotAnEmptyCeiling(t *testing.T) {
	if (Ceiling{RequireRepo: true}).Empty() {
		t.Error("a ceiling requiring a repository policy reports itself as constraining nothing")
	}
	if !(Ceiling{}).Empty() {
		t.Error("an actually-empty ceiling stopped reporting itself as empty")
	}
}

// Where rta looked has to be reported even when it found nothing, because the
// empty case is the one that needs explaining. For an MCP server this names a
// directory the operator did not choose.
func TestTheSearchOriginIsReportedEvenWithNoPolicy(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	chdir(t, dir)

	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if c.SearchedFrom == "" {
		t.Fatal("no search origin reported — `none in force` cannot say where it looked")
	}
	// t.Chdir resolves symlinks on macOS, where TempDir is under /var -> /private/var.
	if !strings.HasSuffix(c.SearchedFrom, filepath.Base(dir)) {
		t.Errorf("SearchedFrom = %q, want the working directory", c.SearchedFrom)
	}
	if c.RepoFound {
		t.Error("RepoFound is true with no repository policy anywhere")
	}
}

// OperatorPath must follow RTA_CONFIG. It used to be built from
// os.UserConfigDir directly, so on a portable or managed setup — the
// deployments most likely to rely on a ceiling — the operator's policy was
// looked for somewhere other than beside their config, and a requirement set
// there would have been written to a file rta never reads.
func TestTheOperatorPolicySitsBesideTheConfigWhereverThatIs(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))

	got := OperatorPath()
	if want := filepath.Join(dir, "policy.yaml"); got != want {
		t.Errorf("OperatorPath = %q, want %q — it does not follow RTA_CONFIG", got, want)
	}

	// And it is actually read from there.
	chdir(t, t.TempDir())
	if err := os.WriteFile(got, []byte("maxTTL: 5m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if c.MaxTTL.String() != "5m0s" {
		t.Errorf("maxTTL = %v — the operator policy beside RTA_CONFIG was not read", c.MaxTTL)
	}
}

// The operator's own constraints are the other half of the answer, and they
// need no new mechanism: they intersect like any other source, so they survive
// the repository file being deleted, emptied or never checked out.
func TestTheOperatorsOwnCeilingSurvivesTheRepositoryFileVanishing(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	chdir(t, dir)
	operatorPolicy(t, "maxTTL: 30m\nnever:\n  - vault.snapshot\n")
	write(t, dir, "maxTTL: 15m\n")

	tight, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if tight.MaxTTL.String() != "15m0s" {
		t.Errorf("maxTTL = %v, want the stricter repository value", tight.MaxTTL)
	}

	// Now the repository file goes away, which is the whole scenario.
	if err := os.Remove(filepath.Join(dir, RepoFile)); err != nil {
		t.Fatal(err)
	}
	after, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if after.Empty() {
		t.Fatal("deleting the repository file left no ceiling at all")
	}
	if after.MaxTTL.String() != "30m0s" {
		t.Errorf("maxTTL = %v, want the operator's own 30m to still stand", after.MaxTTL)
	}
	if len(after.Never) != 1 {
		t.Errorf("the operator's own prohibitions went with the repository file: %+v", after.Never)
	}
}
