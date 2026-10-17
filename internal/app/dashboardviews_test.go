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
	// The flag that makes a profile link it is not the one that created it:
	// `profile set` says --dashboard where every dashboard command says --view.
	if !strings.Contains(out, "rta profile set <name> --dashboard dbs") {
		t.Errorf("the receipt does not say how a profile links the view:\n%s", out)
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

// `dashboard views` counts the entries a view states (a tile it pins and a tile
// it adds), which is not the number of tiles it draws: the automatic ones come
// with the view that states none, and one entry can draw several panels. The
// column said "tiles" beside `dashboard sections` and `dashboard list`, which
// count what is drawn, so one word named three numbers.
func TestViewsCountsTheEntriesAViewStatesAndSaysSo(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "dbs", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "views", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, `"Entries"`) || strings.Contains(out, `"Tiles"`) {
		t.Errorf("the count is not named for what it counts:\n%s", out)
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
	if got := loadedConfig(t).Profiles["prod"].Dashboard.Describe(); got != "dbs" {
		t.Errorf("the profile selects %q", got)
	}
	show, _, _ := run("profile", "show", "prod")
	if !strings.Contains(show, "dbs") {
		t.Errorf("the profile card does not name its view:\n%s", show)
	}
	if _, errOut, err := run("profile", "set", "prod", "--dashboard", "none"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard.Describe(); got != "" {
		t.Errorf("none left the profile selecting %q", got)
	}
}

func TestADanglingViewIsReportedByCheckAndNeverRefusesTheProfile(t *testing.T) {
	out, errOut, _, err := configRun(t, configRegistry(t),
		"profiles:\n  prod:\n    dashboard: ghost\n    plugins:\n      net:\n        set:\n          timeout: 5\n",
		"config", "check", "-o", "pretty")
	if err == nil || !strings.Contains(out+errOut, "links dashboard view ghost") {
		t.Errorf("the dangling name is not reported: %v\n%s\n%s", err, out, errOut)
	}
}

// A profile links the views of its environment with --dashboard, repeated: the
// first is drawn when it is switched on and V walks all of them.
func TestProfileSetDashboardIsRepeatedToLinkSeveralViewsInOrder(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, view := range []string{"ops", "ops-apps", "databases"} {
		if _, errOut, err := run("dashboard", "add", "db.status", "--view", view, "--set", "dbname="+view); err != nil {
			t.Fatalf("%s: %v %q", view, err, errOut)
		}
	}
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal",
		"--dashboard", "ops", "--dashboard", "ops-apps", "--dashboard", "ops"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard.Names; strings.Join(got, ",") != "ops,ops-apps" {
		t.Errorf("the profile links %v, want ops and ops-apps in the order given, a repeat dropped", got)
	}
	show, _, _ := run("profile", "show", "prod")
	if !strings.Contains(show, "ops, ops-apps") || !strings.Contains(show, "the first is drawn") {
		t.Errorf("the card does not say the profile links two screens and which is drawn:\n%s", show)
	}
	if _, errOut, err := run("profile", "set", "prod", "--dashboard", "default", "--dashboard", "databases"); err != nil {
		t.Fatalf("default is a legal entry: %v %q", err, errOut)
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard.Names; strings.Join(got, ",") != "default,databases" {
		t.Errorf("the profile links %v", got)
	}
	if _, errOut, err := run("profile", "set", "prod", "--dashboard", "none"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard.Names; len(got) != 0 {
		t.Errorf("none alone left the profile linking %v", got)
	}
}

func TestNoneBesideANameIsRefusedAndALinkToAMissingViewNamesTheOnesThatExist(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "ops", "--set", "dbname=ops"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal",
		"--dashboard", "ops", "--dashboard", "none"); err == nil || !strings.Contains(errOut, "core.profile.dashboard.none") {
		t.Errorf("none beside a name was accepted: %v\n%s", err, errOut)
	}
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal",
		"--dashboard", "ops", "--dashboard", "ghost"); err == nil || !strings.Contains(errOut, "core.dashboard.noview") {
		t.Errorf("a second name nothing states was accepted: %v\n%s", err, errOut)
	}
	if _, ok := loadedConfig(t).Profiles["prod"]; ok {
		t.Error("a refused command still wrote the profile")
	}
}

// The listing is an inventory, so it shows every view and who links it, and a
// view linked to several profiles is listed against each.
func TestViewsSaysWhoLinksEachViewIncludingTheDashboardBlock(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, view := range []string{"ops", "databases"} {
		if _, errOut, err := run("dashboard", "add", "db.status", "--view", view, "--set", "dbname="+view); err != nil {
			t.Fatalf("%s: %v %q", view, err, errOut)
		}
	}
	for _, args := range [][]string{
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "ops", "--dashboard", "databases"},
		{"profile", "set", "stage", "--plugin", "db", "--set", "host=stage.internal", "--dashboard", "default", "--dashboard", "databases"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
	}
	out, errOut, err := run("dashboard", "views", "-o", "csv")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	rows := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(rows[0], "View,Linked To,") {
		t.Fatalf("the columns = %q", rows[0])
	}
	for _, want := range []string{"databases,\"prod, stage\"", "ops,prod,", "default,stage,"} {
		found := false
		for _, row := range rows[1:] {
			found = found || strings.HasPrefix(row, want)
		}
		if !found {
			t.Errorf("no row starts with %q:\n%s", want, out)
		}
	}
}

// Naming a view addresses it: the filter is for what V walks, never for what a
// command may act on, so an SRE can edit staging's screen while prod is on.
func TestACommandCanStillAddressAViewAnotherProfileLinks(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "stage-only", "--set", "dbname=a"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, args := range [][]string{
		{"profile", "set", "stage", "--plugin", "db", "--set", "host=stage.internal", "--dashboard", "stage-only"},
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"},
		{"use", "prod"},
		{"dashboard", "add", "db.status", "--view", "stage-only", "--set", "dbname=b"},
		{"profile", "set", "prod", "--dashboard", "stage-only"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
	}
	if got := loadedConfig(t).Profiles["prod"].Dashboard.Names; strings.Join(got, ",") != "stage-only" {
		t.Errorf("linking a view another profile links was refused or lost: %v", got)
	}
}

// Without --view a command acts on the first screen the profile links, which is
// the one drawn on arrival; the receipt says so when there are others.
func TestACommandWithoutAViewSaysWhichOfSeveralLinkedScreensItActedOn(t *testing.T) {
	run := session(t, setRegistry(t))
	for _, view := range []string{"ops", "ops-apps"} {
		if _, errOut, err := run("dashboard", "add", "db.status", "--view", view, "--set", "dbname="+view); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
	for _, args := range [][]string{
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "ops", "--dashboard", "ops-apps"},
		{"use", "prod"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=extra")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "the first of 2 screens prod links") || !strings.Contains(out, "--view ops-apps") {
		t.Errorf("the receipt does not say which linked screen it wrote to or how to name another:\n%s", out)
	}
}
