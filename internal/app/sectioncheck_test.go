package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// A `sections:` entry that no tile is in states nothing, and a name or a column
// count that cannot be what it says does nothing either. The check and the
// doctor say so, with the key and the line, and neither refuses the file.

// sectionsDropIn is a view in a drop-in with one of each: a section a tile is in,
// one no tile is in, the group the automatic db tile makes under prod (which holds
// two connections) and the one it would make under staging (which holds one),
// a column count below zero, and an entry whose section cannot be a heading.
func sectionsDropIn() string {
	return `dashboard:
  views:
    ops:
      sections:
        databases:
          width: 3
        ghost:
          collapsed: true
        db · prod:
          collapsed: true
        db · staging:
          collapsed: true
        negative:
          width: -2
      add:
      - id: db.status
        profile: prod
        section: databases
      - id: db.status
        profile: staging
        section: ` + strings.Repeat("x", 70) + `
`
}

// twoProfiles gives prod two db connections and staging one.
func twoProfiles(t *testing.T, run func(args ...string) (string, string, error)) {
	t.Helper()
	for _, args := range [][]string{
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"},
		{"profile", "set", "prod", "--plugin", "db/analytics", "--set", "host=analytics.prod.internal"},
		{"profile", "set", "staging", "--plugin", "db", "--set", "host=staging.internal"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
}

func TestConfigCheckSaysWhatASectionStatesThatDoesNothing(t *testing.T) {
	run := session(t, setRegistry(t))
	twoProfiles(t, run)
	dropIn(t, "10-ops.yaml", sectionsDropIn())
	out, errOut, err := run("config", "check", "-o", "json")
	if err == nil {
		t.Fatalf("a configuration with problems passed:\n%s", out)
	}
	text := out + errOut
	for _, want := range []string{
		"dashboard.views.ops.sections.ghost",
		`no tile of this screen is in a section called \"ghost\"`,
		"dashboard.views.ops.sections.db · staging",
		"dashboard.views.ops.sections.negative.width",
		"width -2 is not a width",
		"dashboard.views.ops.add[1].section",
		"is not a name a section can have",
		filepath.Join("config.d", "10-ops.yaml"),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}
	for _, not := range []string{
		"views.ops.sections.databases",
		"views.ops.sections.db · prod",
	} {
		if strings.Contains(text, not) {
			t.Errorf("the check names %q, which is a section a tile can be in:\n%s", not, text)
		}
	}
	// The names a person can use are in the sentence that says theirs is not one.
	if !strings.Contains(text, "the sections here are") || !strings.Contains(text, "databases") ||
		!strings.Contains(text, "db · prod") {
		t.Errorf("the check does not list the sections there are:\n%s", text)
	}
}

// The file is still read and drawn: a warning is not a refusal, and the doctor
// says each once.
func TestDoctorSaysWhatASectionStatesThatDoesNothing(t *testing.T) {
	run := session(t, setRegistry(t))
	twoProfiles(t, run)
	dropIn(t, "10-ops.yaml", sectionsDropIn())
	var rows []string
	for _, r := range doctorTable(setRegistry(t), false).Rows {
		if r[0] == "dashboard" {
			rows = append(rows, r[2])
		}
	}
	joined := strings.Join(rows, "\n")
	for _, want := range []string{
		"dashboard.views.ops.sections.ghost: no tile of this screen",
		"dashboard.views.ops.sections.db · staging: no tile of this screen",
		"dashboard.views.ops.sections.negative.width: width -2",
		"dashboard.views.ops.add[1].section",
	} {
		if strings.Count(joined, want) != 1 {
			t.Errorf("the doctor says %q %d times:\n%s", want, strings.Count(joined, want), joined)
		}
	}
	if strings.Contains(joined, "sections.databases") || strings.Contains(joined, "sections.db · prod") {
		t.Errorf("the doctor names a section a tile can be in:\n%s", joined)
	}
	if out, errOut, err := run("dashboard", "views"); err != nil || !strings.Contains(out, "ops") {
		t.Errorf("a warning stopped the file being read: %v %q\n%s", err, errOut, out)
	}
}

// A section stated in a profile's own screen and in the dashboard block is held
// to the same rules, in the file that states it.
func TestConfigCheckReadsSectionsInTheBlockAndInAProfilesOwnScreen(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "10-block.yaml", "dashboard:\n  width: -1\n  sections:\n    nothing:\n      collapsed: true\n")
	dropIn(t, "20-shop.yaml", `profiles:
  shop:
    plugins:
      db:
        set:
          host: shop.internal
    dashboard:
      sections:
        lost:
          collapsed: true
`)
	out, errOut, err := run("config", "check", "-o", "json")
	text := out + errOut
	if err == nil {
		t.Fatalf("the check passed:\n%s", text)
	}
	for _, want := range []string{
		"dashboard.width", "dashboard.sections.nothing",
		"profiles.shop.dashboard.sections.lost", "10-block.yaml", "20-shop.yaml",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}
}

// A configuration whose sections all have tiles, and whose group names are ones a
// profile with several connections would give, is clean.
func TestConfigCheckPassesSectionsThatHaveTiles(t *testing.T) {
	run := session(t, setRegistry(t))
	twoProfiles(t, run)
	dropIn(t, "10-ops.yaml", `dashboard:
  sections:
    databases:
      collapsed: true
      width: 2
    db · prod:
      collapsed: true
  add:
  - id: db.status
    profile: staging
    section: databases
`)
	out, errOut, err := run("config", "check", "-o", "json")
	if err != nil {
		t.Errorf("a configuration whose sections have tiles did not pass: %v\n%s%s", err, out, errOut)
	}
}
