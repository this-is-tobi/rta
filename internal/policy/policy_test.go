package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
	// Cleared too, now that the operator policy path is derived from the
	// config location: a runner with RTA_CONFIG exported would otherwise
	// point every test at a real file on that machine.
	t.Setenv("RTA_CONFIG", "")
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

// Forbids used to derive a target's namespace with its own hand-rolled copy
// of plugin.Namespace rather than calling it, and the two disagreed on a
// leading-dot target: plugin.Namespace(".vault") is "" (Cut splits at the
// dot, which sits at index 0, into an empty prefix), while the hand-rolled
// version's `i > 0` check treated that same index 0 as "no dot found" and
// returned the string unchanged.
//
// An empty Never entry is what turns that disagreement into two different
// verdicts to tell apart: the hand-rolled version never produced "" for any
// input, so it never matched such an entry, while plugin.Namespace does for
// exactly the leading-dot case this covers. An ordinary capability or
// namespace entry cannot show the same difference — both versions derive a
// "kv.get"-shaped target identically and disagree only here.
func TestForbidsUsesPluginNamespaceNotAHandRolledCopy(t *testing.T) {
	c := Ceiling{Never: []string{""}}
	if why := c.Forbids(".vault", "", ""); why == "" {
		t.Fatal("a leading-dot target was not forbidden by an empty Never entry — " +
			"Forbids is not deriving its namespace the way plugin.Namespace does")
	}
}

// A policy that cannot be parsed is not a policy, and must not read as an
// absent one — that is the failure this whole package is written against.
// The subtract-only property this package rests on is an argument about
// authority, and it does not reach availability. A hostile repository ships
// this file, Load walks up to 64 parents to find it, and Ceiling() is
// deliberately uncached — so every gated call re-reads it. Unguarded, 510
// bytes aimed at any declared []string field here cost 37.8 seconds and
// 34.7 GB before the decoder gave up on a type mismatch, which means the
// mismatch is not a defense: the memory is committed before the error.
//
// Asserting the refusal is *fast* is the point. A slow refusal is the same
// wall reached by a longer route.
func TestAPolicyBombIsRefusedBeforeItIsDecoded(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 6; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := range 10 {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	// Aimed at a field Ceiling actually declares: an anchor chain sitting in
	// keys it ignores is skipped cheaply, so a bomb in an undeclared key
	// would prove nothing.
	fmt.Fprintf(&b, "never: [*a5, *a5, *a5, *a5, *a5, *a5, *a5, *a5, *a5, *a5]\n")
	write(t, dir, b.String())
	chdir(t, dir)

	done := make(chan *view.Error, 1)
	go func() {
		_, verr := Load()
		done <- verr
	}()
	select {
	case verr := <-done:
		if verr == nil {
			t.Fatal("an alias-expansion bomb loaded as a policy")
		}
		if verr.Code != "policy.malformed" {
			t.Fatalf("want policy.malformed, got %s: %s", verr.Code, verr.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Load did not return within 2s — it expanded the bomb instead of refusing it")
	}
}

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
