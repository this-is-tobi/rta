package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// `rta dashboard add --section`, the SECTION column of `list`, and `rta dashboard
// sections`: the way to state a section from a shell, and to see what the
// dashboard groups.

// A section is a property of the entry, so the entry that is written says it, the
// list says which section each tile is in, and the command that puts the entry
// back says it too.
func TestDashboardAddPutsATileInASectionAndListSaysWhichOne(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod", "--section", "databases")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "databases — drawn with the other tiles that name it") {
		t.Errorf("the receipt does not say the section:\n%s", out)
	}
	add := loadedConfig(t).Dashboard.Add
	if len(add) != 1 || add[0].Section != "databases" || add[0].Profile != "prod" {
		t.Fatalf("add = %+v", add)
	}

	csv, errOut, err := run("dashboard", "list", "-o", "csv")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if !strings.HasPrefix(lines[0], "Tile,Profile,Section,Source,") {
		t.Errorf("the columns of list are %q", lines[0])
	}
	found := false
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "db.status,prod,databases,added,") {
			found = true
		}
	}
	if !found {
		t.Errorf("the added tile is not listed in its section:\n%s", csv)
	}

	// Adding it again without the section states the entry without one: the add
	// replaces what it names, as it does for a span and an input.
	if _, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].Section != "" {
		t.Errorf("add without --section kept %+v", add)
	}
	if _, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod", "--section", "  databases "); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err = run("dashboard", "rm", "db.status", "--profile", "prod")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "rta dashboard add db.status --profile prod --section databases`") {
		t.Errorf("the way back does not put the entry in its section:\n%s", out)
	}
}

// The line a receipt names to put a removed entry back is the whole entry and the
// view it was on, the one H names in the TUI for the same entry
// (TestHSpellsTheWayBackOfAnEntryWhole): both are config.Tile.AddCommand, so a
// field one of them left out would be left out of the other as well.
func TestDashboardRmNamesTheWholeEntryAsTheWayBack(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	const line = "rta dashboard add db.status --profile prod --span 2 --section host --set dbname=shop --view ops"
	args := strings.Fields(strings.TrimPrefix(line, "rta "))
	if out, errOut, err := run(args...); err != nil {
		t.Fatalf("%v %q\n%s", err, errOut, out)
	}
	out, errOut, err := run("dashboard", "rm", "db.status", "--profile", "prod", "--view", "ops")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "`"+line+"`") {
		t.Errorf("the way back is not %q:\n%s", line, out)
	}
}

// The automatic tile can be given a section without being given anything else:
// the entry takes its place, as it does for a span.
func TestDashboardAddOfTheAutomaticTileWithOnlyASectionTakesItsPlace(t *testing.T) {
	run := session(t, setRegistry(t))
	_, errOut, err := run("dashboard", "add", "db.status")
	if err == nil || !strings.Contains(errOut, "--section") {
		t.Errorf("a bare add of the automatic tile does not name --section among what it takes: %v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--section", "host")
	if err != nil {
		t.Fatalf("a section is a reason to write the entry: %v %q", err, errOut)
	}
	if !strings.Contains(out, "in place of") {
		t.Errorf("the receipt does not say the entry took the automatic tile's place:\n%s", out)
	}
	list, _, err := run("dashboard", "list", "-o", "json")
	if err != nil || strings.Count(list, `"db.status"`) != 1 || !strings.Contains(list, `"host"`) {
		t.Errorf("list = %v\n%s", err, list)
	}
}

// What cannot be a heading is refused where it is typed, and nothing is written.
func TestDashboardAddRefusesASectionNoOneCanName(t *testing.T) {
	run := session(t, setRegistry(t))
	for name, section := range map[string]string{
		"too long":       strings.Repeat("a", config.MaxSectionName+1),
		"a control code": "red" + string(rune(0x1b)) + "[31m",
		"a line break":   "two\nlines",
		"reordering":     "ab" + string(rune(0x202e)) + "cd",
	} {
		t.Run(name, func(t *testing.T) {
			_, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=shop", "--section", section)
			if err == nil || !strings.Contains(errOut, "core.dashboard.section") {
				t.Errorf("accepted %q: %v %q", section, err, errOut)
			}
		})
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Errorf("a refused add wrote %s", config.Path())
	}
}

// The writers keep what is stated beside the entry they add: a view's and a
// profile's own screen's `sections:` and their comments stay where they were
// when a tile is put in one of them.
func TestAddingToAViewOrAProfilesOwnScreenKeepsItsSections(t *testing.T) {
	run := session(t, setRegistry(t))
	opsFile := dropIn(t, "10-ops.yaml", `dashboard:
  views:
    ops:
      sections:
        databases:
          collapsed: true  # folded until I need them
          columns: 3
`)
	staging := dropIn(t, "20-staging.yaml", `profiles:
  staging:
    plugins:
      db:
        set:
          host: staging.internal
    dashboard:
      sections:
        mine:
          collapsed: true
`)
	for _, args := range [][]string{
		{"dashboard", "add", "db.status", "--profile", "staging", "--view", "ops", "--section", "databases"},
		{"dashboard", "add", "db.status", "--profile", "staging", "--view", "staging", "--section", "mine"},
	} {
		if out, errOut, err := run(args...); err != nil {
			t.Fatalf("%v: %v %q\n%s", args, err, errOut, out)
		}
	}
	cfg := loadedConfig(t)
	ops, _ := cfg.Block("ops")
	if got := ops.SectionOf("databases"); !got.Collapsed || got.Columns != 3 || len(ops.Add) != 1 || ops.Add[0].Section != "databases" {
		t.Errorf("ops = %+v", ops)
	}
	own, _ := cfg.Block("staging")
	if !own.SectionOf("mine").Collapsed || len(own.Add) != 1 || own.Add[0].Section != "mine" {
		t.Errorf("staging's own screen = %+v", own)
	}
	if text := fileText(t, opsFile); !strings.Contains(text, "# folded until I need them") || !strings.Contains(text, "section: databases") {
		t.Errorf("the view's file after the write:\n%s", text)
	}
	if text := fileText(t, staging); !strings.Contains(text, "mine:") || !strings.Contains(text, "section: mine") ||
		strings.Contains(text, "views:") {
		t.Errorf("the profile's entry after the write:\n%s", text)
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Errorf("the writes made %s", config.Path())
	}
}

// Every section the configuration draws or states, view by view, with how it
// starts and how wide it is, and the file that states the view.
func TestDashboardSectionsListsEverySectionWithHowItStarts(t *testing.T) {
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
	dropIn(t, "10-ops.yaml", `dashboard:
  views:
    ops:
      sections:
        databases:
          collapsed: true
          columns: 3
        ghost:
          collapsed: true
      add:
      - id: db.status
        profile: prod
        section: databases
`)
	dropIn(t, "20-block.yaml", `dashboard:
  sections:
    db · prod:
      collapsed: true
`)
	csv, errOut, err := run("dashboard", "sections", "-o", "csv")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	rows := map[string]string{}
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if lines[0] != "View,Section,Tiles,Starts,Columns,File" {
		t.Errorf("the columns are %q", lines[0])
	}
	for _, line := range lines[1:] {
		fields := strings.SplitN(line, ",", 3)
		rows[fields[0]+"|"+strings.Trim(fields[1], `"`)] = line
	}
	for key, want := range map[string]string{
		// The automatic tile expanded into prod's two connections under the group
		// named after the environment that is on, and the block says it starts folded.
		"default|db · prod": `default,db · prod,2,folded,,`,
		// A view's own: how many tiles name it, how it starts, how wide it is.
		"ops|databases": `ops,databases,2,folded,3,`,
		// A section a view states and no tile is in is listed, with none.
		"ops|ghost": `ops,ghost,0,folded,,`,
	} {
		got, ok := rows[key]
		if !ok || !strings.HasPrefix(strings.ReplaceAll(got, `"`, ""), want) {
			t.Errorf("%s is %q, want a row starting %q\n%s", key, got, want, csv)
		}
	}
	if got := rows["ops|databases"]; !strings.HasSuffix(got, filepath.Join("config.d", "10-ops.yaml")) {
		t.Errorf("the view's row does not name its file: %q", got)
	}
	if got := rows["default|db · prod"]; !strings.HasSuffix(got, filepath.Join("config.d", "20-block.yaml")) {
		t.Errorf("the block's row does not name its file: %q", got)
	}
}

// A name a file states is shown as a listed name is, whatever it holds: the
// sections table and the SECTION column of list drew a line break in one as a row
// of its own, in every format a table has but JSON, which escapes it anyway.
func TestSectionNamesWithALineBreakStayOnOneRow(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "10-hostile.yaml", `dashboard:
  add:
  - id: db.status
    section: "two\nlines"
  views:
    "vE\nline2":
      add:
      - id: db.status
        profile: x
        section: "red\u001b[31m"
`)
	for _, args := range [][]string{{"dashboard", "sections"}, {"dashboard", "list"}} {
		for _, format := range []string{"csv", "md"} {
			out, errOut, err := run(append(args, "-o", format)...)
			if err != nil {
				t.Fatalf("%v: %v %q", args, err, errOut)
			}
			if strings.Contains(out, "two\nlines") || strings.Contains(out, "vE\nline2") || strings.Contains(out, "<br>") {
				t.Errorf("%v -o %s breaks a row at a line break in a name:\n%s", args, format, out)
			}
			if strings.ContainsRune(out, 0x1b) {
				t.Errorf("%v -o %s carries an escape character:\n%s", args, format, out)
			}
		}
		out, _, err := run(append(args, "-o", "csv")...)
		if err != nil || !strings.Contains(out, `"""two\nlines"""`) {
			t.Errorf("%v does not spell the name out where a script reads it: %v\n%s", args, err, out)
		}
	}
	out, _, err := run("dashboard", "sections", "-o", "csv")
	if err != nil || !strings.Contains(out, `"""vE\nline2"""`) || !strings.Contains(out, `\x1b[31m`) {
		t.Errorf("the sections table does not spell out the view's and the section's names: %v\n%s", err, out)
	}
}

// `rta config` says there are sections, the file that states them, and the command
// that lists them, as it does for the tiles.
func TestConfigListsTheSectionsTheDashboardStates(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "10-block.yaml", "dashboard:\n  sections:\n    a:\n      collapsed: true\n    b:\n      columns: 2\n")
	out, errOut, err := run("config", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{`"dashboard.sections"`, "2 sections — `rta dashboard sections`", filepath.Join("config.d", "10-block.yaml")} {
		if !strings.Contains(out, want) {
			t.Errorf("the config listing does not say %q:\n%s", want, out)
		}
	}
}

// With nothing grouped there is nothing to list, and the table says how to start.
func TestDashboardSectionsSaysSoWhenThereAreNone(t *testing.T) {
	run := session(t, setRegistry(t))
	// -o md, the format that is a person's wherever it is written: the sentence a
	// table has for having no rows is never printed into a pipe.
	out, errOut, err := run("dashboard", "sections", "-o", "md")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "No section is stated or drawn") || !strings.Contains(out, "--section") {
		t.Errorf("sections with none says:\n%s", out)
	}
	out, _, err = run("dashboard", "sections", "-o", "json")
	if err != nil || !strings.Contains(out, `"rows": []`) && !strings.Contains(out, `"rows":[]`) {
		t.Errorf("a script gets %v\n%s", err, out)
	}
}

// --section completes to the sections of the view the command writes to, with
// how many tiles name each.
func TestShellCompletionOffersTheSectionsOfTheViewBeingWritten(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "10-ops.yaml", `dashboard:
  views:
    ops:
      sections:
        quiet:
          collapsed: true
      add:
      - id: db.status
        profile: a
        section: databases
      - id: db.status
        profile: b
        section: databases
      - id: db.status
        profile: c
        section: "bad\tname"
`)
	out, _, err := run("__complete", "dashboard", "add", "db.status", "--view", "ops", "--section", "")
	if err != nil || !strings.Contains(out, "databases\t2 tiles") || !strings.Contains(out, "quiet\t0 tiles, starts folded") {
		t.Errorf("--section completes to:\n%s (%v)", out, err)
	}
	// A name with a tab in it would end its candidate and start a description.
	if strings.Contains(out, "bad") {
		t.Errorf("a name that cannot be a heading is offered:\n%s", out)
	}
	// Another view has none of them.
	out, _, err = run("__complete", "dashboard", "add", "db.status", "--section", "")
	if err != nil || strings.Contains(out, "databases") {
		t.Errorf("--section on the default block completes to:\n%s (%v)", out, err)
	}
}
