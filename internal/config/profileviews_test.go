package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A profile's own screen is a view named after the profile, and every question
// there is about views has the same answer for it as for one under `views:`.

func TestAProfilesOwnScreenIsTheViewNamedAfterIt(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml": "dashboard:\n  width: 2\n  views:\n    ops:\n      width: 3\n" +
			"profiles:\n  prod:\n    dashboard: ops\n    plugins:\n      db: {}\n",
		"config.d/20-staging.yaml": stagingDropIn,
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ViewNames(), ","); got != "ops,staging" {
		t.Errorf("views = %s", got)
	}
	for profile, want := range map[string]string{"staging": "staging", "prod": "ops", "": "", "ghost": ""} {
		if got := cfg.ViewFor(profile); got != want {
			t.Errorf("ViewFor(%q) = %q, want %q", profile, got, want)
		}
	}
	if !cfg.HasView("staging") || !cfg.InProfile("staging") || cfg.InProfile("ops") || cfg.HasView("ghost") {
		t.Errorf("HasView/InProfile: staging %v/%v, ops in profile %v, ghost %v",
			cfg.HasView("staging"), cfg.InProfile("staging"), cfg.InProfile("ops"), cfg.HasView("ghost"))
	}
	block, ok := cfg.Block("staging")
	if !ok || len(block.Add) != 2 || block.Width != 0 || block.Views != nil {
		t.Errorf("staging = %+v, %v: it took something from the block", block, ok)
	}
	if got := cfg.TrustedDashboardFor("staging"); len(got.Add) != 2 || !slices.Equal(got.Order, []string{"note.list", "sys.overview"}) {
		t.Errorf("the screen drawn for staging = %+v", got)
	}
	for _, tc := range []struct{ view, file string }{
		{"staging", "config.d/20-staging.yaml"}, {"ops", "config.yaml"}, {"new", "config.yaml"},
	} {
		if got, want := Where("views", tc.view), filepath.Join(dir, tc.file); got != want {
			t.Errorf("Where(views, %s) = %s, want %s", tc.view, got, want)
		}
	}
}

// Any profile can select another's screen by name, as it can any view: the name
// is the view's, wherever it is written.
func TestAProfileCanSelectAnotherProfilesOwnScreenByName(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte("profiles:\n  staging:\n    dashboard:\n      width: 3\n" +
		"  qa:\n    dashboard: staging\n  lost:\n    dashboard: ghost\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ViewFor("qa"); got != "staging" {
		t.Errorf("qa draws %q", got)
	}
	if got := cfg.ViewFor("lost"); got != "" {
		t.Errorf("a name nothing states drew %q", got)
	}
}

// A name has one statement. When the view under `views:` and the profile's own
// block share one the view keeps the name, the block is not drawn and not
// addressable, and the profile draws the block of the dashboard: — never either
// of the two by an order nobody wrote down.
func TestAViewAndABlockOfTheSameNameAreNeitherDrawn(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte("dashboard:\n  width: 2\n  views:\n    staging:\n      width: 5\n" +
		"profiles:\n  staging:\n    dashboard:\n      width: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ViewFor("staging"); got != "" {
		t.Errorf("staging draws %q: one of the two was chosen", got)
	}
	if block, _ := cfg.Block("staging"); block.Width != 5 {
		t.Errorf("the name addresses %+v, want the view under views:", block)
	}
	if cfg.InProfile("staging") || strings.Join(cfg.ViewNames(), ",") != "staging" {
		t.Errorf("the block is addressable: views = %v", cfg.ViewNames())
	}
	reason, hint := cfg.ShadowedScreen("staging")
	for _, want := range []string{"writes its dashboard in its entry", "view called staging", "neither is drawn"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason lacks %q: %s", want, reason)
		}
	}
	if !strings.Contains(hint, "keep one") {
		t.Errorf("the hint = %q", hint)
	}
	if r, _ := cfg.ShadowedScreen("ghost"); r != "" {
		t.Errorf("a profile that is not there is shadowed: %s", r)
	}
}

// `default` addresses the dashboard: block, so it is nobody's screen.
func TestAProfileCalledDefaultCannotHaveItsScreenAddressed(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte("profiles:\n  default:\n    dashboard:\n      width: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HasView(DefaultView) || cfg.ViewFor("default") != "" || len(cfg.ViewNames()) != 0 {
		t.Errorf("the block of a profile called default is addressable: %v", cfg.ViewNames())
	}
	if reason, _ := cfg.ShadowedScreen("default"); !strings.Contains(reason, "`default` names the `dashboard:` block") {
		t.Errorf("reason = %q", reason)
	}
}

func TestAWriteToAProfilesOwnScreenGoesToTheProfileAndNeverToViews(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":              "output: json\n",
		"config.d/20-staging.yaml": stagingDropIn,
	})
	err := Mutate(func(c Config) (Config, bool) {
		block, _ := c.Block("staging")
		block.Hidden = append(slices.Clone(block.Hidden), "git.overview")
		c.SetBlock("staging", block)
		c.SetBlock("fresh", Dashboard{Width: 1})
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	drop := read(t, filepath.Join(dir, "config.d", "20-staging.yaml"))
	if !strings.Contains(drop, "- git.overview") || strings.Contains(drop, "views:") || strings.Contains(drop, "fresh") {
		t.Errorf("the drop-in after the write:\n%s", drop)
	}
	main := read(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(main, "fresh") || strings.Contains(main, "staging") {
		t.Errorf("the config file after the write — a new view goes under views:, and staging's screen is not there:\n%s", main)
	}
	again, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if block, _ := again.Block("staging"); !slices.Contains(block.Hidden, "git.overview") || len(block.Add) != 2 {
		t.Errorf("staging after the write = %+v", block)
	}
	if again.Dashboard.Views["staging"].Add != nil || len(again.Dashboard.Views) != 1 {
		t.Errorf("a view called staging appeared under views: %+v", again.Dashboard.Views)
	}
}

// A configuration that is nothing but drop-ins stays that: a write into one of
// them is not a reason to make the config file beside them, with its header, an
// empty mapping and the schema file its header names.
func TestAWriteIntoADropInDoesNotMakeTheConfigFile(t *testing.T) {
	dir := layout(t, map[string]string{"config.d/20-staging.yaml": stagingDropIn})
	err := Mutate(func(c Config) (Config, bool) {
		block, _ := c.Block("staging")
		block.Width = 2
		c.SetBlock("staging", block)
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.yaml", SchemaFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was made by a write into a drop-in", name)
		}
	}
	if got := read(t, filepath.Join(dir, "config.d", "20-staging.yaml")); !strings.Contains(got, "width: 2") {
		t.Errorf("the drop-in was not written:\n%s", got)
	}
	// A unit nothing states yet still goes to the config file, which this makes.
	err = Mutate(func(c Config) (Config, bool) { c.Output = "json"; return c, true })
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "config.yaml")); !strings.Contains(got, "output: json") {
		t.Errorf("the config file after a write that has a unit for it:\n%s", got)
	}
}

// What a repository's ./.rta.yaml says is read and listed and is never drawn:
// a profile's own screen is arranged by the file that states it, and the file
// nobody named arranges nothing.
func TestAnUnnamedConfigsProfileScreenIsListedAndNeverDrawn(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	writeFiles(t, dir, map[string]string{".rta.yaml": stagingDropIn})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Trusted() || cfg.ViewFor("staging") != "staging" {
		t.Fatalf("trusted = %v, view = %q: the screen is not there to be refused", cfg.Trusted(), cfg.ViewFor("staging"))
	}
	if got := cfg.TrustedDashboardFor("staging"); len(got.Add)+len(got.Hidden)+len(got.Order)+len(got.Tiles) != 0 {
		t.Errorf("a repository arranged the screen: %+v", got)
	}
}
