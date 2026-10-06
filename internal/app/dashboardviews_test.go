package app

import (
	"strings"
	"testing"
)

// A view is a screen of its own: what a command adds, hides or lists is the view
// it is told about, or the one that is drawn, and nothing that belongs to
// another screen moves.

func TestAddWithAViewCreatesItAndLeavesTheBlockAlone(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=main"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=shop", "--view", "dbs")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "dbs") || !strings.Contains(out, "--view dbs") {
		t.Errorf("the receipt does not name the view or the way back:\n%s", out)
	}
	cfg := loadedConfig(t)
	if len(cfg.Dashboard.Add) != 1 || cfg.Dashboard.Add[0].With["dbname"] != "main" {
		t.Errorf("the block changed: %+v", cfg.Dashboard.Add)
	}
	view, ok := cfg.Dashboard.Views["dbs"]
	if !ok || len(view.Add) != 1 || view.Add[0].With["dbname"] != "shop" {
		t.Errorf("the view = %+v", cfg.Dashboard.Views)
	}
}

func TestACommandWithoutAViewActsOnTheOneThatIsDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "dbs", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "dbs"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("use", "prod"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	// With prod on, a bare add goes to dbs, which is what is on screen.
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=other"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	cfg := loadedConfig(t)
	if len(cfg.Dashboard.Add) != 0 {
		t.Errorf("the block took what the drawn view should have: %+v", cfg.Dashboard.Add)
	}
	if got := len(cfg.Dashboard.Views["dbs"].Add); got != 1 {
		t.Errorf("dbs holds %d entries, want 1 (the same tile, replaced)", got)
	}
	// --view default names the block when that is the one meant.
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=block", "--view", "default"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Dashboard.Add; len(got) != 1 || got[0].With["dbname"] != "block" {
		t.Errorf("the block = %+v", got)
	}
}

func TestListAndRmAndHideNameTheirViewAndRefuseOneThatIsNotThere(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "dbs", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	list, _, err := run("dashboard", "list", "--view", "dbs")
	if err != nil || !strings.Contains(list, "db.status") {
		t.Fatalf("list --view dbs: %v\n%s", err, list)
	}
	for _, args := range [][]string{
		{"dashboard", "list", "--view", "nowhere"},
		{"dashboard", "rm", "db.status", "--view", "nowhere"},
		{"dashboard", "hide", "db.status", "--view", "nowhere"},
	} {
		_, errOut, err := run(args...)
		if err == nil || !strings.Contains(errOut, "core.dashboard.noview") || !strings.Contains(errOut, "dbs") {
			t.Errorf("%v: %v\n%s", args, err, errOut)
		}
	}
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "Not Valid"); err == nil || !strings.Contains(errOut, "core.dashboard.view") {
		t.Errorf("a bad name was taken: %v\n%s", err, errOut)
	}
	out, errOut, err := run("dashboard", "rm", "db.status", "--view", "dbs")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "(view dbs)") || !strings.Contains(out, "--view dbs") {
		t.Errorf("the receipt of rm = %s", out)
	}
	if got := len(loadedConfig(t).Dashboard.Views["dbs"].Add); got != 0 {
		t.Errorf("dbs still holds %d entries", got)
	}
}

func TestViewsListsEveryViewWhoSelectsItAndTheOneDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, args := range [][]string{
		{"dashboard", "add", "db.status", "--view", "dbs", "--set", "dbname=shop"},
		{"dashboard", "add", "db.status", "--view", "ops", "--set", "dbname=ops"},
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "ops"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
	}
	out, errOut, err := run("dashboard", "views", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{"default", "dbs", "ops", "prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("views does not say %q:\n%s", want, out)
		}
	}
	if _, errOut, err := run("use", "prod"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, _, _ = run("dashboard", "views", "-o", "json")
	if !strings.Contains(out, "yes — prod is switched on") {
		t.Errorf("the drawn view is not named once prod is on:\n%s", out)
	}
}

func TestProfileSetDashboardTakesAViewThatExistsOrNone(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "ghost"); err == nil || !strings.Contains(errOut, "core.dashboard.noview") {
		t.Errorf("a view nothing states was selected: %v\n%s", err, errOut)
	}
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "dbs", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "dbs"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard; got != "dbs" {
		t.Errorf("the profile selects %q", got)
	}
	show, _, _ := run("profile", "show", "prod")
	if !strings.Contains(show, "dbs") {
		t.Errorf("the profile card does not name its view:\n%s", show)
	}
	if _, errOut, err := run("profile", "set", "prod", "--dashboard", "none"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard; got != "" {
		t.Errorf("none left the profile selecting %q", got)
	}
}

func TestADanglingViewIsReportedByCheckAndNeverRefusesTheProfile(t *testing.T) {
	out, errOut, _, err := configRun(t, configRegistry(t),
		"profiles:\n  prod:\n    dashboard: ghost\n    plugins:\n      net:\n        set:\n          timeout: 5\n",
		"config", "check", "-o", "pretty")
	if err == nil || !strings.Contains(out+errOut, "selects dashboard view ghost") {
		t.Errorf("the dangling name is not reported: %v\n%s\n%s", err, out, errOut)
	}
}
