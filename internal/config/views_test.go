package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const viewsYAML = `dashboard:
  columns: 2
  hidden: [gen.overview]
  views:
    ops:
      columns: 3
      tiles:
      - id: kube.overview
        profile: prod
    dbs:
      add:
      - id: pg.overview
        profile: staging
profiles:
  prod:
    dashboard: ops
    plugins:
      db:
        set:
          host: p.example
  stage:
    dashboard: nowhere
    plugins:
      db:
        set:
          host: s.example
  plain:
    plugins:
      db:
        set:
          host: x.example
`

func TestAProfileSelectsTheViewItNames(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte(viewsYAML))
	if err != nil {
		t.Fatal(err)
	}
	for profile, want := range map[string]string{"prod": "ops", "stage": "", "plain": "", "": "", "unknown": ""} {
		if got := cfg.ViewFor(profile); got != want {
			t.Errorf("ViewFor(%q) = %q, want %q: a name that is not a view draws the default", profile, got, want)
		}
	}
	if names := strings.Join(cfg.ViewNames(), ","); names != "dbs,ops" {
		t.Errorf("views = %s", names)
	}
	if got := cfg.Profiles["prod"].UnknownKeys(); len(got) != 0 {
		t.Errorf("a profile's dashboard key is reported as unknown: %v", got)
	}
}

// A view is a whole arrangement and replaces the block while it is drawn: it
// takes nothing from the block, not even its columns.
func TestAViewReplacesTheBlockAndDoesNotOverlayIt(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte(viewsYAML))
	if err != nil {
		t.Fatal(err)
	}
	base, ok := cfg.Block("")
	if !ok || base.Columns != 2 || len(base.Hidden) != 1 || base.Views != nil {
		t.Errorf("the default block = %+v", base)
	}
	if again, _ := cfg.Block(DefaultView); again.Columns != 2 {
		t.Errorf("the default addressed by name = %+v", again)
	}
	ops, ok := cfg.Block("ops")
	if !ok || ops.Columns != 3 || len(ops.Tiles) != 1 || len(ops.Hidden) != 0 {
		t.Errorf("ops = %+v: it took something from the block", ops)
	}
	if _, ok := cfg.Block("nowhere"); ok {
		t.Error("a view nothing states was found")
	}
	if got := cfg.TrustedDashboardFor("ops"); got.Columns != 3 || got.Views != nil {
		t.Errorf("the trusted view = %+v", got)
	}
}

func TestAViewNotTrustedDrawsTheAutomaticDashboard(t *testing.T) {
	cfg := Config{Dashboard: Dashboard{Views: map[string]View{"ops": {Columns: 3}}}}
	if got := cfg.TrustedDashboardFor("ops"); got.Columns != 0 || got.Views != nil {
		t.Errorf("an untrusted config drew %+v", got)
	}
}

func TestSetBlockStatesAViewAndKeepsTheOthers(t *testing.T) {
	configAt(t)
	cfg, _ := Parse([]byte(viewsYAML))
	cfg.SetBlock("fresh", Dashboard{Hidden: []string{"sys.overview"}})
	cfg.SetBlock("ops", Dashboard{Columns: 4})
	cfg.SetBlock("", Dashboard{Columns: 5})
	if got := strings.Join(cfg.ViewNames(), ","); got != "dbs,fresh,ops" {
		t.Errorf("views = %s", got)
	}
	if cfg.Dashboard.Columns != 5 || len(cfg.Dashboard.Views) != 3 {
		t.Errorf("setting the default lost the views: %+v", cfg.Dashboard)
	}
	if ops, _ := cfg.Block("ops"); ops.Columns != 4 || len(ops.Tiles) != 0 {
		t.Errorf("ops = %+v", ops)
	}
}

func TestViewNamesAreWordsAndNotTheDefault(t *testing.T) {
	for name, ok := range map[string]bool{
		"ops": true, "db-2": true, "a": true, "default": false, "": false, "Ops": false,
		"1ops": false, "a_b": false, strings.Repeat("a", 33): false,
	} {
		if got := ValidViewName(name); got != ok {
			t.Errorf("ValidViewName(%q) = %v, want %v", name, got, ok)
		}
	}
}

// Each view is a unit of its own, so the views of a configuration can be in
// as many files as a person likes, and a write to one lands in the file that
// states it.
func TestViewsAreUnitsOfTheirOwnInTheFilesThatStateThem(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":          "dashboard:\n  columns: 2\n",
		"config.d/10-ops.yaml": "dashboard:\n  views:\n    ops:\n      columns: 3\n",
		"config.d/20-dbs.yaml": "dashboard:\n  views:\n    dbs:\n      columns: 4\n",
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ViewNames(), ","); got != "dbs,ops" || cfg.Dashboard.Columns != 2 {
		t.Fatalf("views = %s, columns = %d", got, cfg.Dashboard.Columns)
	}
	for _, tc := range []struct{ kind, name, file string }{
		{"views", "ops", "config.d/10-ops.yaml"},
		{"views", "dbs", "config.d/20-dbs.yaml"},
		{"dashboard", "", "config.yaml"},
		{"views", "new", "config.yaml"},
	} {
		if got, want := Where(tc.kind, tc.name), filepath.Join(dir, tc.file); got != want {
			t.Errorf("Where(%s, %s) = %s, want %s", tc.kind, tc.name, got, want)
		}
	}
	mainBefore := read(t, filepath.Join(dir, "config.yaml"))
	err = Mutate(func(c Config) (Config, bool) {
		c.SetBlock("ops", Dashboard{Columns: 6})
		c.SetBlock("fresh", Dashboard{Columns: 1})
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "config.d", "10-ops.yaml")); !strings.Contains(got, "columns: 6") {
		t.Errorf("the view's own file after the write:\n%s", got)
	}
	if got := read(t, filepath.Join(dir, "config.d", "20-dbs.yaml")); !strings.Contains(got, "columns: 4") || strings.Contains(got, "fresh") {
		t.Errorf("another view's file after the write:\n%s", got)
	}
	got := read(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(got, "fresh") || strings.Contains(got, "ops") || got == mainBefore {
		t.Errorf("the config file after the write:\n%s\nwant the new view and none of ops", got)
	}
	again, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(again.ViewNames(), ","); names != "dbs,fresh,ops" || again.Dashboard.Columns != 2 {
		t.Errorf("after the write: views = %s, columns = %d", names, again.Dashboard.Columns)
	}
}

func TestAViewStatedTwiceIsRefusedNamingBothFiles(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml":          "dashboard:\n  views:\n    ops:\n      columns: 3\n",
		"config.d/10-ops.yaml": "dashboard:\n  views:\n    ops:\n      columns: 4\n",
	})
	_, err := LoadFile()
	if err == nil || !strings.Contains(err.Error(), "dashboard view ops") {
		t.Errorf("error = %v, want the view named", err)
	}
}

func TestViewNamesAreCheckedOnTheirWayIn(t *testing.T) {
	configAt(t)
	found := CheckText([]byte("dashboard:\n  views:\n    ops:\n      colums: 3\n"))
	if len(found) != 1 || !strings.Contains(found[0].Key, "colums") {
		t.Errorf("a key a view does not have = %v", found)
	}
	if !slices.Contains(profileNamesWithKey(), "dashboard") {
		t.Error("dashboard is not a key a profile may carry")
	}
}

func profileNamesWithKey() []string {
	var out []string
	for k := range profileKeys {
		out = append(out, k)
	}
	return out
}
