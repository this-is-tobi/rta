package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// A profile or a view is named by a key in a config file, and a config file can
// be a team's drop-in. The names the loader accepts as a profile and refuses to
// use were still printed as they were, and a newline in one was a second line:
// a table row of its own with every other cell empty, a break in the middle of
// the hint that lists the configured profiles. A name that is not plain is
// listed as every listed name is (quoted, with the character written out) in
// every listing that carries one.
const hostileNames = `profiles:
  "pE\nline2":
    plugins:
      db: {}
dashboard:
  views:
    "vE\nline3":
      add:
      - id: db.status
`

func TestANameWithANewlineIsOneLineInEveryListing(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "30-hostile.yaml", hostileNames)

	for _, args := range [][]string{
		{"profile", "list", "-o", "json"},
		{"dashboard", "views", "-o", "json"},
		{"config", "check", "-o", "json"},
	} {
		out, _, _ := run(args...)
		var v struct {
			Rows [][]string `json:"rows"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		for _, row := range v.Rows {
			for _, cell := range row {
				if strings.Contains(cell, "\n") {
					t.Errorf("%v: a cell spans two lines: %q", args, cell)
				}
			}
		}
	}

	_, errOut, _ := run("use", "nosuch")
	if strings.Contains(errOut, "\nline2") {
		t.Errorf("the hint listing the configured profiles was cut at a name's newline:\n%s", errOut)
	}
}

// The writers refuse a view name that is not lower case, digits and hyphens,
// and the reader took any. A view the writers could not have made is named by
// the check, with what a view may be called, so a name that reached the file by
// hand is not the only place the rule is unsaid.
func TestConfigCheckNamesAViewWhoseNameNoWriterWouldMake(t *testing.T) {
	run := session(t, setRegistry(t))
	dropIn(t, "30-bigops.yaml", "dashboard:\n  views:\n    Big Ops:\n      add:\n      - id: db.status\n")
	out, _, _ := run("config", "check", "-o", "json")
	if !strings.Contains(out, "dashboard.views.Big Ops") || !strings.Contains(out, "not a name a view can have") {
		t.Errorf("config check does not name the view:\n%s", out)
	}
}
