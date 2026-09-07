package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
)

// `--plugin pg/analytics` states one of several connections to a plugin, in
// the same grammar the config key uses, and each instance's blocks stay its
// own: stating the analytics instance must not steal or disturb the default.
func TestProfileSetStatesInstancesSeparately(t *testing.T) {
	run := session(t, setRegistry(t))

	if _, stderr, err := run("profile", "set", "staging",
		"--plugin", "db", "--set", "host=main.internal"); err != nil {
		t.Fatalf("set default: %v\n%s", err, stderr)
	}
	if _, stderr, err := run("profile", "set", "staging",
		"--plugin", "db/analytics", "--set", "host=analytics.internal"); err != nil {
		t.Fatalf("set analytics: %v\n%s", err, stderr)
	}

	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["staging"]
	if len(p.Plugins) != 2 {
		t.Fatalf("plugins = %v, want the default and analytics side by side", p.PluginKeys())
	}
	if _, conn, ok := p.ForInstance("db", ""); !ok || conn.Set["host"] != "main.internal" {
		t.Errorf("the default instance lost its block: %v", p.Plugins)
	}
	if _, conn, ok := p.ForInstance("db", "analytics"); !ok || conn.Set["host"] != "analytics.internal" {
		t.Errorf("the analytics instance is wrong: %v", p.Plugins)
	}

	// Restating the default touches only the default.
	if _, stderr, err := run("profile", "set", "staging",
		"--plugin", "db", "--set", "host=main2.internal"); err != nil {
		t.Fatalf("restate default: %v\n%s", err, stderr)
	}
	cfg, _ = config.LoadFile()
	p = cfg.Profiles["staging"]
	if _, conn, _ := p.ForInstance("db", "analytics"); conn.Set["host"] != "analytics.internal" {
		t.Errorf("restating the default disturbed analytics: %v", conn.Set)
	}
}

// A label that does not parse is refused before it lands in the file.
func TestProfileSetRefusesABadInstanceLabel(t *testing.T) {
	run := session(t, setRegistry(t))
	_, stderr, err := run("profile", "set", "staging",
		"--plugin", "db/Bad_Label", "--set", "host=x")
	if err == nil {
		t.Fatal("a malformed instance label was written")
	}
	if !strings.Contains(stderr, "instance label") {
		t.Errorf("stderr = %q, want the label named", stderr)
	}
	if cfg, _ := config.LoadFile(); len(cfg.Profiles) != 0 {
		t.Errorf("the file was written anyway: %+v", cfg.Profiles)
	}
}

// `rta profile rm --plugin db/analytics` removes one instance and keeps the
// rest of the environment.
func TestProfileRemoveTakesOneInstance(t *testing.T) {
	run := session(t, setRegistry(t))
	mustRun := func(args ...string) {
		t.Helper()
		if _, stderr, err := run(args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, stderr)
		}
	}
	mustRun("profile", "set", "staging", "--plugin", "db", "--set", "host=main.internal")
	mustRun("profile", "set", "staging", "--plugin", "db/analytics", "--set", "host=analytics.internal")
	mustRun("profile", "rm", "staging", "--plugin", "db/analytics")

	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["staging"]
	if _, _, ok := p.ForInstance("db", "analytics"); ok {
		t.Error("analytics survived its removal")
	}
	if _, conn, ok := p.ForInstance("db", ""); !ok || conn.Set["host"] != "main.internal" {
		t.Errorf("removing analytics disturbed the default: %v", p.Plugins)
	}
}

// A switch is the whole environment; an instance is one connection inside
// it. `rta use staging/analytics` is refused rather than silently narrowed.
func TestUseRefusesAnInstanceRef(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, stderr, err := run("profile", "set", "staging",
		"--plugin", "db/analytics", "--set", "host=analytics.internal"); err != nil {
		t.Fatalf("set: %v\n%s", err, stderr)
	}
	_, stderr, err := run("use", "staging/analytics")
	if err == nil {
		t.Fatal("use accepted an instance ref")
	}
	if !strings.Contains(stderr, "whole environment") || !strings.Contains(stderr, "--profile staging/analytics") {
		t.Errorf("refusal does not teach the per-call form: %q", stderr)
	}
}

// D3: --dry-run used to be silently ignored here too — `rta use staging
// --dry-run` switched to staging for real, the same silent-write shape
// profile rm and set had.
func TestUseDryRunDoesNotSwitch(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, stderr, err := run("profile", "set", "staging",
		"--plugin", "db", "--set", "host=staging.internal"); err != nil {
		t.Fatalf("set: %v\n%s", err, stderr)
	}

	out, stderr, err := run("use", "staging", "--dry-run")
	if err != nil {
		t.Fatalf("use --dry-run: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "staging") {
		t.Errorf("dry-run output does not name the environment: %q", out)
	}
	if got := profile.LoadSelection(); got.Active != "" {
		t.Errorf("active selection = %q, want none — --dry-run switched for real", got.Active)
	}

	// Real, without --dry-run, still switches — the flag is not the only
	// path that works.
	if _, stderr, err := run("use", "staging"); err != nil {
		t.Fatalf("use: %v\n%s", err, stderr)
	}
	if got := profile.LoadSelection(); got.Active != "staging" {
		t.Errorf("active selection = %q, want staging", got.Active)
	}

	// And switching off previews the same way.
	out, stderr, err = run("use", "--off", "--dry-run")
	if err != nil {
		t.Fatalf("use --off --dry-run: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "Nothing switched on") {
		t.Errorf("dry-run --off output = %q, want the switched-off preview", out)
	}
	if got := profile.LoadSelection(); got.Active != "staging" {
		t.Errorf("active selection = %q, want staging — --off --dry-run switched off for real", got.Active)
	}
}
