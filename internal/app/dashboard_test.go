package app

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// `rta dashboard add` and `rm` write the `add:` list `rta profile set` cannot,
// and refuse the shapes buildTiles would have dropped from the file without
// a word.

func TestDashboardAddWritesAnEntryAndTheTUIDrawsIt(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod", "--set", "dbname=shop")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "added db.status@prod") {
		t.Errorf("receipt = %q, want the key it added", out)
	}
	cfg := loadedConfig(t)
	if len(cfg.Dashboard.Add) != 1 {
		t.Fatalf("add = %v, want one entry", cfg.Dashboard.Add)
	}
	entry := cfg.Dashboard.Add[0]
	if entry.ID != "db.status" || entry.Profile != "prod" || entry.With["dbname"] != "shop" {
		t.Errorf("entry = %+v", entry)
	}
	list, _, err := run("dashboard", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "prod") || !strings.Contains(list, "added") {
		t.Errorf("list = %q, want the pinned tile and its source", list)
	}
}

// Adding the same tile twice replaces it: a script that runs on every boot
// leaves one tile, and the receipt says nothing changed when nothing did.
func TestDashboardAddIsIdempotentPerKey(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, _, err := run("dashboard", "add", "db.status", "--set", "dbname=shop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("a repeat was not reported as unchanged: %q", out)
	}
	out, _, err = run("dashboard", "add", "db.status", "--set", "dbname=other")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "updated db.status") {
		t.Errorf("a change was not reported as an update: %q", out)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].With["dbname"] != "other" {
		t.Errorf("add = %v, want one entry carrying the new value", add)
	}
}

func TestDashboardAddRefusesWhatATileCannotBe(t *testing.T) {
	run := session(t, setRegistry(t))
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"an unknown capability", []string{"nope.here"}, "core.dashboard.unknown"},
		{"a mutation", []string{"grant.allow"}, "core.dashboard.notread"},
		{"a profile nobody configured", []string{"db.status", "--profile", "ghost"}, "core.profile.unknown"},
		{"an input it does not declare", []string{"db.status", "--set", "colour=red"}, "core.dashboard.set.unknown"},
		{"a credential in plaintext", []string{"db.status", "--set", "password=hunter2"}, "core.dashboard.set.secret"},
		{"the profile as an input", []string{"db.status", "--set", "profile=prod"}, "core.dashboard.set.profile"},
		{"a value of the wrong type", []string{"db.status", "--set", "port=many"}, "core.dashboard.set.type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errOut, err := run(append([]string{"dashboard", "add"}, c.args...)...)
			if err == nil {
				t.Fatalf("accepted %v", c.args)
			}
			if !strings.Contains(errOut, c.code) {
				t.Errorf("refusal = %q, want %s", errOut, c.code)
			}
		})
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Errorf("a refused add wrote %s", config.Path())
	}
}

func TestDashboardAddDryRunWritesNothing(t *testing.T) {
	run := session(t, setRegistry(t))
	out, errOut, err := run("dashboard", "add", "db.status", "--dry-run")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "would") {
		t.Errorf("dry run did not say would: %q", out)
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Errorf("--dry-run wrote %s", config.Path())
	}
}

func TestDashboardRemoveTakesDownOneKeyAndNamesTheOthers(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, p := range []string{"prod", "staging"} {
		if _, errOut, err := run("profile", "set", p, "--plugin", "db", "--set", "host="+p+".internal"); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		if _, errOut, err := run("dashboard", "add", "db.status", "--profile", p); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
	if _, errOut, err := run("dashboard", "rm", "db.status", "--profile", "prod"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].Profile != "staging" {
		t.Errorf("add = %v, want only the staging tile left", add)
	}
	_, errOut, err := run("dashboard", "rm", "db.status")
	if err == nil {
		t.Fatal("removing a tile that was never added succeeded")
	}
	if !strings.Contains(errOut, "core.dashboard.absent") || !strings.Contains(errOut, "db.status@staging") {
		t.Errorf("refusal = %q, want the code and the keys that are there", errOut)
	}
}

func loadedConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
