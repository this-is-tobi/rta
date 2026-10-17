package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// A profile can write its screen in its own entry, and the commands that read
// and write views treat that block as the view it is: one named after the
// profile.

// stagingEntry is an environment whole, with the screen it shows written in
// the entry, as a person puts it in a drop-in.
const stagingEntry = `profiles:
  staging:
    note: shared
    plugins:
      db:
        set:
          host: staging.internal
    dashboard:
      # the database first
      add:
      - id: db.status
        with:
          dbname: orders
      hidden:
      - sys.overview
      order:
      - db.status
`

// dropIn writes a drop-in beside the config file a session runs against and
// returns its path.
func dropIn(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(config.Path()), "config.d", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// --dashboard names a view, and naming one over a block would throw the block
// away, which no flag spelling should do without being asked: `none` included,
// since it means "back to the dashboard: block" and not "forget my tiles".
func TestProfileSetDashboardRefusesAProfileWhoseScreenIsItsOwn(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	if _, errOut, err := run("dashboard", "add", "db.status", "--view", "ops", "--set", "dbname=shop"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, named := range []string{"ops", "none", "default", "ghost"} {
		_, errOut, err := run("profile", "set", "staging", "--dashboard", named)
		if err == nil || !strings.Contains(errOut, "core.profile.dashboard.inline") ||
			!strings.Contains(errOut, "1 tile") || !strings.Contains(errOut, "--view staging") {
			t.Errorf("--dashboard %s: %v\n%s", named, err, errOut)
		}
	}
	if got := fileText(t, path); got != stagingEntry {
		t.Errorf("a refusal wrote to the drop-in:\n%s", got)
	}
}

// What the flag does not mention is left as it was, the block included.
func TestProfileSetKeepsTheBlockWhenItIsAboutSomethingElse(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	if _, errOut, err := run("profile", "set", "staging", "--note", "the shared one"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	p := loadedConfig(t).Profiles["staging"]
	if p.Note != "the shared one" || p.Dashboard.Inline == nil || len(p.Dashboard.Inline.Add) != 1 {
		t.Errorf("after the write: %+v", p)
	}
	text := fileText(t, path)
	for _, want := range []string{"# the database first", "dbname: orders", "- sys.overview"} {
		if !strings.Contains(text, want) {
			t.Errorf("the drop-in lost %q:\n%s", want, text)
		}
	}
}

// The screen of a profile is listed with the views: what selects it, the file
// it is in — the drop-in that holds the entry — and, once the environment is
// switched on, that it is the one drawn and why.
func TestViewsListsAProfilesOwnScreenWithItsFileAndWhyItIsDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	out, errOut, err := run("dashboard", "views", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{`"staging"`, "staging (its own)", filepath.Join("config.d", "20-staging.yaml")} {
		if !strings.Contains(out, want) {
			t.Errorf("views does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "is switched on") {
		t.Errorf("a view is drawn with nothing switched on:\n%s", out)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if out, _, _ = run("dashboard", "views", "-o", "json"); !strings.Contains(out, "yes — staging is switched on") {
		t.Errorf("the drawn view does not say why:\n%s", out)
	}
}

// Switching the environment on is what makes its screen the one every
// dashboard command acts on and draws, and switching off puts the
// dashboard: block back.
func TestTheDashboardCommandsDrawAProfilesOwnScreenWhileItIsOn(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	has := func(args ...string) bool {
		t.Helper()
		out, errOut, err := run(append(args, "-o", "json")...)
		if err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
		return strings.Contains(out, "orders")
	}
	if has("dashboard", "list") {
		t.Fatal("the dashboard: block draws a tile only staging's screen adds")
	}
	if !has("dashboard", "list", "--view", "staging") {
		t.Error("--view staging does not draw the screen written in the entry")
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !has("dashboard", "list") {
		t.Error("with staging switched on, the screen it states is not the one listed")
	}
	if has("dashboard", "list", "--view", "default") {
		t.Error("--view default does not name the dashboard: block while staging is on")
	}
	if _, errOut, err := run("use", "--off"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if has("dashboard", "list") {
		t.Error("the screen stayed after the environment was switched off")
	}
}

// The writers add, hide, unhide and remove in the screen written in the entry,
// and put the change in the entry, in the file that holds it, with the
// person's comments around it: never in the config file, and never as a view
// of the same name beside the profile.
func TestTheDashboardWritersChangeTheEntryAProfilesScreenIsWrittenIn(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"dashboard", "add", "db.status", "--profile", "staging", "--view", "staging"}, "added db.status@staging"},
		{[]string{"dashboard", "hide", "grant.list", "--view", "staging"}, "hid grant.list"},
		{[]string{"dashboard", "unhide", "grant.list", "--view", "staging"}, "brought back grant.list"},
		{[]string{"dashboard", "rm", "db.status", "--profile", "staging", "--view", "staging"}, "removed db.status@staging"},
	}
	for _, step := range steps {
		out, errOut, err := run(step.args...)
		if err != nil {
			t.Fatalf("%v: %v %q", step.args, err, errOut)
		}
		if !strings.Contains(out, step.want) || !strings.Contains(out, "staging") ||
			!strings.Contains(out, filepath.Join("config.d", "20-staging.yaml")) {
			t.Errorf("%v: the receipt does not name the view and the file the entry is in:\n%s", step.args, out)
		}
		cfg := loadedConfig(t)
		if len(cfg.Dashboard.Views) != 0 || len(cfg.Dashboard.Add) != 0 || len(cfg.Dashboard.Hidden) != 0 {
			t.Fatalf("%v: the write landed outside the entry: %+v", step.args, cfg.Dashboard)
		}
		if got := cfg.Profiles["staging"].Plugins["db"].Set["host"]; got != "staging.internal" {
			t.Fatalf("%v: the entry lost its connection: %v", step.args, got)
		}
	}
	// Every change undone, the entry says what it said — its keys written in
	// the order every rewritten entry has, with the comment that was in the
	// block still above what it was above.
	block := loadedConfig(t).Profiles["staging"].Dashboard.Inline
	if block == nil || len(block.Add) != 1 || block.Add[0].With["dbname"] != "orders" ||
		!slices.Equal(block.Hidden, []string{"sys.overview"}) || !slices.Equal(block.Order, []string{"db.status"}) {
		t.Errorf("after every change was undone: %+v", block)
	}
	if got := fileText(t, path); !strings.Contains(got, "      # the database first\n      add:\n") {
		t.Errorf("the comment inside the block was lost:\n%s", got)
	}
	if b, err := os.ReadFile(config.Path()); err == nil {
		t.Errorf("a write into a drop-in created the config file: %q", b)
	}
}

// With no --view the command acts on the screen that is drawn, which with the
// environment switched on is the one in its entry: what is added is what is
// being looked at.
func TestWithTheEnvironmentOnABareDashboardAddWritesToItsEntry(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "staging")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, filepath.Join("config.d", "20-staging.yaml")) {
		t.Errorf("the receipt does not name the entry's file:\n%s", out)
	}
	cfg := loadedConfig(t)
	if len(cfg.Dashboard.Add) != 0 || len(cfg.Profiles["staging"].Dashboard.Inline.Add) != 2 {
		t.Errorf("block %+v, screen %+v", cfg.Dashboard.Add, cfg.Profiles["staging"].Dashboard.Inline.Add)
	}
	if !strings.Contains(fileText(t, path), "profile: staging") {
		t.Errorf("the drop-in does not hold the tile:\n%s", fileText(t, path))
	}
}

// A run that would change nothing, or that only asks, leaves every file exactly
// as it was — the entry byte for byte, and no config file made.
func TestADryRunOrAnUnchangedAddLeavesTheEntryAsItWas(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "staging", "--view", "staging", "--dry-run")
	if err != nil || !strings.Contains(out, "would write") || !strings.Contains(out, filepath.Join("config.d", "20-staging.yaml")) {
		t.Fatalf("dry run: %v %q\n%s", err, errOut, out)
	}
	out, errOut, err = run("dashboard", "add", "db.status", "--set", "dbname=orders", "--view", "staging")
	if err != nil || !strings.Contains(out, "nothing written") {
		t.Fatalf("an add of what is there: %v %q\n%s", err, errOut, out)
	}
	if got := fileText(t, path); got != stagingEntry {
		t.Errorf("the entry changed:\n%s", got)
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Error("a run that wrote nothing made the config file")
	}
}

// A file rta does not honour arranges no screen, whatever its profiles say, and
// the writers refuse to write into it rather than put tiles where nothing
// reads them.
func TestAWorkingDirectoryFilesProfileScreenIsNeitherDrawnNorWritten(t *testing.T) {
	reg := setRegistry(t)
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_CONFIG", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if config.TrustedPath() {
		t.Skip("this machine has a config directory")
	}
	if err := os.WriteFile(".rta.yaml", []byte(stagingEntry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	run := func(args ...string) (string, string, error) {
		root := NewRoot(reg, "test")
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), errOut.String(), err
	}
	out, errOut, err := run("dashboard", "list", "--view", "staging", "-o", "json")
	if err != nil || strings.Contains(out, "orders") {
		t.Errorf("a repository's file arranged the screen: %v %q\n%s", err, errOut, out)
	}
	_, errOut, err = run("dashboard", "add", "db.status", "--view", "staging")
	if err == nil || !strings.Contains(errOut, "core.dashboard.untrusted") {
		t.Errorf("a tile was written where nothing reads it: %v\n%s", err, errOut)
	}
	if b, _ := os.ReadFile(".rta.yaml"); string(b) != stagingEntry {
		t.Errorf(".rta.yaml changed:\n%s", b)
	}
}

// What an environment shows is on its card and in the list: the name of the
// view it selects, or `inline: 1 tile` for the screen written in its entry.
func TestProfileShowAndListSayWhatScreenAProfileShows(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	for _, args := range [][]string{
		{"dashboard", "add", "db.status", "--view", "ops", "--set", "dbname=ops"},
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal", "--dashboard", "ops"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
	}
	show, errOut, err := run("profile", "show", "staging")
	if err != nil || !strings.Contains(show, "inline: 1 tile") || !strings.Contains(show, "rta dashboard list --view staging") {
		t.Errorf("the card of an environment with its own screen: %v %q\n%s", err, errOut, show)
	}
	if show, _, _ = run("profile", "show", "prod"); !strings.Contains(show, "ops — the screens linked to this environment") {
		t.Errorf("the card of an environment that links a view:\n%s", show)
	}
	list, errOut, err := run("profile", "list", "-o", "csv")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	rows := strings.Split(strings.TrimSpace(list), "\n")
	if rows[0] != "Profile,Plugins,Status,Screen,Theme,Note" {
		t.Errorf("the list's columns = %q", rows[0])
	}
	for _, want := range []string{"prod,db,ok,ops,,", "staging,db,ok,inline: 1 tile,,shared"} {
		if !slices.Contains(rows, want) {
			t.Errorf("the list lacks the row %q:\n%s", want, list)
		}
	}
}

// Taking an environment away takes its screen with it, and says so, since the
// tiles are not kept anywhere else.
func TestRemovingAProfileSaysItsOwnScreenWentWithIt(t *testing.T) {
	run := session(t, setRegistry(t))
	path := dropIn(t, "20-staging.yaml", stagingEntry)
	out, errOut, err := run("profile", "rm", "staging", "--dry-run")
	if err != nil || !strings.Contains(out, "its own screen, 1 tile, would go with it") {
		t.Fatalf("the preview: %v %q\n%s", err, errOut, out)
	}
	if got := fileText(t, path); got != stagingEntry {
		t.Fatalf("a preview wrote:\n%s", got)
	}
	out, errOut, err = run("profile", "rm", "staging", "--yes")
	if err != nil || !strings.Contains(out, "its own screen, 1 tile, went with it") {
		t.Fatalf("the removal: %v %q\n%s", err, errOut, out)
	}
	if _, still := loadedConfig(t).Profiles["staging"]; still {
		t.Error("the profile is still there")
	}
}

// A view of the same name beside the block is reported where every profile
// problem is — the list, the card — and the environment is still one that can
// be switched on: it draws the dashboard: block until the two are one.
func TestAViewAndABlockOfTheSameNameAreReportedAndNeitherIsDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	if err := os.WriteFile(config.Path(), []byte("dashboard:\n  views:\n    staging:\n      width: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropIn(t, "20-staging.yaml", stagingEntry)
	list, errOut, err := run("profile", "list", "-o", "csv")
	if err != nil || !strings.Contains(list, "staging,db,warn,inline: 1 tile,") ||
		!strings.Contains(list, "a view called staging") || !strings.Contains(list, "neither is drawn") {
		t.Errorf("the list: %v %q\n%s", err, errOut, list)
	}
	show, _, _ := run("profile", "show", "staging")
	if !strings.Contains(show, "warning") || !strings.Contains(show, "keep one") {
		t.Errorf("the card does not say it:\n%s", show)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("a screen in doubt refused the environment: %v %q", err, errOut)
	}
	out, _, err := run("dashboard", "list", "-o", "json")
	if err != nil || strings.Contains(out, "orders") {
		t.Errorf("one of the two was drawn: %v\n%s", err, out)
	}
	views, _, _ := run("dashboard", "views", "-o", "json")
	if strings.Contains(views, "(its own)") {
		t.Errorf("the block is listed as a view that is drawn:\n%s", views)
	}
}

// shopEntry is an environment whole against the registry the config tests use,
// with its screen written in the entry.
const shopEntry = `profiles:
  shop:
    plugins:
      net:
        set:
          timeout: 5
    dashboard:
      add:
      - id: net.ping
`

// The check holds a screen written in an entry to a view's keys, with the line
// and the file the typo is in, and a drop-in that is right says so.
func TestConfigCheckHoldsAProfilesOwnScreenToAViewsKeys(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/20-shop.yaml": strings.Replace(shopEntry, "      add:", "      tile:\n      - id: net.ping\n      add:", 1),
	})
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "pretty")
	if err == nil || !strings.Contains(out+errOut, "profiles.shop.dashboard.tile") ||
		!strings.Contains(out+errOut, "20-shop.yaml") || !strings.Contains(out+errOut, `did you mean "tiles"`) {
		t.Errorf("the typo in the block was not named: %v\n%s%s", err, out, errOut)
	}
	_, path = split(t, map[string]string{"config.yaml": "output: json\n", "config.d/20-shop.yaml": shopEntry})
	out, errOut, err = runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "pretty")
	if err != nil || strings.Contains(out+errOut, "shop") {
		t.Errorf("a right drop-in was not clean: %v\n%s%s", err, out, errOut)
	}
}

// A profile in one file naming a view in another is not naming one nothing
// states, and a view and a block of one name in two files are reported once, in
// the file that holds the profile, with the line of its `dashboard:` key.
func TestConfigCheckReadsViewsAcrossFiles(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml": "dashboard:\n  views:\n    ops:\n      width: 3\n    shop:\n      width: 5\n",
		"config.d/20-shop.yaml": shopEntry +
			"  lost:\n    plugins:\n      net: {}\n    dashboard: ghost\n  prod:\n    plugins:\n      net: {}\n    dashboard: ops\n",
	})
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "pretty")
	text := out + errOut
	if err == nil {
		t.Fatalf("a collision was not a problem:\n%s", text)
	}
	if got := strings.Count(text, "neither is drawn"); got != 1 {
		t.Errorf("the collision was said %d times:\n%s", got, text)
	}
	if !strings.Contains(text, "profile shop writes its dashboard in its entry") || !strings.Contains(text, "20-shop.yaml") ||
		!strings.Contains(text, "profiles.shop.dashboard") {
		t.Errorf("the collision is not told with its profile, file and key:\n%s", text)
	}
	if !strings.Contains(text, "links dashboard view ghost") {
		t.Errorf("a name nothing states was not reported:\n%s", text)
	}
	if strings.Contains(text, "links dashboard view ops") {
		t.Errorf("a view in another file was called unstated:\n%s", text)
	}
}

// The same collision in one file is one problem too. The check lists the notes
// every profile has, and the collision is one of them, so without leaving that
// note out it was said once with the line of its key and once with none, and
// counted as two problems.
func TestConfigCheckSaysACollisionInOneFileOnceWithItsLine(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml": "dashboard:\n  views:\n    shop:\n      width: 5\n" + shopEntry,
	})
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "pretty")
	text := out + errOut
	if err == nil {
		t.Fatalf("a collision was not a problem:\n%s", text)
	}
	if got := strings.Count(text, "neither is drawn"); got != 1 {
		t.Errorf("the collision was said %d times:\n%s", got, text)
	}
	if !strings.Contains(text, "1 problem in") || !strings.Contains(text, "profiles.shop.dashboard") {
		t.Errorf("the collision is not one problem with the key of its profile:\n%s", text)
	}
}

// Doctor says it once, as a note on the profile where every profile problem is
// said, and still says the name that nothing states.
func TestDoctorReportsAScreenInDoubtOnceAndADanglingNameStill(t *testing.T) {
	_, configDir := isolate(t)
	files := map[string]string{
		"config.yaml": "dashboard:\n  views:\n    shop:\n      width: 5\n",
		"config.d/20-shop.yaml": shopEntry +
			"  lost:\n    plugins:\n      net: {}\n    dashboard: ghost\n",
	}
	for name, text := range files {
		path := filepath.Join(configDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var inDoubt, dangling []string
	for _, r := range doctorTable(configRegistry(t), false).Rows {
		switch {
		case strings.Contains(r[2], "neither is drawn"):
			inDoubt = append(inDoubt, r[0]+": "+r[2])
		case strings.Contains(r[2], "links dashboard view ghost"):
			dangling = append(dangling, r[0]+": "+r[2])
		}
	}
	if len(inDoubt) != 1 || !strings.HasPrefix(inDoubt[0], "profile: ") || !strings.Contains(inDoubt[0], "keep one") {
		t.Errorf("a screen in doubt was said %v", inDoubt)
	}
	if len(dangling) != 1 || !strings.HasPrefix(dangling[0], "dashboard: ") {
		t.Errorf("a name nothing states was said %v", dangling)
	}
}

// A profile's own screen is drawn while that environment is switched on, and a
// receipt that told a person to select it with `profile set --dashboard`, which
// that very profile refuses, would send them to a command that throws it away.
func TestAddToAProfilesOwnScreenSaysWhenItIsDrawn(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "staging", "--view", "staging")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "written in profile staging's own entry") || !strings.Contains(out, "`rta use staging`") ||
		strings.Contains(out, "--dashboard") {
		t.Errorf("the receipt does not say when the screen is drawn:\n%s", out)
	}
}

func TestShellCompletionOffersAProfilesOwnScreenAsAView(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "20-staging.yaml", stagingEntry)
	out, _, err := run("__complete", "dashboard", "list", "--view", "")
	if err != nil || !strings.Contains(out, "staging\t1 entry, written in profile staging") {
		t.Errorf("--view completes to:\n%s (%v)", out, err)
	}
}
