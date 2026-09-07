package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// pgRegistryAt registers "pg" as an external artifact at digest — the shape
// a real rebuild produces, and the one pinKey and repin both have to resolve
// against instead of a caller typing "@digest" by hand.
func pgRegistryAt(t *testing.T, digest string) *registry.Registry {
	t.Helper()
	reg := registry.New()
	p := plugin.Plugin{
		Name: "pg", Summary: "pg plugin",
		Capabilities: []plugin.Capability{{
			ID: "pg.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
					Local: true, Help: "host"},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: "reached " + req.String("host")}, nil
			},
		}},
	}
	if err := reg.RegisterFrom(p, registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: digest}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// 64 hex characters, the shape a real sha256 digest has — Short() truncates
// both to their first 12: "111111111111" and "222222222222".
var (
	pgOldDigest = strings.Repeat("1", 64)
	pgNewDigest = strings.Repeat("2", 64)
)

// repinSession is session, except each call names its own registry rather
// than sharing one for the whole test — repin's entire point is that the
// registry answering "what is pg's digest right now" changes between the
// profile being created and being repinned, which NewRoot re-derives
// (SetInstalled) from its own argument on every call. Sharing session's
// single captured registry across calls cannot exercise that: the second
// SetInstalled would just be undone by the next run() rebuilding root from
// the first registry again.
func repinSession(t *testing.T) func(reg *registry.Registry, args ...string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(home, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", filepath.Join(home, "data"))
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	t.Cleanup(func() { SetInstalled(nil) })
	return func(reg *registry.Registry, args ...string) (string, string, error) {
		root := NewRoot(reg, "test")
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		err := root.ExecuteContext(context.Background())
		return out.String(), errOut.String(), err
	}
}

// seedStaleProfile writes a profile with an entry pinned to whatever oldReg
// resolves pg to, through the ordinary `profile set` path — the way a
// profile actually gets into this state: created against a plugin later
// rebuilt out from under it.
func seedStaleProfile(t *testing.T, run func(*registry.Registry, ...string) (string, string, error),
	oldReg *registry.Registry, name string,
) {
	t.Helper()
	if _, errOut, err := run(oldReg, "profile", "set", name, "--plugin", "pg",
		"--set", "host=db."+name+".internal"); err != nil {
		t.Fatalf("seeding %s: %v %q", name, err, errOut)
	}
}

func TestProfileRepinAllRewritesEveryStaleEntry(t *testing.T) {
	run := repinSession(t)
	old, new_ := pgRegistryAt(t, pgOldDigest), pgRegistryAt(t, pgNewDigest)
	seedStaleProfile(t, run, old, "staging")
	seedStaleProfile(t, run, old, "prod")
	before := configOf(t)
	if !strings.Contains(before, "pg@111111111111:") {
		t.Fatalf("seed did not pin to the old digest: %s", before)
	}

	out, errOut, err := run(new_, "profile", "repin", "--all", "--plugin", "pg")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "repinned to pg@222222222222") {
		t.Errorf("output does not report the new pin: %q", out)
	}
	after := configOf(t)
	if strings.Contains(after, "pg@111111111111") {
		t.Errorf("the old pin is still in the file: %s", after)
	}
	if strings.Count(after, "pg@222222222222:") != 2 {
		t.Errorf("want both profiles repinned, got: %s", after)
	}
	// The point of the whole command: the block beside the key survived.
	if !strings.Contains(after, "db.staging.internal") || !strings.Contains(after, "db.prod.internal") {
		t.Errorf("a set: block was lost across the repin: %s", after)
	}
}

func TestProfileRepinOneProfileLeavesTheOthersStale(t *testing.T) {
	run := repinSession(t)
	old, new_ := pgRegistryAt(t, pgOldDigest), pgRegistryAt(t, pgNewDigest)
	seedStaleProfile(t, run, old, "staging")
	seedStaleProfile(t, run, old, "prod")

	if _, errOut, err := run(new_, "profile", "repin", "staging", "--plugin", "pg"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	after := configOf(t)
	if strings.Count(after, "pg@222222222222:") != 1 {
		t.Errorf("want exactly one profile repinned, got: %s", after)
	}
	if strings.Count(after, "pg@111111111111:") != 1 {
		t.Errorf("want prod left on its old pin, got: %s", after)
	}
}

func TestProfileRepinDryRunWritesNothing(t *testing.T) {
	run := repinSession(t)
	old, new_ := pgRegistryAt(t, pgOldDigest), pgRegistryAt(t, pgNewDigest)
	seedStaleProfile(t, run, old, "staging")
	before := configOf(t)

	out, errOut, err := run(new_, "profile", "repin", "--all", "--plugin", "pg", "--dry-run")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "would repin to pg@222222222222") {
		t.Errorf("output does not say what a real run would do: %q", out)
	}
	if configOf(t) != before {
		t.Error("--dry-run rewrote the config file")
	}
}

func TestProfileRepinReportsAlreadyPinnedEntriesWithoutRewritingThem(t *testing.T) {
	run := repinSession(t)
	reg := pgRegistryAt(t, pgNewDigest)
	seedStaleProfile(t, run, reg, "staging") // seeded against the same digest repin will target

	out, errOut, err := run(reg, "profile", "repin", "--all", "--plugin", "pg")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "already pinned") {
		t.Errorf("output = %q, want it to say the entry was already pinned", out)
	}
}

func TestProfileRepinNarrowsToTheNamedInstance(t *testing.T) {
	run := repinSession(t)
	old, new_ := pgRegistryAt(t, pgOldDigest), pgRegistryAt(t, pgNewDigest)
	if _, errOut, err := run(old, "profile", "set", "staging", "--plugin", "pg",
		"--set", "host=default.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run(old, "profile", "set", "staging", "--plugin", "pg/analytics",
		"--set", "host=analytics.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}

	if _, errOut, err := run(new_, "profile", "repin", "staging", "--plugin", "pg/analytics"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	after := configOf(t)
	if !strings.Contains(after, "pg/analytics@222222222222:") {
		t.Errorf("the named instance was not repinned: %s", after)
	}
	if !strings.Contains(after, "pg@111111111111:") {
		t.Errorf("the default entry should have been left alone, got: %s", after)
	}
}

func TestProfileRepinRefusesWithNeitherAProfileNorAll(t *testing.T) {
	run := repinSession(t)
	reg := pgRegistryAt(t, pgNewDigest)
	_, errOut, err := run(reg, "profile", "repin", "--plugin", "pg")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(errOut, "--all") {
		t.Errorf("errOut = %q, want a hint mentioning --all", errOut)
	}
}

func TestProfileRepinRefusesAnUnknownPlugin(t *testing.T) {
	run := repinSession(t)
	reg := pgRegistryAt(t, pgNewDigest)
	_, errOut, err := run(reg, "profile", "repin", "--all", "--plugin", "nosuch")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(errOut, "not a registered plugin") {
		t.Errorf("errOut = %q, want the unknown-plugin refusal", errOut)
	}
}

func TestProfileRepinRefusesABuiltinPlugin(t *testing.T) {
	run := repinSession(t)
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "sys", Summary: "sys",
		Capabilities: []plugin.Capability{{ID: "sys.cpu", Summary: "cpu", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }}}}); err != nil {
		t.Fatal(err)
	}
	_, errOut, err := run(reg, "profile", "repin", "--all", "--plugin", "sys")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(errOut, "built in") {
		t.Errorf("errOut = %q, want the built-in refusal", errOut)
	}
}
