package policy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// chdir moves into dir for the test, so the walk-up has something to find.
func chdir(t *testing.T, dir string) {
	t.Helper()
	// t.Chdir rather than os.Chdir: the working directory is process-global,
	// the suite runs with -shuffle=on, and t.Chdir both restores it and
	// refuses to run in a parallel test — which os.Chdir would silently do.
	t.Chdir(dir)
}

// isolate points the operator-config source somewhere empty, so a policy file
// on the machine running the tests cannot change what they measure.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RTA_POLICY", "")
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, RepoFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The default must be nothing. A machine with no policy file has to behave
// exactly as it did before this package existed.
func TestNoFileConstrainsNothing(t *testing.T) {
	isolate(t)
	chdir(t, t.TempDir())
	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if !c.Empty() {
		t.Fatalf("a machine with no policy file got a ceiling: %+v", c)
	}
}

func TestAFileIsFoundOnTheWayUp(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "maxTTL: 15m\nnever: [pg.dump]\n")
	chdir(t, deep)

	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if c.MaxTTL != 15*time.Minute {
		t.Errorf("MaxTTL = %v, want 15m — the walk did not reach the repository root", c.MaxTTL)
	}
	if c.Forbids("pg.dump", "", "") == "" {
		t.Error("a target the file forbids was allowed")
	}
}

// **The whole safety argument in one test.** Several files intersect, and on
// every axis the strictest wins — so a nested file can tighten and can never
// loosen, which is what makes it safe to find one by walking up from wherever
// the process happens to be.
func TestNestedFilesTightenAndNeverLoosen(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "maxTTL: 15m\nnever: [pg.dump]\n")
	// The inner file asks for *more* time and forbids something else. The
	// extra time must be ignored; the extra prohibition must be honoured.
	write(t, inner, "maxTTL: 24h\nnever: [vault.snapshot]\n")
	chdir(t, inner)

	c, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if c.MaxTTL != 15*time.Minute {
		t.Errorf("MaxTTL = %v, want 15m — a nested file widened the ceiling above it", c.MaxTTL)
	}
	for _, target := range []string{"pg.dump", "vault.snapshot"} {
		if c.Forbids(target, "", "") == "" {
			t.Errorf("%s was allowed — the union of prohibitions was not taken", target)
		}
	}

	// **And the other direction, which is the half that actually distinguishes
	// the rule.** Above, the stricter file is also the last one the walk
	// visits, so "the strictest wins" and "the last one wins" give the same
	// answer and the assertion cannot tell them apart — a probe that replaced
	// the comparison with "take whatever is set" passed it. Here the *inner*
	// file is the stricter one, so only the real rule gives 5m.
	write(t, root, "maxTTL: 1h\n")
	write(t, inner, "maxTTL: 5m\n")
	c, verr = Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if c.MaxTTL != 5*time.Minute {
		t.Errorf("MaxTTL = %v, want 5m — the strictest value did not win, the last one did", c.MaxTTL)
	}
}

func TestForbidsCoversTheAxesItClaims(t *testing.T) {
	c := Ceiling{
		Never:        []string{"pg.dump", "vault"},
		NeverProfile: []string{"prod"},
		RequireScope: []string{"kv.get"},
	}
	for _, tc := range []struct {
		name                   string
		target, scope, profile string
		forbidden              bool
	}{
		{"a forbidden capability", "pg.dump", "", "", true},
		{"a capability inside a forbidden namespace", "vault.kv.get", "x", "", true},
		{"a namespace grant on a forbidden namespace", "vault", "", "", true},
		{"an ordinary capability", "kv.set", "k", "", false},
		{"a forbidden connection", "pg.query", "", "prod", true},
		{"another connection", "pg.query", "", "staging", false},
		// The "every secret at once" grant, which is the pressure folder
		// scopes exist to relieve — refused here, and the scoped one allowed.
		{"an unscoped grant where a record is required", "kv.get", "", "", true},
		{"the same grant with a record", "kv.get", "db-password", "", false},
		{"the same grant with a folder", "kv.get", "prod/", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := c.Forbids(tc.target, tc.scope, tc.profile)
			if (why != "") != tc.forbidden {
				t.Errorf("Forbids = %q, want forbidden=%v", why, tc.forbidden)
			}
		})
	}
}

// A policy that cannot be parsed is not a policy, and must not read as an
// absent one — that is the failure this whole package is written against.
func TestAMalformedPolicyIsAnErrorAndNotAnAbsentOne(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	write(t, dir, "maxTTL: [this is not a duration]\n")
	chdir(t, dir)
	if _, verr := Load(); verr == nil {
		t.Fatal("a malformed policy file loaded as an empty ceiling, which is the " +
			"one outcome that must not happen quietly")
	}

	write(t, dir, "maxTTL: soon\n")
	verr := func() error {
		_, v := Load()
		if v == nil {
			return nil
		}
		return v
	}()
	if verr == nil {
		t.Fatal("maxTTL that is not a duration was accepted")
	}
}

// An explicit path that is not there is somebody's mistake, and running with
// no ceiling because of it is exactly what this must not do.
func TestAnExplicitPathThatIsMissingIsRefused(t *testing.T) {
	isolate(t)
	chdir(t, t.TempDir())
	t.Setenv("RTA_POLICY", filepath.Join(t.TempDir(), "nope.yaml"))
	if _, verr := Load(); verr == nil {
		t.Fatal("RTA_POLICY naming a file that does not exist ran with no ceiling")
	}
}
