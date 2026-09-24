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

// An option typed in another case runs as the declared spelling, and profile
// set writes it that way; dashboard add wrote it as typed, so the file and
// `dashboard list` showed a spelling the declaration does not have.
func TestDashboardAddWritesAnOptionTheWayItIsDeclared(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "sslmode=REQUIRE"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].With["sslmode"] != "require" {
		t.Errorf("add = %+v, want sslmode written as require", add)
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
		// What every run would refuse, refused before it is written: a tile
		// with either would fail on each refresh with nobody watching.
		{"a number outside its range", []string{"db.status", "--set", "port=70000"}, "core.input.range"},
		{"a value outside its options", []string{"db.status", "--set", "sslmode=allow"}, "core.input.option"},
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

// codec.jwt's token stopped being Required so a pipe could supply it, and
// `rta dashboard add codec.jwt` then wrote a tile answering "no token to
// read" on every refresh: a tile reads no pipe, `--set` refuses a
// credential, and no profile fills it. Refused as it was before, with a hint
// that does not send the reader to a `--set` refusing the same thing, and
// the card no longer offers the command.
func TestDashboardAddRefusesACredentialATileCannotBeGiven(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	run := session(t, reg)
	for _, id := range []string{"codec.jwt", "codec.jwk"} {
		_, errOut, err := run("dashboard", "add", id, "--dry-run")
		if err == nil || !strings.Contains(errOut, "core.dashboard.needs") {
			t.Errorf("%s: err = %v, output %q; want core.dashboard.needs", id, err, errOut)
		}
		if strings.Contains(errOut, "--set") && !strings.Contains(errOut, "a tile cannot be given") {
			t.Errorf("%s: the hint points at --set: %q", id, errOut)
		}
		c, _ := reg.Capability(id)
		if got := dashboardRow(reg, c).Value; !strings.Contains(got, "never a tile") {
			t.Errorf("%s: card says %q, want never a tile", id, got)
		}
	}
}

func TestDashboardAddDryRunWritesNothing(t *testing.T) {
	run := session(t, setRegistry(t))
	out, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=shop", "--dry-run")
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
	out, errOut, err := run("dashboard", "rm", "db.status", "--profile", "prod")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "rta dashboard add db.status --profile prod`") {
		t.Errorf("receipt = %q, want the way back to name the reference removed", out)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].Profile != "staging" {
		t.Errorf("add = %v, want only the staging tile left", add)
	}
	_, errOut, err = run("dashboard", "rm", "db.status")
	if err == nil {
		t.Fatal("removing a tile that was never added succeeded")
	}
	if !strings.Contains(errOut, "core.dashboard.absent") || !strings.Contains(errOut, "db.status@staging") {
		t.Errorf("refusal = %q, want the code and the keys that are there", errOut)
	}
}

// A profile holding several connections to the plugin expands: the receipt
// names every panel, and `list` shows each one. hide takes one panel down
// by its connection and unhide brings it back; an added tile is withdrawn,
// not hidden, and the refusal names the command.
func TestDashboardAddExpandsAndHideTakesOnePanelDown(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, args := range [][]string{
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"},
		{"profile", "set", "prod", "--plugin", "db/analytics", "--set", "host=analytics.prod.internal"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "one panel per db connection") || !strings.Contains(out, "prod/analytics") {
		t.Errorf("receipt = %q, want the expansion named", out)
	}
	list, _, err := run("dashboard", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "prod/analytics") || !strings.Contains(list, "one of several") {
		t.Errorf("list = %q, want both panels, marked as one of several", list)
	}

	if _, errOut, err := run("dashboard", "hide", "db.status", "--profile", "prod/analytics"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if h := loadedConfig(t).Dashboard.Hidden; len(h) != 1 || h[0] != "db.status@prod/analytics" {
		t.Errorf("hidden = %v, want the panel's key", h)
	}
	list, _, _ = run("dashboard", "list", "-o", "json")
	if !strings.Contains(list, "hidden") {
		t.Errorf("list = %q, want the hidden panel marked", list)
	}
	if _, errOut, err := run("dashboard", "unhide", "db.status", "--profile", "prod/analytics"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if h := loadedConfig(t).Dashboard.Hidden; len(h) != 0 {
		t.Errorf("hidden = %v, want it empty again", h)
	}

	_, errOut, err = run("dashboard", "hide", "db.status", "--profile", "ghost")
	if err == nil || !strings.Contains(errOut, "core.dashboard.absent") {
		t.Errorf("hiding a panel that is not on the dashboard: err=%v %q", err, errOut)
	}
	// An added tile that did not expand is withdrawn, not hidden: with the
	// expanding entry gone, the pinned one is the only panel of that key.
	for _, args := range [][]string{
		{"dashboard", "rm", "db.status", "--profile", "prod"},
		{"dashboard", "add", "db.status", "--profile", "prod/analytics"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
	_, errOut, err = run("dashboard", "hide", "db.status", "--profile", "prod/analytics")
	if err == nil || !strings.Contains(errOut, "core.dashboard.notautomatic") {
		t.Errorf("hiding an added tile must point at rm: err=%v %q", err, errOut)
	}
	_, errOut, err = run("dashboard", "unhide", "db.status", "--profile", "prod/analytics")
	if err == nil || !strings.Contains(errOut, "core.dashboard.nothidden") {
		t.Errorf("unhiding what is not hidden: err=%v %q", err, errOut)
	}
	// The automatic tile hides by its capability, as H does, and comes back.
	if _, errOut, err := run("dashboard", "hide", "db.status"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if h := loadedConfig(t).Dashboard.Hidden; len(h) != 1 || h[0] != "db.status" {
		t.Errorf("hidden = %v, want the automatic tile's capability", h)
	}
	if _, errOut, err := run("dashboard", "unhide", "db.status"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
}

// A capability the automatic dashboard already shows is not added twice:
// bare, the add is refused and points at unhide when that is what was
// meant; with --span or --set, the entry takes the automatic tile's place.
func TestDashboardAddOfAnAutomaticTileIsRefusedOrTakesItsPlace(t *testing.T) {
	run := session(t, setRegistry(t))
	_, errOut, err := run("dashboard", "add", "db.status")
	if err == nil || !strings.Contains(errOut, "core.dashboard.automatic") {
		t.Fatalf("a bare add of the automatic tile: err=%v %q", err, errOut)
	}
	if _, errOut, err := run("dashboard", "hide", "db.status"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, _ := run("dashboard", "add", "db.status"); !strings.Contains(errOut, "rta dashboard unhide db.status") {
		t.Errorf("refusal = %q, want the way back for a hidden tile", errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--span", "2")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "in place of") {
		t.Errorf("receipt = %q, want it to say the entry takes the automatic tile's place", out)
	}
	list, _, err := run("dashboard", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(list, `"db.status"`) != 1 || !strings.Contains(list, "added") {
		t.Errorf("list = %q, want one db.status row, the added one", list)
	}
	out, errOut, err = run("dashboard", "rm", "db.status")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "rta dashboard add db.status --span 2`") {
		t.Errorf("receipt = %q, want the whole entry as the way back, since the bare add is refused", out)
	}
}

// With the switch on and the automatic tile expanded into that
// environment's connections, hide by the capability takes every panel
// down, as H on an automatic tile does.
func TestDashboardHideByCapabilityCoversAnExpandedAutomaticTile(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, args := range [][]string{
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"},
		{"profile", "set", "prod", "--plugin", "db/analytics", "--set", "host=analytics.prod.internal"},
		{"use", "prod"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
	list, _, err := run("dashboard", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "prod/analytics") {
		t.Fatalf("list = %q, want the automatic tile expanded under prod", list)
	}
	if _, errOut, err := run("dashboard", "hide", "db.status"); err != nil {
		t.Fatalf("hiding the expanded automatic tile by its capability: %v %q", err, errOut)
	}
	if h := loadedConfig(t).Dashboard.Hidden; len(h) != 1 || h[0] != "db.status" {
		t.Errorf("hidden = %v, want the capability", h)
	}
	list, _, _ = run("dashboard", "list", "-o", "json")
	if strings.Count(list, "hidden") < 2 {
		t.Errorf("list = %q, want both panels hidden", list)
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
